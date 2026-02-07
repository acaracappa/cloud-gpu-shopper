// Package benchmark provides the benchmark orchestration service.
// It provisions GPU instances, deploys the benchmark script, collects results
// via SSH, and stores them in the benchmark store.
package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	benchmarkpkg "github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/benchmark"
	sshpkg "github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/ssh"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/inventory"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/provisioner"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// BenchmarkRunRequest defines the parameters for a benchmark run.
type BenchmarkRunRequest struct {
	Models    []string `json:"models"`               // e.g. ["deepseek-r1:14b", "llama3.1:8b"]
	GPUTypes  []string `json:"gpu_types,omitempty"`  // e.g. ["RTX 4090", "RTX 3090"] — empty = all available
	Providers []string `json:"providers,omitempty"`  // e.g. ["vastai", "tensordock"] — empty = all
	MaxBudget float64  `json:"max_budget,omitempty"` // Total $ budget for the run
	Priority  int      `json:"priority,omitempty"`   // Manifest priority (lower = higher)
}

// BenchmarkRunStatus represents the current state of a benchmark run.
type BenchmarkRunStatus string

const (
	RunStatusPending   BenchmarkRunStatus = "pending"
	RunStatusRunning   BenchmarkRunStatus = "running"
	RunStatusCompleted BenchmarkRunStatus = "completed"
	RunStatusCancelled BenchmarkRunStatus = "cancelled"
	RunStatusFailed    BenchmarkRunStatus = "failed"
)

// BenchmarkRun represents a benchmark orchestration run.
type BenchmarkRun struct {
	ID        string              `json:"id"`
	Status    BenchmarkRunStatus  `json:"status"`
	Request   BenchmarkRunRequest `json:"request"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`

	// Summary (populated from manifest)
	TotalEntries int     `json:"total_entries"`
	Completed    int     `json:"completed"`
	Failed       int     `json:"failed"`
	Running      int     `json:"running"`
	Pending      int     `json:"pending"`
	TotalCost    float64 `json:"total_cost"`
}

// Runner orchestrates benchmark runs across GPU instances.
type Runner struct {
	provisioner *provisioner.Service
	inventory   *inventory.Service
	store       *benchmarkpkg.Store
	manifest    *benchmarkpkg.ManifestStore
	logger      *slog.Logger

	// Active runs tracking
	mu      sync.Mutex
	runs    map[string]*BenchmarkRun
	cancels map[string]context.CancelFunc

	// Concurrency limit for benchmark workers
	workerSem chan struct{}
}

// NewRunner creates a new benchmark runner.
func NewRunner(
	prov *provisioner.Service,
	inv *inventory.Service,
	store *benchmarkpkg.Store,
	manifest *benchmarkpkg.ManifestStore,
	logger *slog.Logger,
) *Runner {
	return &Runner{
		provisioner: prov,
		inventory:   inv,
		store:       store,
		manifest:    manifest,
		logger:      logger,
		runs:        make(map[string]*BenchmarkRun),
		cancels:     make(map[string]context.CancelFunc),
		workerSem:   make(chan struct{}, 3), // Max 3 concurrent benchmarks
	}
}

// StartRun begins a new benchmark run.
func (r *Runner) StartRun(ctx context.Context, req BenchmarkRunRequest) (*BenchmarkRun, error) {
	if len(req.Models) == 0 {
		return nil, fmt.Errorf("at least one model is required")
	}

	runID := "run-" + uuid.New().String()[:8]
	now := time.Now()

	run := &BenchmarkRun{
		ID:        runID,
		Status:    RunStatusPending,
		Request:   req,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Determine GPU types to benchmark
	gpuTypes := req.GPUTypes
	if len(gpuTypes) == 0 {
		// Use what's currently available
		offers, err := r.inventory.ListOffers(ctx, models.OfferFilter{})
		if err != nil {
			return nil, fmt.Errorf("failed to list offers: %w", err)
		}
		seen := make(map[string]bool)
		for _, o := range offers {
			if !seen[o.GPUType] {
				gpuTypes = append(gpuTypes, o.GPUType)
				seen[o.GPUType] = true
			}
		}
	}

	// Determine providers to use
	providers := req.Providers
	if len(providers) == 0 {
		providers = []string{"vastai", "tensordock"}
	}

	// Create manifest entries: models x GPU types x providers
	entryCount := 0
	for _, model := range req.Models {
		for _, gpu := range gpuTypes {
			for _, prov := range providers {
				entry := &benchmarkpkg.ManifestEntry{
					RunID:    runID,
					GPUType:  gpu,
					Provider: prov,
					Model:    model,
					Priority: req.Priority,
				}
				if err := r.manifest.Create(ctx, entry); err != nil {
					return nil, fmt.Errorf("failed to create manifest entry: %w", err)
				}
				entryCount++
			}
		}
	}

	run.TotalEntries = entryCount
	run.Pending = entryCount

	r.mu.Lock()
	r.runs[runID] = run
	r.mu.Unlock()

	// Start background processing
	runCtx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancels[runID] = cancel
	r.mu.Unlock()

	go r.processRun(runCtx, run)

	r.logger.Info("benchmark run started",
		slog.String("run_id", runID),
		slog.Int("entries", entryCount),
		slog.Float64("max_budget", req.MaxBudget))

	return run, nil
}

// GetRun returns the current state of a benchmark run.
func (r *Runner) GetRun(ctx context.Context, runID string) (*BenchmarkRun, error) {
	r.mu.Lock()
	run, ok := r.runs[runID]
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("run not found: %s", runID)
	}
	// Copy the run to avoid holding the lock during DB queries
	snapshot := *run
	r.mu.Unlock()

	// Refresh summary from manifest (operates on snapshot, not shared state)
	summary, err := r.manifest.GetSummary(ctx, runID)
	if err == nil {
		snapshot.Pending = summary[benchmarkpkg.ManifestStatusPending]
		snapshot.Running = summary[benchmarkpkg.ManifestStatusRunning]
		snapshot.Completed = summary[benchmarkpkg.ManifestStatusSuccess]
		snapshot.Failed = summary[benchmarkpkg.ManifestStatusFailed] + summary[benchmarkpkg.ManifestStatusTimeout]
	}

	cost, err := r.manifest.GetTotalCost(ctx, runID)
	if err == nil {
		snapshot.TotalCost = cost
	}

	return &snapshot, nil
}

// GetRunEntries returns manifest entries for a run.
func (r *Runner) GetRunEntries(ctx context.Context, runID string) ([]*benchmarkpkg.ManifestEntry, error) {
	return r.manifest.ListByRun(ctx, runID)
}

// CancelRun cancels a running benchmark.
func (r *Runner) CancelRun(ctx context.Context, runID string) error {
	r.mu.Lock()
	cancel, ok := r.cancels[runID]
	run := r.runs[runID]
	r.mu.Unlock()

	if !ok {
		return fmt.Errorf("run not found: %s", runID)
	}

	cancel()

	if run != nil {
		run.Status = RunStatusCancelled
		run.UpdatedAt = time.Now()
	}

	r.logger.Info("benchmark run cancelled", slog.String("run_id", runID))
	return nil
}

// processRun processes all manifest entries for a run.
func (r *Runner) processRun(ctx context.Context, run *BenchmarkRun) {
	defer func() {
		r.mu.Lock()
		delete(r.cancels, run.ID)
		r.mu.Unlock()
	}()

	r.mu.Lock()
	run.Status = RunStatusRunning
	run.UpdatedAt = time.Now()
	r.mu.Unlock()

	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		default:
		}

		// Get next pending entries
		entries, err := r.manifest.GetPendingByPriority(ctx, run.ID, 3)
		if err != nil {
			r.logger.Error("failed to get pending entries", slog.String("error", err.Error()))
			break
		}
		if len(entries) == 0 {
			// Wait for running entries to complete
			wg.Wait()
			break
		}

		// Check budget
		if run.Request.MaxBudget > 0 {
			totalCost, _ := r.manifest.GetTotalCost(ctx, run.ID)
			if totalCost >= run.Request.MaxBudget {
				r.logger.Warn("budget exhausted", slog.Float64("total_cost", totalCost), slog.Float64("budget", run.Request.MaxBudget))
				break
			}
		}

		for _, entry := range entries {
			// Mark running BEFORE dispatching to prevent double-dispatch
			// when the outer loop re-fetches pending entries.
			workerID := "worker-" + uuid.New().String()[:8]
			if err := r.manifest.MarkRunning(ctx, entry.ID, workerID, ""); err != nil {
				r.logger.Error("failed to mark entry running", slog.String("error", err.Error()))
				continue
			}

			select {
			case <-ctx.Done():
				wg.Wait()
				r.updateRunStatus(run)
				return
			case r.workerSem <- struct{}{}:
				wg.Add(1)
				go func(e *benchmarkpkg.ManifestEntry) {
					defer wg.Done()
					defer func() { <-r.workerSem }()
					r.processEntry(ctx, run, e)
				}(entry)
			}
		}
	}

	wg.Wait()
	r.updateRunStatus(run)
}

// updateRunStatus updates the run's final status.
func (r *Runner) updateRunStatus(run *BenchmarkRun) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if run.Status == RunStatusCancelled {
		return
	}

	summary, _ := r.manifest.GetSummary(context.Background(), run.ID)
	run.Pending = summary[benchmarkpkg.ManifestStatusPending]
	run.Running = summary[benchmarkpkg.ManifestStatusRunning]
	run.Completed = summary[benchmarkpkg.ManifestStatusSuccess]
	run.Failed = summary[benchmarkpkg.ManifestStatusFailed] + summary[benchmarkpkg.ManifestStatusTimeout]

	if run.Pending == 0 && run.Running == 0 {
		if run.Failed > 0 && run.Completed == 0 {
			run.Status = RunStatusFailed
		} else {
			run.Status = RunStatusCompleted
		}
	}
	run.UpdatedAt = time.Now()
}

// processEntry handles a single manifest entry: provision, benchmark, collect, cleanup.
// The entry is already marked as running by processRun before dispatch.
func (r *Runner) processEntry(ctx context.Context, run *BenchmarkRun, entry *benchmarkpkg.ManifestEntry) {
	r.logger.Info("processing benchmark entry",
		slog.String("entry_id", entry.ID),
		slog.String("model", entry.Model),
		slog.String("gpu_type", entry.GPUType),
		slog.String("provider", entry.Provider))

	// Step 1: Find a suitable offer
	offers, err := r.inventory.ListOffers(ctx, models.OfferFilter{
		Provider: entry.Provider,
		GPUType:  entry.GPUType,
	})
	if err != nil || len(offers) == 0 {
		reason := "no offers found"
		if err != nil {
			reason = err.Error()
		}
		r.logger.Warn("no offers for benchmark", slog.String("gpu_type", entry.GPUType), slog.String("provider", entry.Provider))
		_ = r.manifest.MarkFailed(ctx, entry.ID, reason, "find_offer")
		return
	}

	offer := &offers[0]
	entry.OfferID = offer.ID
	entry.PriceHour = offer.PricePerHour

	// Step 2: Provision a session (benchmark script deployed via SSH after session is ready)
	session, err := r.provisioner.CreateSession(ctx, models.CreateSessionRequest{
		ConsumerID:     "benchmark-" + run.ID,
		OfferID:        offer.ID,
		WorkloadType:   models.WorkloadBenchmark,
		ReservationHrs: 1,
		AutoRetry:      true,
		MaxRetries:     2,
		RetryScope:     "same_gpu",
	}, offer)
	if err != nil {
		r.logger.Error("failed to provision session for benchmark",
			slog.String("error", err.Error()),
			slog.String("entry_id", entry.ID))
		_ = r.manifest.MarkFailed(ctx, entry.ID, err.Error(), "provision")
		return
	}

	entry.SessionID = session.ID
	_ = r.manifest.Update(ctx, entry)

	r.logger.Info("benchmark session provisioned",
		slog.String("session_id", session.ID),
		slog.String("entry_id", entry.ID))

	// Step 3: Wait for session to be running with SSH access
	var sshHost, sshUser, sshKey string
	var sshPort int

	pollCtx, pollCancel := context.WithTimeout(ctx, 15*time.Minute)
	defer pollCancel()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	sessionReady := false
	for !sessionReady {
		select {
		case <-pollCtx.Done():
			r.logger.Warn("timeout waiting for benchmark session",
				slog.String("session_id", session.ID))
			_ = r.manifest.MarkTimeout(ctx, entry.ID, "ssh_wait")
			r.cleanupSession(ctx, session.ID)
			return
		case <-ticker.C:
			s, err := r.provisioner.GetSession(ctx, session.ID)
			if err != nil {
				continue
			}
			if s.Status == models.StatusFailed {
				_ = r.manifest.MarkFailed(ctx, entry.ID, s.Error, "provision")
				r.cleanupSession(ctx, session.ID)
				return
			}
			if s.Status == models.StatusRunning && s.SSHHost != "" {
				sshHost = s.SSHHost
				sshPort = s.SSHPort
				sshUser = s.SSHUser
				sshKey = session.SSHPrivateKey // From creation response
				sessionReady = true
			}
		}
	}

	// Step 4: Deploy and run the benchmark script via SSH with correct session ID
	benchmarkCmd := buildBenchmarkOnStartCmd(entry.Model, session.ID, offer.PricePerHour, entry.Provider, offer.Location)
	r.logger.Info("deploying benchmark script via SSH",
		slog.String("session_id", session.ID),
		slog.String("ssh_host", sshHost))

	_, deployErr := sshpkg.RunCommand(ctx, sshHost, sshPort, sshUser, sshKey, benchmarkCmd)
	if deployErr != nil {
		r.logger.Error("failed to deploy benchmark script",
			slog.String("error", deployErr.Error()),
			slog.String("session_id", session.ID))
		_ = r.manifest.MarkFailed(ctx, entry.ID, "deploy failed: "+deployErr.Error(), "deploy")
		r.cleanupSession(ctx, session.ID)
		return
	}

	// Step 5: Poll for results via SSH
	resultCtx, resultCancel := context.WithTimeout(ctx, 20*time.Minute)
	defer resultCancel()

	resultTicker := time.NewTicker(30 * time.Second)
	defer resultTicker.Stop()

	var resultJSON string
	for {
		select {
		case <-resultCtx.Done():
			r.logger.Warn("timeout waiting for benchmark results",
				slog.String("session_id", session.ID))
			_ = r.manifest.MarkTimeout(ctx, entry.ID, "result_collection")
			r.cleanupSession(ctx, session.ID)
			return
		case <-resultTicker.C:
			// Check if benchmark is complete
			output, err := sshpkg.RunCommand(resultCtx, sshHost, sshPort, sshUser, sshKey,
				"test -f /tmp/benchmark_complete && cat /tmp/benchmark_result.json")
			if err != nil {
				continue // Not ready yet
			}
			if strings.TrimSpace(output) != "" {
				resultJSON = strings.TrimSpace(output)
				goto resultsCollected
			}
		}
	}

resultsCollected:
	r.logger.Info("benchmark results collected",
		slog.String("session_id", session.ID),
		slog.Int("result_bytes", len(resultJSON)))

	// Step 6: Parse and save results
	var result benchmarkpkg.BenchmarkResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		r.logger.Error("failed to parse benchmark results",
			slog.String("error", err.Error()),
			slog.String("session_id", session.ID))
		_ = r.manifest.MarkFailed(ctx, entry.ID, "invalid result JSON: "+err.Error(), "parse")
		r.cleanupSession(ctx, session.ID)
		return
	}

	if err := r.store.Save(ctx, &result); err != nil {
		r.logger.Error("failed to save benchmark results",
			slog.String("error", err.Error()))
		_ = r.manifest.MarkFailed(ctx, entry.ID, "save failed: "+err.Error(), "save")
		r.cleanupSession(ctx, session.ID)
		return
	}

	// Calculate cost: session duration * price/hour
	duration := time.Since(session.CreatedAt)
	totalCost := duration.Hours() * offer.PricePerHour

	_ = r.manifest.MarkSuccess(ctx, entry.ID, result.ID, result.Results.AvgTokensPerSecond, totalCost)

	r.logger.Info("benchmark entry completed",
		slog.String("entry_id", entry.ID),
		slog.String("benchmark_id", result.ID),
		slog.Float64("avg_tps", result.Results.AvgTokensPerSecond),
		slog.Float64("cost", totalCost))

	// Step 7: Cleanup session
	r.cleanupSession(ctx, session.ID)
}

// cleanupSession destroys a benchmark session.
func (r *Runner) cleanupSession(ctx context.Context, sessionID string) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := r.provisioner.DestroySession(cleanupCtx, sessionID); err != nil {
		r.logger.Error("failed to destroy benchmark session",
			slog.String("session_id", sessionID),
			slog.String("error", err.Error()))
	}
}

// buildBenchmarkOnStartCmd creates the on-start command that runs the benchmark script.
func buildBenchmarkOnStartCmd(model, sessionID string, pricePerHour float64, provider, location string) string {
	// The benchmark script is embedded via base64 in P2. For P1, we assume
	// the script is deployed separately or already on the instance.
	// This creates a command that downloads and runs the script.
	return fmt.Sprintf(
		"nohup /tmp/gpu-benchmark.sh %s %s %.4f %s %s > /tmp/benchmark.log 2>&1 &",
		shellQuote(model), shellQuote(sessionID), pricePerHour, shellQuote(provider), shellQuote(location),
	)
}

// shellQuote wraps a string in single quotes for safe shell usage.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
