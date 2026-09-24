package echo

import (
	"log"
	"net/http"
	"slices"

	wth2 "github.com/Acconut/webtransport-h2-go"
)

// Handler accepts one WebTransport session and echoes datagrams and streams.
func Handler() http.Handler {
	wtServer := &wth2.Server{
		SelectProtocol: func(r *http.Request, available []string) (string, error) {
			if slices.Contains(available, Protocol) {
				return Protocol, nil
			}
			return "", nil
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := wtServer.Upgrade(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer session.Close()
		log.Printf("echo session protocol=%q", session.Protocol)
		runEchoServer(r.Context(), session)
	})
}
