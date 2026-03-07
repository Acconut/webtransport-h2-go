package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http2"

	wth2 "github.com/Acconut/webtransport-h2-go"
)

func main() {
	if !strings.Contains(os.Getenv("GODEBUG"), "http2xconnect=1") {
		log.Fatal("GODEBUG must contain http2xconnect=1 for this example to work")
	}

	cert, clientRoots, err := generateSelfSignedCert()
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
			err := wtServer.Upgrade(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "OK: WebTransport over HTTP/2\n")
		}),
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
	}
	wtClient := &wth2.Client{
		RoundTripper: &t,
	}
	req, err := wth2.NewRequest(fmt.Sprintf("https://%s/test", addr), []string{"baton"})
	if err != nil {
		log.Fatal(err)
	}
	resp, err := wtClient.Connect(req)
	if err != nil {
		log.Fatalf("round trip failed: %v", err)
	}
	fmt.Println("resp", resp)
}

// generateSelfSignedCert creates a self-signed certificate for localhost and returns
// the TLS certificate for use in server config and an x509 CertPool containing that
// certificate for use in client config (so the client trusts the server).
func generateSelfSignedCert() (tls.Certificate, *x509.CertPool, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:              []string{"localhost"},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	var certPem, keyPem []byte
	certPem = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDer, _ := x509.MarshalECPrivateKey(key)
	keyPem = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer})

	cert, err := tls.X509KeyPair(certPem, keyPem)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(x509Cert)

	return cert, pool, nil
}
