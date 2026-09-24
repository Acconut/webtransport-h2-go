package wth2

import (
	"fmt"
	"io"
	"net/http"

	"github.com/shogo82148/go-sfv"
	"golang.org/x/net/http2"
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

// Connect establishes a WebTransport session over HTTP/2 extended CONNECT.
//
// The returned io.Closer is the CONNECT request body. Closing it sends FIN on
// the client's send half of the CONNECT stream. [Session.Close] does not close
// it; callers should close this after the session is done (and after any
// [Session.CloseWithError], which must be followed by FIN per the drafts).
func (c *Client) Connect(url string, availableProtocols []string, headers http.Header) (*Session, io.Closer, error) {
	pr, pw := io.Pipe()
	req, err := http.NewRequest("CONNECT", url, pr)
	if err != nil {
		return nil, nil, err
	}
	ctx, peerSettings := http2.ContextWithPeerWebTransportSettings(req.Context())
	req = req.WithContext(ctx)
	req.Header = headers.Clone()
	req.Header.Set(":protocol", "webtransport")

	if len(availableProtocols) > 0 {
		encodedAvailableProtocols, err := encodeAvailableProtocolsHeader(availableProtocols)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("WT-Available-Protocols", encodedAvailableProtocols)
	}

	res, err := c.RoundTripper.RoundTrip(req)
	if err != nil {
		_ = pw.Close()
		return nil, nil, fmt.Errorf("webtransport connection failed: %w", err)
	}
	// Must not close res.Body here; the session reads capsules from it.
	fmt.Println("Received response")

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		_ = pw.Close()
		_ = res.Body.Close()
		return nil, nil, fmt.Errorf("webtransport connection failed: %d %s", res.StatusCode, res.Status)
	}

	protocol, err := parseSelectedProtocolHeader(res.Header.Values("WT-Protocol"))
	if err != nil {
		_ = pw.Close()
		_ = res.Body.Close()
		return nil, nil, fmt.Errorf("invalid WT-Protocol header: %w", err)
	}

	var peer *peerInitialLimits
	if settings, ok := peerSettings(); ok {
		copied := peerLimitsFromSettings(settings)
		peer = &copied
	}
	return newSessionWithPeerLimits(res.Body, pw, protocol, false, peer), pw, nil
}

func peerLimitsFromSettings(s http2.WebTransportSettings) peerInitialLimits {
	return peerInitialLimits{
		maxData:              uint64(s.InitialMaxData),
		maxStreamsUni:        uint64(s.InitialMaxStreamsUni),
		maxStreamsBidi:       uint64(s.InitialMaxStreamsBidi),
		streamDataUni:        uint64(s.InitialMaxStreamDataUni),
		streamDataBidiLocal:  uint64(s.InitialMaxStreamDataBidiLocal),
		streamDataBidiRemote: uint64(s.InitialMaxStreamDataBidiRemote),
	}
}

func parseSelectedProtocolHeader(h []string) (string, error) {
	// WT-Protocol is optional; absent means no application protocol was negotiated.
	if len(h) == 0 || (len(h) == 1 && h[0] == "") {
		return "", nil
	}
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
