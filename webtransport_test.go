package wth2

import (
	"io"
	"testing"
	"testing/synctest"
)

func TestUnidirectionalStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientResBody, serverResBody := io.Pipe()
		serverReqBody, clientReqBody := io.Pipe()

		go func() {
			client := newSession(clientResBody, clientReqBody, "test", false)
			stream, err := client.OpenStream()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("client opened stream id=%d", stream.(*Stream).ID)
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
