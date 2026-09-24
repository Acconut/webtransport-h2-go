package wth2

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/quicvarint"
)

var ErrSessionClosed = errors.New("session closed")

// SessionCloseError is returned by [Session.CloseErr] after the session ends
// with an application error (local [Session.CloseWithError] or peer WT_CLOSE_SESSION).
type SessionCloseError struct {
	Code    uint32
	Message string
	Remote  bool // true if the peer sent WT_CLOSE_SESSION
}

func (e *SessionCloseError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("session closed with application error 0x%x", e.Code)
	}
	return fmt.Sprintf("session closed with application error 0x%x: %s", e.Code, e.Message)
}

type Session struct {
	Protocol string
	// StreamReceiveBufferSize is the maximum buffered receive bytes per stream.
	// A value <= 0 disables the limit.
	StreamReceiveBufferSize int
	// DatagramReceiveBufferSize is the maximum buffered receive bytes for
	// datagrams on the session. A value <= 0 disables the limit.
	DatagramReceiveBufferSize int

	// Client-initiated streams use even IDs, server-initiated streams use odd IDs.
	isServer bool

	log     *log.Logger
	reader  quicvarint.Reader
	writer  quicvarint.Writer
	flusher http.Flusher // optional; discovered from the CONNECT stream writer

	stop                          atomic.Bool
	closeOnce                     sync.Once
	done                          chan struct{}
	closeDatagramDeliveryOnce     sync.Once
	writeMu                       sync.Mutex
	incomingStreams               chan *Stream
	incomingUnidirectionalStreams chan *ReceiveStream
	datagramBuffer                *datagramReceiveBuffer

	closeErrMu sync.Mutex
	closeErr   error

	// TODO: Lock
	streams        map[uint64]*Stream        // Bidirectional streams.
	receiveStreams map[uint64]*ReceiveStream // Peer-initiated, unidirectional streams.
	sendStreams    map[uint64]*SendStream    // Self-initiated, unidirectional streams.

	createdStreamCounter uint64

	// limits is the peer's grant for data and streams this endpoint sends.
	limits sendLimits
}

const defaultReceiveBufferSize = 64 * 1024

func newSession(reader io.Reader, writer io.Writer, protocol string, isServer bool) *Session {
	return newSessionWithPeerLimits(reader, writer, protocol, isServer, nil)
}

// newSessionWithPeerLimits is newSession with the peer's initial send limits.
// A nil peer leaves enforcement off so in-memory sessions keep working.
// The limits are installed before the read loop starts, so a capsule cannot
// race ahead of the SETTINGS snapshot.
func newSessionWithPeerLimits(reader io.Reader, writer io.Writer, protocol string, isServer bool, peer *peerInitialLimits) *Session {
	prefix := "[client] "
	if isServer {
		prefix = "[server] "
	}
	session := &Session{
		Protocol:                  protocol,
		StreamReceiveBufferSize:   defaultReceiveBufferSize,
		DatagramReceiveBufferSize: defaultReceiveBufferSize,
		isServer:                  isServer,
		log:                       log.New(log.Writer(), prefix, log.LstdFlags),
		reader:                    quicvarint.NewReader(reader),
		writer:                    quicvarint.NewWriter(writer),
		flusher:                   flusherFromWriter(writer),
		done:                      make(chan struct{}),
		// Buffered so the read loop can accept peer-opened streams before the
		// application calls Accept* (e.g. server baton setup vs client connect).
		incomingStreams:               make(chan *Stream, 16),
		incomingUnidirectionalStreams: make(chan *ReceiveStream, 16),
		streams:                       make(map[uint64]*Stream),
		receiveStreams:                make(map[uint64]*ReceiveStream),
		sendStreams:                   make(map[uint64]*SendStream),
	}
	session.datagramBuffer = newDatagramReceiveBuffer(session.DatagramReceiveBufferSize)
	if peer != nil {
		session.adoptPeerSendLimits(*peer)
	}

	go session.readLoop()

	return session
}

// Done is closed when the session has been closed locally or the peer ended it.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// CloseErr returns a [SessionCloseError] if the session closed with an
// application error, otherwise nil (clean close or still open).
func (s *Session) CloseErr() error {
	s.closeErrMu.Lock()
	defer s.closeErrMu.Unlock()
	return s.closeErr
}

// Close stops session-level delivery (Accept, datagrams, read loop signalling).
// It does not close the underlying CONNECT stream reader or writer: the HTTP
// owner must FIN that stream (client: close the request-body closer from
// [Client.Connect]; server: return from the handler after [Session.Done]).
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		s.stop.Store(true)
		s.closeDatagramDelivery()
		close(s.done)
	})
}

// CloseWithError sends a WT_CLOSE_SESSION capsule and then [Close]s the session.
// Per the WebTransport drafts, the sender must then FIN the CONNECT stream;
// this method does not do that (see [Close]).
func (s *Session) CloseWithError(code uint32, message string) error {
	if len(message) > 1024 {
		return errors.New("close error message exceeds 1024 bytes")
	}
	if s.stop.Load() {
		return ErrSessionClosed
	}
	payload := make([]byte, 4+len(message))
	binary.BigEndian.PutUint32(payload[:4], code)
	copy(payload[4:], message)
	if err := s.writeCapsule(uint64(CapsuleWTCloseSession), payload); err != nil {
		return err
	}
	s.Flush()
	s.setCloseErr(&SessionCloseError{Code: code, Message: message, Remote: false})
	s.Close()
	return nil
}

func (s *Session) setCloseErr(err error) {
	s.closeErrMu.Lock()
	defer s.closeErrMu.Unlock()
	if s.closeErr == nil {
		s.closeErr = err
	}
}

func (s *Session) readLoop() {
	defer s.Close()

	for {
		if s.stop.Load() {
			return
		}

		typ, content, err := http3.ParseCapsule(s.reader)
		if err != nil {
			if isCleanConnectTeardown(err) {
				return
			}
			if !s.stop.Load() {
				s.log.Printf("read error: %v", err)
			}
			return
		}
		switch CapsuleType(typ) {
		case CapsulePadding:
			// PADDING has no semantic value; consume and ignore it.
			padding, err := io.ReadAll(content)
			if err != nil {
				if !isCleanConnectTeardown(err) && !s.stop.Load() {
					s.log.Printf("read error: %v", err)
				}
				return
			}
			s.log.Printf("received capsule %s len=%d", CapsuleType(typ), len(padding))
			for _, b := range padding {
				// RFC allows receivers to ignore validation; we only log non-zero bytes.
				if b != 0 {
					s.log.Printf("received non-zero PADDING byte")
					break
				}
			}
		case CapsuleWTStream, CapsuleWTStreamFin:
			id, err := quicvarint.Read(quicvarint.NewReader(content))
			if err != nil {
				// TODO: Probably better to discard data and continue with next capsule
				s.log.Printf("read error: %v", err)
				return
			}
			s.log.Printf("received capsule %s stream_id=%d bidi=%v ours=%v", CapsuleType(typ), id, isBidirectionalStream(id), s.isOurStream(id))
			if isBidirectionalStream(id) {
				if stream, ok := s.streams[id]; ok {
					// Provide data to stream
					err = stream.receiveStreamData(content)
					if err != nil {
						s.log.Printf("read error: %v", err)
						return
					}
					if CapsuleType(typ) == CapsuleWTStreamFin {
						stream.finishReceive()
					}
				} else if !s.isOurStream(id) {
					// New peer-initiated bidirectional stream -> accept
					stream := newStream(s, id)
					s.streams[id] = stream
					s.trackSendStream(id)
					if !s.enqueueIncomingStream(stream) {
						return
					}
					err = stream.receiveStreamData(content)
					if err != nil {
						s.log.Printf("read error: %v", err)
						return
					}
					if CapsuleType(typ) == CapsuleWTStreamFin {
						stream.finishReceive()
					}
				} else {
					// Peer initiated stream with our ID -> reject
					s.log.Printf("dropping peer-initiated stream with our ID: %d", id)
					io.Copy(io.Discard, s.reader)
				}
			} else {
				// Unidirectional stream
				if stream, ok := s.receiveStreams[id]; ok {
					err = stream.receiveStreamData(content)
					if err != nil {
						s.log.Printf("read error: %v", err)
						return
					}
					if CapsuleType(typ) == CapsuleWTStreamFin {
						stream.finishReceive()
						delete(s.receiveStreams, id)
					}
				} else if !s.isOurStream(id) {
					// New peer-initiated unidirectional stream -> accept
					stream := newReceiveStream(s, id)
					s.receiveStreams[id] = stream
					if !s.enqueueIncomingUniStream(stream) {
						return
					}
					err = stream.receiveStreamData(content)
					if err != nil {
						s.log.Printf("read error: %v", err)
						return
					}
					if CapsuleType(typ) == CapsuleWTStreamFin {
						stream.finishReceive()
						delete(s.receiveStreams, id)
					}
				} else {
					// Peer sent data on our local send-only stream ID -> reject
					s.log.Printf("dropping peer data for local send-only stream: %d", id)
					_, _ = io.Copy(io.Discard, content)
				}
			}
		case CapsuleDatagram:
			payload, err := io.ReadAll(content)
			if err != nil {
				if !isCleanConnectTeardown(err) && !s.stop.Load() {
					s.log.Printf("read error: %v", err)
				}
				return
			}
			s.log.Printf("received capsule %s len=%d", CapsuleType(typ), len(payload))
			s.deliverDatagram(payload)
		case CapsuleWTCloseSession:
			payload, err := io.ReadAll(content)
			if err != nil {
				if !isCleanConnectTeardown(err) && !s.stop.Load() {
					s.log.Printf("read error: %v", err)
				}
				return
			}
			code, message, err := parseWTCloseSession(payload)
			if err != nil {
				s.log.Printf("invalid WT_CLOSE_SESSION: %v", err)
				return
			}
			s.log.Printf("received capsule %s code=0x%x msg=%q", CapsuleType(typ), code, message)
			s.setCloseErr(&SessionCloseError{Code: code, Message: message, Remote: true})
			return
		case CapsuleWTDrainSession:
			_, _ = io.Copy(io.Discard, content)
			s.log.Printf("received capsule %s", CapsuleType(typ))
			// Drain is advisory; keep the session open until Close / peer FIN.
		case CapsuleWTMaxData, CapsuleWTMaxStreamData, CapsuleWTMaxStreamsBidi, CapsuleWTMaxStreamsUni:
			err := s.handleSendLimitCapsule(CapsuleType(typ), content)
			if err == nil {
				break
			}
			if fc := flowControlError(err); fc != nil {
				s.log.Printf("flow control error: %v", fc)
				s.setCloseErr(fc)
				return
			}
			if !isCleanConnectTeardown(err) && !s.stop.Load() {
				s.log.Printf("read error: %v", err)
			}
			return
		default:
			s.log.Printf("received capsule %s", CapsuleType(typ))
			_, _ = io.Copy(io.Discard, content)
		}
	}
}

func parseWTCloseSession(payload []byte) (code uint32, message string, err error) {
	if len(payload) < 4 {
		return 0, "", errors.New("WT_CLOSE_SESSION payload too short")
	}
	code = binary.BigEndian.Uint32(payload[:4])
	message = string(payload[4:])
	if len(message) > 1024 {
		return 0, "", errors.New("WT_CLOSE_SESSION message exceeds 1024 bytes")
	}
	return code, message, nil
}

// isCleanConnectTeardown reports whether err is a normal CONNECT-stream end
// (peer FIN / HTTP/2 RST_STREAM with NO_ERROR), not a fault.
func isCleanConnectTeardown(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	// net/http's bundled HTTP/2 uses its own StreamError type; match by text
	// so we work with both net/http and golang.org/x/net/http2.
	msg := err.Error()
	return strings.Contains(msg, "NO_ERROR") && strings.Contains(msg, "stream error:")
}

func (s *Session) enqueueIncomingStream(stream *Stream) bool {
	select {
	case s.incomingStreams <- stream:
		return true
	case <-s.done:
		return false
	}
}

func (s *Session) enqueueIncomingUniStream(stream *ReceiveStream) bool {
	select {
	case s.incomingUnidirectionalStreams <- stream:
		return true
	case <-s.done:
		return false
	}
}

func (s *Session) deliverDatagram(payload []byte) {
	if !s.datagramBuffer.write(payload) {
		s.log.Printf("dropping datagram: receive buffer full")
	}
}

func (s *Session) closeDatagramDelivery() {
	s.closeDatagramDeliveryOnce.Do(func() {
		s.datagramBuffer.close()
	})
}

func (s *Session) isOurStream(id uint64) bool {
	if s.isServer {
		return id&0b01 == 1
	}
	return id&0b01 == 0
}

func isBidirectionalStream(id uint64) bool {
	return id&0b10 == 0
}

func (s *Session) nextStreamID(bidirectional bool) uint64 {
	baseID := s.createdStreamCounter

	if !bidirectional {
		baseID |= 0b10
	}

	if s.isServer {
		baseID |= 0b01
	}

	s.createdStreamCounter += 4
	return baseID
}

func (s *Session) OpenStream() (*Stream, error) {
	if s.stop.Load() {
		return nil, ErrSessionClosed
	}
	id, err := s.allocLocalStream(true)
	if err != nil {
		return nil, err
	}
	stream := newStream(s, id)
	s.log.Printf("open stream bidi=true id=%d", stream.ID)
	s.streams[stream.ID] = stream
	return stream, nil
}

func (s *Session) OpenUnidirectionalStream() (*SendStream, error) {
	if s.stop.Load() {
		return nil, ErrSessionClosed
	}
	id, err := s.allocLocalStream(false)
	if err != nil {
		return nil, err
	}
	stream := newSendStream(s, id)
	s.log.Printf("open stream bidi=false id=%d", stream.ID)
	s.sendStreams[stream.ID] = stream
	return stream, nil
}

func (s *Session) AcceptStream(ctx context.Context) (*Stream, error) {
	select {
	case stream := <-s.incomingStreams:
		s.log.Printf("accepted stream id=%d", stream.ID)
		return stream, nil
	case <-s.done:
		return nil, ErrSessionClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Session) AcceptUnidirectionalStream(ctx context.Context) (*ReceiveStream, error) {
	select {
	case stream := <-s.incomingUnidirectionalStreams:
		return stream, nil
	case <-s.done:
		return nil, ErrSessionClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Session) writeCapsule(typ uint64, data []byte) (err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.log.Printf("sent capsule %s len=%d", CapsuleType(typ), len(data))
	return http3.WriteCapsule(s.writer, http3.CapsuleType(typ), data)
}

// Flush pushes any capsules buffered on the underlying CONNECT stream to the peer.
// On HTTP/2 servers this typically flushes the ResponseWriter's 4 KiB write buffer.
// It is a no-op when the writer does not implement [http.Flusher] (e.g. in-memory pipes).
//
// Flush must be serialized with capsule writes: the HTTP/2 ResponseWriter's bufio
// is not safe for concurrent Write and Flush.
func (s *Session) Flush() {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.flusher.Flush()
}

type noopFlusher struct{}

func (_ noopFlusher) Flush() {}

func flusherFromWriter(w io.Writer) http.Flusher {
	if f, ok := w.(http.Flusher); ok {
		return f
	}
	return noopFlusher{}
}

// SendDatagram sends an unreliable datagram on the session.
// Datagrams are not subject to WebTransport flow control.
func (s *Session) SendDatagram(payload []byte) error {
	if s.stop.Load() {
		return ErrSessionClosed
	}
	return s.writeCapsule(uint64(CapsuleDatagram), payload)
}

// ReceiveDatagram waits for the next datagram from the peer.
// The receiver may drop datagrams when the session receive buffer is full.
func (s *Session) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	return s.datagramBuffer.read(ctx)
}
