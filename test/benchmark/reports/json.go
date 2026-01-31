package reports

import (
	"encoding/json"
	"os"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/storage"
)

// JSONReport represents a complete benchmark report in JSON format
type JSONReport struct {
	GeneratedAt    time.Time            `json:"generated_at"`
	ReportVersion  string               `json:"report_version"`
	Summary        *ReportSummary       `json:"summary"`
	Recommendations []Recommendation    `json:"recommendations,omitempty"`
	Results        []*storage.BenchmarkResult `json:"results"`
	ModelStats     map[string]*storage.ModelStats `json:"model_stats,omitempty"`
}

// ReportSummary contains aggregate statistics
type ReportSummary struct {
	TotalRuns         int     `json:"total_runs"`
	SuccessfulRuns    int     `json:"successful_runs"`
	PartialRuns       int     `json:"partial_runs"`
	FailedRuns        int     `json:"failed_runs"`
	ModelsTestad      int     `json:"models_tested"`
	GPUsTestad        int     `json:"gpus_tested"`
	ProvidersUsed     int     `json:"providers_used"`
	TotalCost         float64 `json:"total_cost"`
	TotalDurationMins float64 `json:"total_duration_mins"`
	DateRange         struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	} `json:"date_range"`
}

// Recommendation represents a GPU recommendation for a model
type Recommendation struct {
	ModelID      string  `json:"model_id"`
	ModelName    string  `json:"model_name"`
	OptimizeFor  string  `json:"optimize_for"`
	GPUType      string  `json:"gpu_type"`
	Provider     string  `json:"provider"`
	PricePerHour float64 `json:"price_per_hour"`
	ThroughputTPS float64 `json:"throughput_tps"`
	TTFTMS       float64 `json:"ttft_ms"`
	MaxConcurrent int    `json:"max_concurrent"`
	CostPer1kTokens float64 `json:"cost_per_1k_tokens"`
	Confidence   float64 `json:"confidence"`
	SampleSize   int     `json:"sample_size"`
	LastTested   time.Time `json:"last_tested"`
}

// JSONReportGenerator generates JSON benchmark reports
type JSONReportGenerator struct {
	store *storage.BenchmarkStore
}

// NewJSONReportGenerator creates a new JSON report generator
func NewJSONReportGenerator(store *storage.BenchmarkStore) *JSONReportGenerator {
	return &JSONReportGenerator{store: store}
}

// Generate creates a JSON report for the given model (or all models if empty)
func (g *JSONReportGenerator) Generate(modelID string) (*JSONReport, error) {
	report := &JSONReport{
		GeneratedAt:   time.Now(),
		ReportVersion: "1.0.0",
		ModelStats:    make(map[string]*storage.ModelStats),
	}

	// Get all results
	filter := storage.BenchmarkFilter{
		OrderBy:   "date",
		OrderDesc: true,
	}
	if modelID != "" {
		filter.ModelID = modelID
	}

	results, err := g.store.List(nil, filter)
	if err != nil {
		return nil, err
	}
	report.Results = results

	// Calculate summary
	report.Summary = g.calculateSummary(results)

	// Get unique models and stats
	models := make(map[string]bool)
	for _, r := range results {
		models[r.ModelID] = true
	}

	for model := range models {
		stats, err := g.store.GetModelStats(nil, model)
		if err == nil {
			report.ModelStats[model] = stats
		}
	}

	// Generate recommendations
	report.Recommendations = g.generateRecommendations(results)

	return report, nil
}

// WriteToFile writes the report to a file
func (g *JSONReportGenerator) WriteToFile(report *JSONReport, path string) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// WriteToStdout writes the report to stdout
func (g *JSONReportGenerator) WriteToStdout(report *JSONReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

func (g *JSONReportGenerator) calculateSummary(results []*storage.BenchmarkResult) *ReportSummary {
	summary := &ReportSummary{}

	if len(results) == 0 {
		return summary
	}

	models := make(map[string]bool)
	gpus := make(map[string]bool)
	providers := make(map[string]bool)

	var minDate, maxDate time.Time

	for _, r := range results {
		summary.TotalRuns++

		switch r.Status {
		case storage.BenchmarkStatusComplete:
			summary.SuccessfulRuns++
		case storage.BenchmarkStatusPartial:
			summary.PartialRuns++
		case storage.BenchmarkStatusFailed:
			summary.FailedRuns++
		}

		models[r.ModelID] = true
		gpus[r.GPUType] = true
		providers[r.Provider] = true

		// Calculate cost
		durationHrs := float64(r.TestDurationSec) / 3600.0
		summary.TotalCost += r.PricePerHour * durationHrs
		summary.TotalDurationMins += float64(r.TestDurationSec) / 60.0

		// Track date range
		if minDate.IsZero() || r.RunDate.Before(minDate) {
			minDate = r.RunDate
		}
		if maxDate.IsZero() || r.RunDate.After(maxDate) {
			maxDate = r.RunDate
		}
	}

	summary.ModelsTestad = len(models)
	summary.GPUsTestad = len(gpus)
	summary.ProvidersUsed = len(providers)
	summary.DateRange.Start = minDate
	summary.DateRange.End = maxDate

	return summary
}

func (g *JSONReportGenerator) generateRecommendations(results []*storage.BenchmarkResult) []Recommendation {
	var recommendations []Recommendation

	// Group results by model
	byModel := make(map[string][]*storage.BenchmarkResult)
	for _, r := range results {
		if r.Status == storage.BenchmarkStatusComplete {
			byModel[r.ModelID] = append(byModel[r.ModelID], r)
		}
	}

	// For each model, find best by cost, throughput, and latency
	for modelID, modelResults := range byModel {
		if len(modelResults) == 0 {
			continue
		}

		// Best by cost
		bestByCost := findBest(modelResults, func(a, b *storage.BenchmarkResult) bool {
			return a.CostPer1kTokens < b.CostPer1kTokens && a.CostPer1kTokens > 0
		})
		if bestByCost != nil {
			recommendations = append(recommendations, createRecommendation(modelID, "cost", bestByCost, len(modelResults)))
		}

		// Best by throughput
		bestByThroughput := findBest(modelResults, func(a, b *storage.BenchmarkResult) bool {
			return a.ThroughputTPS > b.ThroughputTPS
		})
		if bestByThroughput != nil {
			recommendations = append(recommendations, createRecommendation(modelID, "throughput", bestByThroughput, len(modelResults)))
		}

		// Best by latency
		bestByLatency := findBest(modelResults, func(a, b *storage.BenchmarkResult) bool {
			return a.TTFTMS < b.TTFTMS && a.TTFTMS > 0
		})
		if bestByLatency != nil {
			recommendations = append(recommendations, createRecommendation(modelID, "latency", bestByLatency, len(modelResults)))
		}
	}

	return recommendations
}

func findBest(results []*storage.BenchmarkResult, better func(a, b *storage.BenchmarkResult) bool) *storage.BenchmarkResult {
	if len(results) == 0 {
		return nil
	}

	best := results[0]
	for _, r := range results[1:] {
		if better(r, best) {
			best = r
		}
	}
	return best
}

func createRecommendation(modelID, optimizeFor string, result *storage.BenchmarkResult, sampleSize int) Recommendation {
	// Calculate confidence based on recency and sample size
	daysSinceTest := time.Since(result.RunDate).Hours() / 24
	recencyFactor := 1.0 - (daysSinceTest / 90.0) // Decay over 90 days
	if recencyFactor < 0.1 {
		recencyFactor = 0.1
	}

	sizeFactor := float64(sampleSize) / 10.0 // Scale to 10 samples
	if sizeFactor > 1.0 {
		sizeFactor = 1.0
	}

	confidence := (recencyFactor*0.6 + sizeFactor*0.4) * 100

	return Recommendation{
		ModelID:         modelID,
		OptimizeFor:     optimizeFor,
		GPUType:         result.GPUType,
		Provider:        result.Provider,
		PricePerHour:    result.PricePerHour,
		ThroughputTPS:   result.ThroughputTPS,
		TTFTMS:          result.TTFTMS,
		MaxConcurrent:   result.MaxConcurrent,
		CostPer1kTokens: result.CostPer1kTokens,
		Confidence:      confidence,
		SampleSize:      sampleSize,
		LastTested:      result.RunDate,
	}
}
