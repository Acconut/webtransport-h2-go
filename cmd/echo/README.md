# echo

Echo client and server for WebTransport over HTTP/2. The client sends one payload and checks that the bytes it gets back are the same.

`-mode` selects the channel:

- `bidi` — bidirectional stream, reply on the same stream
- `uni` — unidirectional stream, reply on a new unidirectional stream
- `datagram` — datagram, reply as a datagram

With no `-cert` and `-key`, `serve` uses an ephemeral self-signed certificate.

```bash
go run ./cmd/echo serve -addr localhost:4433
go run ./cmd/echo client -url https://localhost:4433/webtransport/echo -insecure -mode uni
go run ./cmd/echo selftest -mode datagram
```

`serve` listens on `-addr` (default `localhost:4433`) at `-path` (default `/webtransport/echo`).

`client` requires `-url`. `-data` is the payload (default `Hello, world!`). `-insecure` skips TLS verification. `-timeout` defaults to 15s.

`selftest` runs the server and client in one process. It takes `-mode`, `-data`, and `-timeout`.
