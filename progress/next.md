# Future Work

Phase 14 (security & stability hardening) is complete. The next committed phase is 15.

## Next: Phase 15 — Stateless Retry Cookie Handshake

Design and checklist live in `progress/phase-15.md`. Bumps `ProtocolVersion` to 2 and replaces the interim pending-table / rate-limit mitigation with a proper cookie round-trip that costs the server zero state for un-cookied CONNECTs.

## sendmmsg/recvmmsg Batch I/O

Replace the current multi-goroutine coalescing with `golang.org/x/net/ipv4` `ReadBatch`/`WriteBatch` for true multi-message syscalls on Linux. This would reduce context switch overhead beyond what concurrent goroutines achieve.

## Additional Ideas

- **FEC (Forward Error Correction)**: add optional Reed-Solomon or XOR parity packets for proactive loss recovery on unreliable channels.
- **Priority queues**: per-channel send priority to ensure critical messages (e.g., game state) are sent before lower-priority data (e.g., cosmetic updates).
- **Adaptive bandwidth QoS**: use the bandwidth estimator to automatically adjust send rate or compression level based on available bandwidth.
- **Connection migration validation**: add a challenge-response step when migration is detected, to prevent spoofed tokens from hijacking connections.
- **Batch coalescing for client**: extend write coalescing to the client side for scenarios where the client sends to multiple servers.
