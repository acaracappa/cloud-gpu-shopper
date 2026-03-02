package benchmark

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestManifest(t *testing.T) *ManifestStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store, err := NewManifestStore(db)
	require.NoError(t, err)
	return store
}

func TestMarkRunning_OnlyClaimsPendingEntries(t *testing.T) {
	store := setupTestManifest(t)
	ctx := context.Background()

	entry := &ManifestEntry{
		RunID:    "run-test",
		GPUType:  "RTX A4000",
		Provider: "bluelobster",
		Model:    "llama3.1:8b",
	}
	require.NoError(t, store.Create(ctx, entry))

	// First claim should succeed
	err := store.MarkRunning(ctx, entry.ID, "worker-1", "")
	require.NoError(t, err)

	// Second claim on already-running entry should fail
	err = store.MarkRunning(ctx, entry.ID, "worker-2", "")
	assert.Error(t, err, "should not be able to claim an already-running entry")

	// Mark success
	require.NoError(t, store.MarkSuccess(ctx, entry.ID, "bench-1", 100.0, 0.05))

	// Claim on completed entry should fail
	err = store.MarkRunning(ctx, entry.ID, "worker-3", "")
	assert.Error(t, err, "should not be able to claim a completed entry")
}
