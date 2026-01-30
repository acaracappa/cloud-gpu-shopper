// Package idle provides GPU idle detection based on utilization samples.
package idle

import (
	"sync"
	"time"
)

// DefaultThresholdPct is the default GPU utilization percentage below which
// the GPU is considered idle.
const DefaultThresholdPct = 5.0

// Detector tracks GPU idle time based on utilization samples.
// It is safe for concurrent use.
type Detector struct {
	thresholdPct float64
	idleSeconds  int
	lastSample   time.Time
	mu           sync.Mutex
}

// NewDetector creates a new idle detector with the given threshold.
// If thresholdPct is <= 0, DefaultThresholdPct is used.
func NewDetector(thresholdPct float64) *Detector {
	if thresholdPct <= 0 {
		thresholdPct = DefaultThresholdPct
	}
	return &Detector{
		thresholdPct: thresholdPct,
		lastSample:   time.Now(),
	}
}

// RecordSample records a GPU utilization sample.
// If utilization is below the threshold, idle time is incremented by the
// duration since the last sample. If utilization is at or above the threshold,
// idle time is reset to zero.
func (d *Detector) RecordSample(utilizationPct float64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	elapsed := int(now.Sub(d.lastSample).Seconds())

	if utilizationPct < d.thresholdPct {
		d.idleSeconds += elapsed
	} else {
		d.idleSeconds = 0
	}

	d.lastSample = now
}

// IdleSeconds returns the current consecutive idle duration in seconds.
func (d *Detector) IdleSeconds() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.idleSeconds
}

// Reset resets the idle counter to zero.
func (d *Detector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.idleSeconds = 0
}
