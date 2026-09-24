# devious-baton

Client and server for the Devious Baton interop protocol over WebTransport.

The server accepts sessions at `/webtransport/devious-baton`. With no `-cert` and `-key`, `serve` uses an ephemeral self-signed certificate.

```bash
go run ./cmd/devious-baton serve -addr localhost:4433
go run ./cmd/devious-baton client -url 'https://localhost:4433/webtransport/devious-baton?baton=42&count=1' -insecure
go run ./cmd/devious-baton selftest -baton 200 -count 1
```

`serve` listens on `-addr` (default `localhost:4433`). `-max-count` rejects a `count` query above that value (default 16). `-padding` is the number of padding bytes in messages this process sends.

`client` requires `-url`. `-baton` and `-count` are added to the query when the URL does not already set them. `-insecure` skips TLS verification. `-padding` applies to messages this client sends.

`selftest` runs the server and client in one process. `-baton` defaults to 200 and `-count` defaults to 1.
