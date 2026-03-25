# WebTransport over HTTP/2 (Go POC)

Proof-of-concept implementation of [WebTransport over HTTP/2](https://www.ietf.org/archive/id/draft-ietf-webtrans-http2-14.txt) in Go.

WebTransport provides low-level client–server communication (streams, datagrams) over HTTP. This variant runs over HTTP/2 when UDP/QUIC is not available, using extended CONNECT and the Capsule Protocol.

## References

- **WebTransport over HTTP/2**: [draft-ietf-webtrans-http2-14](https://www.ietf.org/archive/id/draft-ietf-webtrans-http2-14.txt)
- **WebTransport framework (overview)**: [draft-ietf-webtrans-overview-12](https://www.ietf.org/archive/id/draft-ietf-webtrans-overview-12.txt)

## Status

Early POC; developed interactively, piece by piece.

The tables below track support for settings and capsules:

### HTTP/2 SETTINGS

| Setting | Receive | Send |
| --- | --- | --- |
| `SETTINGS_ENABLE_CONNECT_PROTOCOL` | Yss | Yes |
| `SETTINGS_WT_MAX_SESSIONS` | No | No |
| `SETTINGS_WT_INITIAL_MAX_DATA` | No | No |
| `SETTINGS_WT_INITIAL_MAX_STREAM_DATA_UNI` | No | No |
| `SETTINGS_WT_INITIAL_MAX_STREAM_DATA_BIDI_LOCAL` | No | No |
| `SETTINGS_WT_INITIAL_MAX_STREAM_DATA_BIDI_REMOTE` | No | No |
| `SETTINGS_WT_INITIAL_MAX_STREAMS_UNI` | No | No |
| `SETTINGS_WT_INITIAL_MAX_STREAMS_BIDI` | No | No |

### Capsule types

| Capsule | Receive | Send |
| --- | --- | --- |
| PADDING | No | No |
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
| DATAGRAM | No | No |
| WT_CLOSE_SESSION | No | No |
| WT_DRAIN_SESSION | No | No |

## License

MIT. See [LICENSE](LICENSE).
