package wth2

import (
	"errors"
	"io"
	"testing"
	"testing/synctest"
)

func TestCloseWithErrorDeliversCode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientResBody, serverResBody := io.Pipe()
		serverReqBody, clientReqBody := io.Pipe()

		serverDone := make(chan error, 1)
		go func() {
			server := newSession(serverReqBody, serverResBody, "test", true)
			<-server.Done()
			serverDone <- server.CloseErr()
			_ = clientReqBody.Close()
			_ = serverResBody.Close()
		}()

		client := newSession(clientResBody, clientReqBody, "test", false)
		if err := client.CloseWithError(0x01, "DA_YAMN"); err != nil {
			t.Fatal(err)
		}
		_ = clientReqBody.Close() // FIN after CloseWithError

		synctest.Wait()

		err := <-serverDone
		var closeErr *SessionCloseError
		if !errors.As(err, &closeErr) {
			t.Fatalf("CloseErr=%v", err)
		}
		if closeErr.Code != 0x01 || closeErr.Message != "DA_YAMN" || !closeErr.Remote {
			t.Fatalf("%+v", closeErr)
		}
	})
}

func TestCleanCloseDoesNotSetCloseErr(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientResBody, serverResBody := io.Pipe()
		serverReqBody, clientReqBody := io.Pipe()

		go func() {
			server := newSession(serverReqBody, serverResBody, "test", true)
			<-server.Done()
			_ = clientReqBody.Close()
			_ = serverResBody.Close()
		}()

		client := newSession(clientResBody, clientReqBody, "test", false)
		client.Close()
		_ = clientReqBody.Close()
		_ = clientResBody.Close()

		synctest.Wait()

		if err := client.CloseErr(); err != nil {
			t.Fatalf("expected nil CloseErr, got %v", err)
		}
	})
}
