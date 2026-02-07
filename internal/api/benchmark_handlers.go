package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/benchmark"
	benchsvc "github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/benchmark"
)

// BenchmarkQuery defines query parameters for benchmark endpoints
type BenchmarkQuery struct {
	Model    string `form:"model"`
	GPU      string `form:"gpu"`
	Provider string `form:"provider"`
	Runtime  string `form:"runtime"`
	Limit    int    `form:"limit"`
}

// BenchmarkRecommendationQuery defines query for hardware recommendations
type BenchmarkRecommendationQuery struct {
	Model  string  `form:"model" binding:"required"`
	MinTPS float64 `form:"min_tps"`
}

// handleListBenchmarks lists benchmark results with optional filters
func (s *Server) handleListBenchmarks(c *gin.Context) {
	if s.benchmarkStore == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark service not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	var query BenchmarkQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     "invalid query parameters: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	ctx := c.Request.Context()
	var results []*benchmark.BenchmarkResult
	var err error

	switch {
	case query.Model != "":
		results, err = s.benchmarkStore.ListByModel(ctx, query.Model)
	case query.GPU != "":
		results, err = s.benchmarkStore.ListByGPU(ctx, query.GPU)
	default:
		results, err = s.benchmarkStore.ListRecent(ctx, limit)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to fetch benchmarks: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Apply limit if filtering by model/gpu
	if len(results) > limit {
		results = results[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"benchmarks": results,
		"count":      len(results),
	})
}

// handleGetBenchmark retrieves a single benchmark by ID
func (s *Server) handleGetBenchmark(c *gin.Context) {
	if s.benchmarkStore == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark service not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	id := c.Param("id")
	result, err := s.benchmarkStore.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to fetch benchmark: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if result == nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:     "benchmark not found",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Include cost analysis in response
	costAnalysis := benchmark.CalculateCostAnalysis(result)

	c.JSON(http.StatusOK, gin.H{
		"benchmark":     result,
		"cost_analysis": costAnalysis,
	})
}

// handleGetBestBenchmark returns the best performing benchmark for a model
func (s *Server) handleGetBestBenchmark(c *gin.Context) {
	if s.benchmarkStore == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark service not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	model := c.Query("model")
	if model == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     "model parameter is required",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	result, err := s.benchmarkStore.GetBestForModel(c.Request.Context(), model)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to fetch benchmark: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if result == nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:     "no benchmarks found for model: " + sanitizeInput(model, 128),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"benchmark":     result,
		"cost_analysis": benchmark.CalculateCostAnalysis(result),
	})
}

// handleGetCheapestBenchmark returns the most cost-effective benchmark for a model
func (s *Server) handleGetCheapestBenchmark(c *gin.Context) {
	if s.benchmarkStore == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark service not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	model := c.Query("model")
	if model == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     "model parameter is required",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	minTPS := 0.0
	if minTPSStr := c.Query("min_tps"); minTPSStr != "" {
		var err error
		minTPS, err = strconv.ParseFloat(minTPSStr, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     "invalid min_tps value",
				RequestID: c.GetString("request_id"),
			})
			return
		}
	}

	result, err := s.benchmarkStore.GetCheapestForModel(c.Request.Context(), model, minTPS)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to fetch benchmark: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if result == nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:     "no benchmarks found for model: " + sanitizeInput(model, 128),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"benchmark":     result,
		"cost_analysis": benchmark.CalculateCostAnalysis(result),
	})
}

// handleGetHardwareRecommendations returns hardware recommendations for a model
func (s *Server) handleGetHardwareRecommendations(c *gin.Context) {
	if s.benchmarkStore == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark service not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	model := c.Query("model")
	if model == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     "model parameter is required",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	recommendations, err := s.benchmarkStore.GetModelRecommendations(c.Request.Context(), model)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to get recommendations: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"model":           model,
		"recommendations": recommendations,
		"count":           len(recommendations),
	})
}

// handleCreateBenchmark creates a new benchmark record
func (s *Server) handleCreateBenchmark(c *gin.Context) {
	if s.benchmarkStore == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark service not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	var result benchmark.BenchmarkResult
	if err := c.ShouldBindJSON(&result); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     "invalid benchmark data: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if err := s.benchmarkStore.Save(c.Request.Context(), &result); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to save benchmark: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"benchmark":     result,
		"cost_analysis": benchmark.CalculateCostAnalysis(&result),
	})
}

// handleCompareBenchmarks compares benchmarks for the same model across hardware
func (s *Server) handleCompareBenchmarks(c *gin.Context) {
	if s.benchmarkStore == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark service not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	model := c.Query("model")
	if model == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     "model parameter is required",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	ctx := c.Request.Context()

	// Get all benchmarks for the model
	results, err := s.benchmarkStore.ListByModel(ctx, model)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to fetch benchmarks: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if len(results) == 0 {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:     "no benchmarks found for model: " + sanitizeInput(model, 128),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Use the best as baseline
	best, _ := s.benchmarkStore.GetBestForModel(ctx, model)
	if best == nil {
		best = results[0]
	}

	comparison := &benchmark.BenchmarkComparison{
		Baseline:    best,
		Comparisons: make([]*benchmark.ComparisonEntry, 0, len(results)),
	}

	for _, r := range results {
		if r.ID == best.ID {
			continue
		}

		entry := &benchmark.ComparisonEntry{
			Result: r,
		}

		// Calculate speedup relative to baseline
		if best.Results.AvgTokensPerSecond > 0 {
			entry.SpeedupFactor = r.Results.AvgTokensPerSecond / best.Results.AvgTokensPerSecond
		}

		// Calculate cost efficiency
		baseCost := benchmark.CalculateCostAnalysis(best)
		thisCost := benchmark.CalculateCostAnalysis(r)
		if baseCost.TokensPerDollar > 0 {
			entry.CostEfficiency = thisCost.TokensPerDollar / baseCost.TokensPerDollar
		}

		// Calculate memory efficiency (tokens/sec per GB VRAM)
		if best.Hardware.GPUMemoryMiB > 0 && r.Hardware.GPUMemoryMiB > 0 {
			baseMemEff := best.Results.AvgTokensPerSecond / (float64(best.Hardware.GPUMemoryMiB) / 1024)
			thisMemEff := r.Results.AvgTokensPerSecond / (float64(r.Hardware.GPUMemoryMiB) / 1024)
			if baseMemEff > 0 {
				entry.MemoryEfficiency = thisMemEff / baseMemEff
			}
		}

		comparison.Comparisons = append(comparison.Comparisons, entry)
	}

	c.JSON(http.StatusOK, comparison)
}

// ── Benchmark Runs ──────────────────────────────────────────────────────────

// handleStartBenchmarkRun starts a new benchmark run.
func (s *Server) handleStartBenchmarkRun(c *gin.Context) {
	if s.benchmarkRunner == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark runner not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	var req benchsvc.BenchmarkRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     "invalid request: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if len(req.Models) == 0 {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     "at least one model is required",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	run, err := s.benchmarkRunner.StartRun(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to start benchmark run: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"run": run,
	})
}

// handleGetBenchmarkRun returns the status of a benchmark run.
func (s *Server) handleGetBenchmarkRun(c *gin.Context) {
	if s.benchmarkRunner == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark runner not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	runID := c.Param("id")
	run, err := s.benchmarkRunner.GetRun(c.Request.Context(), runID)
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:     "run not found: " + sanitizeInput(runID, 128),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	entries, _ := s.benchmarkRunner.GetRunEntries(c.Request.Context(), runID)

	c.JSON(http.StatusOK, gin.H{
		"run":     run,
		"entries": entries,
	})
}

// handleCancelBenchmarkRun cancels a running benchmark.
func (s *Server) handleCancelBenchmarkRun(c *gin.Context) {
	if s.benchmarkRunner == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark runner not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	runID := c.Param("id")
	if err := s.benchmarkRunner.CancelRun(c.Request.Context(), runID); err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:     "run not found: " + sanitizeInput(runID, 128),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "cancelled",
	})
}

// ── Benchmark Schedules ─────────────────────────────────────────────────────

// handleCreateBenchmarkSchedule creates a new benchmark schedule.
func (s *Server) handleCreateBenchmarkSchedule(c *gin.Context) {
	if s.benchmarkScheduler == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark scheduler not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	var sched benchsvc.Schedule
	if err := c.ShouldBindJSON(&sched); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     "invalid request: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if sched.Name == "" || sched.CronExpr == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     "name and cron expression are required",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	sched.Enabled = true
	if err := s.benchmarkScheduler.GetStore().Create(c.Request.Context(), &sched); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to create schedule: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"schedule": sched})
}

// handleListBenchmarkSchedules lists all benchmark schedules.
func (s *Server) handleListBenchmarkSchedules(c *gin.Context) {
	if s.benchmarkScheduler == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark scheduler not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	schedules, err := s.benchmarkScheduler.GetStore().List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to list schedules: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"schedules": schedules,
		"count":     len(schedules),
	})
}

// handleUpdateBenchmarkSchedule updates an existing benchmark schedule.
func (s *Server) handleUpdateBenchmarkSchedule(c *gin.Context) {
	if s.benchmarkScheduler == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark scheduler not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	id := c.Param("id")
	store := s.benchmarkScheduler.GetStore()

	existing, err := store.Get(c.Request.Context(), id)
	if err != nil || existing == nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:     "schedule not found: " + sanitizeInput(id, 128),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	var update struct {
		Name    string                        `json:"name"`
		Cron    string                        `json:"cron"`
		Request *benchsvc.BenchmarkRunRequest `json:"run_request"`
		Enabled *bool                         `json:"enabled"` // Pointer so omitted field != false
	}
	if err := c.ShouldBindJSON(&update); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     "invalid request: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Merge only provided fields
	if update.Name != "" {
		existing.Name = update.Name
	}
	if update.Cron != "" {
		existing.CronExpr = update.Cron
	}
	if update.Request != nil && len(update.Request.Models) > 0 {
		existing.Request = *update.Request
	}
	if update.Enabled != nil {
		existing.Enabled = *update.Enabled
	}

	if err := store.Update(c.Request.Context(), existing); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to update schedule: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"schedule": existing})
}

// handleDeleteBenchmarkSchedule deletes a benchmark schedule.
func (s *Server) handleDeleteBenchmarkSchedule(c *gin.Context) {
	if s.benchmarkScheduler == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:     "benchmark scheduler not available",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	id := c.Param("id")
	if err := s.benchmarkScheduler.GetStore().Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:     "schedule not found: " + sanitizeInput(id, 128),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
