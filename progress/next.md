# Future Work

All 11 implementation phases are complete. Below are potential improvements for future development.

## Stateless Cookies

Add a stateless cookie mechanism to the handshake to mitigate connection flooding attacks. The server would issue a cookie in its first response (without allocating state), and the client would echo it back before the server commits resources to the connection.

## sendmmsg/recvmmsg Batch I/O

Replace the current multi-goroutine coalescing with `golang.org/x/net/ipv4` `ReadBatch`/`WriteBatch` for true multi-message syscalls on Linux. This would reduce context switch overhead beyond what concurrent goroutines achieve.

## Additional Ideas

- **FEC (Forward Error Correction)**: add optional Reed-Solomon or XOR parity packets for proactive loss recovery on unreliable channels.
- **Priority queues**: per-channel send priority to ensure critical messages (e.g., game state) are sent before lower-priority data (e.g., cosmetic updates).
- **Adaptive bandwidth QoS**: use the bandwidth estimator to automatically adjust send rate or compression level based on available bandwidth.
- **Connection migration validation**: add a challenge-response step when migration is detected, to prevent spoofed tokens from hijacking connections.
- **Batch coalescing for client**: extend write coalescing to the client side for scenarios where the client sends to multiple servers.
