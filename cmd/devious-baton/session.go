package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"

	wth2 "github.com/Acconut/webtransport-h2-go"
)

type streamKind int

const (
	kindUni streamKind = iota
	kindPeerBidi
	kindSelfBidi
	kindDatagram
)

func (k streamKind) String() string {
	switch k {
	case kindUni:
		return "uni"
	case kindPeerBidi:
		return "peer-bidi"
	case kindSelfBidi:
		return "self-bidi"
	case kindDatagram:
		return "datagram"
	default:
		return "unknown"
	}
}

// batonSession runs the Devious Baton exchange on a WebTransport session.
type batonSession struct {
	sess     *wth2.Session
	isServer bool
	cfg      batonConfig
	log      *log.Logger

	openMu sync.Mutex // serialize Open* until the library locks stream maps

	active atomic.Int64
	inflight sync.WaitGroup // handlers still reading/writing capsules
	done   chan struct{}
	errMu  sync.Mutex
	err    error
}

func runBaton(ctx context.Context, sess *wth2.Session, isServer bool, cfg batonConfig) error {
	prefix := "[baton-client] "
	if isServer {
		prefix = "[baton-server] "
	}
	b := &batonSession{
		sess:     sess,
		isServer: isServer,
		cfg:      cfg,
		log:      log.New(log.Writer(), prefix, log.LstdFlags),
		done:     make(chan struct{}),
	}
	b.active.Store(int64(cfg.Count))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go b.acceptBidiLoop(ctx)
	go b.acceptUniLoop(ctx)
	go b.datagramLoop(ctx)

	if isServer {
		if err := b.setup(); err != nil {
			b.fail(err)
		}
	}

	select {
	case <-b.done:
	case <-ctx.Done():
		b.fail(ctx.Err())
	}

	// Stop accepting and wait for in-flight baton handlers to finish writing
	// before returning (HTTP/2 panics on Write after the handler returns).
	cancel()
	b.inflight.Wait()
	sess.Close()

	b.errMu.Lock()
	defer b.errMu.Unlock()
	if b.err != nil && !errors.Is(b.err, context.Canceled) {
		return b.err
	}
	return nil
}

func (b *batonSession) track(fn func()) {
	b.inflight.Add(1)
	go func() {
		defer b.inflight.Done()
		fn()
	}()
}

func (b *batonSession) setup() error {
	baton := b.cfg.Baton
	if baton == 0 {
		var buf [1]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return err
		}
		baton = buf[0]
		if baton == 0 {
			baton = 1
		}
		b.cfg.Baton = baton
	}
	b.log.Printf("setup: count=%d baton=%d padding=%d", b.cfg.Count, baton, b.cfg.Padding)
	for i := 0; i < b.cfg.Count; i++ {
		if err := b.sendUni(baton); err != nil {
			return err
		}
	}
	return nil
}

func (b *batonSession) acceptBidiLoop(ctx context.Context) {
	for {
		stream, err := b.sess.AcceptStream(ctx)
		if err != nil {
			return
		}
		b.track(func() { b.handleIncoming(stream, kindPeerBidi) })
	}
}

func (b *batonSession) acceptUniLoop(ctx context.Context) {
	for {
		stream, err := b.sess.AcceptUnidirectionalStream(ctx)
		if err != nil {
			return
		}
		b.track(func() { b.handleIncoming(stream, kindUni) })
	}
}

func (b *batonSession) datagramLoop(ctx context.Context) {
	for {
		payload, err := b.sess.ReceiveDatagram(ctx)
		if err != nil {
			return
		}
		b.track(func() { b.handleDatagram(payload) })
	}
}

type readerCloser interface {
	io.Reader
	io.Closer
}

func (b *batonSession) handleIncoming(r readerCloser, kind streamKind) {
	data, err := io.ReadAll(r)
	if err != nil {
		b.fail(fmt.Errorf("read %s: %w", kind, err))
		return
	}
	_, baton, err := decodeBaton(data)
	if err != nil {
		b.fail(fmt.Errorf("%w (BRUH): %v", errCode(errBRUH), err))
		return
	}
	b.log.Printf("recv %s baton=%d (%d bytes)", kind, baton, len(data))
	b.afterReceive(baton, data, kind, r)
}

func (b *batonSession) handleDatagram(payload []byte) {
	// Mirror datagrams exercise the DATAGRAM path; advancing protocol state from
	// them would fork the baton chain (identical message already arrives on a stream).
	_, baton, err := decodeBaton(payload)
	if err != nil {
		b.fail(fmt.Errorf("%w (BRUH): %v", errCode(errBRUH), err))
		return
	}
	b.log.Printf("recv datagram baton=%d (%d bytes; ignored for progression)", baton, len(payload))
}

func (b *batonSession) afterReceive(baton byte, raw []byte, kind streamKind, src readerCloser) {
	// Side-path datagram mirror (§4.4).
	if b.isServer && baton%7 == 0 {
		if err := b.sess.SendDatagram(raw); err != nil {
			b.log.Printf("send datagram: %v", err)
		} else {
			b.sess.Flush()
			b.log.Printf("sent datagram mirror baton=%d", baton)
		}
	}
	if !b.isServer && baton%7 == 1 {
		if err := b.sess.SendDatagram(raw); err != nil {
			b.log.Printf("send datagram: %v", err)
		} else {
			b.sess.Flush()
			b.log.Printf("sent datagram mirror baton=%d", baton)
		}
	}

	if baton == 0 {
		b.completeOne()
		if kind == kindPeerBidi {
			if s, ok := src.(*wth2.Stream); ok {
				_ = s.Close()
			}
		}
		return
	}

	next := baton + 1 // wraps at 256 → 0
	if err := b.sendNext(next, kind, src); err != nil {
		b.fail(err)
	}
}

func (b *batonSession) sendNext(baton byte, from streamKind, src readerCloser) error {
	msg := encodeBaton(make([]byte, b.cfg.Padding), baton)

	var err error
	switch from {
	case kindUni, kindDatagram:
		err = b.sendOnNewBidi(msg, baton)
	case kindPeerBidi:
		stream, ok := src.(*wth2.Stream)
		if !ok {
			return fmt.Errorf("peer-bidi source is not a bidirectional stream")
		}
		b.log.Printf("send peer-bidi baton=%d", baton)
		if _, err = stream.Write(msg); err != nil {
			return err
		}
		if err = stream.Close(); err != nil {
			return err
		}
		b.sess.Flush()
	case kindSelfBidi:
		err = b.sendUniMsg(msg, baton)
	default:
		return fmt.Errorf("unknown receive kind %v", from)
	}
	if err != nil {
		return err
	}
	// Sender of baton 0 has completed the exchange (§4.5).
	if baton == 0 {
		b.completeOne()
	}
	return nil
}

func (b *batonSession) sendOnNewBidi(msg []byte, baton byte) error {
	b.openMu.Lock()
	stream, err := b.sess.OpenStream()
	b.openMu.Unlock()
	if err != nil {
		return fmt.Errorf("%w (DA_YAMN): %v", errCode(errDAYAMN), err)
	}
	b.log.Printf("send self-bidi id=%d baton=%d", stream.ID, baton)
	if _, err := stream.Write(msg); err != nil {
		return err
	}
	if err := stream.SendStream.Close(); err != nil {
		return err
	}
	b.sess.Flush()
	if baton == 0 {
		// Terminal message: peer will not reply on this stream.
		_ = stream.ReceiveStream.Close()
		return nil
	}
	// Keep receive side open for the peer's reply on this stream.
	b.track(func() { b.handleIncoming(stream, kindSelfBidi) })
	return nil
}

func (b *batonSession) sendUni(baton byte) error {
	msg := encodeBaton(make([]byte, b.cfg.Padding), baton)
	return b.sendUniMsg(msg, baton)
}

func (b *batonSession) sendUniMsg(msg []byte, baton byte) error {
	b.openMu.Lock()
	stream, err := b.sess.OpenUnidirectionalStream()
	b.openMu.Unlock()
	if err != nil {
		return fmt.Errorf("%w (DA_YAMN): %v", errCode(errDAYAMN), err)
	}
	b.log.Printf("send uni id=%d baton=%d", stream.ID, baton)
	if _, err := stream.Write(msg); err != nil {
		return err
	}
	if err := stream.Close(); err != nil {
		return err
	}
	b.sess.Flush()
	return nil
}

func (b *batonSession) completeOne() {
	n := b.active.Add(-1)
	b.log.Printf("baton complete; active=%d", n)
	if n <= 0 {
		b.finish(nil)
	}
}

func (b *batonSession) fail(err error) {
	// Ignore write failures once the exchange is already done; another baton
	// may still be flushing while active has already reached zero.
	select {
	case <-b.done:
		b.log.Printf("ignoring error after done: %v", err)
		return
	default:
	}
	b.finish(err)
}

func (b *batonSession) finish(err error) {
	b.errMu.Lock()
	if b.err == nil && err != nil {
		b.err = err
		b.log.Printf("session error: %v", err)
	}
	b.errMu.Unlock()
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

type errCode uint32

func (e errCode) Error() string {
	switch uint32(e) {
	case errDAYAMN:
		return "DA_YAMN"
	case errBRUH:
		return "BRUH"
	case errSUS:
		return "SUS"
	case errBORED:
		return "BORED"
	default:
		return fmt.Sprintf("session error 0x%x", uint32(e))
	}
}
