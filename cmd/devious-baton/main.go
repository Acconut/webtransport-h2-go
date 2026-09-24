package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
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

	var (
		cert tls.Certificate
		err  error
	)
	switch {
	case *certFile != "" && *keyFile != "":
		cert, err = tls.LoadX509KeyPair(*certFile, *keyFile)
		if err != nil {
			return err
		}
	case *selfsigned || (*certFile == "" && *keyFile == ""):
		cert, _, err = generateSelfSignedCert()
		if err != nil {
			return err
		}
		log.Printf("using ephemeral self-signed certificate")
	default:
		return fmt.Errorf("provide both -cert and -key, or use -selfsigned")
	}

	wtServer := &wth2.Server{}

	mux := http.NewServeMux()
	mux.HandleFunc(batonPath, func(w http.ResponseWriter, r *http.Request) {
		cfg, err := parseBatonQuery(r.URL.Query())
		if err != nil {
			rejectBadRequest(w, err.Error())
			return
		}
		if cfg.Count > *maxCount {
			rejectBadRequest(w, fmt.Sprintf("count %d exceeds server max %d", cfg.Count, *maxCount))
			return
		}
		cfg.Padding = *padding

		session, err := wtServer.Upgrade(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer session.Close()

		ctx := r.Context()
		if err := runBaton(ctx, session, true, cfg); err != nil {
			log.Printf("baton session: %v", err)
		}
	})

	server := &http.Server{
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2"},
		},
	}
	if err := http2.ConfigureServer(server, &http2.Server{
		WebTransport: http2.DefaultWebTransportSettings(),
	}); err != nil {
		return err
	}

	ln, err := tls.Listen("tcp", *addr, server.TLSConfig)
	if err != nil {
		return err
	}
	log.Printf("listening on https://%s%s", ln.Addr(), batonPath)

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

func cmdClient(args []string) error {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	rawURL := fs.String("url", "", "WebTransport URL including path and query (required)")
	insecure := fs.Bool("insecure", false, "skip TLS certificate verification")
	baton := fs.Int("baton", 0, "initial baton (1–255); adds query param if url has none")
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
	if q.Get("baton") == "" && *baton != 0 {
		q.Set("baton", fmt.Sprintf("%d", *baton))
	}
	if q.Get("count") == "" && *count != 0 {
		q.Set("count", fmt.Sprintf("%d", *count))
	}
	u.RawQuery = q.Encode()

	cfg, err := parseBatonQuery(u.Query())
	if err != nil {
		return err
	}
	cfg.Padding = *padding

	tlsConfig := &tls.Config{InsecureSkipVerify: *insecure}
	t := &http2.Transport{
		TLSClientConfig: tlsConfig,
		WebTransport:    http2.DefaultClientWebTransportSettings(),
	}
	client := &wth2.Client{RoundTripper: t}

	log.Printf("connecting to %s", u.String())
	session, reqBody, err := client.Connect(u.String(), nil, http.Header{})
	if err != nil {
		return err
	}
	defer session.Close()
	defer reqBody.Close() // FIN CONNECT request body (session.Close does not)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runBaton(ctx, session, false, cfg)
}

func cmdSelftest(args []string) error {
	fs := flag.NewFlagSet("selftest", flag.ExitOnError)
	baton := fs.Int("baton", 200, "initial baton (1–255)")
	count := fs.Int("count", 1, "number of parallel batons")
	padding := fs.Int("padding", 0, "padding bytes in baton messages")
	_ = fs.Parse(args)

	if *baton < 1 || *baton > 255 {
		return fmt.Errorf("baton must be 1–255")
	}
	if *count < 1 {
		return fmt.Errorf("count must be >= 1")
	}

	cfg := batonConfig{
		Version: 0,
		Baton:   byte(*baton),
		Count:   *count,
		Padding: *padding,
	}

	cert, roots, err := generateSelfSignedCert()
	if err != nil {
		return err
	}

	wtServer := &wth2.Server{}
	mux := http.NewServeMux()
	serverErr := make(chan error, 1)
	mux.HandleFunc(batonPath, func(w http.ResponseWriter, r *http.Request) {
		qcfg, err := parseBatonQuery(r.URL.Query())
		if err != nil {
			rejectBadRequest(w, err.Error())
			return
		}
		qcfg.Padding = cfg.Padding
		session, err := wtServer.Upgrade(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer session.Close()
		serverErr <- runBaton(r.Context(), session, true, qcfg)
	})

	httpServer := &http.Server{
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2"},
		},
	}
	if err := http2.ConfigureServer(httpServer, &http2.Server{
		WebTransport: http2.DefaultWebTransportSettings(),
	}); err != nil {
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
		Path:     batonPath,
		RawQuery: cfg.query().Encode(),
	}

	t := &http2.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots},
		WebTransport:    http2.DefaultClientWebTransportSettings(),
	}
	client := &wth2.Client{RoundTripper: t}

	log.Printf("selftest connect %s", u.String())
	session, reqBody, err := client.Connect(u.String(), nil, http.Header{})
	if err != nil {
		return fmt.Errorf("client connect: %w", err)
	}
	defer session.Close()
	defer reqBody.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	clientErr := make(chan error, 1)
	go func() {
		clientErr <- runBaton(ctx, session, false, cfg)
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
