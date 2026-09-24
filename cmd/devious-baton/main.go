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
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/net/http2"

	wth2 "github.com/Acconut/webtransport-h2-go"
	"github.com/Acconut/webtransport-h2-go/internal/baton"
	"github.com/Acconut/webtransport-h2-go/internal/serve"
	"github.com/Acconut/webtransport-h2-go/internal/tlsx"
)

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
	fmt.Fprintf(os.Stderr, `Usage: devious-baton <command> [flags]

Commands:
  serve      Run a Devious Baton WebTransport server
  client     Connect to a Devious Baton endpoint
  selftest   In-process server+client exchange (no external peer)

Examples:
  go run ./cmd/devious-baton serve -addr localhost:4433
  go run ./cmd/devious-baton client -url 'https://localhost:4433/webtransport/devious-baton?baton=42&count=1' -insecure
  go run ./cmd/devious-baton selftest -baton 200 -count 1
`)
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "localhost:4433", "TLS listen address")
	certFile := fs.String("cert", "", "TLS certificate PEM (optional with -selfsigned)")
	keyFile := fs.String("key", "", "TLS private key PEM (optional with -selfsigned)")
	selfsigned := fs.Bool("selfsigned", false, "use an ephemeral self-signed certificate")
	maxCount := fs.Int("max-count", 16, "reject count query param above this")
	padding := fs.Int("padding", 0, "padding bytes in baton messages we send")
	_ = fs.Parse(args)

	cert, err := tlsx.LoadCertificate(*certFile, *keyFile, *selfsigned)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle(baton.Path, baton.Handler(*maxCount, *padding))

	server, err := newServer(cert, mux)
	if err != nil {
		return err
	}
	return serve.UntilSignal(*addr, server, func(a net.Addr) {
		log.Printf("listening on https://%s%s", a, baton.Path)
	})
}

func cmdClient(args []string) error {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	rawURL := fs.String("url", "", "WebTransport URL including path and query (required)")
	insecure := fs.Bool("insecure", false, "skip TLS certificate verification")
	batonValue := fs.Int("baton", 0, "initial baton (1–255); adds query param if url has none")
	count := fs.Int("count", 0, "baton count; adds query param if url has none")
	padding := fs.Int("padding", 0, "padding bytes in baton messages we send")
	_ = fs.Parse(args)

	if *rawURL == "" {
		return fmt.Errorf("-url is required")
	}
	u, err := url.Parse(*rawURL)
	if err != nil {
		return err
	}
	q := u.Query()
	if q.Get("baton") == "" && *batonValue != 0 {
		q.Set("baton", fmt.Sprintf("%d", *batonValue))
	}
	if q.Get("count") == "" && *count != 0 {
		q.Set("count", fmt.Sprintf("%d", *count))
	}
	u.RawQuery = q.Encode()

	cfg, err := baton.ParseQuery(u.Query())
	if err != nil {
		return err
	}
	cfg.Padding = *padding

	log.Printf("connecting to %s", u.String())
	session, reqBody, err := dial(&tls.Config{InsecureSkipVerify: *insecure}, u.String(), nil)
	if err != nil {
		return err
	}
	defer session.Close()
	defer reqBody.Close() // FIN CONNECT request body (session.Close does not)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return baton.Run(ctx, session, false, cfg)
}

func cmdSelftest(args []string) error {
	fs := flag.NewFlagSet("selftest", flag.ExitOnError)
	batonValue := fs.Int("baton", 200, "initial baton (1–255)")
	count := fs.Int("count", 1, "number of parallel batons")
	padding := fs.Int("padding", 0, "padding bytes in baton messages")
	_ = fs.Parse(args)

	if *batonValue < 1 || *batonValue > 255 {
		return fmt.Errorf("baton must be 1–255")
	}
	if *count < 1 {
		return fmt.Errorf("count must be >= 1")
	}

	cfg := baton.Config{
		Version: 0,
		Baton:   byte(*batonValue),
		Count:   *count,
		Padding: *padding,
	}

	cert, roots, err := tlsx.GenerateSelfSignedCert()
	if err != nil {
		return err
	}

	wtServer := &wth2.Server{}
	mux := http.NewServeMux()
	serverErr := make(chan error, 1)
	mux.HandleFunc(baton.Path, func(w http.ResponseWriter, r *http.Request) {
		qcfg, err := baton.ParseQuery(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		qcfg.Padding = cfg.Padding
		session, err := wtServer.Upgrade(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer session.Close()
		serverErr <- baton.Run(r.Context(), session, true, qcfg)
	})

	httpServer, err := newServer(cert, mux)
	if err != nil {
		return err
	}

	ln, err := tls.Listen("tcp", "localhost:0", httpServer.TLSConfig)
	if err != nil {
		return err
	}
	defer ln.Close()

	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("server: %v", err)
		}
	}()
	defer httpServer.Close()

	host := ln.Addr().(*net.TCPAddr).String()
	u := url.URL{
		Scheme:   "https",
		Host:     host,
		Path:     baton.Path,
		RawQuery: cfg.Query().Encode(),
	}

	log.Printf("selftest connect %s", u.String())
	session, reqBody, err := dial(&tls.Config{RootCAs: roots}, u.String(), nil)
	if err != nil {
		return fmt.Errorf("client connect: %w", err)
	}
	defer session.Close()
	defer reqBody.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	clientErr := make(chan error, 1)
	go func() {
		clientErr <- baton.Run(ctx, session, false, cfg)
	}()

	var cerr, serr error
	select {
	case cerr = <-clientErr:
		// FIN CONNECT send half so the server read loop sees EOF instead of RST.
		_ = reqBody.Close()
		serr = <-serverErr
	case serr = <-serverErr:
		_ = reqBody.Close()
		cerr = <-clientErr
	case <-ctx.Done():
		_ = reqBody.Close()
		return ctx.Err()
	}

	_ = httpServer.Shutdown(context.Background())

	if cerr != nil {
		return fmt.Errorf("client: %w", cerr)
	}
	if serr != nil {
		return fmt.Errorf("server: %w", serr)
	}
	log.Printf("selftest ok (baton=%d count=%d hops≈%d)", cfg.Baton, cfg.Count, 256-int(cfg.Baton))
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

func dial(tlsConfig *tls.Config, rawURL string, protocols []string) (*wth2.Session, io.Closer, error) {
	t := &http2.Transport{
		TLSClientConfig: tlsConfig,
		WebTransport:    http2.DefaultClientWebTransportSettings(),
	}
	client := &wth2.Client{RoundTripper: t}
	return client.Connect(rawURL, protocols, http.Header{})
}
