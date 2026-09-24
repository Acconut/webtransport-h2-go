// Package serve runs an HTTP server on TLS until the process is interrupted.
package serve

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// UntilSignal listens on addr with server.TLSConfig until SIGINT or SIGTERM, then shuts down.
// onListen, when set, is called with the bound address before accepting.
func UntilSignal(addr string, server *http.Server, onListen func(net.Addr)) error {
	ln, err := tls.Listen("tcp", addr, server.TLSConfig)
	if err != nil {
		return err
	}
	if onListen != nil {
		onListen(ln.Addr())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
