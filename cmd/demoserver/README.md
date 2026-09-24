# demoserver

One TLS server for both interop endpoints:

- `/webtransport/echo`
- `/webtransport/devious-baton`

`GET /release` returns the commit this binary was built from. Set it at link time; otherwise the response is `unknown`.

```bash
go build -ldflags "-X main.release=$(git rev-parse HEAD)" -o demoserver ./cmd/demoserver
./demoserver -addr :443 -cert cert.pem -key key.pem
```

`-addr` defaults to `localhost:4433`. With no `-cert` and `-key`, the server uses an ephemeral self-signed certificate (`-selfsigned` does the same). `-max-count` (default 16) and `-padding` (default 0) apply to Devious Baton sessions.
