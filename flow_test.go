package wth2

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/quicvarint"
)

func TestLocalStreamsCopyPeerSendLimits(t *testing.T) {
	peer := &peerInitialLimits{
		maxData:              1000,
		maxStreamsUni:        2,
		maxStreamsBidi:       3,
		streamDataUni:        10,
		streamDataBidiLocal:  20,
		streamDataBidiRemote: 30,
	}
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		writer.Close()
		reader.Close()
	})
	session := newSessionWithPeerLimits(reader, io.Discard, "test", false, peer)
	t.Cleanup(session.Close)

	bidi, err := session.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	uni, err := session.OpenUnidirectionalStream()
	if err != nil {
		t.Fatal(err)
	}
	bidi2, err := session.OpenStream()
	if err != nil {
		t.Fatal(err)
	}

	if got := streamSendLimit(t, session, bidi.ID); got != 30 {
		t.Fatalf("local bidi limit = %d, want 30 (peer BIDI_REMOTE)", got)
	}
	if got := streamSendLimit(t, session, bidi2.ID); got != 30 {
		t.Fatalf("second local bidi limit = %d, want 30", got)
	}
	if got := streamSendLimit(t, session, uni.ID); got != 10 {
		t.Fatalf("local uni limit = %d, want 10 (peer UNI)", got)
	}
	session.limits.mu.Lock()
	defer session.limits.mu.Unlock()
	if session.limits.maxData != 1000 || session.limits.maxStreamsBidi != 3 || session.limits.maxStreamsUni != 2 {
		t.Fatalf("session limits = data %d bidi %d uni %d, want 1000, 3, 2",
			session.limits.maxData, session.limits.maxStreamsBidi, session.limits.maxStreamsUni)
	}
}

func TestPeerBidiStreamCopiesLocalSendLimit(t *testing.T) {
	peer := &peerInitialLimits{
		streamDataBidiLocal:  20,
		streamDataBidiRemote: 30,
		streamDataUni:        10,
	}
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		writer.Close()
		reader.Close()
	})
	// Server receives a client-initiated bidirectional stream (id 0).
	session := newSessionWithPeerLimits(reader, io.Discard, "test", true, peer)
	t.Cleanup(session.Close)

	payload := quicvarint.Append(nil, 0)
	payload = append(payload, 'x')
	if err := http3.WriteCapsule(quicvarint.NewWriter(writer), http3.CapsuleType(CapsuleWTStream), payload); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := session.AcceptStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stream.ID != 0 {
		t.Fatalf("stream id = %d, want 0", stream.ID)
	}
	if got := streamSendLimit(t, session, stream.ID); got != 20 {
		t.Fatalf("peer bidi limit = %d, want 20 (peer BIDI_LOCAL)", got)
	}
}

func TestSessionWithoutPeerLimitsDoesNotCopy(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		writer.Close()
		reader.Close()
	})
	session := newSession(reader, io.Discard, "test", false)
	t.Cleanup(session.Close)
	stream, err := session.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	session.limits.mu.Lock()
	defer session.limits.mu.Unlock()
	if session.limits.active {
		t.Fatal("in-memory session should not enforce peer limits")
	}
	if _, ok := session.limits.streamMax[stream.ID]; ok {
		t.Fatal("stream limit was copied without peer SETTINGS")
	}
}

func streamSendLimit(t *testing.T, s *Session, id uint64) uint64 {
	t.Helper()
	s.limits.mu.Lock()
	defer s.limits.mu.Unlock()
	got, ok := s.limits.streamMax[id]
	if !ok {
		t.Fatalf("no send limit for stream %d", id)
	}
	return got
}
