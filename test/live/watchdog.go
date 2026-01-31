//go:build live
// +build live

package live

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Note: We need context for diagnostics collection, so we use it in monitor()

// Watchdog monitors test execution and enforces safety limits
type Watchdog struct {
	config      *TestConfig
	startTime   time.Time
	cancel      context.CancelFunc
	mu          sync.Mutex
	spendByProv map[Provider]float64
	timeByProv  map[Provider]time.Duration
	instances   map[string]InstanceInfo // instanceID -> info
	diagManager *DiagnosticsManager     // optional diagnostics manager
	providers   *ProviderFactory        // provider factory for direct API access
}

// InstanceInfo tracks a live instance for cleanup
type InstanceInfo struct {
	InstanceID string
	SessionID  string
	Provider   Provider
	StartTime  time.Time
	PriceHour  float64
}

// NewWatchdog creates a new safety watchdog
func NewWatchdog(config *TestConfig) *Watchdog {
	return &Watchdog{
		config:      config,
		startTime:   time.Now(),
		spendByProv: make(map[Provider]float64),
		timeByProv:  make(map[Provider]time.Duration),
		instances:   make(map[string]InstanceInfo),
		providers:   NewProviderFactory(config),
	}
}

// SetProviderFactory sets the provider factory for direct API access
func (w *Watchdog) SetProviderFactory(pf *ProviderFactory) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.providers = pf
}

// SetDiagnosticsManager sets the diagnostics manager for automatic collection
func (w *Watchdog) SetDiagnosticsManager(dm *DiagnosticsManager) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.diagManager = dm
}

// GetProviderFactory returns the provider factory for direct API access
func (w *Watchdog) GetProviderFactory() *ProviderFactory {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.providers
}

// Start begins watchdog monitoring with a parent context
func (w *Watchdog) Start(parentCtx context.Context) context.Context {
	ctx, cancel := context.WithCancel(parentCtx)
	w.cancel = cancel

	go w.monitor(ctx)
	return ctx
}

// Stop stops the watchdog and cleans up all instances
func (w *Watchdog) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.cleanupAllInstances()
}

// TrackInstance registers an instance for tracking and cleanup
func (w *Watchdog) TrackInstance(info InstanceInfo) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.instances[info.InstanceID] = info
	log.Printf("WATCHDOG: Tracking instance %s (provider=%s, session=%s)",
		info.InstanceID, info.Provider, info.SessionID)
}

// UntrackInstance removes an instance from tracking (after successful destroy)
func (w *Watchdog) UntrackInstance(instanceID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if info, ok := w.instances[instanceID]; ok {
		// Record spend for this instance
		duration := time.Since(info.StartTime)
		spend := info.PriceHour * duration.Hours()
		w.spendByProv[info.Provider] += spend
		w.timeByProv[info.Provider] += duration
		delete(w.instances, instanceID)
		log.Printf("WATCHDOG: Untracked instance %s (spent=$%.4f, duration=%v)",
			instanceID, spend, duration)
	}
}

// CheckLimits returns an error if any limits are exceeded
func (w *Watchdog) CheckLimits() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	totalSpend := 0.0
	for _, spend := range w.spendByProv {
		totalSpend += spend
	}

	// Add estimated spend for running instances
	for _, info := range w.instances {
		duration := time.Since(info.StartTime)
		totalSpend += info.PriceHour * duration.Hours()
	}

	totalRuntime := time.Since(w.startTime)

	// Check global limits
	if totalSpend > w.config.MaxTotalSpendUSD {
		return &LimitExceededError{
			Limit:   "total_spend",
			Current: totalSpend,
			Max:     w.config.MaxTotalSpendUSD,
		}
	}

	if totalRuntime > w.config.MaxTotalRuntime {
		return &LimitExceededError{
			Limit:   "total_runtime",
			Current: float64(totalRuntime.Minutes()),
			Max:     float64(w.config.MaxTotalRuntime.Minutes()),
		}
	}

	// Check per-provider limits
	for prov, cfg := range w.config.Providers {
		if !cfg.Enabled {
			continue
		}

		provSpend := w.spendByProv[prov]
		// Add running instances
		for _, info := range w.instances {
			if info.Provider == prov {
				duration := time.Since(info.StartTime)
				provSpend += info.PriceHour * duration.Hours()
			}
		}

		if provSpend > w.config.MaxPerProviderUSD {
			return &LimitExceededError{
				Limit:    string(prov) + "_spend",
				Current:  provSpend,
				Max:      w.config.MaxPerProviderUSD,
				Provider: prov,
			}
		}
	}

	return nil
}

// GetStats returns current spend and runtime statistics
func (w *Watchdog) GetStats() WatchdogStats {
	w.mu.Lock()
	defer w.mu.Unlock()

	stats := WatchdogStats{
		TotalRuntime:    time.Since(w.startTime),
		SpendByProv:     make(map[Provider]float64),
		ActiveInstances: len(w.instances),
	}

	// Copy spend by provider
	for prov, spend := range w.spendByProv {
		stats.SpendByProv[prov] = spend
	}

	// Add running instance costs
	for _, info := range w.instances {
		duration := time.Since(info.StartTime)
		stats.SpendByProv[info.Provider] += info.PriceHour * duration.Hours()
		stats.TotalSpend += info.PriceHour * duration.Hours()
	}

	// Add completed instance costs
	for _, spend := range w.spendByProv {
		stats.TotalSpend += spend
	}

	return stats
}

// WatchdogStats contains current watchdog statistics
type WatchdogStats struct {
	TotalRuntime    time.Duration
	TotalSpend      float64
	SpendByProv     map[Provider]float64
	ActiveInstances int
}

// LimitExceededError indicates a safety limit was exceeded
type LimitExceededError struct {
	Limit    string
	Current  float64
	Max      float64
	Provider Provider
}

func (e *LimitExceededError) Error() string {
	if e.Provider != "" {
		return fmt.Sprintf("limit %s exceeded for %s: %.2f > %.2f",
			e.Limit, e.Provider, e.Current, e.Max)
	}
	return fmt.Sprintf("limit %s exceeded: %.2f > %.2f", e.Limit, e.Current, e.Max)
}

func (w *Watchdog) monitor(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("WATCHDOG: Context cancelled, cleaning up...")
			w.cleanupAllInstances()
			return

		case <-ticker.C:
			stats := w.GetStats()
			log.Printf("WATCHDOG: Runtime=%v, Spend=$%.4f, Active=%d",
				stats.TotalRuntime.Round(time.Second),
				stats.TotalSpend,
				stats.ActiveInstances)

			if err := w.CheckLimits(); err != nil {
				log.Printf("WATCHDOG: LIMIT EXCEEDED: %v", err)
				// Collect diagnostics before cleanup
				w.collectDiagnosticsOnLimitExceeded(ctx, err)
				w.cleanupAllInstances()
				w.cancel()
				return
			}
		}
	}
}

func (w *Watchdog) cleanupAllInstances() {
	w.mu.Lock()
	instances := make([]InstanceInfo, 0, len(w.instances))
	for _, info := range w.instances {
		instances = append(instances, info)
	}
	w.mu.Unlock()

	if len(instances) == 0 {
		log.Println("WATCHDOG: No instances to clean up")
		return
	}

	log.Printf("WATCHDOG: Cleaning up %d instances...", len(instances))

	// Use a timeout for cleanup operations
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, info := range instances {
		log.Printf("WATCHDOG: Force destroying instance %s (session=%s, provider=%s)",
			info.InstanceID, info.SessionID, info.Provider)

		// Try to destroy directly via provider API
		if err := w.forceDestroyInstance(ctx, info); err != nil {
			log.Printf("WATCHDOG: Failed to destroy instance %s: %v", info.InstanceID, err)
		} else {
			log.Printf("WATCHDOG: Successfully destroyed instance %s", info.InstanceID)
		}
	}

	log.Println("WATCHDOG: Cleanup complete")
}

// forceDestroyInstance destroys an instance directly via the provider API.
// This is a fallback when the HTTP API is unavailable.
func (w *Watchdog) forceDestroyInstance(ctx context.Context, info InstanceInfo) error {
	if w.providers == nil {
		return fmt.Errorf("provider factory not initialized")
	}

	prov, err := w.providers.GetProvider(info.Provider)
	if err != nil {
		return fmt.Errorf("failed to get provider %s: %w", info.Provider, err)
	}

	if err := prov.DestroyInstance(ctx, info.InstanceID); err != nil {
		return fmt.Errorf("failed to destroy instance %s: %w", info.InstanceID, err)
	}

	return nil
}

// collectDiagnosticsOnLimitExceeded collects diagnostics from all active instances
func (w *Watchdog) collectDiagnosticsOnLimitExceeded(ctx context.Context, limitErr error) {
	w.mu.Lock()
	dm := w.diagManager
	w.mu.Unlock()

	if dm == nil || !dm.IsEnabled() {
		return
	}

	log.Printf("WATCHDOG: Collecting diagnostics before cleanup (reason: %v)", limitErr)

	// Create a timeout context for diagnostics collection
	diagCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	dm.CollectAllSnapshots(diagCtx, fmt.Sprintf("limit_exceeded_%s", limitErr.Error()))
}
