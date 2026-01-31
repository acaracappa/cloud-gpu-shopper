package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/storage"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/test/benchmark/models"
)

// Config holds the benchmark runner configuration
type Config struct {
	ServerURL     string
	DatabasePath  string
	AnsiblePath   string
	MaxCost       float64
	MaxDuration   time.Duration
	AlertCost     float64
	AlertDuration time.Duration
	SSHKeyPath    string
	SSHUser       string
	ResultsDir    string
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		ServerURL:     "http://localhost:8080",
		DatabasePath:  "./data/gpu-shopper.db",
		AnsiblePath:   "./test/ansible",
		MaxCost:       10.0,
		MaxDuration:   2 * time.Hour,
		AlertCost:     5.0,
		AlertDuration: 90 * time.Minute,
		SSHKeyPath:    os.ExpandEnv("$HOME/.ssh/id_rsa"),
		SSHUser:       "root",
		ResultsDir:    "./benchmark-results",
	}
}

// Runner orchestrates the benchmark workflow
type Runner struct {
	config    *Config
	catalog   *models.Catalog
	db        *storage.DB
	apiClient *APIClient
	ansible   *AnsibleExecutor
	watchdog  *Watchdog
}

// NewRunner creates a new benchmark runner
func NewRunner(config *Config) (*Runner, error) {
	catalog := models.NewCatalog()

	db, err := storage.New(config.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	apiClient := NewAPIClient(config.ServerURL)
	ansible := NewAnsibleExecutor(config.AnsiblePath, config.SSHKeyPath, config.SSHUser)

	return &Runner{
		config:    config,
		catalog:   catalog,
		db:        db,
		apiClient: apiClient,
		ansible:   ansible,
	}, nil
}

// Close cleans up runner resources
func (r *Runner) Close() error {
	return r.db.Close()
}

// BenchmarkPlan represents a planned benchmark run
type BenchmarkPlan struct {
	Model         *models.Model
	GPU           *models.GPU
	Provider      string
	EstimatedCost float64
}

// BenchmarkRun represents an in-progress or completed benchmark
type BenchmarkRun struct {
	ID            string
	Plan          *BenchmarkPlan
	SessionID     string
	Session       *Session
	StartTime     time.Time
	EndTime       time.Time
	Status        string
	Results       *BenchmarkResults
	FailedSteps   []string
	Error         string
}

// BenchmarkResults holds the combined results from all benchmark types
type BenchmarkResults struct {
	Throughput  *ThroughputResults  `json:"throughput,omitempty"`
	Latency     *LatencyResults     `json:"latency,omitempty"`
	Concurrency *ConcurrencyResults `json:"concurrency,omitempty"`
}

// ThroughputResults from the throughput benchmark
type ThroughputResults struct {
	TokensPerSecond float64 `json:"tokens_per_second"`
	TotalTokens     int     `json:"total_tokens"`
	TotalRequests   int     `json:"total_requests"`
	AvgLatencyMs    float64 `json:"avg_latency_ms"`
	SuccessRate     float64 `json:"success_rate"`
}

// LatencyResults from the latency benchmark
type LatencyResults struct {
	TTFTMs       float64 `json:"ttft_ms"`
	TTFTMedianMs float64 `json:"ttft_median_ms"`
	TTFTP90Ms    float64 `json:"ttft_p90_ms"`
	TTFTP99Ms    float64 `json:"ttft_p99_ms"`
	SuccessRate  float64 `json:"success_rate"`
}

// ConcurrencyResults from the concurrency benchmark
type ConcurrencyResults struct {
	OptimalConcurrency int     `json:"optimal_concurrency"`
	OptimalThroughput  float64 `json:"optimal_throughput_tps"`
	DegradationPoint   int     `json:"degradation_point"`
}

// Run executes a single benchmark for the given model/GPU combination
func (r *Runner) Run(ctx context.Context, plan *BenchmarkPlan) (*BenchmarkRun, error) {
	run := &BenchmarkRun{
		ID:        fmt.Sprintf("bench-%d", time.Now().UnixNano()),
		Plan:      plan,
		StartTime: time.Now(),
		Status:    "running",
	}

	// Start watchdog
	r.watchdog = NewWatchdog(r.config.MaxCost, r.config.MaxDuration, r.config.AlertCost, r.config.AlertDuration)
	watchdogCtx, cancelWatchdog := context.WithCancel(ctx)
	defer cancelWatchdog()

	go r.watchdog.Start(watchdogCtx)

	// Ensure cleanup happens
	defer func() {
		run.EndTime = time.Now()
		r.cleanup(ctx, run)
	}()

	// Phase 1: Provision
	fmt.Printf("Phase 1: Provisioning %s GPU from %s...\n", plan.GPU.Type, plan.Provider)
	session, err := r.provision(ctx, plan)
	if err != nil {
		run.Status = "failed"
		run.Error = fmt.Sprintf("provision failed: %v", err)
		return run, err
	}
	run.SessionID = session.ID
	run.Session = session
	r.watchdog.SetSession(session.ID, plan.GPU.TypicalPriceHr)

	// Phase 2: Setup node
	fmt.Printf("Phase 2: Setting up node...\n")
	if err := r.setupNode(ctx, session); err != nil {
		run.Status = "failed"
		run.Error = fmt.Sprintf("setup failed: %v", err)
		run.FailedSteps = append(run.FailedSteps, "setup")
		return run, err
	}

	// Phase 3: Deploy vLLM
	fmt.Printf("Phase 3: Deploying vLLM with %s...\n", plan.Model.ID)
	if err := r.deployVLLM(ctx, session, plan.Model); err != nil {
		run.Status = "failed"
		run.Error = fmt.Sprintf("deploy failed: %v", err)
		run.FailedSteps = append(run.FailedSteps, "deploy")
		return run, err
	}

	// Phase 4: Run benchmarks
	fmt.Printf("Phase 4: Running benchmarks...\n")
	results, failedSteps := r.runBenchmarks(ctx, session)
	run.Results = results
	run.FailedSteps = append(run.FailedSteps, failedSteps...)

	// Determine final status
	if len(run.FailedSteps) == 0 {
		run.Status = "complete"
	} else if run.Results != nil && (run.Results.Throughput != nil || run.Results.Latency != nil || run.Results.Concurrency != nil) {
		run.Status = "partial"
	} else {
		run.Status = "failed"
	}

	// Phase 5: Store results
	fmt.Printf("Phase 5: Storing results...\n")
	if err := r.storeResults(ctx, run); err != nil {
		fmt.Printf("Warning: failed to store results: %v\n", err)
	}

	return run, nil
}

// provision creates a GPU session via the API
func (r *Runner) provision(ctx context.Context, plan *BenchmarkPlan) (*Session, error) {
	// First, check if the server is reachable
	if err := r.apiClient.HealthCheck(ctx); err != nil {
		return nil, fmt.Errorf("server not reachable: %w", err)
	}

	// Query inventory for matching GPU
	fmt.Printf("  Querying inventory for %s GPUs...\n", plan.GPU.Type)
	offers, err := r.apiClient.GetInventory(ctx, plan.GPU.Type, plan.Model.MinVRAMGB)
	if err != nil {
		return nil, fmt.Errorf("failed to get inventory: %w", err)
	}

	if len(offers) == 0 {
		return nil, fmt.Errorf("no %s GPUs available with %dGB+ VRAM", plan.GPU.Type, plan.Model.MinVRAMGB)
	}

	// Find the best offer (cheapest available)
	var bestOffer *InventoryOffer
	for i := range offers {
		offer := &offers[i]
		if !offer.Available {
			continue
		}
		if plan.Provider != "" && offer.Provider != plan.Provider {
			continue
		}
		if bestOffer == nil || offer.PricePerHour < bestOffer.PricePerHour {
			bestOffer = offer
		}
	}

	if bestOffer == nil {
		return nil, fmt.Errorf("no available offers for %s from provider %s", plan.GPU.Type, plan.Provider)
	}

	fmt.Printf("  Selected offer: %s from %s at $%.2f/hr\n", bestOffer.ID, bestOffer.Provider, bestOffer.PricePerHour)

	// Create session
	fmt.Printf("  Creating session...\n")
	sessionReq := &SessionRequest{
		ConsumerID:       "benchmark-suite",
		OfferID:          bestOffer.ID,
		WorkloadType:     "benchmark",
		ReservationHours: 2,
	}

	createResp, err := r.apiClient.CreateSession(ctx, sessionReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	sessionResp := &createResp.Session

	// Save SSH private key from server to temp file for Ansible
	sshKeyPath := r.config.SSHKeyPath
	if createResp.SSHPrivateKey != "" {
		tmpFile, err := os.CreateTemp("", "benchmark-ssh-*")
		if err != nil {
			return nil, fmt.Errorf("failed to create temp SSH key file: %w", err)
		}
		sshKeyPath = tmpFile.Name()
		if err := os.WriteFile(sshKeyPath, []byte(createResp.SSHPrivateKey), 0600); err != nil {
			return nil, fmt.Errorf("failed to write SSH key: %w", err)
		}
		fmt.Printf("  SSH key saved to: %s\n", sshKeyPath)
	}

	fmt.Printf("  Session created: %s\n", sessionResp.ID)
	fmt.Printf("  Waiting for session to be ready...\n")

	// Wait for session to be ready
	readySession, err := r.apiClient.WaitForSession(ctx, sessionResp.ID, 10*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("session failed to become ready: %w", err)
	}

	fmt.Printf("  Session ready: %s@%s:%d\n", readySession.SSHUser, readySession.SSHHost, readySession.SSHPort)

	return &Session{
		ID:         readySession.ID,
		SSHHost:    readySession.SSHHost,
		SSHPort:    readySession.SSHPort,
		SSHUser:    readySession.SSHUser,
		Provider:   readySession.Provider,
		GPUType:    readySession.GPUType,
		SSHKeyPath: sshKeyPath,
	}, nil
}

// Session represents a provisioned GPU session
type Session struct {
	ID         string
	SSHHost    string
	SSHPort    int
	SSHUser    string
	Provider   string
	GPUType    string
	SSHKeyPath string
}

// setupNode runs the setup-node.yml playbook
func (r *Runner) setupNode(ctx context.Context, session *Session) error {
	return r.ansible.RunPlaybookWithKey(ctx, "setup-node.yml", session.SSHHost, session.SSHPort, nil, session.SSHKeyPath)
}

// deployVLLM runs the deploy-vllm.yml playbook
func (r *Runner) deployVLLM(ctx context.Context, session *Session, model *models.Model) error {
	vars := map[string]string{
		"model_id":        model.HuggingFaceID,
		"tensor_parallel": fmt.Sprintf("%d", model.TensorParallel),
		"max_model_len":   fmt.Sprintf("%d", model.MaxModelLen),
	}
	return r.ansible.RunPlaybookWithKey(ctx, "deploy-vllm.yml", session.SSHHost, session.SSHPort, vars, session.SSHKeyPath)
}

// runBenchmarks executes all benchmark workloads
func (r *Runner) runBenchmarks(ctx context.Context, session *Session) (*BenchmarkResults, []string) {
	results := &BenchmarkResults{}
	var failedSteps []string

	// Run run-benchmark.yml playbook
	err := r.ansible.RunPlaybookWithKey(ctx, "run-benchmark.yml", session.SSHHost, session.SSHPort, nil, session.SSHKeyPath)
	if err != nil {
		failedSteps = append(failedSteps, "benchmark")
		return results, failedSteps
	}

	// Load results from fetched files
	resultsDir := filepath.Join("./results", fmt.Sprintf("%s_%d", session.SSHHost, session.SSHPort))

	// Load throughput results
	if data, err := os.ReadFile(filepath.Join(resultsDir, "throughput.json")); err == nil {
		var tp struct {
			TokensPerSecond float64 `json:"tokens_per_second"`
			TotalTokens     int     `json:"total_tokens"`
			TotalRequests   int     `json:"total_requests"`
			AvgLatencyMs    float64 `json:"avg_latency_ms"`
			SuccessRate     float64 `json:"success_rate"`
		}
		if json.Unmarshal(data, &tp) == nil {
			results.Throughput = &ThroughputResults{
				TokensPerSecond: tp.TokensPerSecond,
				TotalTokens:     tp.TotalTokens,
				TotalRequests:   tp.TotalRequests,
				AvgLatencyMs:    tp.AvgLatencyMs,
				SuccessRate:     tp.SuccessRate,
			}
		}
	} else {
		failedSteps = append(failedSteps, "throughput")
	}

	// Load latency results
	if data, err := os.ReadFile(filepath.Join(resultsDir, "latency.json")); err == nil {
		var lat struct {
			TTFTMs       float64 `json:"ttft_ms"`
			TTFTMedianMs float64 `json:"ttft_median_ms"`
			TTFTP90Ms    float64 `json:"ttft_p90_ms"`
			TTFTP99Ms    float64 `json:"ttft_p99_ms"`
			SuccessRate  float64 `json:"success_rate"`
		}
		if json.Unmarshal(data, &lat) == nil {
			results.Latency = &LatencyResults{
				TTFTMs:       lat.TTFTMs,
				TTFTMedianMs: lat.TTFTMedianMs,
				TTFTP90Ms:    lat.TTFTP90Ms,
				TTFTP99Ms:    lat.TTFTP99Ms,
				SuccessRate:  lat.SuccessRate,
			}
		}
	} else {
		failedSteps = append(failedSteps, "latency")
	}

	// Load concurrency results
	if data, err := os.ReadFile(filepath.Join(resultsDir, "concurrency.json")); err == nil {
		var conc struct {
			OptimalConcurrency int     `json:"optimal_concurrency"`
			OptimalThroughput  float64 `json:"optimal_throughput_tps"`
			DegradationPoint   int     `json:"degradation_point"`
		}
		if json.Unmarshal(data, &conc) == nil {
			results.Concurrency = &ConcurrencyResults{
				OptimalConcurrency: conc.OptimalConcurrency,
				OptimalThroughput:  conc.OptimalThroughput,
				DegradationPoint:   conc.DegradationPoint,
			}
		}
	} else {
		failedSteps = append(failedSteps, "concurrency")
	}

	return results, failedSteps
}

// storeResults persists benchmark results to the database
func (r *Runner) storeResults(ctx context.Context, run *BenchmarkRun) error {
	store := storage.NewBenchmarkStore(r.db)

	// Calculate cost per 1k tokens
	durationHrs := run.EndTime.Sub(run.StartTime).Hours()
	totalCost := run.Plan.GPU.TypicalPriceHr * durationHrs

	var costPer1kTokens float64
	if run.Results != nil && run.Results.Throughput != nil && run.Results.Throughput.TotalTokens > 0 {
		costPer1kTokens = (totalCost / float64(run.Results.Throughput.TotalTokens)) * 1000
	}

	result := &storage.BenchmarkResult{
		ID:                     run.ID,
		RunDate:                run.StartTime,
		ModelID:                run.Plan.Model.ID,
		ModelParamsB:           run.Plan.Model.ParametersB,
		GPUType:                run.Plan.GPU.Type,
		GPUVramGB:              run.Plan.GPU.VRAMGB,
		Provider:               run.Plan.Provider,
		PricePerHour:           run.Plan.GPU.TypicalPriceHr,
		Status:                 run.Status,
		FailedSteps:            run.FailedSteps,
		TestDurationSec:        int(run.EndTime.Sub(run.StartTime).Seconds()),
		SessionID:              run.SessionID,
		AnsiblePlaybookVersion: "1.0.0",
		CostPer1kTokens:        costPer1kTokens,
	}

	if run.Results != nil {
		if run.Results.Throughput != nil {
			result.ThroughputTPS = run.Results.Throughput.TokensPerSecond
		}
		if run.Results.Latency != nil {
			result.TTFTMS = run.Results.Latency.TTFTMs
		}
		if run.Results.Concurrency != nil {
			result.MaxConcurrent = run.Results.Concurrency.OptimalConcurrency
		}
	}

	return store.Create(ctx, result)
}

// cleanup runs the cleanup playbook and terminates the session
func (r *Runner) cleanup(ctx context.Context, run *BenchmarkRun) {
	if run.SessionID == "" {
		return
	}

	fmt.Printf("Cleaning up session %s...\n", run.SessionID)

	// Use a background context for cleanup in case the main context is cancelled
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Try to run cleanup playbook if we have session info
	if run.Session != nil {
		fmt.Printf("  Running cleanup playbook...\n")
		err := r.ansible.RunPlaybookWithKey(cleanupCtx, "cleanup.yml", run.Session.SSHHost, run.Session.SSHPort, nil, run.Session.SSHKeyPath)
		if err != nil {
			fmt.Printf("  Warning: cleanup playbook failed: %v\n", err)
		}
	}

	// Terminate the session via API
	fmt.Printf("  Terminating session via API...\n")
	if err := r.apiClient.DeleteSession(cleanupCtx, run.SessionID); err != nil {
		fmt.Printf("  Warning: failed to delete session: %v\n", err)
	}

	fmt.Printf("Cleanup complete\n")
}
