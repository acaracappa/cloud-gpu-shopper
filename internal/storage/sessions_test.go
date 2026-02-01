package storage

import (
	"context"
	"fmt"
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

	err = store.Update(ctx, session)
	require.NoError(t, err)

	// Verify update
	retrieved, err := store.Get(ctx, "sess-002")
	require.NoError(t, err)
	assert.Equal(t, models.StatusRunning, retrieved.Status)
	assert.Equal(t, "provider-instance-123", retrieved.ProviderID)
	assert.Equal(t, "192.168.1.100", retrieved.SSHHost)
	assert.Equal(t, 22, retrieved.SSHPort)
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
	results, err := store.ListInternal(ctx, SessionFilter{ConsumerID: "consumer-001"})
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Test filter by provider
	results, err = store.ListInternal(ctx, SessionFilter{Provider: "tensordock"})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "sess-list-2", results[0].ID)

	// Test filter by status
	results, err = store.ListInternal(ctx, SessionFilter{Status: models.StatusRunning})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "sess-list-1", results[0].ID)

	// Test filter by multiple statuses
	results, err = store.ListInternal(ctx, SessionFilter{Statuses: []models.SessionStatus{models.StatusRunning, models.StatusPending}})
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Test limit
	results, err = store.ListInternal(ctx, SessionFilter{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestSessionStore_GetActiveSessions(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	now := time.Now()

	// Create sessions with various statuses
	// Use different offer IDs for active statuses to avoid unique constraint violation
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
			OfferID:        fmt.Sprintf("offer-%d", i), // Use unique offer IDs to avoid duplicate constraint
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
	assert.Empty(t, retrieved.Error)
	assert.True(t, retrieved.StoppedAt.IsZero())
}

func TestSessionStore_GetActiveSessionByConsumerAndOffer(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	now := time.Now()

	// Create an active session (running)
	activeSession := &models.Session{
		ID:             "sess-active",
		ConsumerID:     "consumer-001",
		Provider:       "vastai",
		OfferID:        "offer-123",
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
	err := store.Create(ctx, activeSession)
	require.NoError(t, err)

	// Should find the active session
	found, err := store.GetActiveSessionByConsumerAndOffer(ctx, "consumer-001", "offer-123")
	require.NoError(t, err)
	assert.Equal(t, "sess-active", found.ID)
	assert.Equal(t, models.StatusRunning, found.Status)
}

func TestSessionStore_GetActiveSessionByConsumerAndOffer_NotFound(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	// No sessions exist
	_, err := store.GetActiveSessionByConsumerAndOffer(ctx, "consumer-001", "offer-123")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestSessionStore_GetActiveSessionByConsumerAndOffer_IgnoresStoppedSessions(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	now := time.Now()

	// Create a stopped session
	stoppedSession := &models.Session{
		ID:             "sess-stopped",
		ConsumerID:     "consumer-001",
		Provider:       "vastai",
		OfferID:        "offer-123",
		GPUType:        "RTX4090",
		GPUCount:       1,
		Status:         models.StatusStopped,
		WorkloadType:   "ml-training",
		ReservationHrs: 4,
		StoragePolicy:  "destroy",
		PricePerHour:   0.50,
		CreatedAt:      now.Add(-time.Hour),
		ExpiresAt:      now,
		StoppedAt:      now,
	}
	err := store.Create(ctx, stoppedSession)
	require.NoError(t, err)

	// Create a failed session
	failedSession := &models.Session{
		ID:             "sess-failed",
		ConsumerID:     "consumer-001",
		Provider:       "vastai",
		OfferID:        "offer-123",
		GPUType:        "RTX4090",
		GPUCount:       1,
		Status:         models.StatusFailed,
		WorkloadType:   "ml-training",
		ReservationHrs: 4,
		StoragePolicy:  "destroy",
		PricePerHour:   0.50,
		CreatedAt:      now.Add(-2 * time.Hour),
		ExpiresAt:      now.Add(-time.Hour),
	}
	err = store.Create(ctx, failedSession)
	require.NoError(t, err)

	// Should not find any active session (stopped and failed are ignored)
	_, err = store.GetActiveSessionByConsumerAndOffer(ctx, "consumer-001", "offer-123")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestSessionStore_GetActiveSessionByConsumerAndOffer_FindsPendingAndProvisioning(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	now := time.Now()

	// Create a pending session
	pendingSession := &models.Session{
		ID:             "sess-pending",
		ConsumerID:     "consumer-001",
		Provider:       "vastai",
		OfferID:        "offer-pending",
		GPUType:        "RTX4090",
		GPUCount:       1,
		Status:         models.StatusPending,
		WorkloadType:   "ml-training",
		ReservationHrs: 4,
		StoragePolicy:  "destroy",
		PricePerHour:   0.50,
		CreatedAt:      now,
		ExpiresAt:      now.Add(4 * time.Hour),
	}
	err := store.Create(ctx, pendingSession)
	require.NoError(t, err)

	// Create a provisioning session
	provisioningSession := &models.Session{
		ID:             "sess-provisioning",
		ConsumerID:     "consumer-002",
		Provider:       "vastai",
		OfferID:        "offer-provisioning",
		GPUType:        "RTX4090",
		GPUCount:       1,
		Status:         models.StatusProvisioning,
		WorkloadType:   "ml-training",
		ReservationHrs: 4,
		StoragePolicy:  "destroy",
		PricePerHour:   0.50,
		CreatedAt:      now,
		ExpiresAt:      now.Add(4 * time.Hour),
	}
	err = store.Create(ctx, provisioningSession)
	require.NoError(t, err)

	// Should find pending session
	found, err := store.GetActiveSessionByConsumerAndOffer(ctx, "consumer-001", "offer-pending")
	require.NoError(t, err)
	assert.Equal(t, "sess-pending", found.ID)
	assert.Equal(t, models.StatusPending, found.Status)

	// Should find provisioning session
	found, err = store.GetActiveSessionByConsumerAndOffer(ctx, "consumer-002", "offer-provisioning")
	require.NoError(t, err)
	assert.Equal(t, "sess-provisioning", found.ID)
	assert.Equal(t, models.StatusProvisioning, found.Status)
}

func TestSessionStore_GetActiveSessionByConsumerAndOffer_DifferentConsumer(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	now := time.Now()

	// Create an active session for consumer-001
	session := &models.Session{
		ID:             "sess-consumer1",
		ConsumerID:     "consumer-001",
		Provider:       "vastai",
		OfferID:        "offer-shared",
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

	// consumer-002 should not find consumer-001's session
	_, err = store.GetActiveSessionByConsumerAndOffer(ctx, "consumer-002", "offer-shared")
	assert.ErrorIs(t, err, ErrNotFound)

	// consumer-001 should find their session
	found, err := store.GetActiveSessionByConsumerAndOffer(ctx, "consumer-001", "offer-shared")
	require.NoError(t, err)
	assert.Equal(t, "sess-consumer1", found.ID)
}

func TestSessionStore_CountSessionsByProviderAndStatus(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	now := time.Now()

	// Create sessions with various provider/status combinations
	sessions := []*models.Session{
		{
			ID: "sess-count-1", ConsumerID: "c1", Provider: "vastai", OfferID: "o1",
			GPUType: "RTX4090", GPUCount: 1, Status: models.StatusRunning,
			WorkloadType: "ml", ReservationHrs: 4, StoragePolicy: "destroy",
			PricePerHour: 0.5, CreatedAt: now, ExpiresAt: now.Add(4 * time.Hour),
		},
		{
			ID: "sess-count-2", ConsumerID: "c1", Provider: "vastai", OfferID: "o2",
			GPUType: "RTX4090", GPUCount: 1, Status: models.StatusRunning,
			WorkloadType: "ml", ReservationHrs: 4, StoragePolicy: "destroy",
			PricePerHour: 0.5, CreatedAt: now, ExpiresAt: now.Add(4 * time.Hour),
		},
		{
			ID: "sess-count-3", ConsumerID: "c1", Provider: "vastai", OfferID: "o3",
			GPUType: "RTX4090", GPUCount: 1, Status: models.StatusPending,
			WorkloadType: "ml", ReservationHrs: 4, StoragePolicy: "destroy",
			PricePerHour: 0.5, CreatedAt: now, ExpiresAt: now.Add(4 * time.Hour),
		},
		{
			ID: "sess-count-4", ConsumerID: "c1", Provider: "tensordock", OfferID: "o4",
			GPUType: "A100", GPUCount: 1, Status: models.StatusRunning,
			WorkloadType: "ml", ReservationHrs: 4, StoragePolicy: "destroy",
			PricePerHour: 1.5, CreatedAt: now, ExpiresAt: now.Add(4 * time.Hour),
		},
		{
			ID: "sess-count-5", ConsumerID: "c1", Provider: "vastai", OfferID: "o5",
			GPUType: "RTX4090", GPUCount: 1, Status: models.StatusStopped,
			WorkloadType: "ml", ReservationHrs: 4, StoragePolicy: "destroy",
			PricePerHour: 0.5, CreatedAt: now.Add(-time.Hour), ExpiresAt: now, StoppedAt: now,
		},
		{
			ID: "sess-count-6", ConsumerID: "c1", Provider: "vastai", OfferID: "o6",
			GPUType: "RTX4090", GPUCount: 1, Status: models.StatusFailed,
			WorkloadType: "ml", ReservationHrs: 4, StoragePolicy: "destroy",
			PricePerHour: 0.5, CreatedAt: now.Add(-time.Hour), ExpiresAt: now,
		},
	}

	for _, s := range sessions {
		err := store.Create(ctx, s)
		require.NoError(t, err)
	}

	// Count should exclude stopped and failed
	counts, err := store.CountSessionsByProviderAndStatus(ctx)
	require.NoError(t, err)

	// Build a map for easier verification
	countMap := make(map[string]int)
	for _, c := range counts {
		key := c.Provider + ":" + c.Status
		countMap[key] = c.Count
	}

	// vastai:running = 2
	assert.Equal(t, 2, countMap["vastai:running"])
	// vastai:pending = 1
	assert.Equal(t, 1, countMap["vastai:pending"])
	// tensordock:running = 1
	assert.Equal(t, 1, countMap["tensordock:running"])
	// stopped and failed should not be present
	assert.Equal(t, 0, countMap["vastai:stopped"])
	assert.Equal(t, 0, countMap["vastai:failed"])
}

func TestSessionStore_CountSessionsByProviderAndStatus_Empty(t *testing.T) {
	db := newTestDB(t)
	store := NewSessionStore(db)
	ctx := context.Background()

	// No sessions - should return empty slice
	counts, err := store.CountSessionsByProviderAndStatus(ctx)
	require.NoError(t, err)
	assert.Empty(t, counts)
}
