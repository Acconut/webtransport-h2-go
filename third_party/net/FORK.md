# Local fork of golang.org/x/net

Base: `golang.org/x/net v0.51.0`

Purpose: advertise and observe WebTransport-over-HTTP/2 SETTINGS
(draft-ietf-webtrans-http2-15) which upstream does not expose.

Patches (search for `webtransport-h2-go`):
- `http2/webtransport_settings.go` — setting IDs, config, peer state
- `http2/http2.go` — SettingID names / Valid() for WT_ENABLED; extended CONNECT enabled
- `http2/server.go` — send + receive WT SETTINGS; expose via request context
- `http2/transport.go` — send + receive WT SETTINGS on ClientConn

Rebase: replace this tree with a newer x/net release, then re-apply the patches.
