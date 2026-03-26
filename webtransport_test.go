package wth2

import (
	"io"
	"testing"
	"testing/synctest"
)

// TestBidirectionalStreamFromClient exercises a client-initiated bidirectional stream.
func TestBidirectionalStreamFromClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientResBody, serverResBody := io.Pipe()
		serverReqBody, clientReqBody := io.Pipe()

		go func() {
			client := newSession(clientResBody, clientReqBody, "test", false)
			stream, err := client.OpenStream()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("client opened stream id=%d", stream.ID)
			if n, err := stream.Write([]byte("Hello, world!")); err != nil {
				t.Fatal(err)
			} else {
				t.Logf("client wrote %d bytes", n)
			}
			stream.Close()
			client.Close()
			serverResBody.Close()
		}()

		go func() {
			server := newSession(serverReqBody, serverResBody, "test", true)
			stream, err := server.AcceptStream(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(stream)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("server read %d bytes", len(content))
			stream.Close()
			server.Close()
			clientReqBody.Close()
		}()

		synctest.Wait()
	})
}

// TestBidirectionalStreamFromServer exercises a server-initiated bidirectional stream.
func TestBidirectionalStreamFromServer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientResBody, serverResBody := io.Pipe()
		serverReqBody, clientReqBody := io.Pipe()

		go func() {
			server := newSession(serverReqBody, serverResBody, "test", true)
			stream, err := server.OpenStream()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("server opened stream id=%d", stream.ID)
			if n, err := stream.Write([]byte("Hello from server")); err != nil {
				t.Fatal(err)
			} else {
				t.Logf("server wrote %d bytes", n)
			}
			stream.Close()
			server.Close()
			clientReqBody.Close()
		}()

		go func() {
			client := newSession(clientResBody, clientReqBody, "test", false)
			stream, err := client.AcceptStream(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(stream)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("client read %d bytes", len(content))
			stream.Close()
			client.Close()
			serverResBody.Close()
		}()

		synctest.Wait()
	})
}

func TestBidirectionalMultipleStreamsFromClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientResBody, serverResBody := io.Pipe()
		serverReqBody, clientReqBody := io.Pipe()

		payloads := []string{
			"stream-0",
			"stream-1",
			"stream-2",
			"stream-3",
			"stream-4",
		}

		go func() {
			client := newSession(clientResBody, clientReqBody, "test", false)
			for _, payload := range payloads {
				stream, err := client.OpenStream()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := stream.Write([]byte(payload)); err != nil {
					t.Fatal(err)
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
			}
			client.Close()
			serverResBody.Close()
		}()

		go func() {
			server := newSession(serverReqBody, serverResBody, "test", true)
			type result struct {
				idx     int
				content string
				err     error
			}
			resCh := make(chan result, len(payloads))

			for i := 0; i < len(payloads); i++ {
				stream, err := server.AcceptStream(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				// Start reading immediately so the session's read loop isn't blocked
				// trying to deliver later peer-initiated streams to `incomingStreams`.
				streamIdx := i
				go func(st io.ReadWriteCloser, idx int) {
					content, err := io.ReadAll(st)
					resCh <- result{idx: idx, content: string(content), err: err}
				}(stream, streamIdx)
			}

			for i := 0; i < len(payloads); i++ {
				res := <-resCh
				if res.err != nil {
					t.Fatal(res.err)
				}
				if res.content != payloads[res.idx] {
					t.Fatalf("stream %d: expected %q, got %q", res.idx, payloads[res.idx], res.content)
				}
			}

			server.Close()
			clientReqBody.Close()
		}()

		synctest.Wait()
	})
}

func TestUnidirectionalStreamFromClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientResBody, serverResBody := io.Pipe()
		serverReqBody, clientReqBody := io.Pipe()

		const payload = "hello from client uni"

		go func() {
			client := newSession(clientResBody, clientReqBody, "test", false)
			stream, err := client.OpenUnidirectionalStream()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Write([]byte(payload)); err != nil {
				t.Fatal(err)
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			client.Close()
			serverResBody.Close()
		}()

		go func() {
			server := newSession(serverReqBody, serverResBody, "test", true)
			stream, err := server.AcceptUnidirectionalStream(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(stream)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != payload {
				t.Fatalf("expected %q, got %q", payload, string(content))
			}
			stream.Close()
			server.Close()
			clientReqBody.Close()
		}()

		synctest.Wait()
	})
}

func TestUnidirectionalStreamFromServer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientResBody, serverResBody := io.Pipe()
		serverReqBody, clientReqBody := io.Pipe()

		const payload = "hello from server uni"

		go func() {
			server := newSession(serverReqBody, serverResBody, "test", true)
			stream, err := server.OpenUnidirectionalStream()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Write([]byte(payload)); err != nil {
				t.Fatal(err)
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			server.Close()
			clientReqBody.Close()
		}()

		go func() {
			client := newSession(clientResBody, clientReqBody, "test", false)
			stream, err := client.AcceptUnidirectionalStream(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(stream)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != payload {
				t.Fatalf("expected %q, got %q", payload, string(content))
			}
			stream.Close()
			client.Close()
			serverResBody.Close()
		}()

		synctest.Wait()
	})
}
