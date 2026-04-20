# Phase 15 — Stateless Retry Cookie Handshake (Planned)

## Goal

Eliminate the remaining handshake amplification / source-spoofing vector identified in `progress/audit.md` §1.3. Phase 14 landed interim mitigations (pending-table cap, per-IP leaky bucket); this phase installs the permanent fix.

An un-cookied `CONNECT` must cost the server zero state, zero crypto, and emit a response no larger than the request. Only a `CONNECT` whose cookie proves return-path reachability is allowed to allocate a pending handshake and run the X25519 / HKDF pipeline.

## Protocol Change

Bump `ProtocolVersion` from `1` to `2`. Existing v1 clients already receive a clean `ControlVersionMismatch`, so the break is visible and recoverable.

### New wire elements

- **`ControlRetry` (0x0A)** — server → client, unencrypted. Payload: 32-byte cookie.
- **`connectPacket.Cookie [32]byte`** — optional on the request, gated by a 1-byte presence flag mirroring the existing `DictHash` pattern (`handshake.go:114-122`).

### Flow

1. Client sends `CONNECT` without a cookie.
2. Server computes `cookie = HMAC-SHA256(cookieKey, clientAddr16 || clientPort || epoch)` and sends back `ControlRetry(cookie)`. No pending state allocated, no key generation.
3. Client re-sends `CONNECT` with the cookie.
4. Server verifies HMAC against the current cookie key, then the previous cookie key (to tolerate rotation). On success, the normal X25519 / HKDF / `pendingHandshake` path runs. On failure, drop silently.

### Server state

- `cookieKey [32]byte` + `cookieKeyPrev [32]byte`, both rotated every `CookieRotation` (default 60 s) inside `tickLoop`.
- No per-client state between the two CONNECTs.

## Files to Touch

- `fastwire.go` — `ProtocolVersion = 2`.
- `packet.go` — `ControlRetry` constant.
- `handshake.go` — `connectPacket.Cookie`, marshal/unmarshal presence flag, `marshalRetry` / `unmarshalRetry`, two-phase `serverProcessConnect` (cookie check first, key derivation second).
- `server.go` — `cookieKey` pair + rotation, RETRY branch in `handleHandshake`, limiter still runs first.
- `client.go` — RETRY handling in `processHandshakePacket`, re-send `CONNECT` with cookie (bound retries at 1).
- `config.go` — `ServerConfig.CookieRotation` (default 60 s).
- `errors.go` — new sentinel `ErrRetryCookieInvalid`.

## Why This Is Phase 15 and Not Phase 14

Phase 14 chose deliberately not to bump `ProtocolVersion` so the hardening landed as a pure additive change. This phase breaks wire compat and should ship under its own release and changelog entry.

## Test Plan

- `serverProcessConnect` without cookie: returns a RETRY packet, no pending state, no key derivation (verified by inspection or by counting allocations).
- Same input with the returned cookie: succeeds, key derivation runs, pending state recorded.
- Cookie from a different remote address: rejected.
- Cookie rotated beyond the previous-key grace window: rejected.
- End-to-end client/server handshake with the cookie round-trip.
- Run `go test -race ./...` after each change.

## Non-Goals

- No DTLS-like hello-retry-request parameter negotiation — this phase does not change what is negotiated, only the order in which resources are committed.
- No per-connection cookie (a single server-wide key is sufficient for the threat model: passive spoofing and amplification).
