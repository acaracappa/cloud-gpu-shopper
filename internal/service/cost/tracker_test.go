package cost

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCostStore implements CostStore for testing
type mockCostStore struct {
	mu      sync.RWMutex
	records []*models.CostRecord
	err     error
}

func newMockCostStore() *mockCostStore {
	return &mockCostStore{
		records: make([]*models.CostRecord, 0),
	}
}

func (m *mockCostStore) Record(ctx context.Context, record *models.CostRecord) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, record)
	return nil
}

func (m *mockCostStore) RecordHourlyForSession(ctx context.Context, session *models.Session) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record := &models.CostRecord{
		SessionID:  session.ID,
		ConsumerID: session.ConsumerID,
		Provider:   session.Provider,
		GPUType:    session.GPUType,
		Hour:       time.Now().Truncate(time.Hour),
		Amount:     session.PricePerHour,
		Currency:   "USD",
	}
	m.records = append(m.records, record)
	return nil
}

func (m *mockCostStore) GetSessionCost(ctx context.Context, sessionID string) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var total float64
	for _, r := range m.records {
		if r.SessionID == sessionID {
			total += r.Amount
		}
	}
	return total, nil
}

func (m *mockCostStore) GetConsumerCost(ctx context.Context, consumerID string, start, end time.Time) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var total float64
	for _, r := range m.records {
		if r.ConsumerID == consumerID && !r.Hour.Before(start) && r.Hour.Before(end) {
			total += r.Amount
		}
	}
	return total, nil
}

func (m *mockCostStore) GetSummary(ctx context.Context, query models.CostQuery) (*models.CostSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	summary := &models.CostSummary{
		ConsumerID:  query.ConsumerID,
		ByProvider:  make(map[string]float64),
		ByGPUType:   make(map[string]float64),
		PeriodStart: query.StartTime,
		PeriodEnd:   query.EndTime,
	}

	sessions := make(map[string]bool)

	for _, r := range m.records {
		if query.ConsumerID != "" && r.ConsumerID != query.ConsumerID {
			continue
		}
		if !query.StartTime.IsZero() && r.Hour.Before(query.StartTime) {
			continue
		}
		if !query.EndTime.IsZero() && !r.Hour.Before(query.EndTime) {
			continue
		}

		summary.TotalCost += r.Amount
		summary.HoursUsed++
		summary.ByProvider[r.Provider] += r.Amount
		summary.ByGPUType[r.GPUType] += r.Amount
		sessions[r.SessionID] = true
	}

	summary.SessionCount = len(sessions)
	return summary, nil
}

func (m *mockCostStore) getRecords() []*models.CostRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*models.CostRecord, len(m.records))
	copy(result, m.records)
	return result
}

// mockSessionStore implements SessionStore for testing
type mockSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*models.Session
}

func newMockSessionStore() *mockSessionStore {
	return &mockSessionStore{
		sessions: make(map[string]*models.Session),
	}
}

func (m *mockSessionStore) add(session *models.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
}

func (m *mockSessionStore) GetActiveSessions(ctx context.Context) ([]*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*models.Session
	for _, s := range m.sessions {
		if s.IsActive() {
			copy := *s
			result = append(result, &copy)
		}
	}
	return result, nil
}

func (m *mockSessionStore) Get(ctx context.Context, id string) (*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.sessions[id]
	if !ok {
		return nil, errors.New("session not found")
	}
	copy := *s
	return &copy, nil
}

// mockConsumerStore implements ConsumerStore for testing
type mockConsumerStore struct {
	mu        sync.RWMutex
	consumers map[string]*models.Consumer
}

func newMockConsumerStore() *mockConsumerStore {
	return &mockConsumerStore{
		consumers: make(map[string]*models.Consumer),
	}
}

func (m *mockConsumerStore) add(consumer *models.Consumer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.consumers[consumer.ID] = consumer
}

func (m *mockConsumerStore) Get(ctx context.Context, id string) (*models.Consumer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.consumers[id]
	if !ok {
		return nil, errors.New("consumer not found")
	}
	copy := *c
	return &copy, nil
}

func (m *mockConsumerStore) GetAll(ctx context.Context) ([]*models.Consumer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*models.Consumer
	for _, c := range m.consumers {
		copy := *c
		result = append(result, &copy)
	}
	return result, nil
}

func (m *mockConsumerStore) Update(ctx context.Context, consumer *models.Consumer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.consumers[consumer.ID] = consumer
	return nil
}

// mockAlertSender implements AlertSender for testing
type mockAlertSender struct {
	mu     sync.Mutex
	alerts []models.BudgetAlert
}

func newMockAlertSender() *mockAlertSender {
	return &mockAlertSender{
		alerts: make([]models.BudgetAlert, 0),
	}
}

func (m *mockAlertSender) SendBudgetAlert(ctx context.Context, alert models.BudgetAlert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts = append(m.alerts, alert)
	return nil
}

func (m *mockAlertSender) getAlerts() []models.BudgetAlert {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]models.BudgetAlert, len(m.alerts))
	copy(result, m.alerts)
	return result
}

func TestTracker_New(t *testing.T) {
	costStore := newMockCostStore()
	sessionStore := newMockSessionStore()
	consumerStore := newMockConsumerStore()

	tracker := New(costStore, sessionStore, consumerStore)

	assert.NotNil(t, tracker)
	assert.Equal(t, DefaultAggregationInterval, tracker.aggregationInterval)
	assert.Equal(t, DefaultBudgetWarningThreshold, tracker.budgetWarningThreshold)
	assert.False(t, tracker.IsRunning())
}

func TestTracker_StartStop(t *testing.T) {
	costStore := newMockCostStore()
	sessionStore := newMockSessionStore()
	consumerStore := newMockConsumerStore()

	tracker := New(costStore, sessionStore, consumerStore,
		WithAggregationInterval(10*time.Millisecond))

	ctx := context.Background()

	err := tracker.Start(ctx)
	require.NoError(t, err)
	assert.True(t, tracker.IsRunning())

	time.Sleep(50 * time.Millisecond)

	tracker.Stop()
	assert.False(t, tracker.IsRunning())
}

func TestTracker_RecordsCostsForRunningSessions(t *testing.T) {
	costStore := newMockCostStore()
	sessionStore := newMockSessionStore()
	consumerStore := newMockConsumerStore()

	// Add a running session
	runningSession := &models.Session{
		ID:           "sess-running",
		ConsumerID:   "consumer-001",
		Provider:     "vastai",
		GPUType:      "RTX4090",
		Status:       models.StatusRunning,
		PricePerHour: 0.50,
	}
	sessionStore.add(runningSession)

	// Add a pending session (should not be billed)
	pendingSession := &models.Session{
		ID:           "sess-pending",
		ConsumerID:   "consumer-001",
		Status:       models.StatusPending,
		PricePerHour: 0.50,
	}
	sessionStore.add(pendingSession)

	tracker := New(costStore, sessionStore, consumerStore)

	ctx := context.Background()
	tracker.RunAggregationNow(ctx)

	records := costStore.getRecords()
	assert.Len(t, records, 1)
	assert.Equal(t, "sess-running", records[0].SessionID)
	assert.Equal(t, 0.50, records[0].Amount)

	metrics := tracker.GetMetrics()
	assert.Equal(t, int64(1), metrics.AggregationsRun)
	assert.Equal(t, int64(1), metrics.CostsRecorded)
}

func TestTracker_BudgetWarning(t *testing.T) {
	costStore := newMockCostStore()
	sessionStore := newMockSessionStore()
	consumerStore := newMockConsumerStore()
	alertSender := newMockAlertSender()

	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	startOfMonth := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Add some existing cost records
	costStore.Record(context.Background(), &models.CostRecord{
		ConsumerID: "consumer-001",
		Hour:       startOfMonth.Add(24 * time.Hour),
		Amount:     80.0, // $80 spent
	})

	// Consumer with $100 budget
	consumerStore.add(&models.Consumer{
		ID:          "consumer-001",
		Name:        "Test Consumer",
		BudgetLimit: 100.0,
	})

	tracker := New(costStore, sessionStore, consumerStore,
		WithAlertSender(alertSender),
		WithBudgetWarningThreshold(0.80),
		WithTimeFunc(func() time.Time { return now }))

	ctx := context.Background()
	tracker.RunAggregationNow(ctx)

	alerts := alertSender.getAlerts()
	assert.Len(t, alerts, 1)
	assert.Equal(t, "warning", alerts[0].AlertType)
	assert.Equal(t, "consumer-001", alerts[0].ConsumerID)

	metrics := tracker.GetMetrics()
	assert.Equal(t, int64(1), metrics.BudgetWarnings)
}

func TestTracker_BudgetExceeded(t *testing.T) {
	costStore := newMockCostStore()
	sessionStore := newMockSessionStore()
	consumerStore := newMockConsumerStore()
	alertSender := newMockAlertSender()

	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	startOfMonth := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Add cost records that exceed budget
	costStore.Record(context.Background(), &models.CostRecord{
		ConsumerID: "consumer-001",
		Hour:       startOfMonth.Add(24 * time.Hour),
		Amount:     110.0, // $110 spent, over $100 limit
	})

	consumerStore.add(&models.Consumer{
		ID:          "consumer-001",
		Name:        "Test Consumer",
		BudgetLimit: 100.0,
	})

	tracker := New(costStore, sessionStore, consumerStore,
		WithAlertSender(alertSender),
		WithTimeFunc(func() time.Time { return now }))

	ctx := context.Background()
	tracker.RunAggregationNow(ctx)

	alerts := alertSender.getAlerts()
	assert.Len(t, alerts, 1)
	assert.Equal(t, "exceeded", alerts[0].AlertType)

	metrics := tracker.GetMetrics()
	assert.Equal(t, int64(1), metrics.BudgetExceeded)

	// Consumer should be marked as alert sent
	updated, _ := consumerStore.Get(ctx, "consumer-001")
	assert.True(t, updated.AlertSent)
}

func TestTracker_NoBudgetLimitNoAlert(t *testing.T) {
	costStore := newMockCostStore()
	sessionStore := newMockSessionStore()
	consumerStore := newMockConsumerStore()
	alertSender := newMockAlertSender()

	// Consumer with no budget limit
	consumerStore.add(&models.Consumer{
		ID:          "consumer-001",
		Name:        "Test Consumer",
		BudgetLimit: 0, // No limit
	})

	// Add lots of cost
	costStore.Record(context.Background(), &models.CostRecord{
		ConsumerID: "consumer-001",
		Hour:       time.Now().Truncate(time.Hour),
		Amount:     1000.0,
	})

	tracker := New(costStore, sessionStore, consumerStore,
		WithAlertSender(alertSender))

	ctx := context.Background()
	tracker.RunAggregationNow(ctx)

	// No alerts should be sent
	alerts := alertSender.getAlerts()
	assert.Empty(t, alerts)
}

func TestTracker_GetSessionCost(t *testing.T) {
	costStore := newMockCostStore()
	sessionStore := newMockSessionStore()

	// Add cost records
	costStore.Record(context.Background(), &models.CostRecord{
		SessionID: "sess-001",
		Amount:    0.50,
	})
	costStore.Record(context.Background(), &models.CostRecord{
		SessionID: "sess-001",
		Amount:    0.50,
	})
	costStore.Record(context.Background(), &models.CostRecord{
		SessionID: "sess-002",
		Amount:    1.00,
	})

	tracker := New(costStore, sessionStore, nil)

	ctx := context.Background()
	cost, err := tracker.GetSessionCost(ctx, "sess-001")

	require.NoError(t, err)
	assert.Equal(t, 1.00, cost)
}

func TestTracker_GetDailySummary(t *testing.T) {
	costStore := newMockCostStore()
	sessionStore := newMockSessionStore()

	now := time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC)
	today := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	yesterday := time.Date(2024, 1, 14, 10, 0, 0, 0, time.UTC)

	// Add today's cost
	costStore.Record(context.Background(), &models.CostRecord{
		SessionID:  "sess-001",
		ConsumerID: "consumer-001",
		Provider:   "vastai",
		GPUType:    "RTX4090",
		Hour:       today,
		Amount:     0.50,
	})

	// Add yesterday's cost
	costStore.Record(context.Background(), &models.CostRecord{
		SessionID:  "sess-001",
		ConsumerID: "consumer-001",
		Provider:   "vastai",
		GPUType:    "RTX4090",
		Hour:       yesterday,
		Amount:     1.00,
	})

	tracker := New(costStore, sessionStore, nil,
		WithTimeFunc(func() time.Time { return now }))

	ctx := context.Background()
	summary, err := tracker.GetDailySummary(ctx, "consumer-001")

	require.NoError(t, err)
	assert.Equal(t, 0.50, summary.TotalCost)
	assert.Equal(t, 1, summary.SessionCount)
}

func TestTracker_GetMonthlySummary(t *testing.T) {
	costStore := newMockCostStore()
	sessionStore := newMockSessionStore()

	now := time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC)
	thisMonth := time.Date(2024, 1, 10, 10, 0, 0, 0, time.UTC)
	lastMonth := time.Date(2023, 12, 15, 10, 0, 0, 0, time.UTC)

	// Add this month's cost
	costStore.Record(context.Background(), &models.CostRecord{
		SessionID:  "sess-001",
		ConsumerID: "consumer-001",
		Provider:   "vastai",
		GPUType:    "RTX4090",
		Hour:       thisMonth,
		Amount:     10.00,
	})

	// Add last month's cost
	costStore.Record(context.Background(), &models.CostRecord{
		SessionID:  "sess-002",
		ConsumerID: "consumer-001",
		Provider:   "tensordock",
		GPUType:    "A100",
		Hour:       lastMonth,
		Amount:     50.00,
	})

	tracker := New(costStore, sessionStore, nil,
		WithTimeFunc(func() time.Time { return now }))

	ctx := context.Background()
	summary, err := tracker.GetMonthlySummary(ctx, "consumer-001")

	require.NoError(t, err)
	assert.Equal(t, 10.00, summary.TotalCost)
	assert.Equal(t, 1, summary.SessionCount)
	assert.Equal(t, 10.00, summary.ByProvider["vastai"])
	assert.Equal(t, 10.00, summary.ByGPUType["RTX4090"])
}

func TestTracker_RecordCost(t *testing.T) {
	costStore := newMockCostStore()
	sessionStore := newMockSessionStore()

	tracker := New(costStore, sessionStore, nil)

	ctx := context.Background()
	err := tracker.RecordCost(ctx, &models.CostRecord{
		SessionID:  "sess-001",
		ConsumerID: "consumer-001",
		Provider:   "vastai",
		GPUType:    "RTX4090",
		Hour:       time.Now().Truncate(time.Hour),
		Amount:     0.75,
		Currency:   "USD",
	})

	require.NoError(t, err)

	records := costStore.getRecords()
	assert.Len(t, records, 1)
	assert.Equal(t, 0.75, records[0].Amount)
}

func TestTracker_CostRecordingError(t *testing.T) {
	costStore := newMockCostStore()
	costStore.err = errors.New("database error")
	sessionStore := newMockSessionStore()

	sessionStore.add(&models.Session{
		ID:           "sess-001",
		Status:       models.StatusRunning,
		PricePerHour: 0.50,
	})

	tracker := New(costStore, sessionStore, nil)

	ctx := context.Background()
	tracker.RunAggregationNow(ctx)

	metrics := tracker.GetMetrics()
	assert.Equal(t, int64(1), metrics.Errors)
}

func TestTracker_MultipleSessions(t *testing.T) {
	costStore := newMockCostStore()
	sessionStore := newMockSessionStore()

	// Add multiple running sessions
	for i := 1; i <= 5; i++ {
		sessionStore.add(&models.Session{
			ID:           "sess-" + string(rune('0'+i)),
			ConsumerID:   "consumer-001",
			Provider:     "vastai",
			GPUType:      "RTX4090",
			Status:       models.StatusRunning,
			PricePerHour: 0.50,
		})
	}

	tracker := New(costStore, sessionStore, nil)

	ctx := context.Background()
	tracker.RunAggregationNow(ctx)

	records := costStore.getRecords()
	assert.Len(t, records, 5)

	metrics := tracker.GetMetrics()
	assert.Equal(t, int64(5), metrics.CostsRecorded)
}

func TestTracker_GetPeriodSummary(t *testing.T) {
	costStore := newMockCostStore()
	sessionStore := newMockSessionStore()

	// Add costs at different times
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		costStore.Record(context.Background(), &models.CostRecord{
			SessionID:  "sess-001",
			ConsumerID: "consumer-001",
			Provider:   "vastai",
			GPUType:    "RTX4090",
			Hour:       baseTime.Add(time.Duration(i) * 24 * time.Hour),
			Amount:     1.00,
		})
	}

	tracker := New(costStore, sessionStore, nil)

	ctx := context.Background()
	start := baseTime.Add(3 * 24 * time.Hour)
	end := baseTime.Add(7 * 24 * time.Hour)
	summary, err := tracker.GetPeriodSummary(ctx, "consumer-001", start, end)

	require.NoError(t, err)
	assert.Equal(t, 4.00, summary.TotalCost) // Days 3, 4, 5, 6
}
