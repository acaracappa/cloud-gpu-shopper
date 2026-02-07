package benchmark

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ManifestStatus represents the status of a benchmark manifest entry
type ManifestStatus string

const (
	ManifestStatusPending ManifestStatus = "pending"
	ManifestStatusRunning ManifestStatus = "running"
	ManifestStatusSuccess ManifestStatus = "success"
	ManifestStatusFailed  ManifestStatus = "failed"
	ManifestStatusTimeout ManifestStatus = "timeout"
	ManifestStatusSkipped ManifestStatus = "skipped"
)

// ManifestEntry represents a single benchmark test in a run
type ManifestEntry struct {
	ID       string         `json:"id"`
	RunID    string         `json:"run_id"`
	GPUType  string         `json:"gpu_type"`
	Provider string         `json:"provider"`
	Model    string         `json:"model"`
	Status   ManifestStatus `json:"status"`
	Priority int            `json:"priority"` // P0=highest, P2=lowest

	// Worker tracking
	WorkerID   string `json:"worker_id,omitempty"`
	OutputFile string `json:"output_file,omitempty"`

	// Session info
	SessionID string  `json:"session_id,omitempty"`
	OfferID   string  `json:"offer_id,omitempty"`
	PriceHour float64 `json:"price_per_hour,omitempty"`

	// Results
	BenchmarkID     string  `json:"benchmark_id,omitempty"`
	TokensPerSecond float64 `json:"tokens_per_second,omitempty"`
	TotalCost       float64 `json:"total_cost,omitempty"`

	// Failure info
	FailureReason string `json:"failure_reason,omitempty"`
	FailureStage  string `json:"failure_stage,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// ManifestStore provides persistence for benchmark orchestration manifests
type ManifestStore struct {
	db *sql.DB
}

// NewManifestStore creates a new manifest store
func NewManifestStore(db *sql.DB) (*ManifestStore, error) {
	s := &ManifestStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate manifest tables: %w", err)
	}
	return s, nil
}

func (s *ManifestStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS benchmark_manifest (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			gpu_type TEXT NOT NULL,
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			priority INTEGER NOT NULL DEFAULT 1,

			-- Worker tracking
			worker_id TEXT,
			output_file TEXT,

			-- Session info
			session_id TEXT,
			offer_id TEXT,
			price_per_hour REAL,

			-- Results
			benchmark_id TEXT,
			tokens_per_second REAL,
			total_cost REAL,

			-- Failure info
			failure_reason TEXT,
			failure_stage TEXT,

			-- Timestamps
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			completed_at DATETIME
		);

		CREATE INDEX IF NOT EXISTS idx_manifest_run_id ON benchmark_manifest(run_id);
		CREATE INDEX IF NOT EXISTS idx_manifest_status ON benchmark_manifest(status);
	`)
	if err != nil {
		return err
	}

	// Idempotent column additions for tables created by older schema
	alters := []string{
		"ALTER TABLE benchmark_manifest ADD COLUMN priority INTEGER NOT NULL DEFAULT 1",
		"ALTER TABLE benchmark_manifest ADD COLUMN worker_id TEXT",
		"ALTER TABLE benchmark_manifest ADD COLUMN output_file TEXT",
		"ALTER TABLE benchmark_manifest ADD COLUMN offer_id TEXT",
		"ALTER TABLE benchmark_manifest ADD COLUMN price_per_hour REAL",
		"ALTER TABLE benchmark_manifest ADD COLUMN total_cost REAL",
	}
	for _, stmt := range alters {
		_, _ = s.db.Exec(stmt) // Ignore "duplicate column" errors
	}

	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_manifest_priority ON benchmark_manifest(priority, status)")

	return nil
}

// Create inserts a new manifest entry
func (s *ManifestStore) Create(ctx context.Context, entry *ManifestEntry) error {
	if entry.ID == "" {
		entry.ID = "manifest-" + uuid.New().String()[:8]
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	if entry.Status == "" {
		entry.Status = ManifestStatusPending
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO benchmark_manifest (
			id, run_id, gpu_type, provider, model, status, priority,
			worker_id, output_file, session_id, offer_id, price_per_hour,
			benchmark_id, tokens_per_second, total_cost,
			failure_reason, failure_stage, created_at, started_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		entry.ID, entry.RunID, entry.GPUType, entry.Provider, entry.Model,
		entry.Status, entry.Priority, entry.WorkerID, entry.OutputFile,
		entry.SessionID, entry.OfferID, entry.PriceHour,
		entry.BenchmarkID, entry.TokensPerSecond, entry.TotalCost,
		entry.FailureReason, entry.FailureStage, entry.CreatedAt,
		entry.StartedAt, entry.CompletedAt,
	)
	return err
}

// Update modifies an existing manifest entry
func (s *ManifestStore) Update(ctx context.Context, entry *ManifestEntry) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE benchmark_manifest SET
			status = ?, worker_id = ?, output_file = ?,
			session_id = ?, offer_id = ?, price_per_hour = ?,
			benchmark_id = ?, tokens_per_second = ?, total_cost = ?,
			failure_reason = ?, failure_stage = ?,
			started_at = ?, completed_at = ?
		WHERE id = ?
	`,
		entry.Status, entry.WorkerID, entry.OutputFile,
		entry.SessionID, entry.OfferID, entry.PriceHour,
		entry.BenchmarkID, entry.TokensPerSecond, entry.TotalCost,
		entry.FailureReason, entry.FailureStage,
		entry.StartedAt, entry.CompletedAt, entry.ID,
	)
	return err
}

// Get retrieves a manifest entry by ID
func (s *ManifestStore) Get(ctx context.Context, id string) (*ManifestEntry, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, run_id, gpu_type, provider, model, status, priority,
			worker_id, output_file, session_id, offer_id, price_per_hour,
			benchmark_id, tokens_per_second, total_cost,
			failure_reason, failure_stage, created_at, started_at, completed_at
		FROM benchmark_manifest WHERE id = ?
	`, id)
	return s.scanEntry(row)
}

// ListByRun returns all entries for a specific run
func (s *ManifestStore) ListByRun(ctx context.Context, runID string) ([]*ManifestEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, gpu_type, provider, model, status, priority,
			worker_id, output_file, session_id, offer_id, price_per_hour,
			benchmark_id, tokens_per_second, total_cost,
			failure_reason, failure_stage, created_at, started_at, completed_at
		FROM benchmark_manifest
		WHERE run_id = ?
		ORDER BY priority ASC, created_at ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanEntries(rows)
}

// GetPendingByPriority returns pending entries ordered by priority
func (s *ManifestStore) GetPendingByPriority(ctx context.Context, runID string, limit int) ([]*ManifestEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, gpu_type, provider, model, status, priority,
			worker_id, output_file, session_id, offer_id, price_per_hour,
			benchmark_id, tokens_per_second, total_cost,
			failure_reason, failure_stage, created_at, started_at, completed_at
		FROM benchmark_manifest
		WHERE run_id = ? AND status = 'pending'
		ORDER BY priority ASC, created_at ASC
		LIMIT ?
	`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanEntries(rows)
}

// GetRunning returns all running entries for a run
func (s *ManifestStore) GetRunning(ctx context.Context, runID string) ([]*ManifestEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, gpu_type, provider, model, status, priority,
			worker_id, output_file, session_id, offer_id, price_per_hour,
			benchmark_id, tokens_per_second, total_cost,
			failure_reason, failure_stage, created_at, started_at, completed_at
		FROM benchmark_manifest
		WHERE run_id = ? AND status = 'running'
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanEntries(rows)
}

// GetSummary returns a summary of manifest statuses for a run
func (s *ManifestStore) GetSummary(ctx context.Context, runID string) (map[ManifestStatus]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM benchmark_manifest
		WHERE run_id = ?
		GROUP BY status
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summary := make(map[ManifestStatus]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		summary[ManifestStatus(status)] = count
	}
	return summary, rows.Err()
}

// GetTotalCost returns total cost for a run
func (s *ManifestStore) GetTotalCost(ctx context.Context, runID string) (float64, error) {
	var total sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
		SELECT SUM(total_cost) FROM benchmark_manifest
		WHERE run_id = ? AND total_cost > 0
	`, runID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Float64, nil
}

// MarkRunning marks an entry as running with worker info
func (s *ManifestStore) MarkRunning(ctx context.Context, id, workerID, outputFile string) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE benchmark_manifest SET
			status = 'running', worker_id = ?, output_file = ?, started_at = ?
		WHERE id = ?
	`, workerID, outputFile, now, id)
	return err
}

// MarkSuccess marks an entry as successful with results
func (s *ManifestStore) MarkSuccess(ctx context.Context, id, benchmarkID string, tps, cost float64) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE benchmark_manifest SET
			status = 'success', benchmark_id = ?, tokens_per_second = ?,
			total_cost = ?, completed_at = ?
		WHERE id = ?
	`, benchmarkID, tps, cost, now, id)
	return err
}

// MarkFailed marks an entry as failed
func (s *ManifestStore) MarkFailed(ctx context.Context, id, reason, stage string) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE benchmark_manifest SET
			status = 'failed', failure_reason = ?, failure_stage = ?, completed_at = ?
		WHERE id = ?
	`, reason, stage, now, id)
	return err
}

// MarkTimeout marks an entry as timed out
func (s *ManifestStore) MarkTimeout(ctx context.Context, id, stage string) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE benchmark_manifest SET
			status = 'timeout', failure_reason = 'timeout', failure_stage = ?, completed_at = ?
		WHERE id = ?
	`, stage, now, id)
	return err
}

func (s *ManifestStore) scanEntry(row *sql.Row) (*ManifestEntry, error) {
	var e ManifestEntry
	var workerID, outputFile, sessionID, offerID sql.NullString
	var priceHour, tps, cost sql.NullFloat64
	var benchmarkID, failureReason, failureStage sql.NullString
	var startedAt, completedAt sql.NullTime

	err := row.Scan(
		&e.ID, &e.RunID, &e.GPUType, &e.Provider, &e.Model, &e.Status, &e.Priority,
		&workerID, &outputFile, &sessionID, &offerID, &priceHour,
		&benchmarkID, &tps, &cost,
		&failureReason, &failureStage, &e.CreatedAt, &startedAt, &completedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	e.WorkerID = workerID.String
	e.OutputFile = outputFile.String
	e.SessionID = sessionID.String
	e.OfferID = offerID.String
	e.PriceHour = priceHour.Float64
	e.BenchmarkID = benchmarkID.String
	e.TokensPerSecond = tps.Float64
	e.TotalCost = cost.Float64
	e.FailureReason = failureReason.String
	e.FailureStage = failureStage.String
	if startedAt.Valid {
		e.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		e.CompletedAt = &completedAt.Time
	}

	return &e, nil
}

func (s *ManifestStore) scanEntries(rows *sql.Rows) ([]*ManifestEntry, error) {
	var entries []*ManifestEntry
	for rows.Next() {
		var e ManifestEntry
		var workerID, outputFile, sessionID, offerID sql.NullString
		var priceHour, tps, cost sql.NullFloat64
		var benchmarkID, failureReason, failureStage sql.NullString
		var startedAt, completedAt sql.NullTime

		err := rows.Scan(
			&e.ID, &e.RunID, &e.GPUType, &e.Provider, &e.Model, &e.Status, &e.Priority,
			&workerID, &outputFile, &sessionID, &offerID, &priceHour,
			&benchmarkID, &tps, &cost,
			&failureReason, &failureStage, &e.CreatedAt, &startedAt, &completedAt,
		)
		if err != nil {
			return nil, err
		}

		e.WorkerID = workerID.String
		e.OutputFile = outputFile.String
		e.SessionID = sessionID.String
		e.OfferID = offerID.String
		e.PriceHour = priceHour.Float64
		e.BenchmarkID = benchmarkID.String
		e.TokensPerSecond = tps.Float64
		e.TotalCost = cost.Float64
		e.FailureReason = failureReason.String
		e.FailureStage = failureStage.String
		if startedAt.Valid {
			e.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			e.CompletedAt = &completedAt.Time
		}

		entries = append(entries, &e)
	}
	return entries, rows.Err()
}
