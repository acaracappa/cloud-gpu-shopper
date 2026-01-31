package reports

import (
	"context"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/storage"
)

// Storage provides a high-level interface for persisting and querying benchmark results
type Storage struct {
	store *storage.BenchmarkStore
}

// NewStorage creates a new storage wrapper
func NewStorage(db *storage.DB) *Storage {
	return &Storage{
		store: storage.NewBenchmarkStore(db),
	}
}

// Store returns the underlying BenchmarkStore for direct access
func (s *Storage) Store() *storage.BenchmarkStore {
	return s.store
}

// SaveResult persists a benchmark result
func (s *Storage) SaveResult(ctx context.Context, result *storage.BenchmarkResult) error {
	return s.store.Create(ctx, result)
}

// UpdateResult updates an existing benchmark result
func (s *Storage) UpdateResult(ctx context.Context, result *storage.BenchmarkResult) error {
	return s.store.Update(ctx, result)
}

// GetResult retrieves a benchmark result by ID
func (s *Storage) GetResult(ctx context.Context, id string) (*storage.BenchmarkResult, error) {
	return s.store.Get(ctx, id)
}

// DeleteResult removes a benchmark result
func (s *Storage) DeleteResult(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// ListResults returns all benchmark results matching the filter
func (s *Storage) ListResults(ctx context.Context, filter storage.BenchmarkFilter) ([]*storage.BenchmarkResult, error) {
	return s.store.List(ctx, filter)
}

// GetModelHistory returns benchmark history for a specific model
func (s *Storage) GetModelHistory(ctx context.Context, modelID string, limit int) ([]*storage.BenchmarkResult, error) {
	return s.store.GetHistoryForModel(ctx, modelID, limit)
}

// GetGPUHistory returns benchmark history for a specific GPU type
func (s *Storage) GetGPUHistory(ctx context.Context, gpuType string, limit int) ([]*storage.BenchmarkResult, error) {
	return s.store.List(ctx, storage.BenchmarkFilter{
		GPUType:   gpuType,
		OrderBy:   "date",
		OrderDesc: true,
		Limit:     limit,
	})
}

// GetRecentResults returns the most recent benchmark results
func (s *Storage) GetRecentResults(ctx context.Context, limit int) ([]*storage.BenchmarkResult, error) {
	return s.store.List(ctx, storage.BenchmarkFilter{
		OrderBy:   "date",
		OrderDesc: true,
		Limit:     limit,
	})
}

// GetSuccessfulResults returns only completed benchmark results
func (s *Storage) GetSuccessfulResults(ctx context.Context, modelID string, limit int) ([]*storage.BenchmarkResult, error) {
	return s.store.List(ctx, storage.BenchmarkFilter{
		ModelID:   modelID,
		Status:    storage.BenchmarkStatusComplete,
		OrderBy:   "date",
		OrderDesc: true,
		Limit:     limit,
	})
}

// GetModelStats returns aggregate statistics for a model
func (s *Storage) GetModelStats(ctx context.Context, modelID string) (*storage.ModelStats, error) {
	return s.store.GetModelStats(ctx, modelID)
}

// GetBestByMetric returns the best benchmark results for a model sorted by the given metric
func (s *Storage) GetBestByMetric(ctx context.Context, modelID string, metric string, limit int) ([]*storage.BenchmarkResult, error) {
	return s.store.GetBestForModel(ctx, modelID, metric, limit)
}

// GetLatestForModelGPU returns the most recent benchmark for a model/GPU combination
func (s *Storage) GetLatestForModelGPU(ctx context.Context, modelID, gpuType string) (*storage.BenchmarkResult, error) {
	return s.store.GetLatestForModelGPU(ctx, modelID, gpuType)
}

// GetResultsInDateRange returns results within a date range
func (s *Storage) GetResultsInDateRange(ctx context.Context, start, end time.Time) ([]*storage.BenchmarkResult, error) {
	return s.store.List(ctx, storage.BenchmarkFilter{
		MinDate:   start,
		MaxDate:   end,
		OrderBy:   "date",
		OrderDesc: true,
	})
}

// GetProviderResults returns results for a specific provider
func (s *Storage) GetProviderResults(ctx context.Context, provider string, limit int) ([]*storage.BenchmarkResult, error) {
	return s.store.List(ctx, storage.BenchmarkFilter{
		Provider:  provider,
		OrderBy:   "date",
		OrderDesc: true,
		Limit:     limit,
	})
}

// CountResults returns the total number of benchmark results
func (s *Storage) CountResults(ctx context.Context) (int, error) {
	results, err := s.store.List(ctx, storage.BenchmarkFilter{})
	if err != nil {
		return 0, err
	}
	return len(results), nil
}

// CountSuccessfulResults returns the number of successful benchmark results
func (s *Storage) CountSuccessfulResults(ctx context.Context) (int, error) {
	results, err := s.store.List(ctx, storage.BenchmarkFilter{
		Status: storage.BenchmarkStatusComplete,
	})
	if err != nil {
		return 0, err
	}
	return len(results), nil
}

// GetUniqueModels returns all unique model IDs in the database
func (s *Storage) GetUniqueModels(ctx context.Context) ([]string, error) {
	results, err := s.store.List(ctx, storage.BenchmarkFilter{})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var models []string
	for _, r := range results {
		if !seen[r.ModelID] {
			seen[r.ModelID] = true
			models = append(models, r.ModelID)
		}
	}
	return models, nil
}

// GetUniqueGPUs returns all unique GPU types in the database
func (s *Storage) GetUniqueGPUs(ctx context.Context) ([]string, error) {
	results, err := s.store.List(ctx, storage.BenchmarkFilter{})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var gpus []string
	for _, r := range results {
		if !seen[r.GPUType] {
			seen[r.GPUType] = true
			gpus = append(gpus, r.GPUType)
		}
	}
	return gpus, nil
}

// GetUniqueProviders returns all unique providers in the database
func (s *Storage) GetUniqueProviders(ctx context.Context) ([]string, error) {
	results, err := s.store.List(ctx, storage.BenchmarkFilter{})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var providers []string
	for _, r := range results {
		if !seen[r.Provider] {
			seen[r.Provider] = true
			providers = append(providers, r.Provider)
		}
	}
	return providers, nil
}
