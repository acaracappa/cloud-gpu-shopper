package cost

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

const (
	// DefaultAggregationInterval is how often to record costs for running sessions
	DefaultAggregationInterval = 1 * time.Hour

	// DefaultBudgetWarningThreshold is the percentage at which to send a warning (80%)
	DefaultBudgetWarningThreshold = 0.80

	// DefaultBudgetExceededThreshold is the percentage at which to send exceeded alert (100%)
	DefaultBudgetExceededThreshold = 1.0
)

// CostStore defines the interface for cost persistence
type CostStore interface {
	Record(ctx context.Context, record *models.CostRecord) error
	RecordHourlyForSession(ctx context.Context, session *models.Session) error
	GetSessionCost(ctx context.Context, sessionID string) (float64, error)
	GetConsumerCost(ctx context.Context, consumerID string, start, end time.Time) (float64, error)
	GetSummary(ctx context.Context, query models.CostQuery) (*models.CostSummary, error)
}

// SessionStore defines the interface for session queries
type SessionStore interface {
	GetActiveSessions(ctx context.Context) ([]*models.Session, error)
	Get(ctx context.Context, id string) (*models.Session, error)
}

// ConsumerStore defines the interface for consumer persistence
type ConsumerStore interface {
	Get(ctx context.Context, id string) (*models.Consumer, error)
	GetAll(ctx context.Context) ([]*models.Consumer, error)
	Update(ctx context.Context, consumer *models.Consumer) error
}

// AlertSender sends budget alerts
type AlertSender interface {
	SendBudgetAlert(ctx context.Context, alert models.BudgetAlert) error
}

// noopAlertSender is a default sender that does nothing
type noopAlertSender struct{}

func (n *noopAlertSender) SendBudgetAlert(ctx context.Context, alert models.BudgetAlert) error {
	return nil
}

// Tracker handles cost aggregation and budget monitoring
type Tracker struct {
	costStore     CostStore
	sessionStore  SessionStore
	consumerStore ConsumerStore
	alertSender   AlertSender
	logger        *slog.Logger

	// Configuration
	aggregationInterval     time.Duration
	budgetWarningThreshold  float64
	budgetExceededThreshold float64

	// For time mocking in tests
	now func() time.Time

	// Shutdown coordination
	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}

	// Metrics
	metrics *Metrics
}

// Metrics tracks cost tracker statistics
type Metrics struct {
	mu              sync.RWMutex
	AggregationsRun int64
	CostsRecorded   int64
	BudgetWarnings  int64
	BudgetExceeded  int64
	Errors          int64
}

// Option configures the cost tracker
type Option func(*Tracker)

// WithLogger sets a custom logger
func WithLogger(logger *slog.Logger) Option {
	return func(t *Tracker) {
		t.logger = logger
	}
}

// WithAggregationInterval sets how often to record costs
func WithAggregationInterval(d time.Duration) Option {
	return func(t *Tracker) {
		t.aggregationInterval = d
	}
}

// WithBudgetWarningThreshold sets the warning threshold percentage
func WithBudgetWarningThreshold(threshold float64) Option {
	return func(t *Tracker) {
		t.budgetWarningThreshold = threshold
	}
}

// WithBudgetExceededThreshold sets the exceeded threshold percentage
func WithBudgetExceededThreshold(threshold float64) Option {
	return func(t *Tracker) {
		t.budgetExceededThreshold = threshold
	}
}

// WithAlertSender sets the alert sender
func WithAlertSender(sender AlertSender) Option {
	return func(t *Tracker) {
		t.alertSender = sender
	}
}

// WithTimeFunc sets a custom time function (for testing)
func WithTimeFunc(fn func() time.Time) Option {
	return func(t *Tracker) {
		t.now = fn
	}
}

// New creates a new cost tracker
func New(costStore CostStore, sessionStore SessionStore, consumerStore ConsumerStore, opts ...Option) *Tracker {
	t := &Tracker{
		costStore:               costStore,
		sessionStore:            sessionStore,
		consumerStore:           consumerStore,
		alertSender:             &noopAlertSender{},
		logger:                  slog.Default(),
		aggregationInterval:     DefaultAggregationInterval,
		budgetWarningThreshold:  DefaultBudgetWarningThreshold,
		budgetExceededThreshold: DefaultBudgetExceededThreshold,
		now:                     time.Now,
		stopCh:                  make(chan struct{}),
		doneCh:                  make(chan struct{}),
		metrics:                 &Metrics{},
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

// Start begins the cost aggregation loop
func (t *Tracker) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return nil
	}
	t.running = true
	t.stopCh = make(chan struct{})
	t.doneCh = make(chan struct{})
	t.mu.Unlock()

	t.logger.Info("cost tracker starting",
		slog.Duration("aggregation_interval", t.aggregationInterval))

	go t.run(ctx)
	return nil
}

// Stop gracefully stops the cost tracker
func (t *Tracker) Stop() {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	// Bug #53 fix: Capture channel references while holding lock to prevent race with Start()
	stopCh := t.stopCh
	doneCh := t.doneCh
	t.mu.Unlock()

	t.logger.Info("cost tracker stopping")
	close(stopCh)
	<-doneCh

	t.mu.Lock()
	t.running = false
	t.mu.Unlock()

	t.logger.Info("cost tracker stopped")
}

// run is the main aggregation loop
func (t *Tracker) run(ctx context.Context) {
	defer close(t.doneCh)

	ticker := time.NewTicker(t.aggregationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t.runAggregation(ctx)
		case <-t.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// runAggregation records costs for all running sessions and checks budgets
func (t *Tracker) runAggregation(ctx context.Context) {
	t.logger.Debug("running cost aggregation")

	t.metrics.mu.Lock()
	t.metrics.AggregationsRun++
	t.metrics.mu.Unlock()

	// Record costs for all running sessions
	t.recordCostsForRunningSessions(ctx)

	// Check budget thresholds
	t.checkBudgetThresholds(ctx)
}

// recordCostsForRunningSessions records hourly costs for all running sessions
func (t *Tracker) recordCostsForRunningSessions(ctx context.Context) {
	sessions, err := t.sessionStore.GetActiveSessions(ctx)
	if err != nil {
		t.logger.Error("failed to get active sessions for cost recording",
			slog.String("error", err.Error()))
		t.metrics.mu.Lock()
		t.metrics.Errors++
		t.metrics.mu.Unlock()
		return
	}

	for _, session := range sessions {
		// Only record costs for running sessions
		if session.Status != models.StatusRunning {
			continue
		}

		if err := t.costStore.RecordHourlyForSession(ctx, session); err != nil {
			t.logger.Error("failed to record cost for session",
				slog.String("session_id", session.ID),
				slog.String("error", err.Error()))
			t.metrics.mu.Lock()
			t.metrics.Errors++
			t.metrics.mu.Unlock()
			continue
		}

		t.logger.Debug("recorded cost for session",
			slog.String("session_id", session.ID),
			slog.Float64("amount", session.PricePerHour))

		// Bug #64 fix: Record cost in Prometheus metrics
		metrics.RecordCost(session.Provider, session.PricePerHour)

		t.metrics.mu.Lock()
		t.metrics.CostsRecorded++
		t.metrics.mu.Unlock()
	}
}

// RecordFinalCost records cost for a session that has terminated.
// It calculates cost for each hour (or partial hour) the session was alive
// and records entries, ensuring short-lived sessions are not missed.
func (t *Tracker) RecordFinalCost(ctx context.Context, session *models.Session) error {
	if session.PricePerHour <= 0 {
		return nil
	}

	endTime := session.StoppedAt
	if endTime.IsZero() {
		endTime = t.now()
	}
	startTime := session.CreatedAt
	if startTime.IsZero() || !endTime.After(startTime) {
		return nil
	}

	currentHour := startTime.Truncate(time.Hour)
	for !currentHour.After(endTime) {
		record := &models.CostRecord{
			SessionID:  session.ID,
			ConsumerID: session.ConsumerID,
			Provider:   session.Provider,
			GPUType:    session.GPUType,
			Hour:       currentHour,
			Amount:     session.PricePerHour,
			Currency:   "USD",
		}
		if err := t.costStore.Record(ctx, record); err != nil {
			return fmt.Errorf("failed to record cost for hour %s: %w", currentHour, err)
		}
		metrics.RecordCost(session.Provider, session.PricePerHour)
		currentHour = currentHour.Add(time.Hour)
	}

	t.logger.Info("recorded final cost for session",
		slog.String("session_id", session.ID),
		slog.Float64("price_per_hour", session.PricePerHour),
		slog.Duration("duration", endTime.Sub(startTime)))

	return nil
}

// checkBudgetThresholds checks if any consumers have exceeded their budget
func (t *Tracker) checkBudgetThresholds(ctx context.Context) {
	if t.consumerStore == nil {
		return
	}

	consumers, err := t.consumerStore.GetAll(ctx)
	if err != nil {
		t.logger.Error("failed to get consumers for budget check",
			slog.String("error", err.Error()))
		return
	}

	now := t.now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	endOfMonth := startOfMonth.AddDate(0, 1, 0)

	for _, consumer := range consumers {
		// Skip consumers without budget limits
		if consumer.BudgetLimit <= 0 {
			continue
		}

		// Get current month spend
		spend, err := t.costStore.GetConsumerCost(ctx, consumer.ID, startOfMonth, endOfMonth)
		if err != nil {
			t.logger.Error("failed to get consumer cost",
				slog.String("consumer_id", consumer.ID),
				slog.String("error", err.Error()))
			continue
		}

		consumer.CurrentSpend = spend
		percentage := spend / consumer.BudgetLimit

		// Check thresholds
		if percentage >= t.budgetExceededThreshold && !consumer.AlertSent {
			t.sendAlert(ctx, consumer, "exceeded", percentage)
			consumer.AlertSent = true
			t.consumerStore.Update(ctx, consumer)

			t.metrics.mu.Lock()
			t.metrics.BudgetExceeded++
			t.metrics.mu.Unlock()
		} else if percentage >= t.budgetWarningThreshold && !consumer.AlertSent {
			t.sendAlert(ctx, consumer, "warning", percentage)
			// Don't mark AlertSent yet - we'll send exceeded alert later
		}
	}
}

// sendAlert sends a budget alert
func (t *Tracker) sendAlert(ctx context.Context, consumer *models.Consumer, alertType string, percentage float64) {
	alert := models.BudgetAlert{
		ConsumerID:   consumer.ID,
		ConsumerName: consumer.Name,
		BudgetLimit:  consumer.BudgetLimit,
		CurrentSpend: consumer.CurrentSpend,
		Percentage:   percentage * 100,
		AlertType:    alertType,
		Timestamp:    t.now(),
	}

	t.logger.Warn("budget alert triggered",
		slog.String("consumer_id", consumer.ID),
		slog.String("alert_type", alertType),
		slog.Float64("percentage", percentage*100),
		slog.Float64("spend", consumer.CurrentSpend),
		slog.Float64("limit", consumer.BudgetLimit))

	// Bug #64 fix: Record budget alert in Prometheus metrics
	metrics.RecordBudgetAlert(alertType)

	if alertType == "warning" {
		t.metrics.mu.Lock()
		t.metrics.BudgetWarnings++
		t.metrics.mu.Unlock()
	}

	if err := t.alertSender.SendBudgetAlert(ctx, alert); err != nil {
		t.logger.Error("failed to send budget alert",
			slog.String("consumer_id", consumer.ID),
			slog.String("error", err.Error()))
	}
}

// GetSessionCost returns total cost for a session
func (t *Tracker) GetSessionCost(ctx context.Context, sessionID string) (float64, error) {
	return t.costStore.GetSessionCost(ctx, sessionID)
}

// GetConsumerCost returns cost for a consumer in a time period
func (t *Tracker) GetConsumerCost(ctx context.Context, consumerID string, start, end time.Time) (float64, error) {
	return t.costStore.GetConsumerCost(ctx, consumerID, start, end)
}

// GetSummary returns a cost summary
func (t *Tracker) GetSummary(ctx context.Context, query models.CostQuery) (*models.CostSummary, error) {
	return t.costStore.GetSummary(ctx, query)
}

// GetDailySummary returns cost summary for today
func (t *Tracker) GetDailySummary(ctx context.Context, consumerID string) (*models.CostSummary, error) {
	now := t.now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfDay := startOfDay.AddDate(0, 0, 1)

	return t.costStore.GetSummary(ctx, models.CostQuery{
		ConsumerID: consumerID,
		StartTime:  startOfDay,
		EndTime:    endOfDay,
	})
}

// GetMonthlySummary returns cost summary for current month
func (t *Tracker) GetMonthlySummary(ctx context.Context, consumerID string) (*models.CostSummary, error) {
	now := t.now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	endOfMonth := startOfMonth.AddDate(0, 1, 0)

	return t.costStore.GetSummary(ctx, models.CostQuery{
		ConsumerID: consumerID,
		StartTime:  startOfMonth,
		EndTime:    endOfMonth,
	})
}

// GetPeriodSummary returns cost summary for a specific period
func (t *Tracker) GetPeriodSummary(ctx context.Context, consumerID string, start, end time.Time) (*models.CostSummary, error) {
	return t.costStore.GetSummary(ctx, models.CostQuery{
		ConsumerID: consumerID,
		StartTime:  start,
		EndTime:    end,
	})
}

// RecordCost manually records a cost entry
func (t *Tracker) RecordCost(ctx context.Context, record *models.CostRecord) error {
	return t.costStore.Record(ctx, record)
}

// GetMetrics returns current tracker metrics
func (t *Tracker) GetMetrics() Metrics {
	t.metrics.mu.RLock()
	defer t.metrics.mu.RUnlock()

	return Metrics{
		AggregationsRun: t.metrics.AggregationsRun,
		CostsRecorded:   t.metrics.CostsRecorded,
		BudgetWarnings:  t.metrics.BudgetWarnings,
		BudgetExceeded:  t.metrics.BudgetExceeded,
		Errors:          t.metrics.Errors,
	}
}

// IsRunning returns whether the tracker is currently running
func (t *Tracker) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// RunAggregationNow triggers an immediate aggregation run
func (t *Tracker) RunAggregationNow(ctx context.Context) {
	t.runAggregation(ctx)
}
