package benchmark

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Schedule defines a recurring benchmark configuration.
type Schedule struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"` // e.g. "weekly-value-check"
	CronExpr  string              `json:"cron"` // e.g. "0 0 * * 0" (weekly)
	Request   BenchmarkRunRequest `json:"run_request"`
	Enabled   bool                `json:"enabled"`
	LastRunID string              `json:"last_run_id,omitempty"`
	LastRunAt *time.Time          `json:"last_run_at,omitempty"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

// ScheduleStore provides persistence for benchmark schedules.
type ScheduleStore struct {
	db *sql.DB
}

// NewScheduleStore creates a new schedule store.
func NewScheduleStore(db *sql.DB) (*ScheduleStore, error) {
	s := &ScheduleStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate schedule tables: %w", err)
	}
	return s, nil
}

func (s *ScheduleStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS benchmark_schedules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			cron_expr TEXT NOT NULL,
			run_request_json TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT 1,
			last_run_id TEXT,
			last_run_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	return err
}

// Create inserts a new schedule.
func (s *ScheduleStore) Create(ctx context.Context, sched *Schedule) error {
	if sched.ID == "" {
		sched.ID = "sched-" + uuid.New().String()[:8]
	}
	if sched.CreatedAt.IsZero() {
		sched.CreatedAt = time.Now()
	}
	sched.UpdatedAt = time.Now()

	reqJSON, err := json.Marshal(sched.Request)
	if err != nil {
		return fmt.Errorf("failed to marshal run request: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO benchmark_schedules (id, name, cron_expr, run_request_json, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, sched.ID, sched.Name, sched.CronExpr, string(reqJSON), sched.Enabled, sched.CreatedAt, sched.UpdatedAt)
	return err
}

// Update modifies an existing schedule.
func (s *ScheduleStore) Update(ctx context.Context, sched *Schedule) error {
	sched.UpdatedAt = time.Now()

	reqJSON, err := json.Marshal(sched.Request)
	if err != nil {
		return fmt.Errorf("failed to marshal run request: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE benchmark_schedules SET
			name = ?, cron_expr = ?, run_request_json = ?, enabled = ?,
			last_run_id = ?, last_run_at = ?, updated_at = ?
		WHERE id = ?
	`, sched.Name, sched.CronExpr, string(reqJSON), sched.Enabled,
		sched.LastRunID, sched.LastRunAt, sched.UpdatedAt, sched.ID)
	return err
}

// Get retrieves a schedule by ID.
func (s *ScheduleStore) Get(ctx context.Context, id string) (*Schedule, error) {
	var sched Schedule
	var reqJSON string
	var lastRunAt sql.NullTime
	var lastRunID sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, cron_expr, run_request_json, enabled, last_run_id, last_run_at, created_at, updated_at
		FROM benchmark_schedules WHERE id = ?
	`, id).Scan(&sched.ID, &sched.Name, &sched.CronExpr, &reqJSON, &sched.Enabled,
		&lastRunID, &lastRunAt, &sched.CreatedAt, &sched.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(reqJSON), &sched.Request); err != nil {
		return nil, err
	}
	if lastRunAt.Valid {
		sched.LastRunAt = &lastRunAt.Time
	}
	sched.LastRunID = lastRunID.String
	return &sched, nil
}

// List returns all schedules.
func (s *ScheduleStore) List(ctx context.Context) ([]*Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, cron_expr, run_request_json, enabled, last_run_id, last_run_at, created_at, updated_at
		FROM benchmark_schedules ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schedules []*Schedule
	for rows.Next() {
		var sched Schedule
		var reqJSON string
		var lastRunAt sql.NullTime
		var lastRunID sql.NullString

		if err := rows.Scan(&sched.ID, &sched.Name, &sched.CronExpr, &reqJSON, &sched.Enabled,
			&lastRunID, &lastRunAt, &sched.CreatedAt, &sched.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(reqJSON), &sched.Request); err != nil {
			return nil, err
		}
		if lastRunAt.Valid {
			sched.LastRunAt = &lastRunAt.Time
		}
		sched.LastRunID = lastRunID.String
		schedules = append(schedules, &sched)
	}
	return schedules, rows.Err()
}

// Delete removes a schedule.
func (s *ScheduleStore) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM benchmark_schedules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("schedule not found")
	}
	return nil
}

// ListEnabled returns all enabled schedules.
func (s *ScheduleStore) ListEnabled(ctx context.Context) ([]*Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, cron_expr, run_request_json, enabled, last_run_id, last_run_at, created_at, updated_at
		FROM benchmark_schedules WHERE enabled = 1 ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schedules []*Schedule
	for rows.Next() {
		var sched Schedule
		var reqJSON string
		var lastRunAt sql.NullTime
		var lastRunID sql.NullString

		if err := rows.Scan(&sched.ID, &sched.Name, &sched.CronExpr, &reqJSON, &sched.Enabled,
			&lastRunID, &lastRunAt, &sched.CreatedAt, &sched.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(reqJSON), &sched.Request); err != nil {
			return nil, err
		}
		if lastRunAt.Valid {
			sched.LastRunAt = &lastRunAt.Time
		}
		sched.LastRunID = lastRunID.String
		schedules = append(schedules, &sched)
	}
	return schedules, rows.Err()
}

// Scheduler checks cron schedules and triggers benchmark runs.
type Scheduler struct {
	runner *Runner
	store  *ScheduleStore
	logger *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewScheduler creates a new benchmark scheduler.
func NewScheduler(runner *Runner, store *ScheduleStore, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		runner: runner,
		store:  store,
		logger: logger,
	}
}

// Start begins the scheduler's periodic check loop.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()

	go s.run(ctx)
	s.logger.Info("benchmark scheduler started")
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.logger.Info("benchmark scheduler stopped")
}

// GetStore returns the schedule store.
func (s *Scheduler) GetStore() *ScheduleStore {
	return s.store
}

func (s *Scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkSchedules(ctx)
		}
	}
}

func (s *Scheduler) checkSchedules(ctx context.Context) {
	schedules, err := s.store.ListEnabled(ctx)
	if err != nil {
		s.logger.Error("failed to list schedules", slog.String("error", err.Error()))
		return
	}

	now := time.Now()
	for _, sched := range schedules {
		if shouldRun(sched, now) {
			s.logger.Info("triggering scheduled benchmark",
				slog.String("schedule_id", sched.ID),
				slog.String("name", sched.Name))

			run, err := s.runner.StartRun(ctx, sched.Request)
			if err != nil {
				s.logger.Error("failed to start scheduled benchmark",
					slog.String("schedule_id", sched.ID),
					slog.String("error", err.Error()))
				continue
			}

			sched.LastRunID = run.ID
			nowT := time.Now()
			sched.LastRunAt = &nowT
			if err := s.store.Update(ctx, sched); err != nil {
				s.logger.Error("failed to update schedule after run",
					slog.String("schedule_id", sched.ID),
					slog.String("error", err.Error()))
			}
		}
	}
}

// shouldRun checks if a schedule should trigger based on its cron expression.
// Simplified cron: "minute hour day-of-month month day-of-week"
// Supports: *, specific values, */N (step values).
func shouldRun(sched *Schedule, now time.Time) bool {
	// Don't re-run within the same minute
	if sched.LastRunAt != nil {
		if now.Sub(*sched.LastRunAt) < 2*time.Minute {
			return false
		}
	}

	parts := strings.Fields(sched.CronExpr)
	if len(parts) != 5 {
		return false
	}

	return matchCronField(parts[0], now.Minute()) &&
		matchCronField(parts[1], now.Hour()) &&
		matchCronField(parts[2], now.Day()) &&
		matchCronField(parts[3], int(now.Month())) &&
		matchCronField(parts[4], int(now.Weekday()))
}

// matchCronField checks if a value matches a cron field.
// Supports: "*" (any), "N" (exact), "*/N" (divisible by N).
func matchCronField(field string, value int) bool {
	if field == "*" {
		return true
	}
	if strings.HasPrefix(field, "*/") {
		divisor, err := strconv.Atoi(strings.TrimPrefix(field, "*/"))
		if err != nil || divisor <= 0 {
			return false
		}
		return value%divisor == 0
	}
	expected, err := strconv.Atoi(field)
	if err != nil {
		return false
	}
	return value == expected
}
