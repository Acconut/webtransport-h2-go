package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"golang.org/x/net/http2"

	wth2 "github.com/Acconut/webtransport-h2-go"
	"github.com/Acconut/webtransport-h2-go/internal/serve"
	"github.com/Acconut/webtransport-h2-go/internal/tlsx"
)

const echoProtocol = "echo"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "client":
		err = cmdClient(os.Args[2:])
	case "selftest":
		err = cmdSelftest(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: echo <command> [flags]

Commands:
  serve      Run an echo WebTransport server
  client     Send one payload and assert the echo
  selftest   In-process server+client exchange (no external peer)

The client and selftest -mode flag selects the channel:
  bidi       Bidirectional stream (reply on the same stream)
  uni        Unidirectional stream (reply on a new unidirectional stream)
  datagram   Datagram (reply as a datagram)

Examples:
  go run ./cmd/echo serve -addr localhost:4433 -selfsigned
  go run ./cmd/echo client -url https://localhost:4433/webtransport/echo -insecure -mode uni
  go run ./cmd/echo selftest -mode datagram
`)
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "localhost:4433", "TLS listen address")
	path := fs.String("path", "/webtransport/echo", "URL path that accepts WebTransport CONNECT")
	certFile := fs.String("cert", "", "TLS certificate PEM (optional with -selfsigned)")
	keyFile := fs.String("key", "", "TLS private key PEM (optional with -selfsigned)")
	selfsigned := fs.Bool("selfsigned", false, "use an ephemeral self-signed certificate")
	_ = fs.Parse(args)

	cert, err := tlsx.LoadCertificate(*certFile, *keyFile, *selfsigned)
	if err != nil {
		return err
	}

	server, err := newServer(cert, echoHandler(*path))
	if err != nil {
		return err
	}
	return serve.UntilSignal(*addr, server, func(a net.Addr) {
		log.Printf("listening on https://%s%s", a, *path)
	})
}

func cmdClient(args []string) error {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	rawURL := fs.String("url", "", "WebTransport URL (required)")
	insecure := fs.Bool("insecure", false, "skip TLS certificate verification")
	modeFlag := fs.String("mode", "bidi", "channel: bidi, uni, or datagram")
	data := fs.String("data", "Hello, world!", "payload to send and expect back")
	timeout := fs.Duration("timeout", 15*time.Second, "time to wait for the echo")
	_ = fs.Parse(args)

	if *rawURL == "" {
		return fmt.Errorf("-url is required")
	}
	mode := echoMode(*modeFlag)

	tlsConfig := &tls.Config{InsecureSkipVerify: *insecure}
	session, reqBody, err := dial(tlsConfig, *rawURL)
	if err != nil {
		return err
	}
	defer session.Close()
	defer reqBody.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	log.Printf("client %s -> %s (%d bytes)", mode, *rawURL, len(*data))
	return runEchoClient(ctx, session, mode, []byte(*data))
}

func cmdSelftest(args []string) error {
	fs := flag.NewFlagSet("selftest", flag.ExitOnError)
	modeFlag := fs.String("mode", "bidi", "channel: bidi, uni, or datagram")
	data := fs.String("data", "Hello, world!", "payload to send and expect back")
	timeout := fs.Duration("timeout", 15*time.Second, "time to wait for the echo")
	_ = fs.Parse(args)

	mode := echoMode(*modeFlag)

	cert, roots, err := tlsx.GenerateSelfSignedCert()
	if err != nil {
		return err
	}

	server, err := newServer(cert, echoHandler("/webtransport/echo"))
	if err != nil {
		return err
	}

	ln, err := tls.Listen("tcp", "localhost:0", server.TLSConfig)
	if err != nil {
		return err
	}
	defer ln.Close()

	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("server: %v", err)
		}
	}()
	defer server.Close()

	host := ln.Addr().(*net.TCPAddr).String()
	rawURL := "https://" + host + "/webtransport/echo"
	log.Printf("selftest %s %s (%d bytes)", mode, rawURL, len(*data))

	session, reqBody, err := dial(&tls.Config{RootCAs: roots}, rawURL)
	if err != nil {
		return fmt.Errorf("client connect: %w", err)
	}
	defer session.Close()
	defer reqBody.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	err = runEchoClient(ctx, session, mode, []byte(*data))
	// FIN the CONNECT stream so the server read loop unblocks.
	_ = reqBody.Close()
	_ = server.Shutdown(context.Background())
	if err != nil {
		return err
	}
	log.Printf("selftest ok (%s, %d bytes)", mode, len(*data))
	return nil
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

func dial(tlsConfig *tls.Config, rawURL string) (*wth2.Session, io.Closer, error) {
	t := &http2.Transport{
		TLSClientConfig: tlsConfig,
		WebTransport:    http2.DefaultClientWebTransportSettings(),
	}
	client := &wth2.Client{RoundTripper: t}
	return client.Connect(rawURL, []string{echoProtocol}, http.Header{})
}

func echoHandler(path string) http.Handler {
	wtServer := &wth2.Server{
		SelectProtocol: func(r *http.Request, available []string) (string, error) {
			if slices.Contains(available, echoProtocol) {
				return echoProtocol, nil
			}
			return "", nil
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		session, err := wtServer.Upgrade(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer session.Close()
		log.Printf("session protocol=%q", session.Protocol)
		runEchoServer(r.Context(), session)
	})
	return mux
}
