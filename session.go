package wth2

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"sync/atomic"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/quicvarint"
)

var ErrSessionClosed = errors.New("session closed")

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

	log    *log.Logger
	reader quicvarint.Reader
	writer quicvarint.Writer

	stop                          atomic.Bool
	closeDatagramDeliveryOnce     sync.Once
	incomingStreams               chan *Stream
	incomingUnidirectionalStreams chan *ReceiveStream
	datagramBuffer                *datagramReceiveBuffer

	// TODO: Lock
	streams        map[uint64]*Stream        // Bidirectional streams.
	receiveStreams map[uint64]*ReceiveStream // Peer-initiated, unidirectional streams.
	sendStreams    map[uint64]*SendStream    // Self-initiated, unidirectional streams.

	createdStreamCounter uint64
}

const defaultReceiveBufferSize = 64 * 1024

func newSession(reader io.Reader, writer io.Writer, protocol string, isServer bool) *Session {
	prefix := "[client] "
	if isServer {
		prefix = "[server] "
	}
	session := &Session{
		Protocol:                      protocol,
		StreamReceiveBufferSize:       defaultReceiveBufferSize,
		DatagramReceiveBufferSize:     defaultReceiveBufferSize,
		isServer:                      isServer,
		log:                           log.New(log.Writer(), prefix, log.LstdFlags),
		reader:                        quicvarint.NewReader(reader),
		writer:                        quicvarint.NewWriter(writer),
		incomingStreams:               make(chan *Stream),
		incomingUnidirectionalStreams: make(chan *ReceiveStream),
		streams:                       make(map[uint64]*Stream),
		receiveStreams:                make(map[uint64]*ReceiveStream),
		sendStreams:                   make(map[uint64]*SendStream),
	}
	session.datagramBuffer = newDatagramReceiveBuffer(session.DatagramReceiveBufferSize)

	go session.readLoop()

	return session
}

func (s *Session) Close() {
	s.stop.Store(true)
	s.closeDatagramDelivery()
}

func (s *Session) readLoop() {
	defer s.closeDatagramDelivery()

	for {
		if s.stop.Load() {
			return
		}

		typ, content, err := http3.ParseCapsule(s.reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			s.log.Printf("read error: %v", err)
			return
		}
		switch CapsuleType(typ) {
		case CapsulePadding:
			// PADDING has no semantic value; consume and ignore it.
			padding, err := io.ReadAll(content)
			if err != nil {
				s.log.Printf("read error: %v", err)
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
					s.incomingStreams <- stream
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
					s.incomingUnidirectionalStreams <- stream
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
				s.log.Printf("read error: %v", err)
				return
			}
			s.log.Printf("received capsule %s len=%d", CapsuleType(typ), len(payload))
			s.deliverDatagram(payload)
		default:
			s.log.Printf("received capsule %s", CapsuleType(typ))
			_, _ = io.Copy(io.Discard, content)
		}
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
	stream := newStream(s, s.nextStreamID(true))
	s.log.Printf("open stream bidi=true id=%d", stream.ID)
	s.streams[stream.ID] = stream
	return stream, nil
}

func (s *Session) OpenUnidirectionalStream() (*SendStream, error) {
	stream := newSendStream(s, s.nextStreamID(false))
	s.log.Printf("open stream bidi=false id=%d", stream.ID)
	s.sendStreams[stream.ID] = stream
	return stream, nil
}

func (s *Session) AcceptStream(ctx context.Context) (*Stream, error) {
	select {
	case stream := <-s.incomingStreams:
		s.log.Printf("accepted stream id=%d", stream.ID)
		return stream, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Session) AcceptUnidirectionalStream(ctx context.Context) (*ReceiveStream, error) {
	select {
	case stream := <-s.incomingUnidirectionalStreams:
		return stream, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Session) writeCapsule(typ uint64, data []byte) (err error) {
	s.log.Printf("sent capsule %s len=%d", CapsuleType(typ), len(data))
	return http3.WriteCapsule(s.writer, http3.CapsuleType(typ), data)
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
