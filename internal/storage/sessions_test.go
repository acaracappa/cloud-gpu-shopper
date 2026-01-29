package storage

import (
	"context"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionStore_Create(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	session := &models.Session{
		ID:             "sess-001",
		ConsumerID:     "consumer-001",
		Provider:       "vastai",
		OfferID:        "offer-123",
		GPUType:        "RTX4090",
		GPUCount:       1,
		Status:         models.StatusPending,
		WorkloadType:   "ml-training",
		ReservationHrs: 4,
		StoragePolicy:  "destroy",
		PricePerHour:   0.50,
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(4 * time.Hour),
	}

	err := store.Create(ctx, session)
	require.NoError(t, err)

	// Verify by retrieving
	retrieved, err := store.Get(ctx, "sess-001")
	require.NoError(t, err)
	assert.Equal(t, session.ID, retrieved.ID)
	assert.Equal(t, session.ConsumerID, retrieved.ConsumerID)
	assert.Equal(t, session.Provider, retrieved.Provider)
	assert.Equal(t, session.GPUType, retrieved.GPUType)
	assert.Equal(t, session.Status, retrieved.Status)
}

func TestSessionStore_Get_NotFound(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	_, err := store.Get(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestSessionStore_Update(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	// Create session
	session := &models.Session{
		ID:             "sess-002",
		ConsumerID:     "consumer-001",
		Provider:       "vastai",
		OfferID:        "offer-123",
		GPUType:        "RTX4090",
		GPUCount:       1,
		Status:         models.StatusPending,
		WorkloadType:   "ml-training",
		ReservationHrs: 4,
		StoragePolicy:  "destroy",
		PricePerHour:   0.50,
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(4 * time.Hour),
	}
	err := store.Create(ctx, session)
	require.NoError(t, err)

	// Update session
	session.Status = models.StatusRunning
	session.ProviderID = "provider-instance-123"
	session.SSHHost = "192.168.1.100"
	session.SSHPort = 22
	session.SSHUser = "root"
	session.AgentEndpoint = "http://192.168.1.100:8080"

	err = store.Update(ctx, session)
	require.NoError(t, err)

	// Verify update
	retrieved, err := store.Get(ctx, "sess-002")
	require.NoError(t, err)
	assert.Equal(t, models.StatusRunning, retrieved.Status)
	assert.Equal(t, "provider-instance-123", retrieved.ProviderID)
	assert.Equal(t, "192.168.1.100", retrieved.SSHHost)
	assert.Equal(t, 22, retrieved.SSHPort)
	assert.Equal(t, "http://192.168.1.100:8080", retrieved.AgentEndpoint)
}

func TestSessionStore_Update_NotFound(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	session := &models.Session{
		ID:     "nonexistent",
		Status: models.StatusRunning,
	}

	err := store.Update(ctx, session)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestSessionStore_List(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	// Create multiple sessions
	now := time.Now()
	sessions := []*models.Session{
		{
			ID:             "sess-list-1",
			ConsumerID:     "consumer-001",
			Provider:       "vastai",
			OfferID:        "offer-1",
			GPUType:        "RTX4090",
			GPUCount:       1,
			Status:         models.StatusRunning,
			WorkloadType:   "ml-training",
			ReservationHrs: 4,
			StoragePolicy:  "destroy",
			PricePerHour:   0.50,
			CreatedAt:      now,
			ExpiresAt:      now.Add(4 * time.Hour),
		},
		{
			ID:             "sess-list-2",
			ConsumerID:     "consumer-002",
			Provider:       "tensordock",
			OfferID:        "offer-2",
			GPUType:        "A100",
			GPUCount:       2,
			Status:         models.StatusPending,
			WorkloadType:   "inference",
			ReservationHrs: 2,
			StoragePolicy:  "persist",
			PricePerHour:   1.50,
			CreatedAt:      now.Add(time.Minute),
			ExpiresAt:      now.Add(2 * time.Hour),
		},
		{
			ID:             "sess-list-3",
			ConsumerID:     "consumer-001",
			Provider:       "vastai",
			OfferID:        "offer-3",
			GPUType:        "RTX3090",
			GPUCount:       1,
			Status:         models.StatusStopped,
			WorkloadType:   "ml-training",
			ReservationHrs: 1,
			StoragePolicy:  "destroy",
			PricePerHour:   0.30,
			CreatedAt:      now.Add(-time.Hour),
			ExpiresAt:      now,
		},
	}

	for _, s := range sessions {
		err := store.Create(ctx, s)
		require.NoError(t, err)
	}

	// Test filter by consumer
	results, err := store.List(ctx, SessionFilter{ConsumerID: "consumer-001"})
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Test filter by provider
	results, err = store.List(ctx, SessionFilter{Provider: "tensordock"})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "sess-list-2", results[0].ID)

	// Test filter by status
	results, err = store.List(ctx, SessionFilter{Status: models.StatusRunning})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "sess-list-1", results[0].ID)

	// Test filter by multiple statuses
	results, err = store.List(ctx, SessionFilter{Statuses: []models.SessionStatus{models.StatusRunning, models.StatusPending}})
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Test limit
	results, err = store.List(ctx, SessionFilter{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestSessionStore_GetActiveSessions(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	now := time.Now()

	// Create sessions with various statuses
	statuses := []models.SessionStatus{
		models.StatusPending,
		models.StatusProvisioning,
		models.StatusRunning,
		models.StatusStopping,
		models.StatusStopped,
		models.StatusFailed,
	}

	for i, status := range statuses {
		session := &models.Session{
			ID:             "sess-active-" + string(rune('a'+i)),
			ConsumerID:     "consumer-001",
			Provider:       "vastai",
			OfferID:        "offer-1",
			GPUType:        "RTX4090",
			GPUCount:       1,
			Status:         status,
			WorkloadType:   "ml-training",
			ReservationHrs: 4,
			StoragePolicy:  "destroy",
			PricePerHour:   0.50,
			CreatedAt:      now,
			ExpiresAt:      now.Add(4 * time.Hour),
		}
		err := store.Create(ctx, session)
		require.NoError(t, err)
	}

	// GetActiveSessions should return pending, provisioning, running
	active, err := store.GetActiveSessions(ctx)
	require.NoError(t, err)
	assert.Len(t, active, 3)
}

func TestSessionStore_GetExpiredSessions(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	now := time.Now()

	// Create an expired running session
	expired := &models.Session{
		ID:             "sess-expired",
		ConsumerID:     "consumer-001",
		Provider:       "vastai",
		OfferID:        "offer-1",
		GPUType:        "RTX4090",
		GPUCount:       1,
		Status:         models.StatusRunning,
		WorkloadType:   "ml-training",
		ReservationHrs: 1,
		StoragePolicy:  "destroy",
		PricePerHour:   0.50,
		CreatedAt:      now.Add(-2 * time.Hour),
		ExpiresAt:      now.Add(-1 * time.Hour), // Already expired
	}
	err := store.Create(ctx, expired)
	require.NoError(t, err)

	// Create a non-expired running session
	notExpired := &models.Session{
		ID:             "sess-not-expired",
		ConsumerID:     "consumer-001",
		Provider:       "vastai",
		OfferID:        "offer-2",
		GPUType:        "RTX4090",
		GPUCount:       1,
		Status:         models.StatusRunning,
		WorkloadType:   "ml-training",
		ReservationHrs: 4,
		StoragePolicy:  "destroy",
		PricePerHour:   0.50,
		CreatedAt:      now,
		ExpiresAt:      now.Add(4 * time.Hour), // Not expired
	}
	err = store.Create(ctx, notExpired)
	require.NoError(t, err)

	// GetExpiredSessions should return only the expired one
	results, err := store.GetExpiredSessions(ctx)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "sess-expired", results[0].ID)
}

func TestSessionStore_UpdateHeartbeat(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	now := time.Now()

	// Create session
	session := &models.Session{
		ID:             "sess-heartbeat",
		ConsumerID:     "consumer-001",
		Provider:       "vastai",
		OfferID:        "offer-1",
		GPUType:        "RTX4090",
		GPUCount:       1,
		Status:         models.StatusRunning,
		WorkloadType:   "ml-training",
		ReservationHrs: 4,
		StoragePolicy:  "destroy",
		PricePerHour:   0.50,
		CreatedAt:      now,
		ExpiresAt:      now.Add(4 * time.Hour),
	}
	err := store.Create(ctx, session)
	require.NoError(t, err)

	// Update heartbeat
	heartbeatTime := now.Add(time.Minute)
	err = store.UpdateHeartbeat(ctx, "sess-heartbeat", heartbeatTime)
	require.NoError(t, err)

	// Verify
	retrieved, err := store.Get(ctx, "sess-heartbeat")
	require.NoError(t, err)
	assert.WithinDuration(t, heartbeatTime, retrieved.LastHeartbeat, time.Second)
}

func TestSessionStore_UpdateHeartbeat_NotFound(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	err := store.UpdateHeartbeat(ctx, "nonexistent", time.Now())
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestSessionStore_NullableFields(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	now := time.Now()

	// Create session with minimal fields (nullable fields left empty)
	session := &models.Session{
		ID:             "sess-nullable",
		ConsumerID:     "consumer-001",
		Provider:       "vastai",
		OfferID:        "offer-1",
		GPUType:        "RTX4090",
		GPUCount:       1,
		Status:         models.StatusPending,
		WorkloadType:   "ml-training",
		ReservationHrs: 4,
		StoragePolicy:  "destroy",
		PricePerHour:   0.50,
		CreatedAt:      now,
		ExpiresAt:      now.Add(4 * time.Hour),
		// SSH fields, AgentEndpoint, Error, etc. are all empty/zero
	}
	err := store.Create(ctx, session)
	require.NoError(t, err)

	// Retrieve and verify nullable fields are handled correctly
	retrieved, err := store.Get(ctx, "sess-nullable")
	require.NoError(t, err)
	assert.Empty(t, retrieved.ProviderID)
	assert.Empty(t, retrieved.SSHHost)
	assert.Zero(t, retrieved.SSHPort)
	assert.Empty(t, retrieved.SSHUser)
	assert.Empty(t, retrieved.AgentEndpoint)
	assert.Empty(t, retrieved.Error)
	assert.True(t, retrieved.LastHeartbeat.IsZero())
	assert.True(t, retrieved.StoppedAt.IsZero())
}
