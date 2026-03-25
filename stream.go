package wth2

import (
	"io"

	"github.com/quic-go/quic-go/quicvarint"
)

type Stream struct {
	ID uint64

	// TODO: Lock
	closed bool

	session    *Session
	pipeWriter io.WriteCloser
	pipeReader io.ReadCloser
}

func newStream(session *Session, id uint64) *Stream {
	pipeReader, pipeWriter := io.Pipe()
	return &Stream{
		ID:         id,
		session:    session,
		pipeWriter: pipeWriter,
		pipeReader: pipeReader,
	}
}

func (s *Stream) Write(p []byte) (n int, err error) {
	if s.closed {
		return 0, io.ErrClosedPipe
	}

	s.session.log.Printf("[stream %v] writing %d bytes", s.ID, len(p))

	capsuleData := make([]byte, quicvarint.Len(s.ID)+len(p))
	quicvarint.Append(capsuleData, s.ID)
	copy(capsuleData[quicvarint.Len(s.ID):], p)
	return len(p), s.session.writeCapsule(uint64(CapsuleWTStream), capsuleData)
}

func (s *Stream) receiveStreamData(data io.Reader) (err error) {
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

func (s *Stream) Read(p []byte) (n int, err error) {
	s.session.log.Printf("[stream %v] waiting for next read..", s.ID)
	return s.pipeReader.Read(p)
}

func (s *Stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.pipeReader.Close()

	s.session.log.Printf("[stream %v] closing stream", s.ID)
	capsuleData := make([]byte, quicvarint.Len(s.ID))
	quicvarint.Append(capsuleData, s.ID)
	return s.session.writeCapsule(uint64(CapsuleWTStreamFin), capsuleData)
}

type ReceiveStream struct {
	ID uint64

	session *Session
}

type SendStream struct {
	ID uint64

	session *Session
}
