package inventory

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"
)

// FailureType categorizes provisioning failures for offer tracking
type FailureType string

const (
	FailureStaleInventory FailureType = "stale_inventory"
	FailureInstanceStopped FailureType = "instance_stopped"
	FailureSSHTimeout      FailureType = "ssh_timeout"
	FailureUnknown         FailureType = "unknown"
)

const (
	// SuppressionThreshold is the number of failures within SuppressionWindow
	// before an offer is suppressed from results
	SuppressionThreshold = 3

	// SuppressionWindow is the time window for counting failures toward suppression
	SuppressionWindow = 30 * time.Minute

	// SuppressionCooldown is how long a suppressed offer stays hidden
	SuppressionCooldown = 30 * time.Minute

	// FailureDecayPeriod is how long failure events are retained before cleanup
	FailureDecayPeriod = 1 * time.Hour

	// GPUTypeFailureThreshold is how many distinct offers of the same GPU type
	// must fail before all offers of that type are degraded
	GPUTypeFailureThreshold = 3
)

// FailureStore is the interface for persisting offer failure data.
// Implemented by storage.OfferFailureStore.
type FailureStore interface {
	RecordFailure(ctx context.Context, offerID, provider, gpuType, failureType, reason string) error
	SetSuppression(ctx context.Context, offerID, provider, gpuType string, suppressedAt time.Time) error
	ClearSuppression(ctx context.Context, offerID string) error
	CleanupOldFailures(ctx context.Context, before time.Time) (int64, error)
	CleanupExpiredSuppressions(ctx context.Context, before time.Time) (int64, error)
}

// OfferFailureTracker tracks provisioning failures across sessions to degrade
// or suppress offers that repeatedly fail. This prevents the system from
// recommending offers that are known to be broken (BUG-010, BUG-011, BUG-012).
type OfferFailureTracker struct {
	mu       sync.RWMutex
	offers   map[string]*offerFailureRecord // keyed by offer ID
	gpuTypes map[string]*gpuTypeRecord      // keyed by "provider:GPUType"

	// Optional persistent storage (nil = in-memory only)
	store  FailureStore
	logger *slog.Logger
}

type offerFailureRecord struct {
	Provider     string
	GPUType      string
	Failures     []failureEvent
	SuppressedAt time.Time // zero if not suppressed
}

type failureEvent struct {
	Type      FailureType
	Timestamp time.Time
	Reason    string
}

type gpuTypeRecord struct {
	FailedOfferIDs map[string]time.Time // offer ID → last failure time
}

// OfferHealthInfo exposes health data for the /api/v1/offer-health endpoint
type OfferHealthInfo struct {
	OfferID              string      `json:"offer_id"`
	Provider             string      `json:"provider"`
	GPUType              string      `json:"gpu_type"`
	RecentFailures       int         `json:"recent_failures"`
	IsSuppressed         bool        `json:"is_suppressed"`
	SuppressedAt         *time.Time  `json:"suppressed_at,omitempty"`
	SuppressedUntil      *time.Time  `json:"suppressed_until,omitempty"`
	ConfidenceMultiplier float64     `json:"confidence_multiplier"`
	LastFailure          *time.Time  `json:"last_failure,omitempty"`
	LastFailureType      FailureType `json:"last_failure_type,omitempty"`
	LastFailureReason    string      `json:"last_failure_reason,omitempty"`
}

// GPUTypeHealthInfo exposes GPU-type-level health data
type GPUTypeHealthInfo struct {
	GPUTypeKey         string  `json:"gpu_type_key"` // "provider:GPUType"
	FailedOfferCount   int     `json:"failed_offer_count"`
	IsDegraded         bool    `json:"is_degraded"`
	DegradationApplied float64 `json:"degradation_applied,omitempty"`
}

// NewOfferFailureTracker creates a new failure tracker
func NewOfferFailureTracker() *OfferFailureTracker {
	return &OfferFailureTracker{
		offers:   make(map[string]*offerFailureRecord),
		gpuTypes: make(map[string]*gpuTypeRecord),
		logger:   slog.Default(),
	}
}

// SetStore attaches a persistent store to the tracker.
// When set, failures and suppressions are written through to the DB.
func (t *OfferFailureTracker) SetStore(store FailureStore, logger *slog.Logger) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.store = store
	if logger != nil {
		t.logger = logger
	}
}

// LoadFromStore replays persisted failure events and suppressions into the
// in-memory tracker. Call this once at startup after SetStore.
func (t *OfferFailureTracker) LoadFromStore(ctx context.Context, failures []StoredFailure, suppressions []StoredSuppression) {
	t.mu.Lock()
	defer t.mu.Unlock()

	loaded := 0
	for _, f := range failures {
		record, exists := t.offers[f.OfferID]
		if !exists {
			record = &offerFailureRecord{
				Provider: f.Provider,
				GPUType:  f.GPUType,
			}
			t.offers[f.OfferID] = record
		}

		record.Failures = append(record.Failures, failureEvent{
			Type:      FailureType(f.FailureType),
			Timestamp: f.CreatedAt,
			Reason:    f.Reason,
		})

		// Update GPU-type record
		gpuKey := f.Provider + ":" + f.GPUType
		gpuRec, exists := t.gpuTypes[gpuKey]
		if !exists {
			gpuRec = &gpuTypeRecord{
				FailedOfferIDs: make(map[string]time.Time),
			}
			t.gpuTypes[gpuKey] = gpuRec
		}
		// Keep the most recent failure time per offer
		if existing, ok := gpuRec.FailedOfferIDs[f.OfferID]; !ok || f.CreatedAt.After(existing) {
			gpuRec.FailedOfferIDs[f.OfferID] = f.CreatedAt
		}
		loaded++
	}

	// Restore suppressions
	for _, s := range suppressions {
		record, exists := t.offers[s.OfferID]
		if !exists {
			record = &offerFailureRecord{
				Provider: s.Provider,
				GPUType:  s.GPUType,
			}
			t.offers[s.OfferID] = record
		}
		record.SuppressedAt = s.SuppressedAt
	}

	// Cleanup any expired data
	t.cleanupLocked(time.Now())

	t.logger.Info("loaded failure tracking data from store",
		slog.Int("failures", loaded),
		slog.Int("suppressions", len(suppressions)),
		slog.Int("tracked_offers", len(t.offers)),
		slog.Int("tracked_gpu_types", len(t.gpuTypes)))
}

// StoredFailure represents a failure event loaded from the DB
type StoredFailure struct {
	OfferID     string
	Provider    string
	GPUType     string
	FailureType string
	Reason      string
	CreatedAt   time.Time
}

// StoredSuppression represents a suppression record loaded from the DB
type StoredSuppression struct {
	OfferID      string
	Provider     string
	GPUType      string
	SuppressedAt time.Time
}

// RecordFailure records a provisioning failure for an offer
func (t *OfferFailureTracker) RecordFailure(offerID, providerName, gpuType string, failureType FailureType, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()

	// Get or create offer record
	record, exists := t.offers[offerID]
	if !exists {
		record = &offerFailureRecord{
			Provider: providerName,
			GPUType:  gpuType,
		}
		t.offers[offerID] = record
	}

	// Append failure event
	record.Failures = append(record.Failures, failureEvent{
		Type:      failureType,
		Timestamp: now,
		Reason:    reason,
	})

	// Check suppression threshold: count failures within SuppressionWindow
	recentCount := 0
	cutoff := now.Add(-SuppressionWindow)
	for _, f := range record.Failures {
		if f.Timestamp.After(cutoff) {
			recentCount++
		}
	}
	newSuppression := false
	if recentCount >= SuppressionThreshold && record.SuppressedAt.IsZero() {
		record.SuppressedAt = now
		newSuppression = true
	}

	// Update GPU-type record
	gpuKey := providerName + ":" + gpuType
	gpuRec, exists := t.gpuTypes[gpuKey]
	if !exists {
		gpuRec = &gpuTypeRecord{
			FailedOfferIDs: make(map[string]time.Time),
		}
		t.gpuTypes[gpuKey] = gpuRec
	}
	gpuRec.FailedOfferIDs[offerID] = now

	// Run cleanup to prune old data
	t.cleanupLocked(now)

	// Write through to persistent store (best-effort, don't block on errors)
	if t.store != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := t.store.RecordFailure(ctx, offerID, providerName, gpuType, string(failureType), reason); err != nil {
				t.logger.Warn("failed to persist offer failure",
					slog.String("offer_id", offerID),
					slog.String("error", err.Error()))
			}
			if newSuppression {
				if err := t.store.SetSuppression(ctx, offerID, providerName, gpuType, now); err != nil {
					t.logger.Warn("failed to persist offer suppression",
						slog.String("offer_id", offerID),
						slog.String("error", err.Error()))
				}
			}
		}()
	}
}

// GetConfidenceMultiplier returns a multiplier (0.0 to 1.0) for an offer's
// availability confidence based on its failure history.
// Returns 0.0 if the offer is suppressed.
// Formula: 0.7^recentFailures per-offer × 0.3 if GPU-type degraded. Minimum 0.05.
func (t *OfferFailureTracker) GetConfidenceMultiplier(offerID, gpuType, providerName string) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	now := time.Now()

	// Check if offer is suppressed
	if record, exists := t.offers[offerID]; exists {
		if !record.SuppressedAt.IsZero() && now.Before(record.SuppressedAt.Add(SuppressionCooldown)) {
			return 0.0
		}
	}

	// Calculate per-offer multiplier
	offerMultiplier := 1.0
	if record, exists := t.offers[offerID]; exists {
		recentCount := t.countRecentFailuresLocked(record, now)
		if recentCount > 0 {
			offerMultiplier = math.Pow(0.7, float64(recentCount))
		}
	}

	// Calculate GPU-type multiplier
	gpuMultiplier := 1.0
	gpuKey := providerName + ":" + gpuType
	if gpuRec, exists := t.gpuTypes[gpuKey]; exists {
		// Count distinct failed offers within decay period
		activeCount := 0
		cutoff := now.Add(-FailureDecayPeriod)
		for _, lastFail := range gpuRec.FailedOfferIDs {
			if lastFail.After(cutoff) {
				activeCount++
			}
		}
		if activeCount >= GPUTypeFailureThreshold {
			gpuMultiplier = 0.3
		}
	}

	result := offerMultiplier * gpuMultiplier
	if result < 0.05 && result > 0.0 {
		result = 0.05
	}
	return result
}

// IsSuppressed returns true if the offer is currently suppressed
func (t *OfferFailureTracker) IsSuppressed(offerID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	record, exists := t.offers[offerID]
	if !exists {
		return false
	}

	if record.SuppressedAt.IsZero() {
		return false
	}

	now := time.Now()
	return now.Before(record.SuppressedAt.Add(SuppressionCooldown))
}

// GetAllHealth returns structured health data for all tracked offers
func (t *OfferFailureTracker) GetAllHealth() ([]OfferHealthInfo, []GPUTypeHealthInfo) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	now := time.Now()

	offers := make([]OfferHealthInfo, 0, len(t.offers))
	for offerID, record := range t.offers {
		recentCount := t.countRecentFailuresLocked(record, now)
		if recentCount == 0 && record.SuppressedAt.IsZero() {
			continue // Skip fully decayed records
		}

		info := OfferHealthInfo{
			OfferID:        offerID,
			Provider:       record.Provider,
			GPUType:        record.GPUType,
			RecentFailures: recentCount,
			IsSuppressed:   !record.SuppressedAt.IsZero() && now.Before(record.SuppressedAt.Add(SuppressionCooldown)),
			ConfidenceMultiplier: t.getConfidenceMultiplierLocked(offerID, record.GPUType, record.Provider, now),
		}

		if !record.SuppressedAt.IsZero() {
			suppressedAt := record.SuppressedAt
			info.SuppressedAt = &suppressedAt
			until := record.SuppressedAt.Add(SuppressionCooldown)
			info.SuppressedUntil = &until
		}

		// Find last failure
		if len(record.Failures) > 0 {
			last := record.Failures[len(record.Failures)-1]
			info.LastFailure = &last.Timestamp
			info.LastFailureType = last.Type
			info.LastFailureReason = last.Reason
		}

		offers = append(offers, info)
	}

	gpuTypes := make([]GPUTypeHealthInfo, 0, len(t.gpuTypes))
	for gpuKey, gpuRec := range t.gpuTypes {
		activeCount := 0
		cutoff := now.Add(-FailureDecayPeriod)
		for _, lastFail := range gpuRec.FailedOfferIDs {
			if lastFail.After(cutoff) {
				activeCount++
			}
		}
		if activeCount == 0 {
			continue
		}

		isDegraded := activeCount >= GPUTypeFailureThreshold
		info := GPUTypeHealthInfo{
			GPUTypeKey:       gpuKey,
			FailedOfferCount: activeCount,
			IsDegraded:       isDegraded,
		}
		if isDegraded {
			info.DegradationApplied = 0.3
		}
		gpuTypes = append(gpuTypes, info)
	}

	return offers, gpuTypes
}

// countRecentFailuresLocked counts failures within SuppressionWindow.
// Must be called with at least a read lock held.
func (t *OfferFailureTracker) countRecentFailuresLocked(record *offerFailureRecord, now time.Time) int {
	count := 0
	cutoff := now.Add(-SuppressionWindow)
	for _, f := range record.Failures {
		if f.Timestamp.After(cutoff) {
			count++
		}
	}
	return count
}

// getConfidenceMultiplierLocked calculates the multiplier without acquiring the lock.
// Must be called with at least a read lock held.
func (t *OfferFailureTracker) getConfidenceMultiplierLocked(offerID, gpuType, providerName string, now time.Time) float64 {
	// Check suppression
	if record, exists := t.offers[offerID]; exists {
		if !record.SuppressedAt.IsZero() && now.Before(record.SuppressedAt.Add(SuppressionCooldown)) {
			return 0.0
		}
	}

	offerMultiplier := 1.0
	if record, exists := t.offers[offerID]; exists {
		recentCount := t.countRecentFailuresLocked(record, now)
		if recentCount > 0 {
			offerMultiplier = math.Pow(0.7, float64(recentCount))
		}
	}

	gpuMultiplier := 1.0
	gpuKey := providerName + ":" + gpuType
	if gpuRec, exists := t.gpuTypes[gpuKey]; exists {
		activeCount := 0
		cutoff := now.Add(-FailureDecayPeriod)
		for _, lastFail := range gpuRec.FailedOfferIDs {
			if lastFail.After(cutoff) {
				activeCount++
			}
		}
		if activeCount >= GPUTypeFailureThreshold {
			gpuMultiplier = 0.3
		}
	}

	result := offerMultiplier * gpuMultiplier
	if result < 0.05 && result > 0.0 {
		result = 0.05
	}
	return result
}

// cleanupLocked prunes expired failure events and stale records.
// Must be called with the write lock held.
func (t *OfferFailureTracker) cleanupLocked(now time.Time) {
	decayCutoff := now.Add(-FailureDecayPeriod)

	// Clean up offer records
	var expiredSuppressions []string
	for offerID, record := range t.offers {
		// Prune old failure events
		var kept []failureEvent
		for _, f := range record.Failures {
			if f.Timestamp.After(decayCutoff) {
				kept = append(kept, f)
			}
		}
		record.Failures = kept

		// Clear expired suppressions
		if !record.SuppressedAt.IsZero() && now.After(record.SuppressedAt.Add(SuppressionCooldown)) {
			record.SuppressedAt = time.Time{}
			expiredSuppressions = append(expiredSuppressions, offerID)
		}

		// Remove record if no failures and not suppressed
		if len(record.Failures) == 0 && record.SuppressedAt.IsZero() {
			delete(t.offers, offerID)
		}
	}

	// Clean up GPU-type records
	for gpuKey, gpuRec := range t.gpuTypes {
		for offerID, lastFail := range gpuRec.FailedOfferIDs {
			if lastFail.Before(decayCutoff) {
				delete(gpuRec.FailedOfferIDs, offerID)
			}
		}
		if len(gpuRec.FailedOfferIDs) == 0 {
			delete(t.gpuTypes, gpuKey)
		}
	}

	// Async cleanup in the store (best-effort)
	if t.store != nil {
		store := t.store
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for _, offerID := range expiredSuppressions {
				if err := store.ClearSuppression(ctx, offerID); err != nil {
					t.logger.Warn("failed to clear suppression from store",
						slog.String("offer_id", offerID),
						slog.String("error", err.Error()))
				}
			}
			// Cleanup old failures from DB
			if deleted, err := store.CleanupOldFailures(ctx, decayCutoff); err != nil {
				t.logger.Warn("failed to cleanup old failures from store",
					slog.String("error", err.Error()))
			} else if deleted > 0 {
				t.logger.Debug("cleaned up old failures from store",
					slog.Int64("deleted", deleted))
			}
		}()
	}
}
