package storage

import (
	"context"
	"fmt"
	"time"
)

// OfferFailureRecord represents a persisted offer failure event
type OfferFailureRecord struct {
	ID          string
	OfferID     string
	Provider    string
	GPUType     string
	FailureType string
	Reason      string
	CreatedAt   time.Time
}

// OfferSuppressionRecord represents a persisted offer suppression state
type OfferSuppressionRecord struct {
	OfferID      string
	Provider     string
	GPUType      string
	SuppressedAt time.Time
}

// OfferFailureStore handles persistence of offer failure tracking data
type OfferFailureStore struct {
	db *DB
}

// NewOfferFailureStore creates a new offer failure store
func NewOfferFailureStore(db *DB) *OfferFailureStore {
	return &OfferFailureStore{db: db}
}

// RecordFailure persists a failure event
func (s *OfferFailureStore) RecordFailure(ctx context.Context, offerID, provider, gpuType, failureType, reason string) error {
	query := `
		INSERT INTO offer_failures (offer_id, provider, gpu_type, failure_type, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query, offerID, provider, gpuType, failureType, reason, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("failed to record offer failure: %w", err)
	}
	return nil
}

// SetSuppression persists a suppression event (upsert)
func (s *OfferFailureStore) SetSuppression(ctx context.Context, offerID, provider, gpuType string, suppressedAt time.Time) error {
	query := `
		INSERT INTO offer_suppressions (offer_id, provider, gpu_type, suppressed_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(offer_id) DO UPDATE SET
			suppressed_at = excluded.suppressed_at
	`
	_, err := s.db.ExecContext(ctx, query, offerID, provider, gpuType, suppressedAt.UTC())
	if err != nil {
		return fmt.Errorf("failed to set offer suppression: %w", err)
	}
	return nil
}

// ClearSuppression removes a suppression record
func (s *OfferFailureStore) ClearSuppression(ctx context.Context, offerID string) error {
	query := `DELETE FROM offer_suppressions WHERE offer_id = ?`
	_, err := s.db.ExecContext(ctx, query, offerID)
	if err != nil {
		return fmt.Errorf("failed to clear offer suppression: %w", err)
	}
	return nil
}

// LoadRecentFailures loads failure events newer than the given cutoff time
func (s *OfferFailureStore) LoadRecentFailures(ctx context.Context, since time.Time) ([]OfferFailureRecord, error) {
	query := `
		SELECT offer_id, provider, gpu_type, failure_type, reason, created_at
		FROM offer_failures
		WHERE created_at > ?
		ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("failed to load recent failures: %w", err)
	}
	defer rows.Close()

	var records []OfferFailureRecord
	for rows.Next() {
		var r OfferFailureRecord
		if err := rows.Scan(&r.OfferID, &r.Provider, &r.GPUType, &r.FailureType, &r.Reason, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan failure record: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating failure records: %w", err)
	}
	return records, nil
}

// LoadActiveSuppressions loads suppressions that haven't expired
func (s *OfferFailureStore) LoadActiveSuppressions(ctx context.Context, cooldownExpiry time.Time) ([]OfferSuppressionRecord, error) {
	query := `
		SELECT offer_id, provider, gpu_type, suppressed_at
		FROM offer_suppressions
		WHERE suppressed_at > ?
	`
	rows, err := s.db.QueryContext(ctx, query, cooldownExpiry.UTC())
	if err != nil {
		return nil, fmt.Errorf("failed to load active suppressions: %w", err)
	}
	defer rows.Close()

	var records []OfferSuppressionRecord
	for rows.Next() {
		var r OfferSuppressionRecord
		if err := rows.Scan(&r.OfferID, &r.Provider, &r.GPUType, &r.SuppressedAt); err != nil {
			return nil, fmt.Errorf("failed to scan suppression record: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating suppression records: %w", err)
	}
	return records, nil
}

// CleanupOldFailures deletes failure events older than the given cutoff
func (s *OfferFailureStore) CleanupOldFailures(ctx context.Context, before time.Time) (int64, error) {
	query := `DELETE FROM offer_failures WHERE created_at < ?`
	result, err := s.db.ExecContext(ctx, query, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old failures: %w", err)
	}
	return result.RowsAffected()
}

// CleanupExpiredSuppressions deletes suppressions older than the given cutoff
func (s *OfferFailureStore) CleanupExpiredSuppressions(ctx context.Context, before time.Time) (int64, error) {
	query := `DELETE FROM offer_suppressions WHERE suppressed_at < ?`
	result, err := s.db.ExecContext(ctx, query, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired suppressions: %w", err)
	}
	return result.RowsAffected()
}

// CountByOfferID returns the count of recent failures per offer
func (s *OfferFailureStore) CountByOfferID(ctx context.Context, since time.Time) (map[string]int, error) {
	query := `
		SELECT offer_id, COUNT(*) as cnt
		FROM offer_failures
		WHERE created_at > ?
		GROUP BY offer_id
	`
	rows, err := s.db.QueryContext(ctx, query, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("failed to count failures by offer: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var offerID string
		var count int
		if err := rows.Scan(&offerID, &count); err != nil {
			return nil, fmt.Errorf("failed to scan count: %w", err)
		}
		counts[offerID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating counts: %w", err)
	}
	return counts, nil
}

