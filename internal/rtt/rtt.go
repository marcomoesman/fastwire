package rtt

import (
	"math"
	"sync"
	"time"
)

// RTT/RTO constants.
const (
	rtoMin   = 0.050 // 50ms minimum RTO
	rtoMax   = 5.0   // 5s maximum RTO
	rttAlpha = 0.125 // SRTT smoothing factor
	rttBeta  = 0.25  // RTTVAR smoothing factor
)

// State tracks RTT measurements and computes RTO using the Jacobson/Karels algorithm.
type State struct {
	srtt      float64 // smoothed RTT in seconds
	rttvar    float64 // RTT variance in seconds
	rto       float64 // retransmission timeout in seconds
	hasSample bool
	mu        sync.Mutex
}

// New creates a State with the initial RTO of 1.0 second.
func New() *State {
	return &State{
		rto: 1.0,
	}
}

// AddSample updates the RTT estimator with a new measurement.
func (r *State) AddSample(sample time.Duration) {
	s := sample.Seconds()
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.hasSample {
		// First sample: RFC 6298 Section 2.2
		r.srtt = s
		r.rttvar = s / 2.0
		r.hasSample = true
	} else {
		// Subsequent samples: Jacobson/Karels
		r.rttvar = (1.0-rttBeta)*r.rttvar + rttBeta*math.Abs(r.srtt-s)
		r.srtt = (1.0-rttAlpha)*r.srtt + rttAlpha*s
	}

	r.rto = r.srtt + 4.0*r.rttvar
	r.rto = max(r.rto, rtoMin)
	r.rto = min(r.rto, rtoMax)
}

// RTO returns the current retransmission timeout.
func (r *State) RTO() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Duration(r.rto * float64(time.Second))
}

// SRTT returns the current smoothed RTT.
func (r *State) SRTT() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Duration(r.srtt * float64(time.Second))
}

// RTTVar returns the current RTT variance.
func (r *State) RTTVar() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Duration(r.rttvar * float64(time.Second))
}
