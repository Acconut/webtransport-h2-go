package wth2

import (
	"fmt"
	"io"
	"net/http"

	"github.com/shogo82148/go-sfv"
)

type Client struct {
	RoundTripper http.RoundTripper
}

func encodeAvailableProtocolsHeader(availableProtocols []string) (string, error) {
	items := make([]sfv.Item, len(availableProtocols))
	for i, protocol := range availableProtocols {
		items[i] = sfv.Item{Value: protocol}
	}
	return sfv.EncodeList(items)
}

func (c *Client) Connect(url string, availableProtocols []string, headers http.Header) (*Session, error) {
	pr, pw := io.Pipe()
	req, err := http.NewRequest("CONNECT", url, pr)
	if err != nil {
		return nil, err
	}
	req.Header = headers.Clone()
	req.Header.Set(":protocol", "webtransport")

	if len(availableProtocols) > 0 {
		encodedAvailableProtocols, err := encodeAvailableProtocolsHeader(availableProtocols)
		if err != nil {
			return nil, err
		}
		req.Header.Set("WT-Available-Protocols", encodedAvailableProtocols)
	}

	res, err := c.RoundTripper.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("webtransport connection failed: %w", err)
	}
	// Must not close body, as it is used by the session
	// defer res.Body.Close()
	fmt.Println("Received response")

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("webtransport connection failed: %d %s", res.StatusCode, res.Status)
	}

	protocol, err := parseSelectedProtocolHeader(res.Header.Values("WT-Protocol"))
	if err != nil {
		return nil, fmt.Errorf("invalid WT-Protocol header: %w", err)
	}

	return newSession(res.Body, pw, protocol, false), nil
}

func parseSelectedProtocolHeader(h []string) (string, error) {
	item, err := sfv.DecodeItem(h)
	if err != nil {
		return "", fmt.Errorf("invalid WT-Protocol header: %w", err)
	}
	protocol, ok := item.Value.(string)
	if !ok {
		return "", fmt.Errorf("invalid WT-Protocol header: expected string item, got %v", item.Value)
	}
	return protocol, nil
}
