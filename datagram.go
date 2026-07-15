package wth2

import (
	"context"
	"io"
	"sync"
)

type datagramReceiveBuffer struct {
	mu sync.Mutex

	queue         [][]byte
	bufferedBytes int
	closed        bool

	maxBytes int

	notify chan struct{}
}

func newDatagramReceiveBuffer(maxBytes int) *datagramReceiveBuffer {
	return &datagramReceiveBuffer{
		maxBytes: maxBytes,
		notify:   make(chan struct{}, 1),
	}
}

func (b *datagramReceiveBuffer) write(p []byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return false
	}
	if b.maxBytes > 0 && b.bufferedBytes+len(p) > b.maxBytes {
		return false
	}

	payload := append([]byte(nil), p...)
	b.queue = append(b.queue, payload)
	b.bufferedBytes += len(payload)

	select {
	case b.notify <- struct{}{}:
	default:
	}
	return true
}

func (b *datagramReceiveBuffer) read(ctx context.Context) ([]byte, error) {
	for {
		b.mu.Lock()
		if len(b.queue) > 0 {
			payload := b.queue[0]
			b.queue = b.queue[1:]
			b.bufferedBytes -= len(payload)
			b.mu.Unlock()
			return payload, nil
		}
		if b.closed {
			b.mu.Unlock()
			return nil, io.EOF
		}
		b.mu.Unlock()

		select {
		case <-b.notify:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (b *datagramReceiveBuffer) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true

	select {
	case b.notify <- struct{}{}:
	default:
	}
}
