package wth2

import (
	"errors"
	"io"
	"sync"

	"github.com/quic-go/quic-go/quicvarint"
)

// TOOD: Make Stream implement net.Conn.
// TODO: Stream should have WT-specific methods, e.g. to send a padding capsule, tell the peer to stop sending, etc.
type Stream struct {
	ID uint64

	*ReceiveStream
	*SendStream

	session *Session
}

type ReceiveStream struct {
	ID uint64

	// TODO: Lock
	closed bool

	session *Session

	recvBuffer *streamReceiveBuffer
}

type SendStream struct {
	ID uint64

	// TODO: Lock
	closed bool

	session *Session
}

func newStream(session *Session, id uint64) *Stream {
	return &Stream{
		ID:            id,
		session:       session,
		ReceiveStream: newReceiveStream(session, id),
		SendStream:    newSendStream(session, id),
	}
}

func newReceiveStream(session *Session, id uint64) *ReceiveStream {
	return &ReceiveStream{
		ID:         id,
		session:    session,
		recvBuffer: newStreamReceiveBuffer(session.StreamReceiveBufferSize),
	}
}

func newSendStream(session *Session, id uint64) *SendStream {
	return &SendStream{
		ID:      id,
		session: session,
	}
}

func (s *SendStream) Write(p []byte) (n int, err error) {
	if s.closed {
		return 0, io.ErrClosedPipe
	}

	s.session.log.Printf("[stream %v] writing %d bytes", s.ID, len(p))

	capsuleData := quicvarint.Append(nil, s.ID)
	capsuleData = append(capsuleData, p...)
	return len(p), s.session.writeCapsule(uint64(CapsuleWTStream), capsuleData)
}

// WritePadding sends a WebTransport PADDING capsule with an all-zero payload of length n.
func (s *SendStream) WritePadding(n int) error {
	if s.closed {
		return io.ErrClosedPipe
	}
	if n < 0 {
		return errors.New("padding length must be non-negative")
	}
	padding := make([]byte, n)
	s.session.log.Printf("[stream %v] writing padding len=%d", s.ID, n)
	return s.session.writeCapsule(uint64(CapsulePadding), padding)
}

func (s *ReceiveStream) receiveStreamData(data io.Reader) (err error) {
	if s.closed {
		// Discard data if stream is closed
		_, _ = io.Copy(io.Discard, data)
		return nil
	}

	s.session.log.Printf("[stream %v] receiving data..", s.ID)
	payload, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	s.session.log.Printf("[stream %v] received data len=%d", s.ID, len(payload))
	_, err = s.recvBuffer.write(payload)
	return err
}

func (s *ReceiveStream) Read(p []byte) (n int, err error) {
	s.session.log.Printf("[stream %v] waiting for next read..", s.ID)
	return s.recvBuffer.read(p)
}

func (s *ReceiveStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.recvBuffer.close()

	return nil
}

func (s *ReceiveStream) finishReceive() {
	s.recvBuffer.close()
}

func (s *SendStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true

	s.session.log.Printf("[stream %v] closing stream", s.ID)
	capsuleData := quicvarint.Append(nil, s.ID)
	return s.session.writeCapsule(uint64(CapsuleWTStreamFin), capsuleData)
}

func (s *Stream) Close() error {
	s.ReceiveStream.Close()
	return s.SendStream.Close()
}

var errStreamReceiveBufferFull = errors.New("stream receive buffer full")

type streamReceiveBuffer struct {
	mu       sync.Mutex
	notEmpty *sync.Cond

	data   []byte
	closed bool

	maxBytes int
}

func newStreamReceiveBuffer(maxBytes int) *streamReceiveBuffer {
	b := &streamReceiveBuffer{maxBytes: maxBytes}
	b.notEmpty = sync.NewCond(&b.mu)
	return b
}

func (b *streamReceiveBuffer) write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return 0, io.ErrClosedPipe
	}
	if b.maxBytes > 0 && len(b.data)+len(p) > b.maxBytes {
		return 0, errStreamReceiveBufferFull
	}

	b.data = append(b.data, p...)
	b.notEmpty.Signal()
	return len(p), nil
}

func (b *streamReceiveBuffer) read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for len(b.data) == 0 && !b.closed {
		b.notEmpty.Wait()
	}
	if len(b.data) == 0 && b.closed {
		return 0, io.EOF
	}

	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

func (b *streamReceiveBuffer) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.notEmpty.Broadcast()
}
