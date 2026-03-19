// Package bandwidth provides an EWMA-based bandwidth estimator.
package bandwidth

import (
	"sync"
	"time"
)

const alpha = 0.2

// Estimator tracks bytes over time and produces a smoothed bytes-per-second estimate.
type Estimator struct {
	mu        sync.Mutex
	bytes     uint64
	estimate  float64
	lastTick  time.Time
	started   bool
}

// New creates a new bandwidth estimator.
func New() *Estimator {
	return &Estimator{lastTick: time.Now()}
}

// Record adds n bytes to the current measurement period.
func (e *Estimator) Record(n uint64) {
	e.mu.Lock()
	e.bytes += n
	e.mu.Unlock()
}

// Tick updates the EWMA estimate. Call once per tick.
func (e *Estimator) Tick() {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()

	dt := now.Sub(e.lastTick).Seconds()
	if dt <= 0 {
		return
	}

	rate := float64(e.bytes) / dt
	if !e.started {
		e.estimate = rate
		e.started = true
	} else {
		e.estimate = alpha*rate + (1-alpha)*e.estimate
	}

	e.bytes = 0
	e.lastTick = now
}

// BytesPerSecond returns the current smoothed bandwidth estimate.
func (e *Estimator) BytesPerSecond() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.estimate
}
