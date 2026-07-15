# TODO: Devious Baton interoperability

Track work needed to support the [Devious Baton protocol](https://www.ietf.org/archive/id/draft-frindell-webtrans-devious-baton-00.html) for WebTransport-over-HTTP/2 interop testing.

Reference: [WebTransport over HTTP/2 (draft-14)](https://www.ietf.org/archive/id/draft-ietf-webtrans-http2-14.txt).

Each section below is intended to be **independently actionable** by a separate agent. Dependencies are called out explicitly.

---

## Already implemented (baseline)

- [x] Extended CONNECT session setup (`:protocol=webtransport`)
- [x] Subprotocol negotiation (`WT-Available-Protocols` / `WT-Protocol`)
- [x] Bidirectional streams (client- and server-initiated)
- [x] Unidirectional streams (client- and server-initiated)
- [x] `WT_STREAM` / `WT_STREAM (FIN)` capsules
- [x] `PADDING` capsules (`WritePadding`)
- [x] `DATAGRAM` capsules (`SendDatagram` / `ReceiveDatagram`)
- [x] `SETTINGS_ENABLE_CONNECT_PROTOCOL`

---

## Transport layer

### 1. HTTP/2 WebTransport SETTINGS

**Goal:** Advertise and parse initial session limits so conformant peers can open streams and send data.

**Spec:** draft-ietf-webtrans-http2 §7 (SETTINGS parameters).

**Tasks:**

- [ ] Define setting IDs and wire up receive/send in the HTTP/2 connection (client + server)
- [ ] `SETTINGS_WT_INITIAL_MAX_DATA`
- [ ] `SETTINGS_WT_INITIAL_MAX_STREAM_DATA_UNI`
- [ ] `SETTINGS_WT_INITIAL_MAX_STREAM_DATA_BIDI_LOCAL`
- [ ] `SETTINGS_WT_INITIAL_MAX_STREAM_DATA_BIDI_REMOTE`
- [ ] `SETTINGS_WT_INITIAL_MAX_STREAMS_UNI`
- [ ] `SETTINGS_WT_INITIAL_MAX_STREAMS_BIDI`
- [ ] `SETTINGS_WT_MAX_SESSIONS` (if required by peers under test)
- [ ] Update README settings table when done

**Notes:** Spec defaults are `0` for most limits — peers may refuse to proceed until limits are exchanged. Choose sensible non-zero defaults for local advertisement (e.g. unlimited or high limits for a POC).

**Depends on:** nothing (foundational).

**Unlocks:** items 2 and 3.

---

### 2. Stream count limits (`WT_MAX_STREAMS`)

**Goal:** Track peer-granted stream credit; block or fail stream opens when exhausted; signal when blocked.

**Spec:** draft-ietf-webtrans-http2 §6.7; Devious Baton §4.1–4.2 (`DA_YAMN` on insufficient credit).

**Tasks:**

- [ ] Parse incoming `WT_MAX_STREAMS` capsules (bidirectional `0x190B4D3F`, unidirectional `0x190B4D3E`)
- [ ] Send `WT_MAX_STREAMS` capsules to grant credit to the peer
- [ ] Parse incoming `WT_STREAMS_BLOCKED` capsules (bidi + uni)
- [ ] Send `WT_STREAMS_BLOCKED` when a local `OpenStream` / `OpenUnidirectionalStream` is blocked
- [ ] Track separate credit counters for locally opened bidi vs uni streams
- [ ] Make `OpenStream()` and `OpenUnidirectionalStream()` respect credit (return a distinct blocked error or block until credit arrives — pick one approach and document it)
- [ ] Expose session-level API to close with error code `DA_YAMN` (see item 5)

**Files likely touched:** `session.go`, `capsule.go`, possibly `client.go` / `server.go` for SETTINGS integration.

**Depends on:** item 1 (initial limits from SETTINGS or first `WT_MAX_STREAMS`).

**Tests to add:**

- Peer grants limited uni credit; N-th `OpenUnidirectionalStream` fails or blocks
- Sending `WT_MAX_STREAMS` increases allowed opens
- `WT_STREAMS_BLOCKED` emitted when appropriate

---

### 3. Flow control (`WT_MAX_DATA`, `WT_MAX_STREAM_DATA`, blocked signals)

**Goal:** Enforce session- and stream-level data limits; support partial writes and backpressure instead of the fixed 64 KiB receive buffer cap.

**Spec:** draft-ietf-webtrans-http2 §6.5–6.6, §6.8–6.9; Devious Baton §4.2–4.3 (padding exercises flow control).

**Tasks:**

- [ ] Parse/send `WT_MAX_DATA` capsules
- [ ] Parse/send `WT_MAX_STREAM_DATA` capsules
- [ ] Parse/send `WT_DATA_BLOCKED` and `WT_STREAM_DATA_BLOCKED` capsules
- [ ] Track session bytes in flight vs peer limit
- [ ] Track per-stream bytes sent vs peer stream limit (uni / bidi-local / bidi-remote)
- [ ] Make `SendStream.Write` / `Stream.Write` block or return partial writes when credit exhausted (match chosen API from item 2)
- [ ] Replace or complement `StreamReceiveBufferSize` hard cap with spec-compliant flow control (large baton padding must not fail with `errStreamReceiveBufferFull`)
- [ ] Expose session-level API to close with error code `BORED` (see item 5)

**Files likely touched:** `session.go`, `stream.go`, `capsule.go`.

**Depends on:** item 1.

**Tests to add:**

- Write blocked until peer sends `WT_MAX_STREAM_DATA`
- Large payload (simulated baton padding) succeeds when credit is granted incrementally
- Blocked signals sent when credit exhausted

---

### 4. Datagrams (`DATAGRAM` capsule)

**Goal:** Send and receive unreliable datagrams on the session.

**Spec:** draft-ietf-webtrans-http2 §6.11; Devious Baton §4.4 (client sends on baton ≡ 1 mod 7, server on baton ≡ 0 mod 7).

**Status:** Done.

**Tasks:**

- [x] Parse incoming `DATAGRAM` capsules (`type=0x00`) in `readLoop`
- [x] Send `DATAGRAM` capsules via `Session.writeCapsule`
- [x] Add `Session.SendDatagram(payload []byte) error`
- [x] Add `Session.ReceiveDatagram(ctx context.Context) ([]byte, error)` with a byte-limited `datagramReceiveBuffer` (drop when full; not flow-controlled)
- [x] Document that datagrams are not flow-controlled (per spec)
- [x] Update README capsule table

**Files:** `session.go`, `datagram.go`.

**Depends on:** nothing (orthogonal to flow control).

**Tests:**

- [x] Client sends datagram, server receives identical payload
- [x] Server sends datagram, client receives identical payload
- [x] Byte-limited receive buffer drops when full and accepts again after reads

---

### 5. Session lifecycle (`WT_CLOSE_SESSION`, `WT_DRAIN_SESSION`, graceful close)

**Goal:** Spec-compliant session teardown with optional application error codes.

**Spec:** draft-ietf-webtrans-http2 (close/drain capsules); Devious Baton §4.5–4.6.

**Tasks:**

- [ ] Parse incoming `WT_CLOSE_SESSION` capsules; surface error code to application
- [ ] Parse incoming `WT_DRAIN_SESSION` capsules
- [ ] Add `Session.CloseWithError(code uint32, message string) error` sending `WT_CLOSE_SESSION`
- [ ] Add `Session.Drain()` sending `WT_DRAIN_SESSION` (if needed for interop)
- [ ] Clean close: FIN on CONNECT stream when all work is done (Devious Baton §4.5)
- [ ] Define Devious Baton session error codes as constants: `DA_YAMN`, `BRUH`, `SUS`, `BORED`
- [ ] Update README capsule table

**Files likely touched:** `session.go`, `capsule.go`, `client.go`, `server.go`.

**Depends on:** nothing for basic close; items 2–3 call into this for error closes.

**Tests to add:**

- `CloseWithError` delivers code to peer
- Peer `WT_CLOSE_SESSION` terminates session read loop with correct code

---

### 6. Stream reset and stop sending (`WT_RESET_STREAM`, `WT_STOP_SENDING`)

**Goal:** Abrupt stream termination and signaling the peer to stop sending.

**Spec:** draft-ietf-webtrans-http2 §6.2–6.3; Devious Baton §4.6 (error codes `IDC`, `WHATEVER`, `I_LIED`).

**Tasks:**

- [ ] Parse incoming `WT_RESET_STREAM` capsules (stream ID, app error code, reliable size)
- [ ] Parse incoming `WT_STOP_SENDING` capsules (stream ID, app error code)
- [ ] Add `Stream.ResetStream(code uint32, reliableSize uint64) error`
- [ ] Add `Stream.StopSending(code uint32) error` (and equivalents on `SendStream` / `ReceiveStream` as appropriate)
- [ ] On reset: cease sending `WT_STREAM` for that stream; discard excess received data above reliable size
- [ ] Surface reset/stop to blocked `Read`/`Write` calls (e.g. `ErrStreamReset` wrapping the app error code)
- [ ] Define Devious Baton stream error codes as constants: `IDC`, `WHATEVER`, `I_LIED`
- [ ] Implement Devious Baton reaction rules: on `STOP_SENDING` or inbound `RESET_STREAM` on bidi, send `RESET_STREAM` with `WHATEVER` unless already closed
- [ ] Update README capsule table

**Files likely touched:** `session.go`, `stream.go`, `capsule.go`.

**Depends on:** nothing strictly, but interacts with item 3 (reliable size vs bytes received is a session error if violated).

**Tests to add:**

- Reset aborts further reads/writes on the stream
- `StopSending` causes peer to stop receiving data for that stream
- Devious Baton `IDC` / `WHATEVER` exchange

---

## Application layer: Devious Baton protocol handler

**Goal:** Implement the baton application so this library (or an example built on it) can interop at `/webtransport/devious-baton`.

**Spec:** draft-frindell-webtrans-devious-baton-00.

**Depends on:** transport items 2–6 (full interop); partial handler can be developed earlier against current stream-only support.

**Suggested location:** new package `baton/` or `example/devious-baton/`.

### 7. Session establishment and query parameters

- [ ] Handle path `/webtransport/devious-baton` (recommended)
- [ ] Parse query params: `version` (default 0), `baton` (1–255 or server random), `count` (default 1)
- [ ] Reject invalid params with HTTP 4xx before session upgrade
- [ ] Reject unsupported `version` with 4xx
- [ ] Reject `count` exceeding server capability with 4xx

### 8. Baton message codec

- [ ] Encode/decode: `padding length (varint) + padding + baton (1 byte)`
- [ ] Reject malformed/truncated messages → close session with `BRUH`
- [ ] Optional: reject unexpected baton values → close with `SUS`

### 9. Baton exchange state machine

- [ ] **Setup (server):** open `count` uni streams; send initial baton on each; close each stream
- [ ] **On receive:** if baton == 0, decrement active baton count; else send baton+1 mod 256 on the correct stream type:
  - received on **uni** → open **bidi**, send, FIN
  - received on **peer-initiated bidi** → send on **same** stream, FIN
  - received on **self-initiated bidi** → open **uni**, send, close
- [ ] Track active batons; when zero, close session cleanly (CONNECT FIN, no error)
- [ ] On insufficient stream credit → `DA_YAMN`
- [ ] On flow-control stall (optional timeout) → `BORED`

### 10. Datagram side path

- [ ] **Client:** on baton ≡ 1 (mod 7), send identical baton message as datagram (padding small enough for one datagram)
- [ ] **Server:** on baton ≡ 0 (mod 7), send identical baton message as datagram
- [ ] Handle incoming datagram baton messages through the same processing logic

**Depends on:** item 4 (datagrams).

### 11. Example / interop binary

- [ ] Replace or supplement `example/main.go` with a runnable Devious Baton server
- [ ] Optional: CLI client that connects to remote baton servers for interop
- [ ] Document how to run against other implementations (e.g. browser client, pywebtransport)

---

## Documentation and tracking

- [ ] Keep README status tables in sync as transport items land
- [ ] Add a short "Devious Baton" section to README linking to this TODO and the spec

---

## Suggested agent assignment order

Item **4** (datagrams) is done.

| Priority | Item | Rationale |
| --- | --- | --- |
| 1 | 1 — SETTINGS | Unblocks credit exchange |
| 2 | 2 — Stream limits | Server setup opens `count` uni streams |
| 3 | 3 — Flow control | Padded baton messages |
| 4 | 5 — Session close | Clean/error termination |
| 5 | 6 — Reset/stop | Error-handling interop |
| 6 | 7–11 — Application | Wire transport into baton handler (item 10 can use datagrams) |

Items **5** and **6** can proceed in parallel once **1** is done (or in parallel with **2**/**3** if interfaces are agreed upfront).
