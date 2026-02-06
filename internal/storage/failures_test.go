package storage

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOfferFailureStore_RecordAndLoad(t *testing.T) {
	db := newTestDB(t)
	store := NewOfferFailureStore(db)
	ctx := context.Background()

	// Record a failure
	err := store.RecordFailure(ctx, "offer-1", "vastai", "RTX 4090", "stale_inventory", "no such machine")
	require.NoError(t, err)

	// Record another failure for a different offer
	err = store.RecordFailure(ctx, "offer-2", "tensordock", "H100 SXM5", "instance_stopped", "stopped immediately")
	require.NoError(t, err)

	// Load recent failures
	since := time.Now().Add(-1 * time.Hour)
	failures, err := store.LoadRecentFailures(ctx, since)
	require.NoError(t, err)
	assert.Len(t, failures, 2)

	// Verify first failure
	assert.Equal(t, "offer-1", failures[0].OfferID)
	assert.Equal(t, "vastai", failures[0].Provider)
	assert.Equal(t, "RTX 4090", failures[0].GPUType)
	assert.Equal(t, "stale_inventory", failures[0].FailureType)
	assert.Equal(t, "no such machine", failures[0].Reason)

	// Verify second failure
	assert.Equal(t, "offer-2", failures[1].OfferID)
	assert.Equal(t, "tensordock", failures[1].Provider)
	assert.Equal(t, "H100 SXM5", failures[1].GPUType)
	assert.Equal(t, "instance_stopped", failures[1].FailureType)
}

func TestOfferFailureStore_LoadRecentFailures_FiltersByTime(t *testing.T) {
	db := newTestDB(t)
	store := NewOfferFailureStore(db)
	ctx := context.Background()

	// Insert an old failure directly (2 hours ago)
	twoHoursAgo := time.Now().Add(-2 * time.Hour).UTC()
	_, err := db.ExecContext(ctx,
		"INSERT INTO offer_failures (offer_id, provider, gpu_type, failure_type, reason, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		"old-offer", "vastai", "RTX 3090", "ssh_timeout", "old failure", twoHoursAgo)
	require.NoError(t, err)

	// Insert a recent failure
	err = store.RecordFailure(ctx, "new-offer", "vastai", "RTX 4090", "stale_inventory", "recent failure")
	require.NoError(t, err)

	// Load only failures from the last hour
	since := time.Now().Add(-1 * time.Hour)
	failures, err := store.LoadRecentFailures(ctx, since)
	require.NoError(t, err)
	assert.Len(t, failures, 1)
	assert.Equal(t, "new-offer", failures[0].OfferID)
}

func TestOfferFailureStore_Suppressions(t *testing.T) {
	db := newTestDB(t)
	store := NewOfferFailureStore(db)
	ctx := context.Background()
	now := time.Now()

	// Set a suppression
	err := store.SetSuppression(ctx, "offer-1", "vastai", "RTX 5080", now)
	require.NoError(t, err)

	// Load active suppressions (cutoff = 1 hour ago, so the suppression should be active)
	cutoff := now.Add(-1 * time.Hour)
	suppressions, err := store.LoadActiveSuppressions(ctx, cutoff)
	require.NoError(t, err)
	assert.Len(t, suppressions, 1)
	assert.Equal(t, "offer-1", suppressions[0].OfferID)
	assert.Equal(t, "vastai", suppressions[0].Provider)
	assert.Equal(t, "RTX 5080", suppressions[0].GPUType)

	// Clear suppression
	err = store.ClearSuppression(ctx, "offer-1")
	require.NoError(t, err)

	// Should be empty now
	suppressions, err = store.LoadActiveSuppressions(ctx, cutoff)
	require.NoError(t, err)
	assert.Len(t, suppressions, 0)
}

func TestOfferFailureStore_SuppressionUpsert(t *testing.T) {
	db := newTestDB(t)
	store := NewOfferFailureStore(db)
	ctx := context.Background()
	now := time.Now()

	// Set suppression
	err := store.SetSuppression(ctx, "offer-1", "vastai", "RTX 5080", now)
	require.NoError(t, err)

	// Update suppression (upsert should work)
	later := now.Add(10 * time.Minute)
	err = store.SetSuppression(ctx, "offer-1", "vastai", "RTX 5080", later)
	require.NoError(t, err)

	// Should still be just one record
	cutoff := now.Add(-1 * time.Hour)
	suppressions, err := store.LoadActiveSuppressions(ctx, cutoff)
	require.NoError(t, err)
	assert.Len(t, suppressions, 1)
}

func TestOfferFailureStore_CleanupOldFailures(t *testing.T) {
	db := newTestDB(t)
	store := NewOfferFailureStore(db)
	ctx := context.Background()

	// Insert failures at different times
	oldTime := time.Now().Add(-2 * time.Hour).UTC()
	recentTime := time.Now().Add(-10 * time.Minute).UTC()

	_, err := db.ExecContext(ctx,
		"INSERT INTO offer_failures (offer_id, provider, gpu_type, failure_type, reason, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		"old-offer", "vastai", "RTX 3090", "ssh_timeout", "old", oldTime)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx,
		"INSERT INTO offer_failures (offer_id, provider, gpu_type, failure_type, reason, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		"new-offer", "vastai", "RTX 4090", "stale_inventory", "new", recentTime)
	require.NoError(t, err)

	// Cleanup failures older than 1 hour
	cutoff := time.Now().Add(-1 * time.Hour)
	deleted, err := store.CleanupOldFailures(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	// Only the recent failure should remain
	failures, err := store.LoadRecentFailures(ctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	assert.Len(t, failures, 1)
	assert.Equal(t, "new-offer", failures[0].OfferID)
}

func TestOfferFailureStore_CleanupExpiredSuppressions(t *testing.T) {
	db := newTestDB(t)
	store := NewOfferFailureStore(db)
	ctx := context.Background()

	// Set an old suppression and a recent one
	oldTime := time.Now().Add(-2 * time.Hour)
	recentTime := time.Now().Add(-5 * time.Minute)

	err := store.SetSuppression(ctx, "old-offer", "vastai", "RTX 3090", oldTime)
	require.NoError(t, err)
	err = store.SetSuppression(ctx, "new-offer", "vastai", "RTX 4090", recentTime)
	require.NoError(t, err)

	// Cleanup suppressions older than 1 hour
	cutoff := time.Now().Add(-1 * time.Hour)
	deleted, err := store.CleanupExpiredSuppressions(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	// Only the recent suppression should remain
	suppressions, err := store.LoadActiveSuppressions(ctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	assert.Len(t, suppressions, 1)
	assert.Equal(t, "new-offer", suppressions[0].OfferID)
}

func TestOfferFailureStore_CountByOfferID(t *testing.T) {
	db := newTestDB(t)
	store := NewOfferFailureStore(db)
	ctx := context.Background()

	// Record multiple failures for different offers
	err := store.RecordFailure(ctx, "offer-1", "vastai", "RTX 4090", "stale_inventory", "fail 1")
	require.NoError(t, err)
	err = store.RecordFailure(ctx, "offer-1", "vastai", "RTX 4090", "ssh_timeout", "fail 2")
	require.NoError(t, err)
	err = store.RecordFailure(ctx, "offer-2", "tensordock", "H100", "instance_stopped", "fail 3")
	require.NoError(t, err)

	since := time.Now().Add(-1 * time.Hour)
	counts, err := store.CountByOfferID(ctx, since)
	require.NoError(t, err)
	assert.Equal(t, 2, counts["offer-1"])
	assert.Equal(t, 1, counts["offer-2"])
}

func TestOfferFailureStore_EmptyDatabase(t *testing.T) {
	db := newTestDB(t)
	store := NewOfferFailureStore(db)
	ctx := context.Background()

	// Load from empty DB
	since := time.Now().Add(-1 * time.Hour)
	failures, err := store.LoadRecentFailures(ctx, since)
	require.NoError(t, err)
	assert.Empty(t, failures)

	suppressions, err := store.LoadActiveSuppressions(ctx, since)
	require.NoError(t, err)
	assert.Empty(t, suppressions)

	counts, err := store.CountByOfferID(ctx, since)
	require.NoError(t, err)
	assert.Empty(t, counts)
}

func TestOfferFailureStore_MigrationsIdempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Run migrations again - should not error (tables already exist)
	err := db.Migrate(ctx)
	require.NoError(t, err)

	// Store should still work
	store := NewOfferFailureStore(db)
	err = store.RecordFailure(ctx, "offer-1", "vastai", "RTX 4090", "stale_inventory", "test")
	require.NoError(t, err)
}
