package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"golang.org/x/net/http2"

	wth2 "github.com/Acconut/webtransport-h2-go"
)

func main() {
	if !strings.Contains(os.Getenv("GODEBUG"), "http2xconnect=1") {
		log.Fatal("GODEBUG must contain http2xconnect=1 for this example to work")
	}

	f, err := os.OpenFile("keys", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	t := http2.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			KeyLogWriter:       f,
		},
		WebTransport: http2.DefaultClientWebTransportSettings(),
	}
	wtClient := &wth2.Client{
		RoundTripper: &t,
	}
	url := "https://fb.mvfst.net:6789/webtransport"
	session, reqBody, err := wtClient.Connect(url, []string{"echo"}, http.Header{})
	if err != nil {
		log.Fatalf("round trip failed: %v", err)
	}

	fmt.Println("[client] session", session.Protocol)

	if err := session.SendDatagram([]byte("Hello, world!")); err != nil {
		log.Fatalf("write failed: %v", err)
	} else {
		fmt.Println("wrote datagram")
	}

	go func() {
		b, err := session.ReceiveDatagram(context.TODO())
		if err != nil {
			log.Printf("failed to receive datagram: %v", err)
		}

		log.Printf("received datagram: %s", b)
	}()

	stream, err := session.OpenStream()
	if err != nil {
		log.Fatalf("open stream failed: %v", err)
	}

	if n, err := stream.Write([]byte("Hello, world!")); err != nil {
		log.Fatalf("write failed: %v", err)
	} else {
		fmt.Println("wrote", n, "bytes")
	}

	// <-time.After(time.Second * 10)

	buf := make([]byte, 13)
	if n, err := stream.Read(buf); err != nil {
		log.Fatalf("read failed: %v", err)
	} else {
		fmt.Println("read", n, "bytes", string(buf))
	}

	stream.Close()
	session.Close()
	reqBody.Close() // FIN CONNECT request body

}
