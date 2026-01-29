package storage

import (
	"context"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestSession(t *testing.T, store *SessionStore, id string) *models.Session {
	t.Helper()
	ctx := context.Background()
	now := time.Now()

	session := &models.Session{
		ID:             id,
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
	return session
}

func TestCostStore_Record(t *testing.T) {
	db := newTestDB(t)
	sessionStore := NewSessionStore(db)
	costStore := NewCostStore(db)
	ctx := context.Background()

	// Create session first (foreign key constraint)
	session := createTestSession(t, sessionStore, "sess-cost-001")

	record := &models.CostRecord{
		SessionID:  session.ID,
		ConsumerID: session.ConsumerID,
		Provider:   session.Provider,
		GPUType:    session.GPUType,
		Hour:       time.Now().Truncate(time.Hour),
		Amount:     0.50,
		Currency:   "USD",
	}

	err := costStore.Record(ctx, record)
	require.NoError(t, err)

	// ID should be generated
	assert.NotEmpty(t, record.ID)
}

func TestCostStore_Record_WithID(t *testing.T) {
	db := newTestDB(t)
	sessionStore := NewSessionStore(db)
	costStore := NewCostStore(db)
	ctx := context.Background()

	session := createTestSession(t, sessionStore, "sess-cost-002")

	record := &models.CostRecord{
		ID:         "custom-cost-id",
		SessionID:  session.ID,
		ConsumerID: session.ConsumerID,
		Provider:   session.Provider,
		GPUType:    session.GPUType,
		Hour:       time.Now().Truncate(time.Hour),
		Amount:     0.50,
		Currency:   "USD",
	}

	err := costStore.Record(ctx, record)
	require.NoError(t, err)

	// ID should remain as provided
	assert.Equal(t, "custom-cost-id", record.ID)
}

func TestCostStore_GetSessionCost(t *testing.T) {
	db := newTestDB(t)
	sessionStore := NewSessionStore(db)
	costStore := NewCostStore(db)
	ctx := context.Background()

	session := createTestSession(t, sessionStore, "sess-cost-003")
	now := time.Now().Truncate(time.Hour)

	// Record multiple cost entries
	for i := 0; i < 3; i++ {
		record := &models.CostRecord{
			SessionID:  session.ID,
			ConsumerID: session.ConsumerID,
			Provider:   session.Provider,
			GPUType:    session.GPUType,
			Hour:       now.Add(time.Duration(i) * time.Hour),
			Amount:     0.50,
			Currency:   "USD",
		}
		err := costStore.Record(ctx, record)
		require.NoError(t, err)
	}

	// Get total cost
	total, err := costStore.GetSessionCost(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, 1.50, total)
}

func TestCostStore_GetSessionCost_NoRecords(t *testing.T) {
	db := newTestDB(t)
	costStore := NewCostStore(db)
	ctx := context.Background()

	total, err := costStore.GetSessionCost(ctx, "nonexistent-session")
	require.NoError(t, err)
	assert.Equal(t, 0.0, total)
}

func TestCostStore_GetConsumerCost(t *testing.T) {
	db := newTestDB(t)
	sessionStore := NewSessionStore(db)
	costStore := NewCostStore(db)
	ctx := context.Background()

	session := createTestSession(t, sessionStore, "sess-cost-004")
	baseTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	// Record costs across different hours
	for i := 0; i < 5; i++ {
		record := &models.CostRecord{
			SessionID:  session.ID,
			ConsumerID: session.ConsumerID,
			Provider:   session.Provider,
			GPUType:    session.GPUType,
			Hour:       baseTime.Add(time.Duration(i) * time.Hour),
			Amount:     1.00,
			Currency:   "USD",
		}
		err := costStore.Record(ctx, record)
		require.NoError(t, err)
	}

	// Query for subset of time range
	start := baseTime.Add(1 * time.Hour)
	end := baseTime.Add(4 * time.Hour)

	total, err := costStore.GetConsumerCost(ctx, session.ConsumerID, start, end)
	require.NoError(t, err)
	assert.Equal(t, 3.00, total) // Hours 1, 2, 3
}

func TestCostStore_GetSummary(t *testing.T) {
	db := newTestDB(t)
	sessionStore := NewSessionStore(db)
	costStore := NewCostStore(db)
	ctx := context.Background()

	// Create sessions with different providers/GPU types
	now := time.Now()

	// Session 1: vastai, RTX4090
	session1 := &models.Session{
		ID:             "sess-summary-1",
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
	err := sessionStore.Create(ctx, session1)
	require.NoError(t, err)

	// Session 2: tensordock, A100
	session2 := &models.Session{
		ID:             "sess-summary-2",
		ConsumerID:     "consumer-001",
		Provider:       "tensordock",
		OfferID:        "offer-2",
		GPUType:        "A100",
		GPUCount:       1,
		Status:         models.StatusRunning,
		WorkloadType:   "inference",
		ReservationHrs: 2,
		StoragePolicy:  "destroy",
		PricePerHour:   1.50,
		CreatedAt:      now,
		ExpiresAt:      now.Add(2 * time.Hour),
	}
	err = sessionStore.Create(ctx, session2)
	require.NoError(t, err)

	baseTime := now.Truncate(time.Hour)

	// Record costs for session 1 (2 hours)
	for i := 0; i < 2; i++ {
		err := costStore.Record(ctx, &models.CostRecord{
			SessionID:  session1.ID,
			ConsumerID: session1.ConsumerID,
			Provider:   session1.Provider,
			GPUType:    session1.GPUType,
			Hour:       baseTime.Add(time.Duration(i) * time.Hour),
			Amount:     0.50,
			Currency:   "USD",
		})
		require.NoError(t, err)
	}

	// Record costs for session 2 (3 hours)
	for i := 0; i < 3; i++ {
		err := costStore.Record(ctx, &models.CostRecord{
			SessionID:  session2.ID,
			ConsumerID: session2.ConsumerID,
			Provider:   session2.Provider,
			GPUType:    session2.GPUType,
			Hour:       baseTime.Add(time.Duration(i) * time.Hour),
			Amount:     1.50,
			Currency:   "USD",
		})
		require.NoError(t, err)
	}

	// Get summary for consumer
	summary, err := costStore.GetSummary(ctx, models.CostQuery{
		ConsumerID: "consumer-001",
	})
	require.NoError(t, err)

	assert.Equal(t, 5.50, summary.TotalCost)       // 2*0.50 + 3*1.50
	assert.Equal(t, 2, summary.SessionCount)       // 2 unique sessions
	assert.Equal(t, 5.0, summary.HoursUsed)        // 5 total cost records
	assert.Equal(t, 1.00, summary.ByProvider["vastai"])
	assert.Equal(t, 4.50, summary.ByProvider["tensordock"])
	assert.Equal(t, 1.00, summary.ByGPUType["RTX4090"])
	assert.Equal(t, 4.50, summary.ByGPUType["A100"])
}

func TestCostStore_GetSummary_WithFilters(t *testing.T) {
	db := newTestDB(t)
	sessionStore := NewSessionStore(db)
	costStore := NewCostStore(db)
	ctx := context.Background()

	now := time.Now()

	session := &models.Session{
		ID:             "sess-summary-filter",
		ConsumerID:     "consumer-filter",
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
	err := sessionStore.Create(ctx, session)
	require.NoError(t, err)

	baseTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	// Record 5 hours of costs
	for i := 0; i < 5; i++ {
		err := costStore.Record(ctx, &models.CostRecord{
			SessionID:  session.ID,
			ConsumerID: session.ConsumerID,
			Provider:   session.Provider,
			GPUType:    session.GPUType,
			Hour:       baseTime.Add(time.Duration(i) * time.Hour),
			Amount:     1.00,
			Currency:   "USD",
		})
		require.NoError(t, err)
	}

	// Get summary with time filter
	summary, err := costStore.GetSummary(ctx, models.CostQuery{
		ConsumerID: "consumer-filter",
		StartTime:  baseTime.Add(1 * time.Hour),
		EndTime:    baseTime.Add(4 * time.Hour),
	})
	require.NoError(t, err)

	assert.Equal(t, 3.00, summary.TotalCost) // Hours 1, 2, 3
	assert.Equal(t, 3.0, summary.HoursUsed)
}

func TestCostStore_GetSummary_ByProvider(t *testing.T) {
	db := newTestDB(t)
	sessionStore := NewSessionStore(db)
	costStore := NewCostStore(db)
	ctx := context.Background()

	now := time.Now()

	// Create two sessions with different providers
	for _, provider := range []string{"vastai", "tensordock"} {
		session := &models.Session{
			ID:             "sess-provider-" + provider,
			ConsumerID:     "consumer-provider",
			Provider:       provider,
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
		err := sessionStore.Create(ctx, session)
		require.NoError(t, err)

		err = costStore.Record(ctx, &models.CostRecord{
			SessionID:  session.ID,
			ConsumerID: session.ConsumerID,
			Provider:   provider,
			GPUType:    session.GPUType,
			Hour:       now.Truncate(time.Hour),
			Amount:     1.00,
			Currency:   "USD",
		})
		require.NoError(t, err)
	}

	// Get summary filtered by provider
	summary, err := costStore.GetSummary(ctx, models.CostQuery{
		ConsumerID: "consumer-provider",
		Provider:   "vastai",
	})
	require.NoError(t, err)

	assert.Equal(t, 1.00, summary.TotalCost)
	assert.Equal(t, 1, summary.SessionCount)
}

func TestCostStore_RecordHourlyForSession(t *testing.T) {
	db := newTestDB(t)
	sessionStore := NewSessionStore(db)
	costStore := NewCostStore(db)
	ctx := context.Background()

	session := createTestSession(t, sessionStore, "sess-hourly")

	err := costStore.RecordHourlyForSession(ctx, session)
	require.NoError(t, err)

	// Verify cost was recorded
	total, err := costStore.GetSessionCost(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, session.PricePerHour, total)
}
