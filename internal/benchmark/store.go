package benchmark

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Store provides persistence for benchmark results.
type Store struct {
	db *sql.DB
}

// NewStore creates a new benchmark store.
func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate benchmark tables: %w", err)
	}
	return s, nil
}

// migrate creates the benchmark tables if they don't exist.
func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS benchmarks (
			id TEXT PRIMARY KEY,
			timestamp DATETIME NOT NULL,

			-- Hardware
			gpu_name TEXT NOT NULL,
			gpu_memory_mib INTEGER NOT NULL,
			gpu_count INTEGER NOT NULL DEFAULT 1,
			driver_version TEXT,
			cuda_version TEXT,
			cpu_model TEXT,
			cpu_cores INTEGER,
			ram_gib INTEGER,

			-- Model
			model_name TEXT NOT NULL,
			model_family TEXT,
			parameter_count TEXT,
			quantization TEXT,
			model_size_gb REAL,
			runtime TEXT NOT NULL,
			runtime_version TEXT,

			-- Test config
			duration_minutes INTEGER NOT NULL,
			max_tokens INTEGER,
			concurrent_reqs INTEGER DEFAULT 1,

			-- Results
			total_requests INTEGER NOT NULL,
			total_tokens INTEGER NOT NULL,
			total_errors INTEGER NOT NULL DEFAULT 0,
			duration_seconds REAL NOT NULL,
			avg_tokens_per_second REAL NOT NULL,
			min_tokens_per_second REAL,
			max_tokens_per_second REAL,
			p50_tokens_per_second REAL,
			p95_tokens_per_second REAL,
			p99_tokens_per_second REAL,
			avg_latency_ms REAL,
			p95_latency_ms REAL,
			requests_per_minute REAL,

			-- GPU stats
			avg_gpu_util REAL,
			max_gpu_util REAL,
			avg_gpu_temp REAL,
			max_gpu_temp REAL,
			avg_power_draw REAL,
			max_memory_used_mib INTEGER,

			-- Provider info
			provider TEXT,
			location TEXT,
			price_per_hour REAL,

			-- Full JSON for detailed data
			full_result_json TEXT,

			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_benchmarks_model ON benchmarks(model_name);
		CREATE INDEX IF NOT EXISTS idx_benchmarks_gpu ON benchmarks(gpu_name);
		CREATE INDEX IF NOT EXISTS idx_benchmarks_timestamp ON benchmarks(timestamp);
	`)
	return err
}

// Save stores a benchmark result.
func (s *Store) Save(ctx context.Context, result *BenchmarkResult) error {
	if result.ID == "" {
		result.ID = uuid.New().String()
	}
	if result.Timestamp.IsZero() {
		result.Timestamp = time.Now()
	}

	fullJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO benchmarks (
			id, timestamp,
			gpu_name, gpu_memory_mib, gpu_count, driver_version, cuda_version,
			cpu_model, cpu_cores, ram_gib,
			model_name, model_family, parameter_count, quantization, model_size_gb,
			runtime, runtime_version,
			duration_minutes, max_tokens, concurrent_reqs,
			total_requests, total_tokens, total_errors, duration_seconds,
			avg_tokens_per_second, min_tokens_per_second, max_tokens_per_second,
			p50_tokens_per_second, p95_tokens_per_second, p99_tokens_per_second,
			avg_latency_ms, p95_latency_ms, requests_per_minute,
			avg_gpu_util, max_gpu_util, avg_gpu_temp, max_gpu_temp,
			avg_power_draw, max_memory_used_mib,
			provider, location, price_per_hour,
			full_result_json
		) VALUES (
			?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?,
			?, ?, ?,
			?
		)
	`,
		result.ID, result.Timestamp,
		result.Hardware.GPUName, result.Hardware.GPUMemoryMiB, result.Hardware.GPUCount,
		result.Hardware.DriverVersion, result.Hardware.CUDAVersion,
		result.Hardware.CPUModel, result.Hardware.CPUCores, result.Hardware.RAMGiB,
		result.Model.Name, result.Model.Family, result.Model.ParameterCount,
		result.Model.Quantization, result.Model.SizeGB,
		result.Model.Runtime, result.Model.RuntimeVersion,
		result.TestConfig.DurationMinutes, result.TestConfig.MaxTokens, result.TestConfig.ConcurrentReqs,
		result.Results.TotalRequests, result.Results.TotalTokens, result.Results.TotalErrors,
		result.Results.DurationSeconds,
		result.Results.AvgTokensPerSecond, result.Results.MinTokensPerSecond, result.Results.MaxTokensPerSecond,
		result.Results.P50TokensPerSecond, result.Results.P95TokensPerSecond, result.Results.P99TokensPerSecond,
		result.Results.AvgLatencyMs, result.Results.P95LatencyMs, result.Results.RequestsPerMinute,
		result.GPUStats.AvgUtilizationPct, result.GPUStats.MaxUtilizationPct,
		result.GPUStats.AvgTemperatureC, result.GPUStats.MaxTemperatureC,
		result.GPUStats.AvgPowerDrawW, result.GPUStats.MaxMemoryUsedMiB,
		result.Provider, result.Location, result.PricePerHour,
		string(fullJSON),
	)
	return err
}

// Get retrieves a benchmark by ID.
func (s *Store) Get(ctx context.Context, id string) (*BenchmarkResult, error) {
	var fullJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT full_result_json FROM benchmarks WHERE id = ?
	`, id).Scan(&fullJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var result BenchmarkResult
	if err := json.Unmarshal([]byte(fullJSON), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListByModel returns all benchmarks for a specific model.
func (s *Store) ListByModel(ctx context.Context, modelName string) ([]*BenchmarkResult, error) {
	return s.query(ctx, `
		SELECT full_result_json FROM benchmarks
		WHERE model_name = ?
		ORDER BY timestamp DESC
	`, modelName)
}

// ListByGPU returns all benchmarks for a specific GPU.
func (s *Store) ListByGPU(ctx context.Context, gpuName string) ([]*BenchmarkResult, error) {
	return s.query(ctx, `
		SELECT full_result_json FROM benchmarks
		WHERE gpu_name LIKE ?
		ORDER BY timestamp DESC
	`, "%"+gpuName+"%")
}

// ListRecent returns the most recent benchmarks.
func (s *Store) ListRecent(ctx context.Context, limit int) ([]*BenchmarkResult, error) {
	return s.query(ctx, `
		SELECT full_result_json FROM benchmarks
		ORDER BY timestamp DESC
		LIMIT ?
	`, limit)
}

// GetBestForModel returns the best performing benchmark for a model.
func (s *Store) GetBestForModel(ctx context.Context, modelName string) (*BenchmarkResult, error) {
	results, err := s.query(ctx, `
		SELECT full_result_json FROM benchmarks
		WHERE model_name = ?
		ORDER BY avg_tokens_per_second DESC
		LIMIT 1
	`, modelName)
	if err != nil || len(results) == 0 {
		return nil, err
	}
	return results[0], nil
}

// GetCheapestForModel returns the most cost-effective benchmark for a model.
func (s *Store) GetCheapestForModel(ctx context.Context, modelName string, minTPS float64) (*BenchmarkResult, error) {
	results, err := s.query(ctx, `
		SELECT full_result_json FROM benchmarks
		WHERE model_name = ? AND avg_tokens_per_second >= ? AND price_per_hour > 0
		ORDER BY (avg_tokens_per_second / price_per_hour) DESC
		LIMIT 1
	`, modelName, minTPS)
	if err != nil || len(results) == 0 {
		return nil, err
	}
	return results[0], nil
}

// query is a helper to run a query and parse results.
func (s *Store) query(ctx context.Context, query string, args ...interface{}) ([]*BenchmarkResult, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*BenchmarkResult
	for rows.Next() {
		var fullJSON string
		if err := rows.Scan(&fullJSON); err != nil {
			return nil, err
		}
		var result BenchmarkResult
		if err := json.Unmarshal([]byte(fullJSON), &result); err != nil {
			return nil, err
		}
		results = append(results, &result)
	}
	return results, rows.Err()
}

// GetModelRecommendations returns hardware recommendations for a model based on benchmarks.
func (s *Store) GetModelRecommendations(ctx context.Context, modelName string) ([]HardwareRecommendation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			gpu_name,
			gpu_memory_mib,
			AVG(avg_tokens_per_second) as avg_tps,
			AVG(price_per_hour) as avg_price,
			COUNT(*) as sample_count
		FROM benchmarks
		WHERE model_name = ? AND total_errors < total_requests * 0.1
		GROUP BY gpu_name
		ORDER BY avg_tps DESC
	`, modelName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var recs []HardwareRecommendation
	for rows.Next() {
		var gpuName string
		var gpuMemory int
		var avgTPS, avgPrice float64
		var sampleCount int
		if err := rows.Scan(&gpuName, &gpuMemory, &avgTPS, &avgPrice, &sampleCount); err != nil {
			return nil, err
		}
		recs = append(recs, HardwareRecommendation{
			Model:           modelName,
			MinVRAMGiB:      gpuMemory / 1024,
			RecommendedGPUs: []string{gpuName},
			ExpectedTPS:     avgTPS,
			EstimatedCost:   avgPrice,
			Notes:           fmt.Sprintf("Based on %d benchmark(s)", sampleCount),
		})
	}
	return recs, rows.Err()
}
