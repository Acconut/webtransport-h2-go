package baton

import (
	"fmt"
	"log"
	"net/http"

	wth2 "github.com/Acconut/webtransport-h2-go"
)

// Handler accepts Devious Baton sessions.
// count above maxCount is rejected. padding is added to messages this server sends.
func Handler(maxCount, padding int) http.Handler {
	wtServer := &wth2.Server{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg, err := ParseQuery(r.URL.Query())
		if err != nil {
			rejectBadRequest(w, err.Error())
			return
		}
		if cfg.Count > maxCount {
			rejectBadRequest(w, fmt.Sprintf("count %d exceeds server max %d", cfg.Count, maxCount))
			return
		}
		cfg.Padding = padding

		session, err := wtServer.Upgrade(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer session.Close()

		if err := Run(r.Context(), session, true, cfg); err != nil {
			log.Printf("baton session: %v", err)
		}
	})
}
