package fastwire

import "time"

// ConnectionStats contains per-connection metrics.
type ConnectionStats struct {
	RTT              time.Duration // smoothed round-trip time
	RTTVariance      time.Duration // RTT variance
	PacketLoss       float64       // 0.0-1.0, based on last 100 reliable packets
	BytesSent        uint64        // total wire bytes sent
	BytesReceived    uint64        // total wire bytes received
	CongestionWindow int           // current congestion window (0 = unlimited)
	Uptime           time.Duration // time since connection was established
	SendBandwidth    float64       // estimated send throughput (bytes/sec)
	RecvBandwidth    float64       // estimated receive throughput (bytes/sec)
}
