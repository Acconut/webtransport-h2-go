// Command demoserver hosts the echo and Devious Baton endpoints on one TLS address.
//
//	go run ./cmd/demoserver -addr :443 -cert cert.pem -key key.pem
//
// Clients then use:
//
//	https://<host>/webtransport/echo
//	https://<host>/webtransport/devious-baton
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"

	"golang.org/x/net/http2"

	"github.com/Acconut/webtransport-h2-go/internal/baton"
	"github.com/Acconut/webtransport-h2-go/internal/echo"
	"github.com/Acconut/webtransport-h2-go/internal/serve"
	"github.com/Acconut/webtransport-h2-go/internal/tlsx"
)

var release = "unknown"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)

	addr := flag.String("addr", "localhost:4433", "TLS listen address")
	certFile := flag.String("cert", "", "TLS certificate PEM (optional with -selfsigned)")
	keyFile := flag.String("key", "", "TLS private key PEM (optional with -selfsigned)")
	selfsigned := flag.Bool("selfsigned", false, "use an ephemeral self-signed certificate")
	maxCount := flag.Int("max-count", 16, "reject Devious Baton count query param above this")
	padding := flag.Int("padding", 0, "padding bytes in baton messages this server sends")
	flag.Parse()

	cert, err := tlsx.LoadCertificate(*certFile, *keyFile, *selfsigned)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle(echo.Path, echo.Handler())
	mux.Handle(baton.Path, baton.Handler(*maxCount, *padding))
	mux.HandleFunc("GET /release", handleRelease)

	server, err := newServer(cert, mux)
	if err != nil {
		log.Fatal(err)
	}
	if err := serve.UntilSignal(*addr, server, func(a net.Addr) {
		log.Printf("echo https://%s%s", a, echo.Path)
		log.Printf("devious-baton https://%s%s", a, baton.Path)
	}); err != nil {
		log.Fatal(err)
	}
}

func handleRelease(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, release)
}

func newServer(cert tls.Certificate, handler http.Handler) (*http.Server, error) {
	server := &http.Server{
		Handler: handler,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2"},
		},
	}
	if err := http2.ConfigureServer(server, &http2.Server{
		WebTransport: http2.DefaultWebTransportSettings(),
	}); err != nil {
		return nil, err
	}
	return server, nil
}
