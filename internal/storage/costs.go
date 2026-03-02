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

// Record records a cost entry for a session.
// If a record already exists for the same session_id and hour, it updates the existing record.
// This prevents duplicate billing when cost aggregation runs more frequently than once per hour.
func (s *CostStore) Record(ctx context.Context, record *models.CostRecord) error {
	if record.ID == "" {
		record.ID = uuid.New().String()
	}

	// Use ON CONFLICT to handle duplicate (session_id, hour) gracefully.
	// When a duplicate is detected, we update the existing record with the latest values.
	// This ensures idempotent behavior for repeated aggregation runs within the same hour.
	query := `
		INSERT INTO costs (id, session_id, consumer_id, provider, gpu_type, hour, amount, currency)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, hour) DO UPDATE SET
			amount = excluded.amount,
			consumer_id = excluded.consumer_id,
			provider = excluded.provider,
			gpu_type = excluded.gpu_type,
			currency = excluded.currency
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

	whereClause, args := s.buildCostFilterClause(query)
	sqlQuery += whereClause

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
	summary.ByProvider, err = s.aggregateCostsByColumn(ctx, "provider", query)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider breakdown: %w", err)
	}

	// Get breakdown by GPU type
	summary.ByGPUType, err = s.aggregateCostsByColumn(ctx, "gpu_type", query)
	if err != nil {
		return nil, fmt.Errorf("failed to get GPU breakdown: %w", err)
	}

	return summary, nil
}

// buildCostFilterClause builds WHERE clause conditions and args from a CostQuery.
// Returns the clause string (starting with " AND" if conditions exist) and the args slice.
func (s *CostStore) buildCostFilterClause(query models.CostQuery) (string, []interface{}) {
	var clause string
	var args []interface{}

	if query.ConsumerID != "" {
		clause += " AND consumer_id = ?"
		args = append(args, query.ConsumerID)
	}

	if query.SessionID != "" {
		clause += " AND session_id = ?"
		args = append(args, query.SessionID)
	}

	if query.Provider != "" {
		clause += " AND provider = ?"
		args = append(args, query.Provider)
	}

	if !query.StartTime.IsZero() {
		clause += " AND hour >= ?"
		args = append(args, query.StartTime)
	}

	if !query.EndTime.IsZero() {
		clause += " AND hour < ?"
		args = append(args, query.EndTime)
	}

	return clause, args
}

// buildBreakdownFilterClause builds WHERE clause for breakdown queries.
// Uses a subset of filters (consumer, time range) appropriate for aggregations.
func (s *CostStore) buildBreakdownFilterClause(query models.CostQuery) (string, []interface{}) {
	var clause string
	var args []interface{}

	if query.ConsumerID != "" {
		clause += " AND consumer_id = ?"
		args = append(args, query.ConsumerID)
	}

	if !query.StartTime.IsZero() {
		clause += " AND hour >= ?"
		args = append(args, query.StartTime)
	}

	if !query.EndTime.IsZero() {
		clause += " AND hour < ?"
		args = append(args, query.EndTime)
	}

	return clause, args
}

// aggregateCostsByColumn aggregates costs grouped by the specified column.
// Returns a map of column value to total amount.
func (s *CostStore) aggregateCostsByColumn(ctx context.Context, column string, query models.CostQuery) (map[string]float64, error) {
	sqlQuery := fmt.Sprintf(`
		SELECT %s, COALESCE(SUM(amount), 0)
		FROM costs
		WHERE 1=1
	`, column)

	whereClause, args := s.buildBreakdownFilterClause(query)
	sqlQuery += whereClause
	sqlQuery += fmt.Sprintf(" GROUP BY %s", column)

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]float64)
	for rows.Next() {
		var key string
		var amount float64
		if err := rows.Scan(&key, &amount); err != nil {
			return nil, fmt.Errorf("failed to scan %s row: %w", column, err)
		}
		result[key] = amount
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating %s rows: %w", column, err)
	}

	return result, nil
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
