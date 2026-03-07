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

func (s *Server) Upgrade(w http.ResponseWriter, r *http.Request) error {
	if r.Method != "CONNECT" {
		return fmt.Errorf("expected CONNECT method")
	}
	if r.Proto != "HTTP/2.0" {
		return fmt.Errorf("expected HTTP/2.0 protocol")
	}
	if r.Header.Get(":protocol") != "webtransport" {
		return fmt.Errorf("expected :protocol header to be set to webtransport")
	}
	if s.SelectProtocol != nil {
		availableProtocols, err := parseAvailableProtocolsHeader(r.Header.Values("WT-Available-Protocols"))
		if err != nil {
			return err
		}

		protocol, err := s.SelectProtocol(r, availableProtocols)
		if err != nil {
			return err
		}

		if protocol != "" {
			if !slices.Contains(availableProtocols, protocol) {
				return fmt.Errorf("selected protocol %q not in available protocols", protocol)
			}

			encodedProtocol, err := encodeSelectedProtocolHeader(protocol)
			if err != nil {
				return err
			}
			w.Header().Set("WT-Protocol", encodedProtocol)
		}
	}

	return nil
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
