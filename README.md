# WebTransport over HTTP/2 (Go POC)

Proof-of-concept implementation of [WebTransport over HTTP/2](https://www.ietf.org/archive/id/draft-ietf-webtrans-http2-14.txt) in Go.

WebTransport provides low-level client–server communication (streams, datagrams) over HTTP. This variant runs over HTTP/2 when UDP/QUIC is not available, using extended CONNECT and the Capsule Protocol.

## References

- **WebTransport over HTTP/2**: [draft-ietf-webtrans-http2-14](https://www.ietf.org/archive/id/draft-ietf-webtrans-http2-14.txt)
- **WebTransport framework (overview)**: [draft-ietf-webtrans-overview-12](https://www.ietf.org/archive/id/draft-ietf-webtrans-overview-12.txt)

## Status

Early POC; developed interactively, piece by piece.

## License

MIT. See [LICENSE](LICENSE).
