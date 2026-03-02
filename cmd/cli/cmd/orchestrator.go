package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

var (
	orchRunID       string
	orchMaxParallel int
	orchBudget      float64
	orchDryRun      bool
	orchOutputDir   string
)

// TestSpec defines a single benchmark test
type TestSpec struct {
	Priority int     `json:"priority"`
	GPUType  string  `json:"gpu_type"`
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	MaxPrice float64 `json:"max_price,omitempty"`
	MinVRAM  int     `json:"min_vram,omitempty"`
}

// WorkerState tracks the state of a running worker
type WorkerState struct {
	ID           string
	Test         *TestSpec
	OutputFile   string
	StartTime    time.Time
	LastProgress time.Time
	SessionID    string
	Status       string
	TPS          float64
	Cost         float64
}

// ManifestEntry for API response
type ManifestEntryResp struct {
	ID              string  `json:"id"`
	RunID           string  `json:"run_id"`
	GPUType         string  `json:"gpu_type"`
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	Status          string  `json:"status"`
	TokensPerSecond float64 `json:"tokens_per_second,omitempty"`
	TotalCost       float64 `json:"total_cost,omitempty"`
	FailureReason   string  `json:"failure_reason,omitempty"`
	FailureStage    string  `json:"failure_stage,omitempty"`
	StartedAt       *string `json:"started_at,omitempty"`
	CompletedAt     *string `json:"completed_at,omitempty"`
}

var orchestratorCmd = &cobra.Command{
	Use:   "orchestrator",
	Short: "Benchmark orchestration commands",
	Long: `Run and monitor benchmark orchestration.

The orchestrator manages parallel benchmark workers to test
GPU performance across different hardware and models.`,
}

var orchRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run benchmark orchestration",
	Long: `Run a set of benchmark tests in parallel.

This command:
1. Validates GPU availability in inventory
2. Creates manifest entries for each test
3. Spawns parallel workers (up to --parallel limit)
4. Monitors progress and handles timeouts
5. Reports results

Example:
  gpu-shopper orchestrator run --budget 15

Tests are defined in priority order (P0-P2). The orchestrator
runs highest priority tests first and stops when budget is exhausted.`,
	RunE: runOrchestrator,
}

var orchStatusCmd = &cobra.Command{
	Use:   "status [run-id]",
	Short: "Check status of a benchmark run",
	RunE:  runOrchStatus,
}

var orchAbortCmd = &cobra.Command{
	Use:   "abort [run-id]",
	Short: "Abort a running benchmark",
	RunE:  runOrchAbort,
}

func init() {
	rootCmd.AddCommand(orchestratorCmd)
	orchestratorCmd.AddCommand(orchRunCmd)
	orchestratorCmd.AddCommand(orchStatusCmd)
	orchestratorCmd.AddCommand(orchAbortCmd)

	// Run flags
	orchRunCmd.Flags().StringVar(&orchRunID, "run-id", "", "Run ID (default: auto-generated)")
	orchRunCmd.Flags().IntVar(&orchMaxParallel, "parallel", 3, "Max parallel workers")
	orchRunCmd.Flags().Float64Var(&orchBudget, "budget", 15.0, "Budget limit in dollars")
	orchRunCmd.Flags().BoolVar(&orchDryRun, "dry-run", false, "Validate only, don't run tests")
	orchRunCmd.Flags().StringVar(&orchOutputDir, "output-dir", "/tmp/bench_workers", "Worker output directory")
}

// Default test matrix - updated based on actual inventory availability (Feb 6, 2026)
func getDefaultTestMatrix() []TestSpec {
	return []TestSpec{
		// P0 - Highest priority (flagship tests on available GPUs)
		{Priority: 0, GPUType: "H100 SXM", Provider: "vastai", Model: "llama3:70b", MinVRAM: 48},
		{Priority: 0, GPUType: "RTX 5090", Provider: "vastai", Model: "llama3:8b", MinVRAM: 8},
		{Priority: 0, GPUType: "RTX 4090", Provider: "vastai", Model: "mistral:7b", MinVRAM: 8},

		// P1 - High priority (new Blackwell GPUs and comparisons)
		{Priority: 1, GPUType: "RTX 5080", Provider: "vastai", Model: "llama3:8b", MinVRAM: 8},
		{Priority: 1, GPUType: "RTX 5070 Ti", Provider: "vastai", Model: "phi3:mini", MinVRAM: 4},
		{Priority: 1, GPUType: "RTX 4090", Provider: "tensordock", Model: "mistral:7b", MinVRAM: 8},

		// P2 - Lower priority (provider comparison, large models)
		{Priority: 2, GPUType: "L40S", Provider: "tensordock", Model: "codellama:34b", MinVRAM: 24},
		{Priority: 2, GPUType: "RTX 3090", Provider: "vastai", Model: "llama3:8b", MinVRAM: 8},
		{Priority: 2, GPUType: "H200 NVL", Provider: "vastai", Model: "llama3:70b", MinVRAM: 80},
	}
}

func runOrchestrator(cmd *cobra.Command, args []string) error {
	tests := getDefaultTestMatrix()

	if orchRunID == "" {
		orchRunID = fmt.Sprintf("run-%s", time.Now().Format("2006-01-02-150405"))
	}

	// Create output directory
	if err := os.MkdirAll(orchOutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	fmt.Println("========================================")
	fmt.Println("    BENCHMARK ORCHESTRATOR")
	fmt.Println("========================================")
	fmt.Printf("Run ID:         %s\n", orchRunID)
	fmt.Printf("Max Parallel:   %d\n", orchMaxParallel)
	fmt.Printf("Budget:         $%.2f\n", orchBudget)
	fmt.Printf("Tests:          %d\n", len(tests))
	fmt.Printf("Output Dir:     %s\n", orchOutputDir)
	fmt.Println()

	// Phase 1: Validate inventory
	fmt.Println("Phase 1: Validating GPU availability...")
	validTests, err := validateInventory(tests)
	if err != nil {
		return fmt.Errorf("inventory validation failed: %w", err)
	}

	if len(validTests) == 0 {
		fmt.Println("No tests have available GPUs. Exiting.")
		return nil
	}

	fmt.Printf("Valid tests: %d/%d\n\n", len(validTests), len(tests))

	// Estimate total cost
	totalEstimate := estimateTotalCost(validTests)
	fmt.Printf("Estimated total cost: $%.2f\n", totalEstimate)
	if totalEstimate > orchBudget {
		fmt.Printf("Warning: Estimate exceeds budget. Will stop at $%.2f\n", orchBudget)
	}
	fmt.Println()

	if orchDryRun {
		fmt.Println("Dry run mode - not executing tests")
		printTestMatrix(validTests)
		return nil
	}

	// Phase 2: Run tests
	fmt.Println("Phase 2: Running benchmarks...")
	return runBenchmarkOrchestration(validTests)
}

func validateInventory(tests []TestSpec) ([]TestSpec, error) {
	var valid []TestSpec

	for _, test := range tests {
		reqURL := fmt.Sprintf("%s/api/v1/inventory?gpu_type=%s&provider=%s&min_vram=%d",
			serverURL, url.QueryEscape(test.GPUType), url.QueryEscape(test.Provider), test.MinVRAM)

		resp, err := http.Get(reqURL)
		if err != nil {
			fmt.Printf("  [SKIP] %s/%s/%s - API error: %v\n",
				test.GPUType, test.Provider, test.Model, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("  [SKIP] %s/%s/%s - HTTP %d\n",
				test.GPUType, test.Provider, test.Model, resp.StatusCode)
			continue
		}

		var result struct {
			Offers []struct {
				ID      string  `json:"id"`
				Price   float64 `json:"price_per_hour"`
				GPUType string  `json:"gpu_type"`
				VRAMGiB int     `json:"vram_gib"`
			} `json:"offers"`
			Count int `json:"count"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			fmt.Printf("  [SKIP] %s/%s/%s - Parse error: %v\n",
				test.GPUType, test.Provider, test.Model, err)
			continue
		}

		if result.Count == 0 {
			fmt.Printf("  [SKIP] %s/%s/%s - No offers\n",
				test.GPUType, test.Provider, test.Model)
			continue
		}

		// Find cheapest offer
		var cheapest float64 = 999999
		for _, o := range result.Offers {
			if o.Price < cheapest {
				cheapest = o.Price
			}
		}

		fmt.Printf("  [OK]   %s/%s/%s - %d offers (from $%.2f/hr)\n",
			test.GPUType, test.Provider, test.Model, result.Count, cheapest)
		valid = append(valid, test)
	}

	return valid, nil
}

func estimateTotalCost(tests []TestSpec) float64 {
	// Estimate ~20 minutes per test at average price
	avgPricePerHour := 1.0
	hoursPerTest := 0.33 // 20 minutes
	return float64(len(tests)) * avgPricePerHour * hoursPerTest
}

func printTestMatrix(tests []TestSpec) {
	fmt.Println("\nTest Matrix:")
	fmt.Println("--------------------------------------------------")
	fmt.Printf("%-3s %-15s %-12s %-20s\n", "P", "GPU", "Provider", "Model")
	fmt.Println("--------------------------------------------------")
	for _, t := range tests {
		fmt.Printf("P%-2d %-15s %-12s %-20s\n",
			t.Priority, t.GPUType, t.Provider, t.Model)
	}
}

func runBenchmarkOrchestration(tests []TestSpec) error {
	var (
		mu             sync.Mutex
		activeWorkers  = make(map[string]*WorkerState)
		completedTests []TestSpec
		failedTests    []TestSpec
		totalSpent     float64
		testQueue      = make([]TestSpec, len(tests))
	)

	copy(testQueue, tests)

	// Worker output watcher
	watcher := time.NewTicker(10 * time.Second)
	defer watcher.Stop()

	// Timeout checker
	timeout := time.NewTicker(30 * time.Second)
	defer timeout.Stop()

	// Main orchestration loop
	fmt.Println("\nStarting orchestration loop...")
	fmt.Printf("Press Ctrl+C to abort\n\n")

	for len(testQueue) > 0 || len(activeWorkers) > 0 {
		mu.Lock()

		// Check budget
		if totalSpent >= orchBudget {
			fmt.Printf("\nBudget exhausted ($%.2f spent). Stopping.\n", totalSpent)
			mu.Unlock()
			break
		}

		// Launch new workers if capacity available
		for len(activeWorkers) < orchMaxParallel && len(testQueue) > 0 {
			test := testQueue[0]
			testQueue = testQueue[1:]

			workerID := fmt.Sprintf("w%d", time.Now().UnixNano()%10000)
			outputFile := filepath.Join(orchOutputDir, fmt.Sprintf("worker_%s.log", workerID))

			worker := &WorkerState{
				ID:           workerID,
				Test:         &test,
				OutputFile:   outputFile,
				StartTime:    time.Now(),
				LastProgress: time.Now(),
				Status:       "starting",
			}
			activeWorkers[workerID] = worker

			fmt.Printf("[%s] STARTING: %s/%s/%s\n",
				workerID, test.GPUType, test.Provider, test.Model)

			// Start worker in background
			go runWorker(worker, &mu)
		}

		mu.Unlock()

		// Wait for events
		select {
		case <-watcher.C:
			mu.Lock()
			checkWorkerProgress(activeWorkers, &completedTests, &failedTests, &totalSpent)
			printStatus(activeWorkers, completedTests, failedTests, totalSpent, len(testQueue))
			mu.Unlock()

		case <-timeout.C:
			mu.Lock()
			checkTimeouts(activeWorkers, &failedTests)
			mu.Unlock()
		}
	}

	// Final summary
	fmt.Println("\n========================================")
	fmt.Println("    ORCHESTRATION COMPLETE")
	fmt.Println("========================================")
	fmt.Printf("Completed: %d\n", len(completedTests))
	fmt.Printf("Failed:    %d\n", len(failedTests))
	fmt.Printf("Spent:     $%.2f\n", totalSpent)

	return nil
}

func runWorker(worker *WorkerState, mu *sync.Mutex) {
	// Create output file
	f, err := os.Create(worker.OutputFile)
	if err != nil {
		mu.Lock()
		worker.Status = "failed"
		mu.Unlock()
		return
	}
	defer f.Close()

	writeLog := func(msg string) {
		ts := time.Now().Format("2006-01-02T15:04:05")
		fmt.Fprintf(f, "[%s] %s\n", ts, msg)
		f.Sync()
	}

	writeLog(fmt.Sprintf("STATUS: STARTING gpu=%s model=%s worker=%s",
		worker.Test.GPUType, worker.Test.Model, worker.ID))

	// Step 1: Query inventory
	mu.Lock()
	worker.Status = "querying"
	mu.Unlock()

	invURL := fmt.Sprintf("%s/api/v1/inventory?gpu_type=%s&provider=%s&min_vram=%d&limit=5",
		serverURL, url.QueryEscape(worker.Test.GPUType), url.QueryEscape(worker.Test.Provider), worker.Test.MinVRAM)

	resp, err := http.Get(invURL)
	if err != nil {
		writeLog(fmt.Sprintf("ERROR: stage=inventory message=%q", err.Error()))
		mu.Lock()
		worker.Status = "failed"
		mu.Unlock()
		return
	}
	defer resp.Body.Close()

	var invResult struct {
		Offers []struct {
			ID      string  `json:"id"`
			Price   float64 `json:"price_per_hour"`
			GPUType string  `json:"gpu_type"`
		} `json:"offers"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&invResult); err != nil || len(invResult.Offers) == 0 {
		writeLog("ERROR: stage=inventory message=\"no offers found\"")
		mu.Lock()
		worker.Status = "failed"
		mu.Unlock()
		return
	}

	selectedOffer := invResult.Offers[0]
	writeLog(fmt.Sprintf("STATUS: INVENTORY_QUERY offers_found=%d selected=%s price=%.2f",
		len(invResult.Offers), selectedOffer.ID, selectedOffer.Price))

	// Step 2: Provision session
	mu.Lock()
	worker.Status = "provisioning"
	mu.Unlock()

	provisionReq := map[string]interface{}{
		"consumer_id":       fmt.Sprintf("benchmark-%s-%s", orchRunID, worker.ID),
		"offer_id":          selectedOffer.ID,
		"workload_type":     "interactive",
		"reservation_hours": 2,
	}

	reqBody, _ := json.Marshal(provisionReq)
	provResp, err := http.Post(
		fmt.Sprintf("%s/api/v1/sessions", serverURL),
		"application/json",
		strings.NewReader(string(reqBody)),
	)
	if err != nil {
		writeLog(fmt.Sprintf("ERROR: stage=provision message=%q", err.Error()))
		mu.Lock()
		worker.Status = "failed"
		mu.Unlock()
		return
	}
	defer provResp.Body.Close()

	if provResp.StatusCode != http.StatusCreated && provResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(provResp.Body)
		writeLog(fmt.Sprintf("ERROR: stage=provision message=%q", string(body)))
		mu.Lock()
		worker.Status = "failed"
		mu.Unlock()
		return
	}

	var sessionResult struct {
		Session struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			SSHHost string `json:"ssh_host"`
			SSHPort int    `json:"ssh_port"`
			SSHUser string `json:"ssh_user"`
		} `json:"session"`
		SSHPrivateKey string `json:"ssh_private_key"`
	}

	if err := json.NewDecoder(provResp.Body).Decode(&sessionResult); err != nil {
		writeLog(fmt.Sprintf("ERROR: stage=provision message=%q", err.Error()))
		mu.Lock()
		worker.Status = "failed"
		mu.Unlock()
		return
	}

	mu.Lock()
	worker.SessionID = sessionResult.Session.ID
	worker.Status = "waiting_ssh"
	mu.Unlock()

	writeLog(fmt.Sprintf("STATUS: PROVISIONING session_id=%s", sessionResult.Session.ID))

	// Step 3: Wait for SSH ready
	maxWait := 10 * time.Minute
	pollInterval := 15 * time.Second
	deadline := time.Now().Add(maxWait)

	for time.Now().Before(deadline) {
		statusResp, err := http.Get(fmt.Sprintf("%s/api/v1/sessions/%s", serverURL, sessionResult.Session.ID))
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		var statusResult struct {
			Session struct {
				Status  string `json:"status"`
				SSHHost string `json:"ssh_host"`
				SSHPort int    `json:"ssh_port"`
				Error   string `json:"error"`
			} `json:"session"`
		}

		if err := json.NewDecoder(statusResp.Body).Decode(&statusResult); err != nil {
			statusResp.Body.Close()
			time.Sleep(pollInterval)
			continue
		}
		statusResp.Body.Close()

		if statusResult.Session.Status == "running" {
			writeLog(fmt.Sprintf("STATUS: SSH_READY host=%s port=%d",
				statusResult.Session.SSHHost, statusResult.Session.SSHPort))

			mu.Lock()
			worker.Status = "ssh_ready"
			worker.LastProgress = time.Now()
			mu.Unlock()
			break
		}

		if statusResult.Session.Status == "failed" {
			writeLog(fmt.Sprintf("ERROR: stage=provision message=%q", statusResult.Session.Error))
			mu.Lock()
			worker.Status = "failed"
			mu.Unlock()
			// Cleanup is handled by server
			return
		}

		time.Sleep(pollInterval)
	}

	mu.Lock()
	if worker.Status != "ssh_ready" {
		worker.Status = "timeout"
		mu.Unlock()
		writeLog("ERROR: stage=ssh_wait message=\"timeout waiting for SSH\"")
		cleanupSession(worker.SessionID)
		return
	}
	mu.Unlock()

	// For now, simulate benchmark progress since actual SSH execution
	// would require key handling and shell execution
	writeLog("STATUS: BENCHMARK_SIMULATED (SSH execution not implemented in CLI)")
	writeLog("NOTE: Use Claude agent for full benchmark execution with SSH")

	// Mark as completed with simulated results
	mu.Lock()
	worker.Status = "completed"
	worker.TPS = 0                           // No actual benchmark
	worker.Cost = selectedOffer.Price * 0.05 // ~3 minutes
	mu.Unlock()

	writeLog(fmt.Sprintf("STATUS: COMPLETED cost=%.2f", worker.Cost))

	// Cleanup
	cleanupSession(sessionResult.Session.ID)
	writeLog(fmt.Sprintf("STATUS: CLEANUP session_id=%s", sessionResult.Session.ID))
}

func cleanupSession(sessionID string) {
	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/api/v1/sessions/%s/done", serverURL, sessionID),
		nil)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func checkWorkerProgress(workers map[string]*WorkerState, completed, failed *[]TestSpec, spent *float64) {
	statusRegex := regexp.MustCompile(`STATUS:\s+(\w+)`)
	progressRegex := regexp.MustCompile(`tps=([\d.]+)`)

	for id, w := range workers {
		if w.Status == "completed" || w.Status == "failed" || w.Status == "timeout" {
			*spent += w.Cost
			if w.Status == "completed" {
				*completed = append(*completed, *w.Test)
			} else {
				*failed = append(*failed, *w.Test)
			}
			delete(workers, id)
			continue
		}

		// Read latest output
		f, err := os.Open(w.OutputFile)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		var lastLine string
		for scanner.Scan() {
			lastLine = scanner.Text()
		}
		f.Close()

		if lastLine != "" {
			// Update last progress time
			w.LastProgress = time.Now()

			// Extract status
			if m := statusRegex.FindStringSubmatch(lastLine); len(m) > 1 {
				w.Status = strings.ToLower(m[1])
			}

			// Extract TPS if available
			if m := progressRegex.FindStringSubmatch(lastLine); len(m) > 1 {
				fmt.Sscanf(m[1], "%f", &w.TPS)
			}
		}
	}
}

func checkTimeouts(workers map[string]*WorkerState, failed *[]TestSpec) {
	const maxIdleTime = 5 * time.Minute
	const maxTotalTime = 25 * time.Minute

	now := time.Now()
	for id, w := range workers {
		idleDuration := now.Sub(w.LastProgress)
		totalDuration := now.Sub(w.StartTime)

		if idleDuration > maxIdleTime {
			fmt.Printf("[%s] TIMEOUT: No progress for %v\n", id, idleDuration.Round(time.Second))
			w.Status = "timeout"
			*failed = append(*failed, *w.Test)
			if w.SessionID != "" {
				cleanupSession(w.SessionID)
			}
			delete(workers, id)
		} else if totalDuration > maxTotalTime {
			fmt.Printf("[%s] TIMEOUT: Total time exceeded %v\n", id, maxTotalTime)
			w.Status = "timeout"
			*failed = append(*failed, *w.Test)
			if w.SessionID != "" {
				cleanupSession(w.SessionID)
			}
			delete(workers, id)
		}
	}
}

func printStatus(workers map[string]*WorkerState, completed, failed []TestSpec, spent float64, queued int) {
	fmt.Printf("\n--- Status @ %s ---\n", time.Now().Format("15:04:05"))
	fmt.Printf("Active: %d | Completed: %d | Failed: %d | Queued: %d | Spent: $%.2f\n",
		len(workers), len(completed), len(failed), queued, spent)

	for id, w := range workers {
		duration := time.Since(w.StartTime).Round(time.Second)
		fmt.Printf("  [%s] %s - %s/%s - %v\n",
			id, w.Status, w.Test.GPUType, w.Test.Model, duration)
	}
}

func runOrchStatus(cmd *cobra.Command, args []string) error {
	runID := orchRunID
	if len(args) > 0 {
		runID = args[0]
	}
	if runID == "" {
		return fmt.Errorf("run-id is required")
	}

	// Read output files from directory
	files, err := filepath.Glob(filepath.Join(orchOutputDir, "worker_*.log"))
	if err != nil {
		return err
	}

	fmt.Printf("Status for run: %s\n", runID)
	fmt.Printf("Found %d worker logs\n\n", len(files))

	for _, f := range files {
		fmt.Printf("=== %s ===\n", filepath.Base(f))

		file, err := os.Open(f)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(file)
		lineCount := 0
		for scanner.Scan() {
			if lineCount >= 10 {
				break
			}
			fmt.Println(scanner.Text())
			lineCount++
		}
		file.Close()

		if lineCount >= 10 {
			fmt.Println("...")
		}
		fmt.Println()
	}

	return nil
}

func runOrchAbort(cmd *cobra.Command, args []string) error {
	fmt.Println("Abort functionality - would terminate all running sessions")
	fmt.Println("For now, use: curl -X DELETE http://localhost:8080/api/v1/sessions/{id}")
	return nil
}
