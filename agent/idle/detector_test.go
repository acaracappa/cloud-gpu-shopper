package idle

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewDetector_DefaultThreshold(t *testing.T) {
	// When threshold is <= 0, should use DefaultThresholdPct
	tests := []struct {
		name      string
		threshold float64
	}{
		{"zero threshold", 0},
		{"negative threshold", -1},
		{"negative float threshold", -5.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDetector(tt.threshold)
			assert.NotNil(t, d)
			assert.Equal(t, DefaultThresholdPct, d.thresholdPct)
		})
	}
}

func TestNewDetector_CustomThreshold(t *testing.T) {
	tests := []struct {
		name      string
		threshold float64
	}{
		{"small positive", 0.1},
		{"typical threshold", 5.0},
		{"high threshold", 50.0},
		{"very high threshold", 99.9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDetector(tt.threshold)
			assert.NotNil(t, d)
			assert.Equal(t, tt.threshold, d.thresholdPct)
		})
	}
}

func TestRecordSample_IncrementsWhenIdle(t *testing.T) {
	d := NewDetector(10.0) // 10% threshold

	// Wait a bit and record an idle sample (below threshold)
	time.Sleep(100 * time.Millisecond)
	d.RecordSample(5.0) // 5% < 10%, should be idle

	// Idle time should be incremented (at least 0 seconds due to rounding)
	idleTime := d.IdleSeconds()
	assert.GreaterOrEqual(t, idleTime, 0)

	// Record another idle sample after waiting
	time.Sleep(1100 * time.Millisecond)
	d.RecordSample(0.0) // 0% < 10%, still idle

	// Should have accumulated at least 1 second
	idleTime = d.IdleSeconds()
	assert.GreaterOrEqual(t, idleTime, 1)
}

func TestRecordSample_ResetsWhenActive(t *testing.T) {
	d := NewDetector(10.0) // 10% threshold

	// Accumulate some idle time
	time.Sleep(1100 * time.Millisecond)
	d.RecordSample(5.0) // idle

	idleTime := d.IdleSeconds()
	assert.GreaterOrEqual(t, idleTime, 1)

	// Now record an active sample (at or above threshold)
	time.Sleep(100 * time.Millisecond)
	d.RecordSample(15.0) // 15% >= 10%, active

	// Idle time should be reset to zero
	assert.Equal(t, 0, d.IdleSeconds())
}

func TestRecordSample_BoundaryConditions(t *testing.T) {
	threshold := 10.0
	d := NewDetector(threshold)

	// Exactly at threshold should NOT be considered idle (resets counter)
	time.Sleep(1100 * time.Millisecond)
	d.RecordSample(5.0) // First, accumulate some idle time

	idleTime := d.IdleSeconds()
	assert.GreaterOrEqual(t, idleTime, 1)

	// Now sample exactly at threshold
	time.Sleep(100 * time.Millisecond)
	d.RecordSample(threshold) // exactly 10%

	// Should reset because utilizationPct >= thresholdPct
	assert.Equal(t, 0, d.IdleSeconds())

	// Just below threshold should be idle
	d2 := NewDetector(threshold)
	time.Sleep(1100 * time.Millisecond)
	d2.RecordSample(threshold - 0.01) // 9.99%

	idleTime = d2.IdleSeconds()
	assert.GreaterOrEqual(t, idleTime, 1)
}

func TestIdleSeconds_ReturnsAccumulatedTime(t *testing.T) {
	d := NewDetector(10.0)

	// Initial idle time should be zero
	assert.Equal(t, 0, d.IdleSeconds())

	// Accumulate idle time over multiple samples
	time.Sleep(1100 * time.Millisecond)
	d.RecordSample(1.0)
	firstIdle := d.IdleSeconds()
	assert.GreaterOrEqual(t, firstIdle, 1)

	time.Sleep(1100 * time.Millisecond)
	d.RecordSample(2.0)
	secondIdle := d.IdleSeconds()
	assert.GreaterOrEqual(t, secondIdle, 2)

	// Second reading should be greater than first
	assert.Greater(t, secondIdle, firstIdle)
}

func TestReset_ClearsIdleTime(t *testing.T) {
	d := NewDetector(10.0)

	// Accumulate some idle time
	time.Sleep(1100 * time.Millisecond)
	d.RecordSample(5.0)

	idleTime := d.IdleSeconds()
	assert.GreaterOrEqual(t, idleTime, 1)

	// Reset should clear idle time
	d.Reset()
	assert.Equal(t, 0, d.IdleSeconds())

	// After reset, can accumulate again
	time.Sleep(1100 * time.Millisecond)
	d.RecordSample(3.0)
	assert.GreaterOrEqual(t, d.IdleSeconds(), 1)
}

func TestConcurrentAccess(t *testing.T) {
	d := NewDetector(10.0)

	var wg sync.WaitGroup
	numGoroutines := 10
	iterations := 100

	// Multiple goroutines recording samples
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Alternate between idle and active samples
				if j%2 == 0 {
					d.RecordSample(1.0) // idle
				} else {
					d.RecordSample(50.0) // active
				}
			}
		}(i)
	}

	// Multiple goroutines reading idle seconds
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = d.IdleSeconds()
			}
		}()
	}

	// Multiple goroutines resetting
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations/10; j++ {
				d.Reset()
			}
		}()
	}

	// All goroutines should complete without race conditions
	wg.Wait()

	// Detector should still be in a valid state
	idleTime := d.IdleSeconds()
	assert.GreaterOrEqual(t, idleTime, 0)
}
