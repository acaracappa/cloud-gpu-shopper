package recommend

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/storage"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/test/benchmark/models"
)

// OptimizationTarget specifies what to optimize for
type OptimizationTarget string

const (
	OptimizeCost       OptimizationTarget = "cost"
	OptimizeThroughput OptimizationTarget = "throughput"
	OptimizeLatency    OptimizationTarget = "latency"
)

// Recommendation represents a GPU recommendation for a model
type Recommendation struct {
	GPUType         string    `json:"gpu_type"`
	Provider        string    `json:"provider"`
	PricePerHour    float64   `json:"price_per_hour"`
	ThroughputTPS   float64   `json:"throughput_tps"`
	TTFTMS          float64   `json:"ttft_ms"`
	MaxConcurrent   int       `json:"max_concurrent"`
	CostPer1kTokens float64   `json:"cost_per_1k_tokens"`
	Confidence      float64   `json:"confidence"`
	SampleSize      int       `json:"sample_size"`
	LastTested      time.Time `json:"last_tested"`
	Rank            int       `json:"rank"`
	Score           float64   `json:"score"`
}

// RecommendationResult contains the recommendation output
type RecommendationResult struct {
	ModelID         string            `json:"model_id"`
	ModelName       string            `json:"model_name"`
	OptimizeFor     OptimizationTarget `json:"optimize_for"`
	Recommendations []Recommendation  `json:"recommendations"`
	TotalCandidates int               `json:"total_candidates"`
	DataAge         time.Duration     `json:"data_age"`
}

// Engine provides GPU recommendations based on benchmark data
type Engine struct {
	store   *storage.BenchmarkStore
	catalog *models.Catalog
}

// NewEngine creates a new recommendation engine
func NewEngine(store *storage.BenchmarkStore) *Engine {
	return &Engine{
		store:   store,
		catalog: models.NewCatalog(),
	}
}

// Recommend returns GPU recommendations for a model
func (e *Engine) Recommend(ctx context.Context, modelID string, optimizeFor OptimizationTarget, limit int) (*RecommendationResult, error) {
	if limit <= 0 {
		limit = 3
	}

	// Validate model
	model, ok := e.catalog.GetModel(modelID)
	if !ok {
		return nil, fmt.Errorf("unknown model: %s", modelID)
	}

	// Get successful benchmarks for this model
	results, err := e.store.List(ctx, storage.BenchmarkFilter{
		ModelID: modelID,
		Status:  storage.BenchmarkStatusComplete,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query benchmarks: %w", err)
	}

	if len(results) == 0 {
		return &RecommendationResult{
			ModelID:         modelID,
			ModelName:       model.Name,
			OptimizeFor:     optimizeFor,
			Recommendations: []Recommendation{},
			TotalCandidates: 0,
		}, nil
	}

	// Group by GPU type, keeping the best/most recent for each
	byGPU := make(map[string][]*storage.BenchmarkResult)
	for _, r := range results {
		byGPU[r.GPUType] = append(byGPU[r.GPUType], r)
	}

	// Create recommendations
	var recommendations []Recommendation
	var oldestData time.Time

	for gpuType, gpuResults := range byGPU {
		// Use the best result for this GPU (most recent among successful ones)
		best := selectBestResult(gpuResults, optimizeFor)
		if best == nil {
			continue
		}

		sampleSize := len(gpuResults)
		confidence := calculateConfidence(best, sampleSize)

		if oldestData.IsZero() || best.RunDate.Before(oldestData) {
			oldestData = best.RunDate
		}

		recommendations = append(recommendations, Recommendation{
			GPUType:         gpuType,
			Provider:        best.Provider,
			PricePerHour:    best.PricePerHour,
			ThroughputTPS:   best.ThroughputTPS,
			TTFTMS:          best.TTFTMS,
			MaxConcurrent:   best.MaxConcurrent,
			CostPer1kTokens: best.CostPer1kTokens,
			Confidence:      confidence,
			SampleSize:      sampleSize,
			LastTested:      best.RunDate,
		})
	}

	// Score and rank recommendations
	scoreRecommendations(recommendations, optimizeFor)

	// Sort by score (descending)
	sort.Slice(recommendations, func(i, j int) bool {
		return recommendations[i].Score > recommendations[j].Score
	})

	// Assign ranks and limit
	for i := range recommendations {
		recommendations[i].Rank = i + 1
	}

	if len(recommendations) > limit {
		recommendations = recommendations[:limit]
	}

	return &RecommendationResult{
		ModelID:         modelID,
		ModelName:       model.Name,
		OptimizeFor:     optimizeFor,
		Recommendations: recommendations,
		TotalCandidates: len(byGPU),
		DataAge:         time.Since(oldestData),
	}, nil
}

// RecommendAll returns recommendations for all models
func (e *Engine) RecommendAll(ctx context.Context, optimizeFor OptimizationTarget, limit int) (map[string]*RecommendationResult, error) {
	results := make(map[string]*RecommendationResult)

	for _, modelID := range e.catalog.ModelList() {
		rec, err := e.Recommend(ctx, modelID, optimizeFor, limit)
		if err != nil {
			continue
		}
		if len(rec.Recommendations) > 0 {
			results[modelID] = rec
		}
	}

	return results, nil
}

// GetDataQuality returns information about the quality of benchmark data
func (e *Engine) GetDataQuality(ctx context.Context, modelID string) (*DataQuality, error) {
	results, err := e.store.List(ctx, storage.BenchmarkFilter{
		ModelID: modelID,
	})
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return &DataQuality{
			ModelID:     modelID,
			HasData:     false,
			IsStale:     true,
			Confidence:  0,
		}, nil
	}

	var successCount int
	var newestDate time.Time
	gpus := make(map[string]bool)
	providers := make(map[string]bool)

	for _, r := range results {
		if r.Status == storage.BenchmarkStatusComplete {
			successCount++
		}
		if newestDate.IsZero() || r.RunDate.After(newestDate) {
			newestDate = r.RunDate
		}
		gpus[r.GPUType] = true
		providers[r.Provider] = true
	}

	dataAge := time.Since(newestDate)
	isStale := dataAge > 90*24*time.Hour // More than 90 days old

	// Calculate confidence
	successRate := float64(successCount) / float64(len(results))
	recencyFactor := 1.0 - (dataAge.Hours() / (90 * 24)) // Decay over 90 days
	if recencyFactor < 0.1 {
		recencyFactor = 0.1
	}
	coverageFactor := float64(len(gpus)) / 5.0 // Assume 5 GPUs is good coverage
	if coverageFactor > 1.0 {
		coverageFactor = 1.0
	}

	confidence := (successRate*0.4 + recencyFactor*0.4 + coverageFactor*0.2) * 100

	return &DataQuality{
		ModelID:         modelID,
		HasData:         true,
		TotalRuns:       len(results),
		SuccessfulRuns:  successCount,
		SuccessRate:     successRate * 100,
		GPUsCovered:     len(gpus),
		ProvidersCovered: len(providers),
		NewestData:      newestDate,
		DataAge:         dataAge,
		IsStale:         isStale,
		Confidence:      confidence,
	}, nil
}

// DataQuality represents the quality of benchmark data for a model
type DataQuality struct {
	ModelID          string        `json:"model_id"`
	HasData          bool          `json:"has_data"`
	TotalRuns        int           `json:"total_runs"`
	SuccessfulRuns   int           `json:"successful_runs"`
	SuccessRate      float64       `json:"success_rate"`
	GPUsCovered      int           `json:"gpus_covered"`
	ProvidersCovered int           `json:"providers_covered"`
	NewestData       time.Time     `json:"newest_data"`
	DataAge          time.Duration `json:"data_age"`
	IsStale          bool          `json:"is_stale"`
	Confidence       float64       `json:"confidence"`
}

// selectBestResult picks the best result from a slice based on optimization target
func selectBestResult(results []*storage.BenchmarkResult, optimizeFor OptimizationTarget) *storage.BenchmarkResult {
	if len(results) == 0 {
		return nil
	}

	// Sort by the optimization metric
	sort.Slice(results, func(i, j int) bool {
		switch optimizeFor {
		case OptimizeCost:
			// Lower cost is better
			if results[i].CostPer1kTokens == 0 {
				return false
			}
			if results[j].CostPer1kTokens == 0 {
				return true
			}
			return results[i].CostPer1kTokens < results[j].CostPer1kTokens
		case OptimizeThroughput:
			// Higher throughput is better
			return results[i].ThroughputTPS > results[j].ThroughputTPS
		case OptimizeLatency:
			// Lower latency is better
			if results[i].TTFTMS == 0 {
				return false
			}
			if results[j].TTFTMS == 0 {
				return true
			}
			return results[i].TTFTMS < results[j].TTFTMS
		default:
			return results[i].RunDate.After(results[j].RunDate)
		}
	})

	return results[0]
}

// calculateConfidence computes a confidence score for a recommendation
func calculateConfidence(result *storage.BenchmarkResult, sampleSize int) float64 {
	// Factors:
	// 1. Recency (40%) - more recent data is more reliable
	// 2. Sample size (30%) - more samples means more reliable
	// 3. Data completeness (30%) - all metrics present

	// Recency factor
	daysSinceTest := time.Since(result.RunDate).Hours() / 24
	recencyFactor := 1.0 - (daysSinceTest / 90.0)
	if recencyFactor < 0.1 {
		recencyFactor = 0.1
	}

	// Sample size factor
	sizeFactor := float64(sampleSize) / 5.0
	if sizeFactor > 1.0 {
		sizeFactor = 1.0
	}

	// Completeness factor
	completeness := 0.0
	if result.ThroughputTPS > 0 {
		completeness += 0.4
	}
	if result.TTFTMS > 0 {
		completeness += 0.3
	}
	if result.MaxConcurrent > 0 {
		completeness += 0.3
	}

	return (recencyFactor*0.4 + sizeFactor*0.3 + completeness*0.3) * 100
}

// scoreRecommendations assigns scores to recommendations based on the optimization target
func scoreRecommendations(recommendations []Recommendation, optimizeFor OptimizationTarget) {
	if len(recommendations) == 0 {
		return
	}

	// Find min/max values for normalization
	var minCost, maxCost float64 = 1e9, 0
	var minThroughput, maxThroughput float64 = 1e9, 0
	var minLatency, maxLatency float64 = 1e9, 0

	for _, r := range recommendations {
		if r.CostPer1kTokens > 0 {
			if r.CostPer1kTokens < minCost {
				minCost = r.CostPer1kTokens
			}
			if r.CostPer1kTokens > maxCost {
				maxCost = r.CostPer1kTokens
			}
		}
		if r.ThroughputTPS > maxThroughput {
			maxThroughput = r.ThroughputTPS
		}
		if r.ThroughputTPS < minThroughput && r.ThroughputTPS > 0 {
			minThroughput = r.ThroughputTPS
		}
		if r.TTFTMS > 0 {
			if r.TTFTMS < minLatency {
				minLatency = r.TTFTMS
			}
			if r.TTFTMS > maxLatency {
				maxLatency = r.TTFTMS
			}
		}
	}

	// Score each recommendation
	for i := range recommendations {
		r := &recommendations[i]

		var primaryScore float64
		switch optimizeFor {
		case OptimizeCost:
			if maxCost > minCost && r.CostPer1kTokens > 0 {
				primaryScore = 1.0 - (r.CostPer1kTokens-minCost)/(maxCost-minCost)
			}
		case OptimizeThroughput:
			if maxThroughput > minThroughput {
				primaryScore = (r.ThroughputTPS - minThroughput) / (maxThroughput - minThroughput)
			}
		case OptimizeLatency:
			if maxLatency > minLatency && r.TTFTMS > 0 {
				primaryScore = 1.0 - (r.TTFTMS-minLatency)/(maxLatency-minLatency)
			}
		}

		// Combine primary score with confidence
		r.Score = primaryScore*0.7 + (r.Confidence/100)*0.3
	}
}

// FormatRecommendation formats a recommendation as a human-readable string
func FormatRecommendation(r *Recommendation) string {
	return fmt.Sprintf("%s (%s) - $%.2f/hr, %.0f tok/s, %.0fms TTFT, $%.4f/1k tokens (confidence: %.0f%%)",
		r.GPUType, r.Provider, r.PricePerHour, r.ThroughputTPS, r.TTFTMS, r.CostPer1kTokens, r.Confidence)
}
