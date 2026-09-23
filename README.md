# WebTransport over HTTP/2 (Go POC)

Proof-of-concept implementation of [WebTransport over HTTP/2](https://datatracker.ietf.org/doc/html/draft-ietf-webtrans-http2-15) in Go.

WebTransport provides low-level client–server communication (streams, datagrams) over HTTP. This variant runs over HTTP/2 when UDP/QUIC is not available, using extended CONNECT and the Capsule Protocol.

## References

- **WebTransport over HTTP/2**: [draft-ietf-webtrans-http2-15](https://datatracker.ietf.org/doc/html/draft-ietf-webtrans-http2-15)
- **WebTransport framework (overview)**: [draft-ietf-webtrans-overview-12](https://www.ietf.org/archive/id/draft-ietf-webtrans-overview-12.txt)

## HTTP/2 stack

Upstream Go (`golang.org/x/net/http2`) has no API for WebTransport SETTINGS. This repo vendors a local fork at [`third_party/net`](third_party/net) (see `FORK.md`) and pins it with:

```go
replace golang.org/x/net => ./third_party/net
```

Use `http2.ConfigureServer` (not only stdlib auto-HTTP/2) so the server sends WT SETTINGS:

```go
http2.ConfigureServer(srv, &http2.Server{
    WebTransport: http2.DefaultWebTransportSettings(),
})

tr := &http2.Transport{
    WebTransport: http2.DefaultClientWebTransportSettings(),
}
```

Peer SETTINGS are available via `http2.PeerWebTransportSettingsFromContext` (server) and `ClientConn.PeerWebTransportSettings` (client).

Extended CONNECT still requires `GODEBUG=http2xconnect=1`.

## Status

Early POC; developed interactively, piece by piece.

The tables below track support for settings and capsules:

### HTTP/2 SETTINGS

| Setting | Receive | Send |
| --- | --- | --- |
| `SETTINGS_ENABLE_CONNECT_PROTOCOL` | Yes | Yes |
| `SETTINGS_WT_ENABLED` | Yes | Yes (server) |
| `SETTINGS_WT_INITIAL_MAX_DATA` | Yes | Yes |
| `SETTINGS_WT_INITIAL_MAX_STREAM_DATA_UNI` | Yes | Yes |
| `SETTINGS_WT_INITIAL_MAX_STREAM_DATA_BIDI_LOCAL` | Yes | Yes |
| `SETTINGS_WT_INITIAL_MAX_STREAM_DATA_BIDI_REMOTE` | Yes | Yes |
| `SETTINGS_WT_INITIAL_MAX_STREAMS_UNI` | Yes | Yes |
| `SETTINGS_WT_INITIAL_MAX_STREAMS_BIDI` | Yes | Yes |

Received SETTINGS are stored on the HTTP/2 connection; applying them as session flow-control credit is still TODO (see `TODO.md`).

### Capsule types

| Capsule | Receive | Send |
| --- | --- | --- |
| PADDING | Yes | Yes |
| WT_RESET_STREAM | No | No |
| WT_STOP_SENDING | No | No |
| WT_STREAM | Yes | Yes |
| WT_STREAM (FIN) | Yes | Yes |
| WT_MAX_DATA | No | No |
| WT_MAX_STREAM_DATA | No | No |
| WT_MAX_STREAMS (bidirectional) | No | No |
| WT_MAX_STREAMS (unidirectional) | No | No |
| WT_DATA_BLOCKED | No | No |
| WT_STREAM_DATA_BLOCKED | No | No |
| WT_STREAMS_BLOCKED (bidirectional) | No | No |
| WT_STREAMS_BLOCKED (unidirectional) | No | No |
| DATAGRAM | Yes | Yes |
| WT_CLOSE_SESSION | No | No |
| WT_DRAIN_SESSION | No | No |

## License

MIT. See [LICENSE](LICENSE).
