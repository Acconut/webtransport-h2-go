// Package echo is an echo endpoint for WebTransport over HTTP/2.
package echo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	wth2 "github.com/Acconut/webtransport-h2-go"
)

const (
	// Path is the URL path the demo server uses for this endpoint.
	Path = "/webtransport/echo"
	// Protocol is offered and selected as the WebTransport application protocol.
	Protocol = "echo"
)

// Mode selects how the client sends a payload.
type Mode string

const (
	ModeBidi     Mode = "bidi"
	ModeUni      Mode = "uni"
	ModeDatagram Mode = "datagram"
)

// runEchoServer echoes whatever the peer sends:
//   - bidirectional stream: same bytes on that stream, then FIN
//   - unidirectional stream: same bytes on a new server-initiated unidirectional stream
//   - datagram: same payload as a datagram
func runEchoServer(ctx context.Context, session *wth2.Session) {
	var openMu sync.Mutex
	var wg sync.WaitGroup
	wg.Go(func() { echoBidiLoop(ctx, session) })
	wg.Go(func() { echoUniLoop(ctx, session, &openMu) })
	wg.Go(func() { echoDatagramLoop(ctx, session) })

	select {
	case <-ctx.Done():
	case <-session.Done():
	}
	wg.Wait()
}

func echoBidiLoop(ctx context.Context, session *wth2.Session) {
	for {
		stream, err := session.AcceptStream(ctx)
		if err != nil {
			logEchoStop("bidi accept", err)
			return
		}
		go func() {
			if err := echoBidi(session, stream); err != nil {
				log.Printf("echo bidi id=%d: %v", stream.ID, err)
			}
		}()
	}
}

func echoBidi(session *wth2.Session, stream *wth2.Stream) error {
	payload, err := io.ReadAll(stream)
	if err != nil {
		return err
	}
	log.Printf("echo bidi id=%d %d bytes", stream.ID, len(payload))
	if _, err := stream.Write(payload); err != nil {
		return err
	}
	if err := stream.Close(); err != nil {
		return err
	}
	session.Flush()
	return nil
}

func echoUniLoop(ctx context.Context, session *wth2.Session, openMu *sync.Mutex) {
	for {
		stream, err := session.AcceptUnidirectionalStream(ctx)
		if err != nil {
			logEchoStop("uni accept", err)
			return
		}
		go func() {
			if err := echoUni(session, stream, openMu); err != nil {
				log.Printf("echo uni id=%d: %v", stream.ID, err)
			}
		}()
	}
}

func echoUni(session *wth2.Session, in *wth2.ReceiveStream, openMu *sync.Mutex) error {
	payload, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	openMu.Lock()
	out, err := session.OpenUnidirectionalStream()
	openMu.Unlock()
	if err != nil {
		return err
	}
	log.Printf("echo uni in=%d out=%d %d bytes", in.ID, out.ID, len(payload))
	if _, err := out.Write(payload); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	session.Flush()
	return nil
}

func echoDatagramLoop(ctx context.Context, session *wth2.Session) {
	for {
		payload, err := session.ReceiveDatagram(ctx)
		if err != nil {
			logEchoStop("datagram", err)
			return
		}
		log.Printf("echo datagram %d bytes", len(payload))
		if err := session.SendDatagram(payload); err != nil {
			log.Printf("echo datagram: %v", err)
			return
		}
		session.Flush()
	}
}

func logEchoStop(what string, err error) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, wth2.ErrSessionClosed) || errors.Is(err, io.EOF) {
		return
	}
	log.Printf("echo %s: %v", what, err)
}

// runEchoClient sends payload on the selected channel and checks the echo.
func RunClient(ctx context.Context, session *wth2.Session, mode Mode, payload []byte) error {
	var got []byte
	var err error
	switch mode {
	case ModeBidi:
		got, err = exchangeBidi(ctx, session, payload)
	case ModeUni:
		got, err = exchangeUni(ctx, session, payload)
	case ModeDatagram:
		got, err = exchangeDatagram(ctx, session, payload)
	default:
		return fmt.Errorf("mode must be bidi, uni, or datagram")
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(payload, got) {
		return fmt.Errorf("echo mismatch (%s): sent %d bytes %q, got %d bytes %q", mode, len(payload), payload, len(got), got)
	}
	log.Printf("echo ok (%s, %d bytes)", mode, len(payload))
	return nil
}

func exchangeBidi(ctx context.Context, session *wth2.Session, payload []byte) ([]byte, error) {
	stream, err := session.OpenStream()
	if err != nil {
		return nil, err
	}
	if _, err := stream.Write(payload); err != nil {
		return nil, err
	}
	// FIN the send half so the server's read completes, and keep the receive half open.
	if err := stream.SendStream.Close(); err != nil {
		return nil, err
	}
	session.Flush()

	done := make(chan struct{})
	var got []byte
	var readErr error
	go func() {
		got, readErr = io.ReadAll(stream)
		close(done)
	}()
	select {
	case <-done:
		return got, readErr
	case <-ctx.Done():
		_ = stream.ReceiveStream.Close()
		return nil, ctx.Err()
	}
}

func exchangeUni(ctx context.Context, session *wth2.Session, payload []byte) ([]byte, error) {
	out, err := session.OpenUnidirectionalStream()
	if err != nil {
		return nil, err
	}
	if _, err := out.Write(payload); err != nil {
		return nil, err
	}
	if err := out.Close(); err != nil {
		return nil, err
	}
	session.Flush()

	in, err := session.AcceptUnidirectionalStream(ctx)
	if err != nil {
		return nil, err
	}
	return readAllContext(ctx, in)
}

func exchangeDatagram(ctx context.Context, session *wth2.Session, payload []byte) ([]byte, error) {
	if err := session.SendDatagram(payload); err != nil {
		return nil, err
	}
	session.Flush()
	return session.ReceiveDatagram(ctx)
}

func readAllContext(ctx context.Context, r io.Reader) ([]byte, error) {
	done := make(chan struct{})
	var got []byte
	var err error
	go func() {
		got, err = io.ReadAll(r)
		close(done)
	}()
	select {
	case <-done:
		return got, err
	case <-ctx.Done():
		if c, ok := r.(io.Closer); ok {
			_ = c.Close()
		}
		return nil, ctx.Err()
	}
}
