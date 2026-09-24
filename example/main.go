package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"

	"golang.org/x/net/http2"

	wth2 "github.com/Acconut/webtransport-h2-go"
	"github.com/Acconut/webtransport-h2-go/internal/tlsx"
)

func main() {
	cert, clientRoots, err := tlsx.GenerateSelfSignedCert()
	if err != nil {
		log.Fatal(err)
	}

	wtServer := &wth2.Server{
		SelectProtocol: func(r *http.Request, availableProtocols []string) (string, error) {
			return "baton", nil
		},
	}

	server := &http.Server{
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2"},
		},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := wtServer.Upgrade(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer session.Close()

			fmt.Println("[server] session", session.Protocol)

			stream, err := session.AcceptStream(r.Context())
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			fmt.Println("[server] accepted stream")
			defer stream.Close()

			content, err := io.ReadAll(stream)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			fmt.Println("[server] content", string(content))
		}),
	}
	if err := http2.ConfigureServer(server, &http2.Server{
		WebTransport: http2.DefaultWebTransportSettings(),
	}); err != nil {
		log.Fatal(err)
	}

	listener, err := tls.Listen("tcp", "localhost:0", server.TLSConfig)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	defer server.Close()

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("server: %v", err)
		}
	}()

	addr := listener.Addr().String()

	t := http2.Transport{
		TLSClientConfig: &tls.Config{RootCAs: clientRoots},
		WebTransport:    http2.DefaultClientWebTransportSettings(),
	}
	wtClient := &wth2.Client{
		RoundTripper: &t,
	}
	url := fmt.Sprintf("https://%s/test", addr)
	session, reqBody, err := wtClient.Connect(url, []string{"baton"}, http.Header{})
	if err != nil {
		log.Fatalf("round trip failed: %v", err)
	}

	fmt.Println("[client] session", session.Protocol)

	stream, err := session.OpenStream()
	if err != nil {
		log.Fatalf("open stream failed: %v", err)
	}

	if n, err := stream.Write([]byte("Hello, world!")); err != nil {
		log.Fatalf("write failed: %v", err)
	} else {
		fmt.Println("wrote", n, "bytes")
	}

	stream.Close()
	session.Close()
	reqBody.Close() // FIN CONNECT request body

	server.Shutdown(context.Background())
}
