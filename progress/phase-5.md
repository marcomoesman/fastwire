# Phase 5 — Fragmentation

## What was built

### Constants (`fragment.go`)
- `MaxHeaderSize = 16` — worst-case packet header size (1+1+5+5+4)
- `MaxFragments = 255` — maximum fragments per message (byte index limit)
- `MaxFragmentPayload = 1155` — per-fragment payload capacity (1200 - 24 - 16 - 5)
- `DefaultFragmentTimeout = 5s` — stale reassembly buffer expiry

### Send-side splitting (`splitMessage`)
- Splits a payload into independently allocated `[FragmentHeader + chunk]` buffers
- Empty payload produces a single fragment with count=1
- Returns `ErrMessageTooLarge` if >255 fragments needed (~294 KB limit)
- Does NOT handle PacketHeader or encryption — pure splitting for composability

### Receive-side reassembly (`reassemblyStore`)
- `reassemblyBuffer` holds per-message fragment slots indexed by FragmentIndex
- `addFragment` validates count/flags consistency, handles duplicates silently
- Copies incoming payload to decouple from pooled caller buffers
- On completion: concatenates in order, removes buffer, returns assembled payload
- `cleanup(timeout)` removes stale incomplete buffers
- `pending()` returns count of in-progress reassemblies

### Connection integration
- `Connection.reassembly` field initialized in `newConnection()`

### Error
- `ErrMessageTooLarge` added to `errors.go`

## Design decisions

1. **Independent allocations per fragment** — each fragment gets its own buffer so retransmission can reference individual fragments without keeping the whole message alive.
2. **Payload copy on receive** — `addFragment` copies the payload slice because callers may reuse pooled buffers.
3. **MaxHeaderSize = 16** — the implementation guide says 14 but the actual arithmetic (1+1+5+5+4) gives 16, confirmed by `packet.go` comments. Using 16 is conservative and correct.
4. **Single-fragment envelope** — small messages that need compression flags use count=1 with fragment flags, reusing the fragment header to carry compression metadata.
5. **Cleanup is caller-driven** — `cleanup()` is called by the tick loop (Phase 8), not by a background goroutine, keeping the design simple and deterministic.

## Test checklist

- [x] Split/reassemble round-trip (4 fragments)
- [x] Out-of-order reassembly (reverse feed)
- [x] Duplicate fragment (silent skip)
- [x] Count mismatch → ErrInvalidFragmentHeader
- [x] Flags mismatch → ErrInvalidFragmentHeader
- [x] Stale buffer cleanup after timeout
- [x] Single-fragment envelope with compression flags
- [x] Message too large (256 fragments) → ErrMessageTooLarge
- [x] Empty payload → 1 fragment, reassembles to empty
- [x] Exact boundary (MaxFragmentPayload → 1 frag, +1 → 2 frags)
- [x] Max fragments (255) succeeds
- [x] Invalid index (index >= count) → error
- [x] Zero count → error
- [x] Connection integration (newConnection creates non-nil reassembly store)
