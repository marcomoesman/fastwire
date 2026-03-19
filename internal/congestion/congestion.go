package congestion

import "sync"

// Mode selects a congestion control strategy.
type Mode int

const (
	// Conservative uses AIMD congestion control.
	Conservative Mode = iota
	// Aggressive disables window gating and uses fast retransmit.
	Aggressive
)

// Default congestion control constants.
const (
	// minCwnd is the minimum congestion window (floor for multiplicative decrease).
	minCwnd = 2.0

	// fastRetransmitThreshold is the number of duplicate acks before triggering fast retransmit.
	fastRetransmitThreshold = 2
)

// Controller governs the send rate for a connection.
type Controller interface {
	// OnAck is called when packets are acknowledged.
	OnAck(ackedPackets int)
	// OnLoss is called when packet loss is detected.
	OnLoss()
	// CanSend reports whether sending is allowed given the current in-flight count.
	CanSend(inFlight int) bool
	// OnDuplicateAck is called when a duplicate ack is received.
	// Returns true if fast retransmit should be triggered.
	OnDuplicateAck() bool
	// HalvesRTO reports whether the RTO should be halved instead of doubled on retransmit.
	HalvesRTO() bool
	// Window returns the current congestion window size in packets.
	// Returns 0 for unlimited (aggressive mode).
	Window() int
}

// NewController creates a Controller for the given mode.
// If initialCwnd is 0 or negative, a default of 4 is used.
func NewController(mode Mode, initialCwnd int) Controller {
	if initialCwnd <= 0 {
		initialCwnd = 4
	}
	switch mode {
	case Aggressive:
		return &AggressiveCC{}
	default:
		return &ConservativeCC{cwnd: float64(initialCwnd)}
	}
}

// --- Conservative (AIMD) ---

// ConservativeCC implements AIMD congestion control.
type ConservativeCC struct {
	cwnd float64
	mu   sync.Mutex
}

func (c *ConservativeCC) OnAck(ackedPackets int) {
	c.mu.Lock()
	c.cwnd += float64(ackedPackets) / c.cwnd
	c.mu.Unlock()
}

func (c *ConservativeCC) OnLoss() {
	c.mu.Lock()
	c.cwnd = max(c.cwnd*0.5, minCwnd)
	c.mu.Unlock()
}

func (c *ConservativeCC) CanSend(inFlight int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return inFlight < int(c.cwnd)
}

func (c *ConservativeCC) OnDuplicateAck() bool {
	return false
}

func (c *ConservativeCC) HalvesRTO() bool {
	return false
}

// Window returns the current congestion window size in packets.
func (c *ConservativeCC) Window() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int(c.cwnd)
}

// Cwnd returns the current congestion window size. Used for testing.
func (c *ConservativeCC) Cwnd() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cwnd
}

// --- Aggressive ---

// AggressiveCC disables window gating and uses fast retransmit.
type AggressiveCC struct {
	dupAckCount int
	mu          sync.Mutex
}

func (a *AggressiveCC) OnAck(_ int) {
	a.mu.Lock()
	a.dupAckCount = 0
	a.mu.Unlock()
}

func (a *AggressiveCC) OnLoss() {
	// No-op: aggressive mode does not reduce send rate on loss.
}

func (a *AggressiveCC) CanSend(_ int) bool {
	return true
}

func (a *AggressiveCC) OnDuplicateAck() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dupAckCount++
	if a.dupAckCount >= fastRetransmitThreshold {
		a.dupAckCount = 0
		return true
	}
	return false
}

func (a *AggressiveCC) HalvesRTO() bool {
	return true
}

// Window returns 0 indicating unlimited send rate.
func (a *AggressiveCC) Window() int {
	return 0
}
