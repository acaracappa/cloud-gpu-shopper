package runner

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Watchdog monitors cost and time limits during benchmark runs
type Watchdog struct {
	maxCost       float64
	maxDuration   time.Duration
	alertCost     float64
	alertDuration time.Duration

	startTime    time.Time
	pricePerHour float64
	sessionID    string

	alertCostSent     bool
	alertDurationSent bool

	mu        sync.RWMutex
	callbacks WatchdogCallbacks
}

// WatchdogCallbacks defines callback functions for watchdog events
type WatchdogCallbacks struct {
	OnCostAlert     func(currentCost, maxCost float64)
	OnDurationAlert func(elapsed, maxDuration time.Duration)
	OnCostExceeded  func(currentCost, maxCost float64)
	OnTimeExceeded  func(elapsed, maxDuration time.Duration)
}

// NewWatchdog creates a new watchdog with the given limits
func NewWatchdog(maxCost float64, maxDuration time.Duration, alertCost float64, alertDuration time.Duration) *Watchdog {
	return &Watchdog{
		maxCost:       maxCost,
		maxDuration:   maxDuration,
		alertCost:     alertCost,
		alertDuration: alertDuration,
		startTime:     time.Now(),
	}
}

// SetSession configures the session being monitored
func (w *Watchdog) SetSession(sessionID string, pricePerHour float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sessionID = sessionID
	w.pricePerHour = pricePerHour
	w.startTime = time.Now()
}

// SetCallbacks sets the callback functions
func (w *Watchdog) SetCallbacks(callbacks WatchdogCallbacks) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.callbacks = callbacks
}

// Start begins monitoring in a goroutine
func (w *Watchdog) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.check(); err != nil {
				fmt.Printf("Watchdog: %v\n", err)
			}
		}
	}
}

// check performs a single check of limits
func (w *Watchdog) check() error {
	w.mu.RLock()
	pricePerHour := w.pricePerHour
	startTime := w.startTime
	callbacks := w.callbacks
	alertCostSent := w.alertCostSent
	alertDurationSent := w.alertDurationSent
	w.mu.RUnlock()

	elapsed := time.Since(startTime)
	currentCost := pricePerHour * elapsed.Hours()

	// Check cost alert threshold
	if !alertCostSent && currentCost >= w.alertCost {
		w.mu.Lock()
		w.alertCostSent = true
		w.mu.Unlock()

		fmt.Printf("Watchdog: Cost alert - $%.2f (%.0f%% of $%.2f limit)\n",
			currentCost, (currentCost/w.maxCost)*100, w.maxCost)

		if callbacks.OnCostAlert != nil {
			callbacks.OnCostAlert(currentCost, w.maxCost)
		}
	}

	// Check duration alert threshold
	if !alertDurationSent && elapsed >= w.alertDuration {
		w.mu.Lock()
		w.alertDurationSent = true
		w.mu.Unlock()

		fmt.Printf("Watchdog: Duration alert - %s (%.0f%% of %s limit)\n",
			elapsed.Round(time.Second), (float64(elapsed)/float64(w.maxDuration))*100, w.maxDuration)

		if callbacks.OnDurationAlert != nil {
			callbacks.OnDurationAlert(elapsed, w.maxDuration)
		}
	}

	// Check cost exceeded
	if currentCost >= w.maxCost {
		fmt.Printf("Watchdog: COST LIMIT EXCEEDED - $%.2f >= $%.2f\n", currentCost, w.maxCost)
		if callbacks.OnCostExceeded != nil {
			callbacks.OnCostExceeded(currentCost, w.maxCost)
		}
		return fmt.Errorf("cost limit exceeded: $%.2f >= $%.2f", currentCost, w.maxCost)
	}

	// Check time exceeded
	if elapsed >= w.maxDuration {
		fmt.Printf("Watchdog: TIME LIMIT EXCEEDED - %s >= %s\n", elapsed.Round(time.Second), w.maxDuration)
		if callbacks.OnTimeExceeded != nil {
			callbacks.OnTimeExceeded(elapsed, w.maxDuration)
		}
		return fmt.Errorf("time limit exceeded: %s >= %s", elapsed.Round(time.Second), w.maxDuration)
	}

	return nil
}

// GetStatus returns the current watchdog status
func (w *Watchdog) GetStatus() WatchdogStatus {
	w.mu.RLock()
	defer w.mu.RUnlock()

	elapsed := time.Since(w.startTime)
	currentCost := w.pricePerHour * elapsed.Hours()

	return WatchdogStatus{
		SessionID:          w.sessionID,
		StartTime:          w.startTime,
		Elapsed:            elapsed,
		CurrentCost:        currentCost,
		MaxCost:            w.maxCost,
		MaxDuration:        w.maxDuration,
		CostPercent:        (currentCost / w.maxCost) * 100,
		DurationPercent:    (float64(elapsed) / float64(w.maxDuration)) * 100,
		CostAlertSent:      w.alertCostSent,
		DurationAlertSent:  w.alertDurationSent,
		CostExceeded:       currentCost >= w.maxCost,
		DurationExceeded:   elapsed >= w.maxDuration,
	}
}

// WatchdogStatus represents the current state of the watchdog
type WatchdogStatus struct {
	SessionID         string        `json:"session_id"`
	StartTime         time.Time     `json:"start_time"`
	Elapsed           time.Duration `json:"elapsed"`
	CurrentCost       float64       `json:"current_cost"`
	MaxCost           float64       `json:"max_cost"`
	MaxDuration       time.Duration `json:"max_duration"`
	CostPercent       float64       `json:"cost_percent"`
	DurationPercent   float64       `json:"duration_percent"`
	CostAlertSent     bool          `json:"cost_alert_sent"`
	DurationAlertSent bool          `json:"duration_alert_sent"`
	CostExceeded      bool          `json:"cost_exceeded"`
	DurationExceeded  bool          `json:"duration_exceeded"`
}

// String returns a human-readable status string
func (s WatchdogStatus) String() string {
	return fmt.Sprintf(
		"Session: %s | Elapsed: %s (%.0f%%) | Cost: $%.2f (%.0f%%) | Alerts: cost=%t duration=%t",
		s.SessionID,
		s.Elapsed.Round(time.Second),
		s.DurationPercent,
		s.CurrentCost,
		s.CostPercent,
		s.CostAlertSent,
		s.DurationAlertSent,
	)
}

// EstimateTotalCost estimates the total cost for a given duration
func EstimateTotalCost(pricePerHour float64, duration time.Duration) float64 {
	return pricePerHour * duration.Hours()
}

// EstimateRemainingBudget calculates remaining budget based on current usage
func (w *Watchdog) EstimateRemainingBudget() float64 {
	status := w.GetStatus()
	return w.maxCost - status.CurrentCost
}

// EstimateRemainingTime calculates remaining time before duration limit
func (w *Watchdog) EstimateRemainingTime() time.Duration {
	status := w.GetStatus()
	remaining := w.maxDuration - status.Elapsed
	if remaining < 0 {
		return 0
	}
	return remaining
}
