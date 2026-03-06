// Simple HTTP/2 example: starts an HTTP/2 server and sends a request to it from the same process.
// The server validates that the request used HTTP/2.
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
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"time"
)

func main() {
	cert, clientRoots, err := generateSelfSignedCert()
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2"},
		},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Proto != "HTTP/2.0" {
				http.Error(w, fmt.Sprintf("expected HTTP/2, got %q", r.Proto), http.StatusHTTPVersionNotSupported)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "OK: %s %s over %s\n", r.Method, r.URL.Path, r.Proto)
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

	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{
		RootCAs: clientRoots,
	}
	client := &http.Client{Transport: t}
	resp, err := client.Get(fmt.Sprintf("https://%s/test", addr))
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("unexpected status: %d %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(body))
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
