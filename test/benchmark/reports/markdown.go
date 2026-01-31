package reports

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/storage"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/test/benchmark/models"
)

// MarkdownReportGenerator generates human-readable Markdown benchmark reports
type MarkdownReportGenerator struct {
	store   *storage.BenchmarkStore
	catalog *models.Catalog
}

// NewMarkdownReportGenerator creates a new Markdown report generator
func NewMarkdownReportGenerator(store *storage.BenchmarkStore) *MarkdownReportGenerator {
	return &MarkdownReportGenerator{
		store:   store,
		catalog: models.NewCatalog(),
	}
}

// Generate creates a Markdown report for the given model (or all models if empty)
func (g *MarkdownReportGenerator) Generate(modelID string) (string, error) {
	var sb strings.Builder

	// Get results
	filter := storage.BenchmarkFilter{
		OrderBy:   "date",
		OrderDesc: true,
	}
	if modelID != "" {
		filter.ModelID = modelID
	}

	results, err := g.store.List(nil, filter)
	if err != nil {
		return "", err
	}

	// Header
	if modelID != "" {
		model, _ := g.catalog.GetModel(modelID)
		modelName := modelID
		if model != nil {
			modelName = model.Name
		}
		sb.WriteString(fmt.Sprintf("# Benchmark Report: %s\n\n", modelName))
	} else {
		sb.WriteString("# GPU Benchmark Report\n\n")
	}

	sb.WriteString(fmt.Sprintf("**Generated**: %s\n\n", time.Now().Format("2006-01-02 15:04:05 MST")))

	// Summary section
	g.writeSummary(&sb, results)

	// Recommendations section
	g.writeRecommendations(&sb, results, modelID)

	// Full results table
	g.writeResultsTable(&sb, results)

	// Cost summary
	g.writeCostSummary(&sb, results)

	return sb.String(), nil
}

// WriteToFile writes the report to a file
func (g *MarkdownReportGenerator) WriteToFile(report string, path string) error {
	return os.WriteFile(path, []byte(report), 0644)
}

// WriteToStdout writes the report to stdout
func (g *MarkdownReportGenerator) WriteToStdout(report string) error {
	_, err := os.Stdout.WriteString(report)
	return err
}

func (g *MarkdownReportGenerator) writeSummary(sb *strings.Builder, results []*storage.BenchmarkResult) {
	sb.WriteString("## Summary\n\n")

	if len(results) == 0 {
		sb.WriteString("No benchmark results available.\n\n")
		return
	}

	// Count statistics
	var successCount, partialCount, failedCount int
	models := make(map[string]bool)
	gpus := make(map[string]bool)

	for _, r := range results {
		switch r.Status {
		case storage.BenchmarkStatusComplete:
			successCount++
		case storage.BenchmarkStatusPartial:
			partialCount++
		case storage.BenchmarkStatusFailed:
			failedCount++
		}
		models[r.ModelID] = true
		gpus[r.GPUType] = true
	}

	sb.WriteString(fmt.Sprintf("- **Total Runs**: %d\n", len(results)))
	sb.WriteString(fmt.Sprintf("- **Successful**: %d\n", successCount))
	if partialCount > 0 {
		sb.WriteString(fmt.Sprintf("- **Partial**: %d\n", partialCount))
	}
	if failedCount > 0 {
		sb.WriteString(fmt.Sprintf("- **Failed**: %d\n", failedCount))
	}
	sb.WriteString(fmt.Sprintf("- **Models Tested**: %d\n", len(models)))
	sb.WriteString(fmt.Sprintf("- **GPUs Tested**: %d\n", len(gpus)))
	sb.WriteString("\n")
}

func (g *MarkdownReportGenerator) writeRecommendations(sb *strings.Builder, results []*storage.BenchmarkResult, modelFilter string) {
	sb.WriteString("## Recommendations\n\n")

	// Group by model
	byModel := make(map[string][]*storage.BenchmarkResult)
	for _, r := range results {
		if r.Status == storage.BenchmarkStatusComplete {
			byModel[r.ModelID] = append(byModel[r.ModelID], r)
		}
	}

	if len(byModel) == 0 {
		sb.WriteString("No successful benchmarks available for recommendations.\n\n")
		return
	}

	// Get sorted model list
	var modelIDs []string
	for id := range byModel {
		modelIDs = append(modelIDs, id)
	}
	sort.Strings(modelIDs)

	for _, modelID := range modelIDs {
		modelResults := byModel[modelID]
		if len(modelResults) == 0 {
			continue
		}

		model, _ := g.catalog.GetModel(modelID)
		modelName := modelID
		if model != nil {
			modelName = fmt.Sprintf("%s (%s)", model.Name, modelID)
		}

		sb.WriteString(fmt.Sprintf("### %s\n\n", modelName))
		sb.WriteString("| Optimize For | GPU | Provider | $/hr | Key Metric |\n")
		sb.WriteString("|--------------|-----|----------|------|------------|\n")

		// Best by cost
		bestCost := findBestResult(modelResults, "cost")
		if bestCost != nil {
			sb.WriteString(fmt.Sprintf("| 💰 Cost | %s | %s | $%.2f | $%.4f/1k tokens |\n",
				bestCost.GPUType, bestCost.Provider, bestCost.PricePerHour, bestCost.CostPer1kTokens))
		}

		// Best by throughput
		bestThroughput := findBestResult(modelResults, "throughput")
		if bestThroughput != nil {
			sb.WriteString(fmt.Sprintf("| 🚀 Throughput | %s | %s | $%.2f | %.0f tok/s |\n",
				bestThroughput.GPUType, bestThroughput.Provider, bestThroughput.PricePerHour, bestThroughput.ThroughputTPS))
		}

		// Best by latency
		bestLatency := findBestResult(modelResults, "latency")
		if bestLatency != nil {
			sb.WriteString(fmt.Sprintf("| ⚡ Latency | %s | %s | $%.2f | %.0fms TTFT |\n",
				bestLatency.GPUType, bestLatency.Provider, bestLatency.PricePerHour, bestLatency.TTFTMS))
		}

		sb.WriteString("\n")
	}
}

func (g *MarkdownReportGenerator) writeResultsTable(sb *strings.Builder, results []*storage.BenchmarkResult) {
	sb.WriteString("## Full Results\n\n")

	if len(results) == 0 {
		sb.WriteString("No results available.\n\n")
		return
	}

	sb.WriteString("| GPU | Provider | $/hr | Throughput | TTFT | Concurrent | $/1k tok | Status |\n")
	sb.WriteString("|-----|----------|------|------------|------|------------|----------|--------|\n")

	for _, r := range results {
		statusIcon := "❌"
		switch r.Status {
		case storage.BenchmarkStatusComplete:
			statusIcon = "✅"
		case storage.BenchmarkStatusPartial:
			statusIcon = "⚠️"
		}

		throughput := "-"
		if r.ThroughputTPS > 0 {
			throughput = fmt.Sprintf("%.0f tok/s", r.ThroughputTPS)
		}

		ttft := "-"
		if r.TTFTMS > 0 {
			ttft = fmt.Sprintf("%.0fms", r.TTFTMS)
		}

		concurrent := "-"
		if r.MaxConcurrent > 0 {
			concurrent = fmt.Sprintf("%d", r.MaxConcurrent)
		}

		costPer1k := "-"
		if r.CostPer1kTokens > 0 {
			costPer1k = fmt.Sprintf("$%.4f", r.CostPer1kTokens)
		}

		sb.WriteString(fmt.Sprintf("| %s | %s | $%.2f | %s | %s | %s | %s | %s |\n",
			r.GPUType, r.Provider, r.PricePerHour,
			throughput, ttft, concurrent, costPer1k, statusIcon))
	}

	sb.WriteString("\n")
}

func (g *MarkdownReportGenerator) writeCostSummary(sb *strings.Builder, results []*storage.BenchmarkResult) {
	sb.WriteString("## Cost Summary\n\n")

	if len(results) == 0 {
		sb.WriteString("No results available.\n\n")
		return
	}

	var totalCost float64
	var totalDuration time.Duration

	for _, r := range results {
		duration := time.Duration(r.TestDurationSec) * time.Second
		totalDuration += duration
		totalCost += r.PricePerHour * duration.Hours()
	}

	sb.WriteString(fmt.Sprintf("- **Total GPU time**: %s\n", totalDuration.Round(time.Minute)))
	sb.WriteString(fmt.Sprintf("- **Total cost**: $%.2f\n", totalCost))

	sb.WriteString("\n---\n\n")
	sb.WriteString("*Report generated by GPU Shopper Benchmark Suite*\n")
}

func findBestResult(results []*storage.BenchmarkResult, metric string) *storage.BenchmarkResult {
	if len(results) == 0 {
		return nil
	}

	best := results[0]
	for _, r := range results[1:] {
		switch metric {
		case "cost":
			if r.CostPer1kTokens > 0 && (best.CostPer1kTokens == 0 || r.CostPer1kTokens < best.CostPer1kTokens) {
				best = r
			}
		case "throughput":
			if r.ThroughputTPS > best.ThroughputTPS {
				best = r
			}
		case "latency":
			if r.TTFTMS > 0 && (best.TTFTMS == 0 || r.TTFTMS < best.TTFTMS) {
				best = r
			}
		}
	}

	// Validate we found something useful
	switch metric {
	case "cost":
		if best.CostPer1kTokens == 0 {
			return nil
		}
	case "throughput":
		if best.ThroughputTPS == 0 {
			return nil
		}
	case "latency":
		if best.TTFTMS == 0 {
			return nil
		}
	}

	return best
}
