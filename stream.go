package wth2

import (
	"io"

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

	pipeWriter io.WriteCloser
	pipeReader io.ReadCloser
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
	pipeReader, pipeWriter := io.Pipe()

	return &ReceiveStream{
		ID:         id,
		session:    session,
		pipeWriter: pipeWriter,
		pipeReader: pipeReader,
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

func (s *ReceiveStream) receiveStreamData(data io.Reader) (err error) {
	if s.closed {
		// Discard data if stream is closed
		io.Copy(io.Discard, data)
		return nil
	}

	s.session.log.Printf("[stream %v] receiving data..", s.ID)
	n, err := io.Copy(s.pipeWriter, data)
	s.session.log.Printf("[stream %v] received data len=%d", s.ID, n)
	return err
}

func (s *ReceiveStream) Read(p []byte) (n int, err error) {
	s.session.log.Printf("[stream %v] waiting for next read..", s.ID)
	return s.pipeReader.Read(p)
}

func (s *ReceiveStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.pipeReader.Close()

	return nil
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
