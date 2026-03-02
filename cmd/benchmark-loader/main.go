// Command benchmark-loader loads benchmark results from directories into the database.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/benchmark"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	dbPath := flag.String("db", "./data/gpu-shopper.db", "Path to SQLite database")
	dataDir := flag.String("dir", "", "Directory containing benchmark results (required)")
	provider := flag.String("provider", "", "Provider name (vastai, tensordock)")
	pricePerHour := flag.Float64("price", 0, "Price per hour for this GPU")
	location := flag.String("location", "", "Location/region")
	flag.Parse()

	if *dataDir == "" {
		log.Fatal("--dir is required")
	}

	// Open database
	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create store
	store, err := benchmark.NewStore(db)
	if err != nil {
		log.Fatalf("Failed to create store: %v", err)
	}

	// Find all benchmark directories
	entries, err := os.ReadDir(*dataDir)
	if err != nil {
		log.Fatalf("Failed to read directory: %v", err)
	}

	ctx := context.Background()
	loaded := 0

	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "benchmark_") {
			continue
		}

		benchDir := filepath.Join(*dataDir, entry.Name())
		metaPath := filepath.Join(benchDir, "metadata.json")
		resultsPath := filepath.Join(benchDir, "results.jsonl")
		gpuPath := filepath.Join(benchDir, "gpu.csv")

		// Check if metadata exists
		if _, err := os.Stat(metaPath); os.IsNotExist(err) {
			log.Printf("Skipping %s: no metadata.json", entry.Name())
			continue
		}

		// Load benchmark
		result, err := benchmark.LoadBenchmarkFromDirectory(benchDir, *provider, *location, *pricePerHour)
		if err != nil {
			log.Printf("Failed to load %s: %v", entry.Name(), err)
			continue
		}

		// Check if results.jsonl exists and has data
		if _, err := os.Stat(resultsPath); os.IsNotExist(err) {
			log.Printf("Skipping %s: no results.jsonl", entry.Name())
			continue
		}

		// Check if gpu.csv exists
		if _, err := os.Stat(gpuPath); os.IsNotExist(err) {
			log.Printf("Warning: %s has no gpu.csv", entry.Name())
		}

		// Save to database
		if err := store.Save(ctx, result); err != nil {
			log.Printf("Failed to save %s: %v", entry.Name(), err)
			continue
		}

		fmt.Printf("Loaded: %s - %s on %s (%.1f TPS)\n",
			result.Model.Name, result.Hardware.GPUName,
			result.Provider, result.Results.AvgTokensPerSecond)
		loaded++
	}

	fmt.Printf("\nLoaded %d benchmarks\n", loaded)
}
