package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// BenchmarkResult represents a single benchmark run result
type BenchmarkResult struct {
	ID                     string    `json:"id"`
	RunDate                time.Time `json:"run_date"`
	ModelID                string    `json:"model_id"`
	ModelParamsB           float64   `json:"model_params_b"`
	GPUType                string    `json:"gpu_type"`
	GPUVramGB              int       `json:"gpu_vram_gb"`
	Provider               string    `json:"provider"`
	PricePerHour           float64   `json:"price_per_hour"`
	ThroughputTPS          float64   `json:"throughput_tps"`
	TTFTMS                 float64   `json:"ttft_ms"`
	MaxConcurrent          int       `json:"max_concurrent"`
	CostPer1kTokens        float64   `json:"cost_per_1k_tokens"`
	Status                 string    `json:"status"`
	FailedSteps            []string  `json:"failed_steps,omitempty"`
	VLLMVersion            string    `json:"vllm_version"`
	TestDurationSec        int       `json:"test_duration_sec"`
	AnsiblePlaybookVersion string    `json:"ansible_playbook_version"`
	SessionID              string    `json:"session_id,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
}

// BenchmarkStatus constants
const (
	BenchmarkStatusComplete = "complete"
	BenchmarkStatusPartial  = "partial"
	BenchmarkStatusFailed   = "failed"
	BenchmarkStatusRunning  = "running"
)

// BenchmarkStore handles benchmark result persistence
type BenchmarkStore struct {
	db *DB
}

// NewBenchmarkStore creates a new benchmark store
func NewBenchmarkStore(db *DB) *BenchmarkStore {
	return &BenchmarkStore{db: db}
}

// Create inserts a new benchmark result
func (s *BenchmarkStore) Create(ctx context.Context, result *BenchmarkResult) error {
	if result.ID == "" {
		result.ID = uuid.New().String()
	}
	if result.RunDate.IsZero() {
		result.RunDate = time.Now()
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now()
	}

	failedStepsJSON, err := json.Marshal(result.FailedSteps)
	if err != nil {
		return fmt.Errorf("failed to marshal failed_steps: %w", err)
	}

	query := `
		INSERT INTO benchmark_results (
			id, run_date, model_id, model_params_b,
			gpu_type, gpu_vram_gb, provider, price_per_hour,
			throughput_tps, ttft_ms, max_concurrent, cost_per_1k_tokens,
			status, failed_steps, vllm_version, test_duration_sec,
			ansible_playbook_version, session_id, created_at
		) VALUES (
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?
		)
	`

	_, err = s.db.ExecContext(ctx, query,
		result.ID, result.RunDate, result.ModelID, result.ModelParamsB,
		result.GPUType, result.GPUVramGB, result.Provider, result.PricePerHour,
		result.ThroughputTPS, result.TTFTMS, result.MaxConcurrent, result.CostPer1kTokens,
		result.Status, string(failedStepsJSON), result.VLLMVersion, result.TestDurationSec,
		result.AnsiblePlaybookVersion, result.SessionID, result.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create benchmark result: %w", err)
	}

	return nil
}

// Get retrieves a benchmark result by ID
func (s *BenchmarkStore) Get(ctx context.Context, id string) (*BenchmarkResult, error) {
	query := `
		SELECT
			id, run_date, model_id, model_params_b,
			gpu_type, gpu_vram_gb, provider, price_per_hour,
			throughput_tps, ttft_ms, max_concurrent, cost_per_1k_tokens,
			status, failed_steps, vllm_version, test_duration_sec,
			ansible_playbook_version, session_id, created_at
		FROM benchmark_results
		WHERE id = ?
	`

	result := &BenchmarkResult{}
	var failedStepsJSON string
	var sessionID sql.NullString

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&result.ID, &result.RunDate, &result.ModelID, &result.ModelParamsB,
		&result.GPUType, &result.GPUVramGB, &result.Provider, &result.PricePerHour,
		&result.ThroughputTPS, &result.TTFTMS, &result.MaxConcurrent, &result.CostPer1kTokens,
		&result.Status, &failedStepsJSON, &result.VLLMVersion, &result.TestDurationSec,
		&result.AnsiblePlaybookVersion, &sessionID, &result.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get benchmark result: %w", err)
	}

	if err := json.Unmarshal([]byte(failedStepsJSON), &result.FailedSteps); err != nil {
		result.FailedSteps = nil
	}
	result.SessionID = sessionID.String

	return result, nil
}

// Update updates an existing benchmark result
func (s *BenchmarkStore) Update(ctx context.Context, result *BenchmarkResult) error {
	failedStepsJSON, err := json.Marshal(result.FailedSteps)
	if err != nil {
		return fmt.Errorf("failed to marshal failed_steps: %w", err)
	}

	query := `
		UPDATE benchmark_results SET
			throughput_tps = ?,
			ttft_ms = ?,
			max_concurrent = ?,
			cost_per_1k_tokens = ?,
			status = ?,
			failed_steps = ?,
			vllm_version = ?,
			test_duration_sec = ?
		WHERE id = ?
	`

	res, err := s.db.ExecContext(ctx, query,
		result.ThroughputTPS,
		result.TTFTMS,
		result.MaxConcurrent,
		result.CostPer1kTokens,
		result.Status,
		string(failedStepsJSON),
		result.VLLMVersion,
		result.TestDurationSec,
		result.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update benchmark result: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}

	return nil
}

// BenchmarkFilter defines criteria for filtering benchmark results
type BenchmarkFilter struct {
	ModelID     string
	GPUType     string
	Provider    string
	Status      string
	MinDate     time.Time
	MaxDate     time.Time
	Limit       int
	OrderBy     string // "throughput", "latency", "cost", "date"
	OrderDesc   bool
}

// List returns benchmark results matching the filter
func (s *BenchmarkStore) List(ctx context.Context, filter BenchmarkFilter) ([]*BenchmarkResult, error) {
	query := `
		SELECT
			id, run_date, model_id, model_params_b,
			gpu_type, gpu_vram_gb, provider, price_per_hour,
			throughput_tps, ttft_ms, max_concurrent, cost_per_1k_tokens,
			status, failed_steps, vllm_version, test_duration_sec,
			ansible_playbook_version, session_id, created_at
		FROM benchmark_results
		WHERE 1=1
	`

	var args []interface{}

	if filter.ModelID != "" {
		query += " AND model_id = ?"
		args = append(args, filter.ModelID)
	}

	if filter.GPUType != "" {
		query += " AND gpu_type = ?"
		args = append(args, filter.GPUType)
	}

	if filter.Provider != "" {
		query += " AND provider = ?"
		args = append(args, filter.Provider)
	}

	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}

	if !filter.MinDate.IsZero() {
		query += " AND run_date >= ?"
		args = append(args, filter.MinDate)
	}

	if !filter.MaxDate.IsZero() {
		query += " AND run_date < ?"
		args = append(args, filter.MaxDate)
	}

	// Apply ordering
	orderColumn := "run_date"
	switch filter.OrderBy {
	case "throughput":
		orderColumn = "throughput_tps"
	case "latency":
		orderColumn = "ttft_ms"
	case "cost":
		orderColumn = "cost_per_1k_tokens"
	case "date":
		orderColumn = "run_date"
	}

	if filter.OrderDesc {
		query += fmt.Sprintf(" ORDER BY %s DESC", orderColumn)
	} else {
		query += fmt.Sprintf(" ORDER BY %s ASC", orderColumn)
	}

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list benchmark results: %w", err)
	}
	defer rows.Close()

	var results []*BenchmarkResult
	for rows.Next() {
		result := &BenchmarkResult{}
		var failedStepsJSON string
		var sessionID sql.NullString

		err := rows.Scan(
			&result.ID, &result.RunDate, &result.ModelID, &result.ModelParamsB,
			&result.GPUType, &result.GPUVramGB, &result.Provider, &result.PricePerHour,
			&result.ThroughputTPS, &result.TTFTMS, &result.MaxConcurrent, &result.CostPer1kTokens,
			&result.Status, &failedStepsJSON, &result.VLLMVersion, &result.TestDurationSec,
			&result.AnsiblePlaybookVersion, &sessionID, &result.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan benchmark result: %w", err)
		}

		if err := json.Unmarshal([]byte(failedStepsJSON), &result.FailedSteps); err != nil {
			result.FailedSteps = nil
		}
		result.SessionID = sessionID.String

		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating benchmark results: %w", err)
	}

	return results, nil
}

// GetLatestForModelGPU returns the most recent benchmark for a model/GPU combination
func (s *BenchmarkStore) GetLatestForModelGPU(ctx context.Context, modelID, gpuType string) (*BenchmarkResult, error) {
	results, err := s.List(ctx, BenchmarkFilter{
		ModelID:   modelID,
		GPUType:   gpuType,
		Status:    BenchmarkStatusComplete,
		OrderBy:   "date",
		OrderDesc: true,
		Limit:     1,
	})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, ErrNotFound
	}
	return results[0], nil
}

// GetBestForModel returns the best benchmark results for a model, ordered by the specified metric
func (s *BenchmarkStore) GetBestForModel(ctx context.Context, modelID string, optimizeFor string, limit int) ([]*BenchmarkResult, error) {
	orderBy := "cost"
	orderDesc := false

	switch optimizeFor {
	case "cost":
		orderBy = "cost"
		orderDesc = false // Lower cost is better
	case "throughput":
		orderBy = "throughput"
		orderDesc = true // Higher throughput is better
	case "latency":
		orderBy = "latency"
		orderDesc = false // Lower latency is better
	}

	return s.List(ctx, BenchmarkFilter{
		ModelID:   modelID,
		Status:    BenchmarkStatusComplete,
		OrderBy:   orderBy,
		OrderDesc: orderDesc,
		Limit:     limit,
	})
}

// GetHistoryForModel returns benchmark history for a model
func (s *BenchmarkStore) GetHistoryForModel(ctx context.Context, modelID string, limit int) ([]*BenchmarkResult, error) {
	return s.List(ctx, BenchmarkFilter{
		ModelID:   modelID,
		OrderBy:   "date",
		OrderDesc: true,
		Limit:     limit,
	})
}

// Delete removes a benchmark result by ID
func (s *BenchmarkStore) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM benchmark_results WHERE id = ?`

	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete benchmark result: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}

	return nil
}

// GetModelStats returns aggregate statistics for a model across all GPUs
type ModelStats struct {
	ModelID         string  `json:"model_id"`
	TotalRuns       int     `json:"total_runs"`
	SuccessfulRuns  int     `json:"successful_runs"`
	AvgThroughput   float64 `json:"avg_throughput"`
	AvgTTFT         float64 `json:"avg_ttft"`
	AvgCostPer1k    float64 `json:"avg_cost_per_1k"`
	BestGPUBySpeed  string  `json:"best_gpu_by_speed"`
	BestGPUByCost   string  `json:"best_gpu_by_cost"`
}

func (s *BenchmarkStore) GetModelStats(ctx context.Context, modelID string) (*ModelStats, error) {
	query := `
		SELECT
			COUNT(*) as total_runs,
			SUM(CASE WHEN status = 'complete' THEN 1 ELSE 0 END) as successful_runs,
			AVG(CASE WHEN status = 'complete' THEN throughput_tps ELSE NULL END) as avg_throughput,
			AVG(CASE WHEN status = 'complete' THEN ttft_ms ELSE NULL END) as avg_ttft,
			AVG(CASE WHEN status = 'complete' THEN cost_per_1k_tokens ELSE NULL END) as avg_cost
		FROM benchmark_results
		WHERE model_id = ?
	`

	stats := &ModelStats{ModelID: modelID}
	var avgThroughput, avgTTFT, avgCost sql.NullFloat64

	err := s.db.QueryRowContext(ctx, query, modelID).Scan(
		&stats.TotalRuns,
		&stats.SuccessfulRuns,
		&avgThroughput,
		&avgTTFT,
		&avgCost,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get model stats: %w", err)
	}

	stats.AvgThroughput = avgThroughput.Float64
	stats.AvgTTFT = avgTTFT.Float64
	stats.AvgCostPer1k = avgCost.Float64

	// Get best GPU by throughput
	bestBySpeed, _ := s.List(ctx, BenchmarkFilter{
		ModelID:   modelID,
		Status:    BenchmarkStatusComplete,
		OrderBy:   "throughput",
		OrderDesc: true,
		Limit:     1,
	})
	if len(bestBySpeed) > 0 {
		stats.BestGPUBySpeed = bestBySpeed[0].GPUType
	}

	// Get best GPU by cost
	bestByCost, _ := s.List(ctx, BenchmarkFilter{
		ModelID:   modelID,
		Status:    BenchmarkStatusComplete,
		OrderBy:   "cost",
		OrderDesc: false,
		Limit:     1,
	})
	if len(bestByCost) > 0 {
		stats.BestGPUByCost = bestByCost[0].GPUType
	}

	return stats, nil
}

// Migration for benchmark_results table
const migrationBenchmarks = `
CREATE TABLE IF NOT EXISTS benchmark_results (
	id TEXT PRIMARY KEY,
	run_date DATETIME NOT NULL,
	model_id TEXT NOT NULL,
	model_params_b REAL NOT NULL,
	gpu_type TEXT NOT NULL,
	gpu_vram_gb INTEGER NOT NULL,
	provider TEXT NOT NULL,
	price_per_hour REAL NOT NULL,

	-- Metrics
	throughput_tps REAL NOT NULL DEFAULT 0,
	ttft_ms REAL NOT NULL DEFAULT 0,
	max_concurrent INTEGER NOT NULL DEFAULT 0,

	-- Computed
	cost_per_1k_tokens REAL NOT NULL DEFAULT 0,

	-- Status
	status TEXT NOT NULL DEFAULT 'running',
	failed_steps TEXT NOT NULL DEFAULT '[]',

	-- Metadata
	vllm_version TEXT NOT NULL DEFAULT '',
	test_duration_sec INTEGER NOT NULL DEFAULT 0,
	ansible_playbook_version TEXT NOT NULL DEFAULT '',
	session_id TEXT,

	-- Timestamps
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_benchmark_model_id ON benchmark_results(model_id);
CREATE INDEX IF NOT EXISTS idx_benchmark_gpu_type ON benchmark_results(gpu_type);
CREATE INDEX IF NOT EXISTS idx_benchmark_provider ON benchmark_results(provider);
CREATE INDEX IF NOT EXISTS idx_benchmark_status ON benchmark_results(status);
CREATE INDEX IF NOT EXISTS idx_benchmark_run_date ON benchmark_results(run_date);
CREATE INDEX IF NOT EXISTS idx_benchmark_model_gpu ON benchmark_results(model_id, gpu_type);
`
