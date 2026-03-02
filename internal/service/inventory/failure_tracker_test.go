package inventory

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNoFailures_MultiplierIsOne(t *testing.T) {
	tracker := NewOfferFailureTracker()
	m := tracker.GetConfidenceMultiplier("offer-1", "RTX 4090", "vastai")
	if m != 1.0 {
		t.Errorf("expected multiplier 1.0 for unknown offer, got %f", m)
	}
}

func TestSingleFailure_MultiplierDegrades(t *testing.T) {
	tracker := NewOfferFailureTracker()
	tracker.RecordFailure("offer-1", "vastai", "RTX 4090", FailureStaleInventory, "not available")

	m := tracker.GetConfidenceMultiplier("offer-1", "RTX 4090", "vastai")
	expected := 0.7
	if diff := m - expected; diff > 0.01 || diff < -0.01 {
		t.Errorf("expected multiplier ~%f, got %f", expected, m)
	}
}

func TestTwoFailures_MultiplierDegradesFurther(t *testing.T) {
	tracker := NewOfferFailureTracker()
	tracker.RecordFailure("offer-1", "vastai", "RTX 4090", FailureStaleInventory, "not available")
	tracker.RecordFailure("offer-1", "vastai", "RTX 4090", FailureStaleInventory, "still not available")

	m := tracker.GetConfidenceMultiplier("offer-1", "RTX 4090", "vastai")
	expected := 0.49 // 0.7^2
	if diff := m - expected; diff > 0.01 || diff < -0.01 {
		t.Errorf("expected multiplier ~%f, got %f", expected, m)
	}
}

func TestThreeFailures_OfferSuppressed(t *testing.T) {
	tracker := NewOfferFailureTracker()
	tracker.RecordFailure("offer-1", "vastai", "RTX 4090", FailureStaleInventory, "fail 1")
	tracker.RecordFailure("offer-1", "vastai", "RTX 4090", FailureStaleInventory, "fail 2")
	tracker.RecordFailure("offer-1", "vastai", "RTX 4090", FailureStaleInventory, "fail 3")

	if !tracker.IsSuppressed("offer-1") {
		t.Error("expected offer to be suppressed after 3 failures")
	}

	m := tracker.GetConfidenceMultiplier("offer-1", "RTX 4090", "vastai")
	if m != 0.0 {
		t.Errorf("expected multiplier 0.0 for suppressed offer, got %f", m)
	}
}

func TestSuppressionExpires(t *testing.T) {
	tracker := NewOfferFailureTracker()

	// Manually inject an old suppression
	tracker.mu.Lock()
	tracker.offers["offer-1"] = &offerFailureRecord{
		Provider: "vastai",
		GPUType:  "RTX 4090",
		Failures: []failureEvent{
			{Type: FailureStaleInventory, Timestamp: time.Now(), Reason: "fail"},
		},
		SuppressedAt: time.Now().Add(-SuppressionCooldown - time.Minute), // expired
	}
	tracker.mu.Unlock()

	if tracker.IsSuppressed("offer-1") {
		t.Error("expected suppression to have expired")
	}

	m := tracker.GetConfidenceMultiplier("offer-1", "RTX 4090", "vastai")
	if m == 0.0 {
		t.Error("expected non-zero multiplier after suppression expires")
	}
}

func TestFailuresDecayAfterPeriod(t *testing.T) {
	tracker := NewOfferFailureTracker()

	// Inject old failures that are past the decay period
	tracker.mu.Lock()
	tracker.offers["offer-1"] = &offerFailureRecord{
		Provider: "vastai",
		GPUType:  "RTX 4090",
		Failures: []failureEvent{
			{Type: FailureStaleInventory, Timestamp: time.Now().Add(-FailureDecayPeriod - time.Minute), Reason: "old fail 1"},
			{Type: FailureStaleInventory, Timestamp: time.Now().Add(-FailureDecayPeriod - 2*time.Minute), Reason: "old fail 2"},
		},
	}
	tracker.mu.Unlock()

	// Trigger cleanup via a new failure on a different offer
	tracker.RecordFailure("offer-2", "vastai", "RTX 3090", FailureStaleInventory, "new fail")

	m := tracker.GetConfidenceMultiplier("offer-1", "RTX 4090", "vastai")
	if m != 1.0 {
		t.Errorf("expected multiplier 1.0 after failures decay, got %f", m)
	}
}

func TestGPUTypeAggregation_ThreeDistinctOffersFail(t *testing.T) {
	tracker := NewOfferFailureTracker()

	// Three different RTX 5080 offers fail
	tracker.RecordFailure("offer-a", "vastai", "RTX 5080", FailureInstanceStopped, "stopped")
	tracker.RecordFailure("offer-b", "vastai", "RTX 5080", FailureInstanceStopped, "stopped")
	tracker.RecordFailure("offer-c", "vastai", "RTX 5080", FailureInstanceStopped, "stopped")

	// A fourth RTX 5080 offer that hasn't failed should still be degraded
	m := tracker.GetConfidenceMultiplier("offer-d", "RTX 5080", "vastai")
	expected := 0.3
	if diff := m - expected; diff > 0.01 || diff < -0.01 {
		t.Errorf("expected GPU-type degradation multiplier ~%f, got %f", expected, m)
	}
}

func TestGPUTypeDegradation_AppliesToNonFailedOffer(t *testing.T) {
	tracker := NewOfferFailureTracker()

	tracker.RecordFailure("offer-1", "vastai", "RTX 5080", FailureInstanceStopped, "stopped")
	tracker.RecordFailure("offer-2", "vastai", "RTX 5080", FailureInstanceStopped, "stopped")
	tracker.RecordFailure("offer-3", "vastai", "RTX 5080", FailureInstanceStopped, "stopped")

	// offer-99 has never failed, but all RTX 5080 are degraded
	m := tracker.GetConfidenceMultiplier("offer-99", "RTX 5080", "vastai")
	if m != 0.3 {
		t.Errorf("expected 0.3 for unfailed offer under GPU-type degradation, got %f", m)
	}

	// Different GPU type should not be affected
	m2 := tracker.GetConfidenceMultiplier("offer-99", "RTX 4090", "vastai")
	if m2 != 1.0 {
		t.Errorf("expected 1.0 for different GPU type, got %f", m2)
	}
}

func TestCombinedOfferAndGPUTypeDegradation(t *testing.T) {
	tracker := NewOfferFailureTracker()

	// Three different offers fail → GPU-type degraded (0.3)
	tracker.RecordFailure("offer-a", "vastai", "RTX 5080", FailureInstanceStopped, "stopped")
	tracker.RecordFailure("offer-b", "vastai", "RTX 5080", FailureInstanceStopped, "stopped")
	tracker.RecordFailure("offer-c", "vastai", "RTX 5080", FailureInstanceStopped, "stopped")

	// offer-a also has its own per-offer degradation (1 failure → 0.7)
	// Combined: 0.7 × 0.3 = 0.21
	m := tracker.GetConfidenceMultiplier("offer-a", "RTX 5080", "vastai")
	expected := 0.7 * 0.3
	if diff := m - expected; diff > 0.02 || diff < -0.02 {
		t.Errorf("expected combined multiplier ~%f, got %f", expected, m)
	}
}

func TestIsSuppressed_ReturnsFalseForUnknown(t *testing.T) {
	tracker := NewOfferFailureTracker()
	if tracker.IsSuppressed("nonexistent") {
		t.Error("expected IsSuppressed=false for unknown offer")
	}
}

func TestGetAllHealth_ReturnsStructuredData(t *testing.T) {
	tracker := NewOfferFailureTracker()
	tracker.RecordFailure("offer-1", "vastai", "RTX 4090", FailureStaleInventory, "not available")
	tracker.RecordFailure("offer-2", "tensordock", "H100 SXM5", FailureInstanceStopped, "stopped")

	offers, gpuTypes := tracker.GetAllHealth()

	if len(offers) != 2 {
		t.Errorf("expected 2 offer health records, got %d", len(offers))
	}

	// Both GPU types should have 1 failed offer each (below threshold, not degraded)
	for _, gt := range gpuTypes {
		if gt.IsDegraded {
			t.Errorf("expected GPU type %s not to be degraded with only 1 failure", gt.GPUTypeKey)
		}
	}

	// Verify fields populated
	for _, o := range offers {
		if o.OfferID == "" || o.Provider == "" || o.GPUType == "" {
			t.Error("health info missing required fields")
		}
		if o.LastFailure == nil {
			t.Error("expected last_failure to be set")
		}
		if o.ConfidenceMultiplier <= 0 || o.ConfidenceMultiplier > 1.0 {
			t.Errorf("confidence multiplier %f out of expected range", o.ConfidenceMultiplier)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	tracker := NewOfferFailureTracker()
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			offerID := "offer-concurrent"
			if i%2 == 0 {
				offerID = "offer-concurrent-2"
			}
			tracker.RecordFailure(offerID, "vastai", "RTX 4090", FailureStaleInventory, "concurrent fail")
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracker.GetConfidenceMultiplier("offer-concurrent", "RTX 4090", "vastai")
			tracker.IsSuppressed("offer-concurrent")
			tracker.GetAllHealth()
		}()
	}

	wg.Wait()
	// No panics = pass
}

func TestCleanupRemovesExpiredRecords(t *testing.T) {
	tracker := NewOfferFailureTracker()

	// Inject a record with only expired failures
	tracker.mu.Lock()
	tracker.offers["old-offer"] = &offerFailureRecord{
		Provider: "vastai",
		GPUType:  "RTX 3090",
		Failures: []failureEvent{
			{Type: FailureStaleInventory, Timestamp: time.Now().Add(-2 * FailureDecayPeriod), Reason: "ancient"},
		},
	}
	tracker.gpuTypes["vastai:RTX 3090"] = &gpuTypeRecord{
		FailedOfferIDs: map[string]time.Time{
			"old-offer": time.Now().Add(-2 * FailureDecayPeriod),
		},
	}
	tracker.mu.Unlock()

	// Trigger cleanup via RecordFailure
	tracker.RecordFailure("new-offer", "tensordock", "RTX 4090", FailureStaleInventory, "new")

	tracker.mu.RLock()
	defer tracker.mu.RUnlock()

	if _, exists := tracker.offers["old-offer"]; exists {
		t.Error("expected old-offer record to be cleaned up")
	}
	if _, exists := tracker.gpuTypes["vastai:RTX 3090"]; exists {
		t.Error("expected GPU-type record to be cleaned up")
	}
}

func TestMinimumMultiplierFloor(t *testing.T) {
	tracker := NewOfferFailureTracker()

	// Record many failures but not enough for suppression (spread across time)
	// We'll manually set 2 recent failures (below suppression threshold)
	// on a GPU type that's degraded → 0.7^2 * 0.3 = 0.147
	// Then with many more failures: 0.7^10 * 0.3 ≈ 0.0085 → should floor at 0.05

	tracker.mu.Lock()
	now := time.Now()
	failures := make([]failureEvent, 10)
	for i := range failures {
		failures[i] = failureEvent{
			Type:      FailureStaleInventory,
			Timestamp: now.Add(-time.Duration(i) * time.Minute),
			Reason:    "fail",
		}
	}
	// Don't trigger suppression (set SuppressedAt to expired)
	tracker.offers["offer-floor"] = &offerFailureRecord{
		Provider:     "vastai",
		GPUType:      "RTX 5080",
		Failures:     failures[:2], // just 2 recent failures = no suppression
		SuppressedAt: time.Time{},
	}
	// Set up GPU-type degradation
	tracker.gpuTypes["vastai:RTX 5080"] = &gpuTypeRecord{
		FailedOfferIDs: map[string]time.Time{
			"offer-x": now,
			"offer-y": now,
			"offer-z": now,
		},
	}
	tracker.mu.Unlock()

	m := tracker.GetConfidenceMultiplier("offer-floor", "RTX 5080", "vastai")
	// 0.7^2 * 0.3 = 0.147, above floor
	if m < 0.05 {
		t.Errorf("expected multiplier >= 0.05, got %f", m)
	}

	// Now test with many failures
	tracker.mu.Lock()
	manyFailures := make([]failureEvent, 20)
	for i := range manyFailures {
		manyFailures[i] = failureEvent{
			Type:      FailureStaleInventory,
			Timestamp: now.Add(-time.Duration(i) * time.Second),
			Reason:    "fail",
		}
	}
	tracker.offers["offer-floor-many"] = &offerFailureRecord{
		Provider: "vastai",
		GPUType:  "RTX 5080",
		Failures: manyFailures,
		// 20 failures → will be suppressed, but let's test the path where
		// suppression expired
		SuppressedAt: now.Add(-SuppressionCooldown - time.Minute),
	}
	tracker.mu.Unlock()

	m2 := tracker.GetConfidenceMultiplier("offer-floor-many", "RTX 5080", "vastai")
	// 0.7^20 * 0.3 is tiny → should be floored to 0.05
	if m2 != 0.05 {
		t.Errorf("expected floor multiplier 0.05, got %f", m2)
	}
}

func TestLoadFromStore_RestoresFailures(t *testing.T) {
	tracker := NewOfferFailureTracker()
	ctx := context.Background()
	now := time.Now()

	failures := []StoredFailure{
		{
			OfferID:     "offer-1",
			Provider:    "vastai",
			GPUType:     "RTX 4090",
			FailureType: "stale_inventory",
			Reason:      "not available",
			CreatedAt:   now.Add(-10 * time.Minute),
		},
		{
			OfferID:     "offer-1",
			Provider:    "vastai",
			GPUType:     "RTX 4090",
			FailureType: "ssh_timeout",
			Reason:      "SSH timed out",
			CreatedAt:   now.Add(-5 * time.Minute),
		},
	}

	tracker.LoadFromStore(ctx, failures, nil)

	// Should have 2 recent failures → multiplier ~0.49
	m := tracker.GetConfidenceMultiplier("offer-1", "RTX 4090", "vastai")
	expected := 0.49
	if diff := m - expected; diff > 0.01 || diff < -0.01 {
		t.Errorf("expected multiplier ~%f after loading from store, got %f", expected, m)
	}
}

func TestLoadFromStore_RestoresSuppressions(t *testing.T) {
	tracker := NewOfferFailureTracker()
	ctx := context.Background()
	now := time.Now()

	suppressions := []StoredSuppression{
		{
			OfferID:      "offer-suppressed",
			Provider:     "vastai",
			GPUType:      "RTX 5080",
			SuppressedAt: now.Add(-5 * time.Minute), // suppressed 5 min ago, should still be active
		},
	}

	tracker.LoadFromStore(ctx, nil, suppressions)

	if !tracker.IsSuppressed("offer-suppressed") {
		t.Error("expected offer to be suppressed after loading from store")
	}

	m := tracker.GetConfidenceMultiplier("offer-suppressed", "RTX 5080", "vastai")
	if m != 0.0 {
		t.Errorf("expected multiplier 0.0 for suppressed offer, got %f", m)
	}
}

func TestLoadFromStore_GPUTypeAggregation(t *testing.T) {
	tracker := NewOfferFailureTracker()
	ctx := context.Background()
	now := time.Now()

	// Load failures from 3 different RTX 5080 offers → GPU-type degradation
	failures := []StoredFailure{
		{OfferID: "5080-a", Provider: "vastai", GPUType: "RTX 5080", FailureType: "instance_stopped", Reason: "loading", CreatedAt: now.Add(-10 * time.Minute)},
		{OfferID: "5080-b", Provider: "vastai", GPUType: "RTX 5080", FailureType: "instance_stopped", Reason: "loading", CreatedAt: now.Add(-8 * time.Minute)},
		{OfferID: "5080-c", Provider: "vastai", GPUType: "RTX 5080", FailureType: "instance_stopped", Reason: "loading", CreatedAt: now.Add(-6 * time.Minute)},
	}

	tracker.LoadFromStore(ctx, failures, nil)

	// A different RTX 5080 offer should be GPU-type degraded
	m := tracker.GetConfidenceMultiplier("5080-new", "RTX 5080", "vastai")
	expected := 0.3
	if diff := m - expected; diff > 0.01 || diff < -0.01 {
		t.Errorf("expected GPU-type degraded multiplier ~%f, got %f", expected, m)
	}
}

func TestLoadFromStore_ExpiredDataIgnored(t *testing.T) {
	tracker := NewOfferFailureTracker()
	ctx := context.Background()

	// Load very old failures (should be cleaned up)
	oldTime := time.Now().Add(-2 * time.Hour)
	failures := []StoredFailure{
		{OfferID: "old-offer", Provider: "vastai", GPUType: "RTX 3090", FailureType: "ssh_timeout", Reason: "old", CreatedAt: oldTime},
	}

	tracker.LoadFromStore(ctx, failures, nil)

	// Should have been cleaned up → multiplier 1.0
	m := tracker.GetConfidenceMultiplier("old-offer", "RTX 3090", "vastai")
	if m != 1.0 {
		t.Errorf("expected multiplier 1.0 for expired failure, got %f", m)
	}
}

func TestLoadFromStore_ExpiredSuppressionIgnored(t *testing.T) {
	tracker := NewOfferFailureTracker()
	ctx := context.Background()

	// Load suppression from 2 hours ago (expired since cooldown is 30min)
	suppressions := []StoredSuppression{
		{
			OfferID:      "expired-offer",
			Provider:     "vastai",
			GPUType:      "RTX 3090",
			SuppressedAt: time.Now().Add(-2 * time.Hour),
		},
	}

	tracker.LoadFromStore(ctx, nil, suppressions)

	if tracker.IsSuppressed("expired-offer") {
		t.Error("expected expired suppression to be cleared after loading from store")
	}
}
