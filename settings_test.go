package wth2_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/http2"

	wth2 "github.com/Acconut/webtransport-h2-go"
)

func TestWebTransportSettingsExchange(t *testing.T) {
	cert, roots, err := testCert()
	if err != nil {
		t.Fatal(err)
	}

	clientLimits := &http2.WebTransportSettings{
		Enabled:                        false,
		InitialMaxData:                 111,
		InitialMaxStreamDataUni:        222,
		InitialMaxStreamDataBidiLocal:  333,
		InitialMaxStreamDataBidiRemote: 444,
		InitialMaxStreamsUni:           5,
		InitialMaxStreamsBidi:          6,
	}
	serverLimits := &http2.WebTransportSettings{
		Enabled:                        true,
		InitialMaxData:                 1001,
		InitialMaxStreamDataUni:        1002,
		InitialMaxStreamDataBidiLocal:  1003,
		InitialMaxStreamDataBidiRemote: 1004,
		InitialMaxStreamsUni:           7,
		InitialMaxStreamsBidi:          8,
	}

	gotClientSettings := make(chan http2.WebTransportSettings, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/wt", func(w http.ResponseWriter, r *http.Request) {
		peer, ok := http2.PeerWebTransportSettingsFromContext(r.Context())
		if !ok {
			http.Error(w, "missing peer WT settings", http.StatusInternalServerError)
			return
		}
		gotClientSettings <- peer

		session, err := (&wth2.Server{}).Upgrade(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer session.Close()
		uni, err := session.OpenUnidirectionalStream()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		limit, ok := session.TestingStreamSendLimit(uni.ID)
		if !ok || limit != uint64(clientLimits.InitialMaxStreamDataUni) {
			http.Error(w, "server did not copy client uni stream limit", http.StatusInternalServerError)
			return
		}
		<-r.Context().Done()
	})

	srv := &http.Server{
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2"},
		},
	}
	if err := http2.ConfigureServer(srv, &http2.Server{WebTransport: serverLimits}); err != nil {
		t.Fatal(err)
	}

	ln, err := tls.Listen("tcp", "localhost:0", srv.TLSConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	defer srv.Close()

	go srv.Serve(ln)

	tr := &http2.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots},
		WebTransport:    clientLimits,
	}
	defer tr.CloseIdleConnections()

	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		RootCAs:    roots,
		NextProtos: []string{"h2"},
		ServerName: "localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Handshake(); err != nil {
		t.Fatal(err)
	}
	if np := conn.ConnectionState().NegotiatedProtocol; np != "h2" {
		t.Fatalf("ALPN = %q, want h2", np)
	}

	cc, err := tr.NewClientConn(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()

	client := &wth2.Client{RoundTripper: cc}
	session, reqBody, err := client.Connect("https://localhost/wt", nil, http.Header{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()
	defer reqBody.Close()

	stream, err := session.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	limit, ok := session.TestingStreamSendLimit(stream.ID)
	if !ok {
		t.Fatal("client session has no send limit for the new stream")
	}
	if limit != uint64(serverLimits.InitialMaxStreamDataBidiRemote) {
		t.Fatalf("client bidi send limit = %d, want %d", limit, serverLimits.InitialMaxStreamDataBidiRemote)
	}

	peerServer := cc.PeerWebTransportSettings()
	if !peerServer.Enabled {
		t.Fatal("expected peer SETTINGS_WT_ENABLED=1")
	}
	if peerServer.InitialMaxData != serverLimits.InitialMaxData {
		t.Fatalf("peer InitialMaxData = %d, want %d", peerServer.InitialMaxData, serverLimits.InitialMaxData)
	}
	if peerServer.InitialMaxStreamsBidi != serverLimits.InitialMaxStreamsBidi {
		t.Fatalf("peer InitialMaxStreamsBidi = %d, want %d", peerServer.InitialMaxStreamsBidi, serverLimits.InitialMaxStreamsBidi)
	}

	select {
	case peerClient := <-gotClientSettings:
		if peerClient.Enabled {
			t.Fatal("client should not advertise SETTINGS_WT_ENABLED")
		}
		if peerClient.InitialMaxData != clientLimits.InitialMaxData {
			t.Fatalf("server saw InitialMaxData = %d, want %d", peerClient.InitialMaxData, clientLimits.InitialMaxData)
		}
		if peerClient.InitialMaxStreamsUni != clientLimits.InitialMaxStreamsUni {
			t.Fatalf("server saw InitialMaxStreamsUni = %d, want %d", peerClient.InitialMaxStreamsUni, clientLimits.InitialMaxStreamsUni)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server to observe client SETTINGS")
	}
}

func testCert() (tls.Certificate, *x509.CertPool, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	return cert, pool, nil
}
