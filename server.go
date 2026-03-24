package wth2

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/shogo82148/go-sfv"
)

type Server struct {
	SelectProtocol func(r *http.Request, availableProtocols []string) (string, error)
}

func (s *Server) Upgrade(w http.ResponseWriter, r *http.Request) (*Session, error) {
	if r.Method != "CONNECT" {
		return nil, fmt.Errorf("expected CONNECT method")
	}
	if r.Proto != "HTTP/2.0" {
		return nil, fmt.Errorf("expected HTTP/2.0 protocol")
	}
	if r.Header.Get(":protocol") != "webtransport" {
		return nil, fmt.Errorf("expected :protocol header to be set to webtransport")
	}

	var protocol string
	if s.SelectProtocol != nil {
		availableProtocols, err := parseAvailableProtocolsHeader(r.Header.Values("WT-Available-Protocols"))
		if err != nil {
			return nil, err
		}

		protocol, err = s.SelectProtocol(r, availableProtocols)
		if err != nil {
			return nil, err
		}

		if protocol != "" {
			if !slices.Contains(availableProtocols, protocol) {
				return nil, fmt.Errorf("selected protocol %q not in available protocols", protocol)
			}

			encodedProtocol, err := encodeSelectedProtocolHeader(protocol)
			if err != nil {
				return nil, err
			}
			w.Header().Set("WT-Protocol", encodedProtocol)
		}
	}

	rc := http.NewResponseController(w)
	fmt.Println(rc.EnableFullDuplex())

	w.WriteHeader(http.StatusOK)
	fmt.Println("Sent header")

	// Very important to flush
	rc.Flush()

	return newSession(r.Body, w, protocol, true), nil
}

func parseAvailableProtocolsHeader(h []string) ([]string, error) {
	list, err := sfv.DecodeList(h)
	if err != nil {
		return nil, fmt.Errorf("invalid WT-Available-Protocols header: %w", err)
	}

	protocols := make([]string, len(list))
	for _, item := range list {
		protocol, ok := item.Value.(string)
		if !ok {
			return nil, fmt.Errorf("invalid WT-Available-Protocols header: expected string item, got %v", item.Value)
		}
		protocols = append(protocols, protocol)
	}
	return protocols, nil
}

func encodeSelectedProtocolHeader(protocol string) (string, error) {
	return sfv.EncodeItem(sfv.Item{Value: protocol})
}
