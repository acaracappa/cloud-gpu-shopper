package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/test/benchmark/models"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/test/benchmark/runner"
)

var (
	serverURL   string
	dbPath      string
	ansiblePath string
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "benchmark",
	Short: "GPU benchmark suite for LLM inference testing",
	Long: `A comprehensive benchmark suite that provisions real GPUs,
deploys vLLM with various model sizes, and runs performance tests.

The benchmark suite measures:
- Throughput (tokens/second)
- Latency (time to first token)
- Concurrency (max parallel requests)
- Cost efficiency (tokens per dollar)

Results are stored for historical comparison and used to generate
recommendations for optimal GPU selection per model.`,
}

// run command
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run benchmark suite",
	Long: `Run the benchmark suite against one or more model/GPU combinations.

Examples:
  # Run benchmarks for all models on all compatible GPUs
  benchmark run --all

  # Benchmark specific model on specific GPU
  benchmark run --model=mistral-7b --gpu=RTX4090

  # Benchmark with cost/time limits
  benchmark run --model=qwen-72b --max-cost=10 --max-duration=2h

  # Dry run to see what would be benchmarked
  benchmark run --model=mistral-7b --dry-run`,
	RunE: runBenchmark,
}

var (
	runModel       string
	runGPU         string
	runAll         bool
	runMaxCost     float64
	runMaxDuration time.Duration
	runDryRun      bool
	runProvider    string
)

// recommend command
var recommendCmd = &cobra.Command{
	Use:   "recommend",
	Short: "Get GPU recommendation for a model",
	Long: `Get GPU recommendations based on historical benchmark data.

The recommendation engine considers:
- Cost efficiency (tokens per dollar)
- Raw throughput (tokens per second)
- Latency (time to first token)
- Data recency and sample size

Examples:
  # Get cost-optimized recommendation
  benchmark recommend --model=mistral-7b

  # Get throughput-optimized recommendation
  benchmark recommend --model=mistral-7b --optimize=throughput

  # Get latency-optimized recommendation
  benchmark recommend --model=mistral-7b --optimize=latency`,
	RunE: recommendGPU,
}

var (
	recommendModel    string
	recommendOptimize string
)

// history command
var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "View benchmark history",
	Long: `View historical benchmark results for a model or GPU.

Examples:
  # View history for a model
  benchmark history --model=mistral-7b

  # View history for a GPU
  benchmark history --gpu=RTX4090

  # View last N results
  benchmark history --model=mistral-7b --limit=10`,
	RunE: showHistory,
}

var (
	historyModel string
	historyGPU   string
	historyLimit int
)

// report command
var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate benchmark report",
	Long: `Generate a comprehensive benchmark report in various formats.

Examples:
  # Generate markdown report for all models
  benchmark report --format=markdown --output=sizing-report.md

  # Generate JSON report for specific model
  benchmark report --format=json --model=mistral-7b --output=mistral-report.json`,
	RunE: generateReport,
}

var (
	reportFormat string
	reportOutput string
	reportModel  string
)

// list command
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available models and GPUs",
	Long: `List all available models and GPUs in the benchmark catalog.

Examples:
  # List all models
  benchmark list models

  # List all GPUs
  benchmark list gpus

  # List compatible GPUs for a model
  benchmark list gpus --model=qwen2.5-72b`,
	RunE: listCatalog,
}

var listModel string

// diagnose command
var diagnoseCmd = &cobra.Command{
	Use:   "diagnose",
	Short: "Diagnose connectivity to a GPU instance",
	Long: `Provision a GPU instance and test SSH connectivity.

This command helps debug the benchmark workflow by:
1. Provisioning a GPU session (or using an existing one)
2. Testing SSH connectivity
3. Checking GPU and Docker availability
4. Optionally keeping the session for manual debugging

Examples:
  # Provision and diagnose a new session
  benchmark diagnose --gpu="RTX 3090"

  # Diagnose an existing session
  benchmark diagnose --session=abc123

  # Keep the session after diagnosis
  benchmark diagnose --gpu="RTX 3090" --no-cleanup`,
	RunE: runDiagnose,
}

var (
	diagnoseGPU       string
	diagnoseProvider  string
	diagnoseSession   string
	diagnoseNoCleanup bool
	diagnoseSSHKey    string
)

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&serverURL, "server", getEnvOrDefault("GPU_SHOPPER_URL", "http://localhost:8080"), "GPU Shopper server URL")
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", getEnvOrDefault("DATABASE_PATH", "./data/gpu-shopper.db"), "Database path")
	rootCmd.PersistentFlags().StringVar(&ansiblePath, "ansible-path", "./test/ansible", "Path to Ansible playbooks")

	// Run command flags
	runCmd.Flags().StringVar(&runModel, "model", "", "Model ID to benchmark (e.g., mistral-7b)")
	runCmd.Flags().StringVar(&runGPU, "gpu", "", "GPU type to benchmark on (e.g., RTX4090)")
	runCmd.Flags().BoolVar(&runAll, "all", false, "Run benchmarks for all model/GPU combinations")
	runCmd.Flags().Float64Var(&runMaxCost, "max-cost", 10.0, "Maximum cost in dollars before aborting")
	runCmd.Flags().DurationVar(&runMaxDuration, "max-duration", 2*time.Hour, "Maximum duration before aborting")
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "Show what would be benchmarked without running")
	runCmd.Flags().StringVar(&runProvider, "provider", "", "Limit to specific provider (vastai, tensordock)")

	// Recommend command flags
	recommendCmd.Flags().StringVar(&recommendModel, "model", "", "Model ID to get recommendation for")
	recommendCmd.Flags().StringVar(&recommendOptimize, "optimize", "cost", "Optimization target: cost, throughput, latency")
	recommendCmd.MarkFlagRequired("model")

	// History command flags
	historyCmd.Flags().StringVar(&historyModel, "model", "", "Model ID to show history for")
	historyCmd.Flags().StringVar(&historyGPU, "gpu", "", "GPU type to show history for")
	historyCmd.Flags().IntVar(&historyLimit, "limit", 20, "Maximum number of results to show")

	// Report command flags
	reportCmd.Flags().StringVar(&reportFormat, "format", "markdown", "Output format: markdown, json")
	reportCmd.Flags().StringVar(&reportOutput, "output", "", "Output file (default: stdout)")
	reportCmd.Flags().StringVar(&reportModel, "model", "", "Limit report to specific model")

	// List command flags
	listCmd.Flags().StringVar(&listModel, "model", "", "Filter GPUs compatible with this model")

	// Diagnose command flags
	diagnoseCmd.Flags().StringVar(&diagnoseGPU, "gpu", "RTX 3090", "GPU type to provision")
	diagnoseCmd.Flags().StringVar(&diagnoseProvider, "provider", "", "Limit to specific provider")
	diagnoseCmd.Flags().StringVar(&diagnoseSession, "session", "", "Existing session ID to diagnose")
	diagnoseCmd.Flags().BoolVar(&diagnoseNoCleanup, "no-cleanup", false, "Don't cleanup session after diagnosis")
	diagnoseCmd.Flags().StringVar(&diagnoseSSHKey, "ssh-key", "./test/benchmark/.keys/benchmark_key", "Path to SSH private key (will use .pub for public key)")

	// Add commands to root
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(recommendCmd)
	rootCmd.AddCommand(historyCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(diagnoseCmd)
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func runBenchmark(cmd *cobra.Command, args []string) error {
	catalog := models.NewCatalog()

	// Validate flags
	if !runAll && runModel == "" {
		return fmt.Errorf("either --all or --model is required")
	}

	if runModel != "" {
		if _, ok := catalog.GetModel(runModel); !ok {
			return fmt.Errorf("unknown model: %s", runModel)
		}
	}

	if runGPU != "" {
		if _, ok := catalog.GetGPU(runGPU); !ok {
			return fmt.Errorf("unknown GPU: %s", runGPU)
		}
	}

	// Build benchmark plan
	var benchmarks []benchmarkPlan
	if runAll {
		for _, modelID := range catalog.ModelList() {
			model, _ := catalog.GetModel(modelID)
			gpus := catalog.GetCompatibleGPUs(modelID)
			for _, gpu := range gpus {
				if runProvider != "" && !contains(gpu.Providers, runProvider) {
					continue
				}
				benchmarks = append(benchmarks, benchmarkPlan{
					Model: model,
					GPU:   gpu,
				})
			}
		}
	} else {
		model, _ := catalog.GetModel(runModel)
		if runGPU != "" {
			gpu, _ := catalog.GetGPU(runGPU)
			benchmarks = append(benchmarks, benchmarkPlan{
				Model: model,
				GPU:   gpu,
			})
		} else {
			gpus := catalog.GetCompatibleGPUs(runModel)
			for _, gpu := range gpus {
				if runProvider != "" && !contains(gpu.Providers, runProvider) {
					continue
				}
				benchmarks = append(benchmarks, benchmarkPlan{
					Model: model,
					GPU:   gpu,
				})
			}
		}
	}

	if len(benchmarks) == 0 {
		return fmt.Errorf("no valid benchmark combinations found")
	}

	// Estimate costs
	estimatedCost := 0.0
	estimatedDuration := 0 * time.Minute
	for _, b := range benchmarks {
		// Assume ~30 minutes per benchmark
		estimatedCost += catalog.EstimateCost(b.GPU.Type, 30)
		estimatedDuration += 30 * time.Minute
	}

	fmt.Printf("Benchmark Plan:\n")
	fmt.Printf("  Combinations: %d\n", len(benchmarks))
	fmt.Printf("  Estimated cost: $%.2f\n", estimatedCost)
	fmt.Printf("  Estimated duration: %s\n", estimatedDuration)
	fmt.Printf("  Max cost limit: $%.2f\n", runMaxCost)
	fmt.Printf("  Max duration limit: %s\n", runMaxDuration)
	fmt.Println()

	if estimatedCost > runMaxCost {
		return fmt.Errorf("estimated cost ($%.2f) exceeds max-cost limit ($%.2f)", estimatedCost, runMaxCost)
	}

	fmt.Println("Benchmarks to run:")
	for i, b := range benchmarks {
		fmt.Printf("  %d. %s on %s (~$%.2f)\n", i+1, b.Model.ID, b.GPU.Type, catalog.EstimateCost(b.GPU.Type, 30))
	}
	fmt.Println()

	if runDryRun {
		fmt.Println("Dry run - no benchmarks will be executed")
		return nil
	}

	// Create runner configuration
	config := runner.DefaultConfig()
	config.ServerURL = serverURL
	config.DatabasePath = dbPath
	config.AnsiblePath = ansiblePath
	config.MaxCost = runMaxCost
	config.MaxDuration = runMaxDuration
	config.AlertCost = runMaxCost * 0.5
	config.AlertDuration = time.Duration(float64(runMaxDuration) * 0.75)

	// Create runner
	r, err := runner.NewRunner(config)
	if err != nil {
		return fmt.Errorf("failed to create runner: %w", err)
	}
	defer r.Close()

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		fmt.Printf("\nReceived signal %v, initiating graceful shutdown...\n", sig)
		cancel()
	}()

	// Execute benchmarks
	var successCount, failCount int
	for i, b := range benchmarks {
		select {
		case <-ctx.Done():
			fmt.Println("\nShutdown requested, skipping remaining benchmarks")
			break
		default:
		}

		fmt.Printf("\n=== Benchmark %d/%d: %s on %s ===\n", i+1, len(benchmarks), b.Model.ID, b.GPU.Type)

		plan := &runner.BenchmarkPlan{
			Model:         b.Model,
			GPU:           b.GPU,
			Provider:      runProvider,
			EstimatedCost: catalog.EstimateCost(b.GPU.Type, 30),
		}

		result, err := r.Run(ctx, plan)
		if err != nil {
			fmt.Printf("Benchmark failed: %v\n", err)
			failCount++
			continue
		}

		if result.Status == "complete" {
			successCount++
			fmt.Printf("Benchmark completed successfully!\n")
			if result.Results != nil && result.Results.Throughput != nil {
				fmt.Printf("  Throughput: %.2f tok/s\n", result.Results.Throughput.TokensPerSecond)
			}
			if result.Results != nil && result.Results.Latency != nil {
				fmt.Printf("  TTFT: %.2f ms\n", result.Results.Latency.TTFTMs)
			}
		} else {
			failCount++
			fmt.Printf("Benchmark status: %s\n", result.Status)
			if len(result.FailedSteps) > 0 {
				fmt.Printf("  Failed steps: %s\n", strings.Join(result.FailedSteps, ", "))
			}
		}
	}

	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("  Total: %d benchmarks\n", len(benchmarks))
	fmt.Printf("  Success: %d\n", successCount)
	fmt.Printf("  Failed: %d\n", failCount)

	if failCount > 0 {
		return fmt.Errorf("%d benchmark(s) failed", failCount)
	}

	return nil
}

type benchmarkPlan struct {
	Model *models.Model
	GPU   *models.GPU
}

func recommendGPU(cmd *cobra.Command, args []string) error {
	catalog := models.NewCatalog()

	model, ok := catalog.GetModel(recommendModel)
	if !ok {
		return fmt.Errorf("unknown model: %s", recommendModel)
	}

	// Validate optimization target
	validOptimize := map[string]bool{"cost": true, "throughput": true, "latency": true}
	if !validOptimize[recommendOptimize] {
		return fmt.Errorf("invalid optimization target: %s (use: cost, throughput, latency)", recommendOptimize)
	}

	fmt.Printf("Recommendation for %s (%s)\n", model.Name, model.ID)
	fmt.Printf("Optimization target: %s\n\n", recommendOptimize)

	// TODO: Query database for historical results
	fmt.Println("Recommendation engine not yet implemented")
	fmt.Println("This will query historical benchmark data and return:")
	fmt.Println("  - Top 3 GPU recommendations with confidence scores")
	fmt.Println("  - Key metrics (throughput, latency, cost/1k tokens)")
	fmt.Println("  - Sample size and data recency")

	// Show compatible GPUs as placeholder
	gpus := catalog.GetCompatibleGPUs(recommendModel)
	fmt.Printf("\nCompatible GPUs (min %dGB VRAM):\n", model.MinVRAMGB)
	for _, gpu := range gpus {
		fmt.Printf("  - %s (%dGB, ~$%.2f/hr)\n", gpu.Type, gpu.VRAMGB, gpu.TypicalPriceHr)
	}

	return nil
}

func showHistory(cmd *cobra.Command, args []string) error {
	if historyModel == "" && historyGPU == "" {
		return fmt.Errorf("either --model or --gpu is required")
	}

	catalog := models.NewCatalog()

	if historyModel != "" {
		if _, ok := catalog.GetModel(historyModel); !ok {
			return fmt.Errorf("unknown model: %s", historyModel)
		}
		fmt.Printf("Benchmark History: %s\n\n", historyModel)
	}

	if historyGPU != "" {
		if _, ok := catalog.GetGPU(historyGPU); !ok {
			return fmt.Errorf("unknown GPU: %s", historyGPU)
		}
		fmt.Printf("Benchmark History: %s\n\n", historyGPU)
	}

	// TODO: Query database for historical results
	fmt.Println("History display not yet implemented")
	fmt.Println("This will show:")
	fmt.Println("  - Past benchmark runs with dates")
	fmt.Println("  - Metrics for each run")
	fmt.Println("  - Trend analysis")

	return nil
}

func generateReport(cmd *cobra.Command, args []string) error {
	validFormats := map[string]bool{"markdown": true, "json": true}
	if !validFormats[reportFormat] {
		return fmt.Errorf("invalid format: %s (use: markdown, json)", reportFormat)
	}

	catalog := models.NewCatalog()

	if reportModel != "" {
		if _, ok := catalog.GetModel(reportModel); !ok {
			return fmt.Errorf("unknown model: %s", reportModel)
		}
	}

	scope := "all models"
	if reportModel != "" {
		scope = reportModel
	}

	fmt.Printf("Generating %s report for %s\n", reportFormat, scope)
	if reportOutput != "" {
		fmt.Printf("Output file: %s\n", reportOutput)
	}

	// TODO: Generate report from database
	fmt.Println("\nReport generation not yet implemented")
	fmt.Println("This will generate:")
	fmt.Println("  - Summary statistics")
	fmt.Println("  - Recommendations table")
	fmt.Println("  - Full results table")
	fmt.Println("  - Cost summary")

	return nil
}

func listCatalog(cmd *cobra.Command, args []string) error {
	catalog := models.NewCatalog()

	if len(args) == 0 {
		return fmt.Errorf("specify what to list: models or gpus")
	}

	switch strings.ToLower(args[0]) {
	case "models":
		fmt.Println("Available Models:")
		fmt.Println()
		fmt.Printf("%-15s %-35s %-10s %-12s %s\n", "ID", "Name", "Params", "Min VRAM", "Tier")
		fmt.Printf("%s\n", strings.Repeat("-", 85))
		for _, id := range catalog.ModelList() {
			model, _ := catalog.GetModel(id)
			params := fmt.Sprintf("%.1fB", model.ParametersB)
			vram := fmt.Sprintf("%dGB", model.MinVRAMGB)
			fmt.Printf("%-15s %-35s %-10s %-12s %s\n",
				model.ID, model.Name, params, vram, model.Tier)
		}

	case "gpus":
		fmt.Println("Available GPUs:")
		fmt.Println()

		var gpuList []*models.GPU
		if listModel != "" {
			model, ok := catalog.GetModel(listModel)
			if !ok {
				return fmt.Errorf("unknown model: %s", listModel)
			}
			fmt.Printf("(Compatible with %s, min %dGB VRAM)\n\n", listModel, model.MinVRAMGB)
			gpuList = catalog.GetCompatibleGPUs(listModel)
		} else {
			for _, t := range catalog.GPUList() {
				gpu, _ := catalog.GetGPU(t)
				gpuList = append(gpuList, gpu)
			}
		}

		fmt.Printf("%-12s %-10s %-12s %s\n", "Type", "VRAM", "~Price/hr", "Providers")
		fmt.Printf("%s\n", strings.Repeat("-", 55))
		for _, gpu := range gpuList {
			vram := fmt.Sprintf("%dGB", gpu.VRAMGB)
			price := fmt.Sprintf("$%.2f", gpu.TypicalPriceHr)
			fmt.Printf("%-12s %-10s %-12s %s\n",
				gpu.Type, vram, price, strings.Join(gpu.Providers, ", "))
		}

	default:
		return fmt.Errorf("unknown list target: %s (use: models or gpus)", args[0])
	}

	return nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func runDiagnose(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nInterrupted, cleaning up...")
		cancel()
	}()

	// Create API client
	apiClient := runner.NewAPIClient(serverURL)

	var session *runner.SessionResponse
	var sshPrivateKey string
	var sshKeyPath string
	var err error

	if diagnoseSession != "" {
		// Use existing session - need a local SSH key
		if _, err := os.Stat(diagnoseSSHKey); err != nil {
			return fmt.Errorf("for existing sessions, you must provide an SSH key with --ssh-key: %w", err)
		}
		sshKeyPath = diagnoseSSHKey

		fmt.Printf("Fetching existing session: %s\n", diagnoseSession)
		session, err = apiClient.GetSession(ctx, diagnoseSession)
		if err != nil {
			return fmt.Errorf("failed to get session: %w", err)
		}
	} else {
		// Provision new session - server generates SSH key
		fmt.Printf("Provisioning %s GPU...\n", diagnoseGPU)

		// Get inventory
		offers, err := apiClient.GetInventory(ctx, diagnoseGPU, 0)
		if err != nil {
			return fmt.Errorf("failed to get inventory: %w", err)
		}

		if len(offers) == 0 {
			return fmt.Errorf("no %s GPUs available", diagnoseGPU)
		}

		// Select cheapest offer
		var bestOffer *runner.InventoryOffer
		for i := range offers {
			offer := &offers[i]
			if !offer.Available {
				continue
			}
			if diagnoseProvider != "" && offer.Provider != diagnoseProvider {
				continue
			}
			if bestOffer == nil || offer.PricePerHour < bestOffer.PricePerHour {
				bestOffer = offer
			}
		}

		if bestOffer == nil {
			return fmt.Errorf("no available offers for %s", diagnoseGPU)
		}

		fmt.Printf("  Selected: %s from %s at $%.2f/hr\n", bestOffer.ID, bestOffer.Provider, bestOffer.PricePerHour)

		// Create session - server generates SSH key pair
		fmt.Printf("  Creating session...\n")
		sessionReq := &runner.SessionRequest{
			ConsumerID:       "benchmark-diagnose",
			OfferID:          bestOffer.ID,
			WorkloadType:     "diagnose",
			ReservationHours: 1,
		}

		createResp, err := apiClient.CreateSession(ctx, sessionReq)
		if err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}

		session = &createResp.Session
		sshPrivateKey = createResp.SSHPrivateKey
		fmt.Printf("  Session created: %s\n", session.ID)

		// Save SSH private key to temp file
		if sshPrivateKey != "" {
			tmpFile, err := os.CreateTemp("", "gpu-benchmark-ssh-*")
			if err != nil {
				return fmt.Errorf("failed to create temp SSH key file: %w", err)
			}
			sshKeyPath = tmpFile.Name()
			defer os.Remove(sshKeyPath) // Clean up temp file when done

			if err := os.WriteFile(sshKeyPath, []byte(sshPrivateKey), 0600); err != nil {
				return fmt.Errorf("failed to write SSH key: %w", err)
			}
			fmt.Printf("  SSH key saved to: %s\n", sshKeyPath)
		} else {
			return fmt.Errorf("server did not return SSH private key")
		}

		// Wait for session to be ready
		fmt.Printf("  Waiting for session to be ready...\n")
		session, err = apiClient.WaitForSession(ctx, session.ID, 10*time.Minute)
		if err != nil {
			// Cleanup on failure
			_ = apiClient.DeleteSession(context.Background(), session.ID)
			return fmt.Errorf("session failed to become ready: %w", err)
		}
	}

	fmt.Printf("\nSession ready:\n")
	fmt.Printf("  ID: %s\n", session.ID)
	fmt.Printf("  Host: %s:%d\n", session.SSHHost, session.SSHPort)
	fmt.Printf("  User: %s\n", session.SSHUser)
	fmt.Printf("  GPU: %s\n", session.GPUType)
	fmt.Printf("  Provider: %s\n", session.Provider)

	// Run diagnostics
	fmt.Printf("\nRunning diagnostics...\n")
	checker := runner.NewSSHChecker(sshKeyPath)
	diag, err := checker.RunDiagnostics(ctx, session.SSHHost, session.SSHPort, session.SSHUser)
	if err != nil {
		fmt.Printf("\nDiagnostics failed: %v\n", err)
	}

	// Print summary
	fmt.Printf("\n=== Diagnosis Summary ===\n")
	fmt.Printf("SSH Connected: %v\n", diag.SSHConnected)
	fmt.Printf("GPU Available: %v\n", diag.GPUAvailable)
	fmt.Printf("Docker Available: %v\n", diag.DockerAvailable)
	fmt.Printf("Ready for Benchmarking: %v\n", diag.IsReady())

	if diag.GPUInfo != "" {
		fmt.Printf("\nGPU Info:\n%s\n", diag.GPUInfo)
	}

	// Cleanup
	if !diagnoseNoCleanup && diagnoseSession == "" {
		fmt.Printf("\nCleaning up session...\n")
		if err := apiClient.DeleteSession(context.Background(), session.ID); err != nil {
			fmt.Printf("Warning: failed to delete session: %v\n", err)
		} else {
			fmt.Printf("Session deleted.\n")
		}
	} else {
		// Save SSH key to a persistent location if this is a new session
		persistentKeyPath := sshKeyPath
		if diagnoseSession == "" && sshPrivateKey != "" {
			persistentKeyPath = fmt.Sprintf("./test/benchmark/.keys/session_%s.key", session.ID[:8])
			if err := os.WriteFile(persistentKeyPath, []byte(sshPrivateKey), 0600); err != nil {
				fmt.Printf("Warning: failed to save SSH key to %s: %v\n", persistentKeyPath, err)
				persistentKeyPath = "(key not saved - was in temp file)"
			}
		}

		fmt.Printf("\nSession kept: %s\n", session.ID)
		fmt.Printf("To connect manually: ssh -i %s -p %d %s@%s\n",
			persistentKeyPath, session.SSHPort, session.SSHUser, session.SSHHost)
		fmt.Printf("To cleanup later: curl -X DELETE %s/api/v1/sessions/%s\n", serverURL, session.ID)
	}

	return nil
}
