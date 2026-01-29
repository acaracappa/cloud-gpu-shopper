package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/google/uuid"
)

// CostStore handles cost record persistence
type CostStore struct {
	db *DB
}

// NewCostStore creates a new cost store
func NewCostStore(db *DB) *CostStore {
	return &CostStore{db: db}
}

// Record records a cost entry for a session
func (s *CostStore) Record(ctx context.Context, record *models.CostRecord) error {
	if record.ID == "" {
		record.ID = uuid.New().String()
	}

	query := `
		INSERT INTO costs (id, session_id, consumer_id, provider, gpu_type, hour, amount, currency)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.ExecContext(ctx, query,
		record.ID,
		record.SessionID,
		record.ConsumerID,
		record.Provider,
		record.GPUType,
		record.Hour,
		record.Amount,
		record.Currency,
	)

	if err != nil {
		return fmt.Errorf("failed to record cost: %w", err)
	}

	return nil
}

// GetSessionCost returns total cost for a session
func (s *CostStore) GetSessionCost(ctx context.Context, sessionID string) (float64, error) {
	query := `SELECT COALESCE(SUM(amount), 0) FROM costs WHERE session_id = ?`

	var total float64
	err := s.db.QueryRowContext(ctx, query, sessionID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("failed to get session cost: %w", err)
	}

	return total, nil
}

// GetConsumerCost returns total cost for a consumer in a time period
func (s *CostStore) GetConsumerCost(ctx context.Context, consumerID string, start, end time.Time) (float64, error) {
	query := `
		SELECT COALESCE(SUM(amount), 0)
		FROM costs
		WHERE consumer_id = ? AND hour >= ? AND hour < ?
	`

	var total float64
	err := s.db.QueryRowContext(ctx, query, consumerID, start, end).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("failed to get consumer cost: %w", err)
	}

	return total, nil
}

// GetSummary returns a cost summary for the given query
func (s *CostStore) GetSummary(ctx context.Context, query models.CostQuery) (*models.CostSummary, error) {
	sqlQuery := `
		SELECT
			COALESCE(SUM(amount), 0) as total_cost,
			COUNT(DISTINCT session_id) as session_count,
			COUNT(*) as hours_used
		FROM costs
		WHERE 1=1
	`

	var args []interface{}

	if query.ConsumerID != "" {
		sqlQuery += " AND consumer_id = ?"
		args = append(args, query.ConsumerID)
	}

	if query.SessionID != "" {
		sqlQuery += " AND session_id = ?"
		args = append(args, query.SessionID)
	}

	if query.Provider != "" {
		sqlQuery += " AND provider = ?"
		args = append(args, query.Provider)
	}

	if !query.StartTime.IsZero() {
		sqlQuery += " AND hour >= ?"
		args = append(args, query.StartTime)
	}

	if !query.EndTime.IsZero() {
		sqlQuery += " AND hour < ?"
		args = append(args, query.EndTime)
	}

	summary := &models.CostSummary{
		ConsumerID:  query.ConsumerID,
		ByProvider:  make(map[string]float64),
		ByGPUType:   make(map[string]float64),
		PeriodStart: query.StartTime,
		PeriodEnd:   query.EndTime,
	}

	err := s.db.QueryRowContext(ctx, sqlQuery, args...).Scan(
		&summary.TotalCost,
		&summary.SessionCount,
		&summary.HoursUsed,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get cost summary: %w", err)
	}

	// Get breakdown by provider
	providerQuery := `
		SELECT provider, COALESCE(SUM(amount), 0)
		FROM costs
		WHERE 1=1
	`
	providerArgs := make([]interface{}, 0, len(args))

	if query.ConsumerID != "" {
		providerQuery += " AND consumer_id = ?"
		providerArgs = append(providerArgs, query.ConsumerID)
	}
	if !query.StartTime.IsZero() {
		providerQuery += " AND hour >= ?"
		providerArgs = append(providerArgs, query.StartTime)
	}
	if !query.EndTime.IsZero() {
		providerQuery += " AND hour < ?"
		providerArgs = append(providerArgs, query.EndTime)
	}

	providerQuery += " GROUP BY provider"

	rows, err := s.db.QueryContext(ctx, providerQuery, providerArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider breakdown: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var provider string
		var amount float64
		if err := rows.Scan(&provider, &amount); err != nil {
			return nil, fmt.Errorf("failed to scan provider row: %w", err)
		}
		summary.ByProvider[provider] = amount
	}

	// Get breakdown by GPU type
	gpuQuery := `
		SELECT gpu_type, COALESCE(SUM(amount), 0)
		FROM costs
		WHERE 1=1
	`
	gpuArgs := make([]interface{}, 0, len(args))

	if query.ConsumerID != "" {
		gpuQuery += " AND consumer_id = ?"
		gpuArgs = append(gpuArgs, query.ConsumerID)
	}
	if !query.StartTime.IsZero() {
		gpuQuery += " AND hour >= ?"
		gpuArgs = append(gpuArgs, query.StartTime)
	}
	if !query.EndTime.IsZero() {
		gpuQuery += " AND hour < ?"
		gpuArgs = append(gpuArgs, query.EndTime)
	}

	gpuQuery += " GROUP BY gpu_type"

	rows, err = s.db.QueryContext(ctx, gpuQuery, gpuArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to get GPU breakdown: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var gpuType string
		var amount float64
		if err := rows.Scan(&gpuType, &amount); err != nil {
			return nil, fmt.Errorf("failed to scan GPU row: %w", err)
		}
		summary.ByGPUType[gpuType] = amount
	}

	return summary, nil
}

// RecordHourlyForSession records an hourly cost entry for a running session
func (s *CostStore) RecordHourlyForSession(ctx context.Context, session *models.Session) error {
	record := &models.CostRecord{
		ID:         uuid.New().String(),
		SessionID:  session.ID,
		ConsumerID: session.ConsumerID,
		Provider:   session.Provider,
		GPUType:    session.GPUType,
		Hour:       time.Now().Truncate(time.Hour),
		Amount:     session.PricePerHour,
		Currency:   "USD",
	}

	return s.Record(ctx, record)
}
