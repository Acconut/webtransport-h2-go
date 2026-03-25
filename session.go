package wth2

import (
	"context"
	"errors"
	"io"
	"log"
	"sync/atomic"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/quicvarint"
)

type Session struct {
	Protocol string

	// Client-initiated streams use even IDs, server-initiated streams use odd IDs.
	isServer bool

	log    *log.Logger
	reader quicvarint.Reader
	writer quicvarint.Writer

	stop                          atomic.Bool
	incomingStreams               chan *Stream
	incomingUnidirectionalStreams chan *ReceiveStream

	// TODO: Lock
	streams        map[uint64]*Stream        // Bidirectional streams.
	receiveStreams map[uint64]*ReceiveStream // Peer-initiated, unidirectional streams.
	sendStreams    map[uint64]*SendStream    // Self-initiated, unidirectional streams.

	createdStreamCounter uint64
}

func newSession(reader io.Reader, writer io.Writer, protocol string, isServer bool) *Session {
	prefix := "[client] "
	if isServer {
		prefix = "[server] "
	}
	session := &Session{
		Protocol:                      protocol,
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

	go session.readLoop()

	return session
}

func (s *Session) Close() {
	s.stop.Store(true)
}

func (s *Session) readLoop() {
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
						stream.pipeWriter.Close()
					}
				} else if !s.isOurStream(id) {
					// New peer-initiated bidirectional stream -> accept
					stream := newStream(s, id)
					s.streams[id] = stream
					s.incomingStreams <- stream
					stream.receiveStreamData(content)
					if err != nil {
						s.log.Printf("read error: %v", err)
						return
					}
					if CapsuleType(typ) == CapsuleWTStreamFin {
						stream.pipeWriter.Close()
					}
				} else {
					// Peer initiated stream with our ID -> reject
					s.log.Printf("dropping peer-initiated stream with our ID: %d", id)
					io.Copy(io.Discard, s.reader)
				}
			} else {
				panic("unimplemented")
				// if stream, ok := s.receiveStreams[id]; ok {
				// 	// Provide data to stream
				// } else if !s.isOurStream(id) {
				// 	// New peer-initiated unidirectional stream -> accept
				// } else {
				// 	// Peer initiated stream with our ID -> reject
				// }
			}
		default:
			s.log.Printf("received capsule %s", CapsuleType(typ))
			_, _ = io.Copy(io.Discard, content)
		}
	}
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

func (s *Session) nextStreamID(unidirectional bool) uint64 {
	baseID := s.createdStreamCounter

	if !unidirectional {
		baseID |= 0b10
	}

	if s.isServer {
		baseID |= 0b01
	}

	s.createdStreamCounter += 4
	return baseID
}

// TODO: Don't return io.ReadWriteCloser; return Stream.
func (s *Session) OpenStream() (*Stream, error) {
	stream := newStream(s, s.nextStreamID(true))
	s.log.Printf("open stream id=%d", stream.ID)
	s.streams[stream.ID] = stream
	return stream, nil
}

func (s *Session) OpenUnidirectionalStream() (io.WriteCloser, error) {
	return nil, nil
}

func (s *Session) AcceptStream(ctx context.Context) (io.ReadWriteCloser, error) {
	select {
	case stream := <-s.incomingStreams:
		s.log.Printf("accepted stream id=%d", stream.ID)
		return stream, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// func (s *Session) AcceptUnidirectionalStream(ctx context.Context) (io.ReadCloser, error) {
// 	select {
// 	case stream := <-s.incomingUnidirectionalStreams:
// 		return stream, nil
// 	case <-ctx.Done():
// 		return nil, ctx.Err()
// 	}
// }

func (s *Session) writeCapsule(typ uint64, data []byte) (err error) {
	s.log.Printf("sent capsule %s len=%d", CapsuleType(typ), len(data))
	return http3.WriteCapsule(s.writer, http3.CapsuleType(typ), data)
}
