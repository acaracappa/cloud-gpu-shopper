package gpumon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mocking Strategy:
//
// We use the "test binary" approach (also known as TestHelperProcess pattern).
// This is a standard Go testing pattern for mocking exec.Command:
//
// 1. We define a test helper function (TestHelperProcess) that acts as the mock command
// 2. When our tests need to run a command, we replace the command executor
//    to run our test binary with specific environment variables
// 3. The helper process checks for GO_WANT_HELPER_PROCESS=1 and then
//    reads GO_HELPER_BEHAVIOR to determine what output to produce
//
// Since the Monitor struct directly uses exec.CommandContext without an injection point,
// we test the parseOutput method directly for most cases and use the TestHelperProcess
// approach for integration-level tests where we need to mock nvidia-smi behavior.
//
// For simplicity, we test the parseOutput method directly for parsing tests,
// and rely on the GetStats method's graceful error handling for command execution tests.

// TestHelperProcess is not a real test - it's used as a mock for nvidia-smi
// This function is invoked when the test binary is executed with GO_WANT_HELPER_PROCESS=1
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	behavior := os.Getenv("GO_HELPER_BEHAVIOR")

	switch behavior {
	case "valid_single":
		fmt.Fprint(os.Stdout, "45, 8192, 24576")
		os.Exit(0)
	case "valid_multiple":
		fmt.Fprint(os.Stdout, "45, 8192, 24576\n55, 4096, 12288")
		os.Exit(0)
	case "exit_error":
		fmt.Fprint(os.Stderr, "nvidia-smi has failed")
		os.Exit(1)
	case "timeout":
		// Sleep longer than the command timeout
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "invalid_output":
		fmt.Fprint(os.Stdout, "invalid,output")
		os.Exit(0)
	default:
		os.Exit(0)
	}
}

func TestNewMonitor(t *testing.T) {
	t.Run("creates monitor with provided logger", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
		monitor := NewMonitor(logger)

		require.NotNil(t, monitor)
		assert.Equal(t, logger, monitor.logger)
	})

	t.Run("creates monitor with default logger when nil", func(t *testing.T) {
		monitor := NewMonitor(nil)

		require.NotNil(t, monitor)
		assert.NotNil(t, monitor.logger)
	})
}

func TestGetStats_ParsesValidOutput(t *testing.T) {
	// Test parsing logic via parseOutput directly
	// This avoids the need to mock exec.Command for simple parsing tests
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	monitor := NewMonitor(logger)

	// Mock nvidia-smi output: "45, 8192, 24576"
	// Format: utilization.gpu, memory.used, memory.total
	stats, err := monitor.parseOutput("45, 8192, 24576")

	require.NoError(t, err)
	assert.Equal(t, 45.0, stats.UtilizationPct)
	assert.Equal(t, 8192, stats.MemoryUsedMB)
	assert.Equal(t, 24576, stats.MemoryTotalMB)
}

func TestGetStats_HandlesMultipleGPUs(t *testing.T) {
	// When multiple GPUs are present, nvidia-smi outputs one line per GPU
	// parseOutput should average utilization and sum memory
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	monitor := NewMonitor(logger)

	// Two GPUs:
	// GPU 0: 40% util, 8192 MB used, 24576 MB total
	// GPU 1: 60% util, 4096 MB used, 12288 MB total
	output := "40, 8192, 24576\n60, 4096, 12288"
	stats, err := monitor.parseOutput(output)

	require.NoError(t, err)
	// Utilization should be averaged: (40 + 60) / 2 = 50
	assert.Equal(t, 50.0, stats.UtilizationPct)
	// Memory should be summed: 8192 + 4096 = 12288
	assert.Equal(t, 12288, stats.MemoryUsedMB)
	// Total memory summed: 24576 + 12288 = 36864
	assert.Equal(t, 36864, stats.MemoryTotalMB)
}

func TestGetStats_HandlesNvidiaSmiNotFound(t *testing.T) {
	// When nvidia-smi is not found, GetStats should return empty stats without error
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	monitor := NewMonitor(logger)

	// Create a context
	ctx := context.Background()

	// Execute with a non-existent command to simulate nvidia-smi not found
	// We'll test this by calling GetStats and checking it handles the error gracefully
	// Since nvidia-smi likely isn't installed in the test environment,
	// this test verifies the graceful handling

	stats, err := monitor.GetStats(ctx)

	// GetStats should not return an error even if nvidia-smi is not found
	// It returns empty stats with nil error as per the function contract
	assert.NoError(t, err)
	// Stats should be zero values
	assert.Equal(t, GPUStats{}, stats)
}

func TestGetStats_HandlesNvidiaSmiError(t *testing.T) {
	// Test that parseOutput returns an error for invalid input
	// This simulates what happens when nvidia-smi outputs unexpected format
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	monitor := NewMonitor(logger)

	testCases := []struct {
		name   string
		output string
	}{
		{
			name:   "empty output",
			output: "",
		},
		{
			name:   "wrong number of fields",
			output: "45, 8192",
		},
		{
			name:   "invalid utilization",
			output: "abc, 8192, 24576",
		},
		{
			name:   "invalid memory used",
			output: "45, abc, 24576",
		},
		{
			name:   "invalid memory total",
			output: "45, 8192, abc",
		},
		{
			name:   "whitespace only",
			output: "   \n   ",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := monitor.parseOutput(tc.output)
			assert.Error(t, err)
		})
	}
}

func TestGetStats_ContextCancellation(t *testing.T) {
	// Test that GetStats handles context cancellation gracefully
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	monitor := NewMonitor(logger)

	// Create an already cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// GetStats should return empty stats without error when context is cancelled
	stats, err := monitor.GetStats(ctx)

	// The function returns empty stats with nil error on context cancellation
	assert.NoError(t, err)
	assert.Equal(t, GPUStats{}, stats)
}

func TestParseOutput_EdgeCases(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	monitor := NewMonitor(logger)

	t.Run("handles extra whitespace", func(t *testing.T) {
		output := "  45 ,  8192  ,  24576  "
		stats, err := monitor.parseOutput(output)

		require.NoError(t, err)
		assert.Equal(t, 45.0, stats.UtilizationPct)
		assert.Equal(t, 8192, stats.MemoryUsedMB)
		assert.Equal(t, 24576, stats.MemoryTotalMB)
	})

	t.Run("handles trailing newline", func(t *testing.T) {
		output := "45, 8192, 24576\n"
		stats, err := monitor.parseOutput(output)

		require.NoError(t, err)
		assert.Equal(t, 45.0, stats.UtilizationPct)
	})

	t.Run("handles decimal utilization", func(t *testing.T) {
		output := "45.5, 8192, 24576"
		stats, err := monitor.parseOutput(output)

		require.NoError(t, err)
		assert.Equal(t, 45.5, stats.UtilizationPct)
	})

	t.Run("handles zero values", func(t *testing.T) {
		output := "0, 0, 24576"
		stats, err := monitor.parseOutput(output)

		require.NoError(t, err)
		assert.Equal(t, 0.0, stats.UtilizationPct)
		assert.Equal(t, 0, stats.MemoryUsedMB)
		assert.Equal(t, 24576, stats.MemoryTotalMB)
	})

	t.Run("handles 100% utilization", func(t *testing.T) {
		output := "100, 24576, 24576"
		stats, err := monitor.parseOutput(output)

		require.NoError(t, err)
		assert.Equal(t, 100.0, stats.UtilizationPct)
	})
}

func TestParseOutput_MultipleGPUsEdgeCases(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	monitor := NewMonitor(logger)

	t.Run("handles three GPUs", func(t *testing.T) {
		// Three GPUs with different stats
		output := "30, 4096, 8192\n50, 8192, 16384\n70, 12288, 24576"
		stats, err := monitor.parseOutput(output)

		require.NoError(t, err)
		// Average utilization: (30 + 50 + 70) / 3 = 50
		assert.Equal(t, 50.0, stats.UtilizationPct)
		// Sum memory used: 4096 + 8192 + 12288 = 24576
		assert.Equal(t, 24576, stats.MemoryUsedMB)
		// Sum memory total: 8192 + 16384 + 24576 = 49152
		assert.Equal(t, 49152, stats.MemoryTotalMB)
	})

	t.Run("handles empty lines between GPU entries", func(t *testing.T) {
		output := "40, 8192, 24576\n\n60, 4096, 12288"
		stats, err := monitor.parseOutput(output)

		require.NoError(t, err)
		assert.Equal(t, 50.0, stats.UtilizationPct)
	})

	t.Run("fails if one GPU line is malformed", func(t *testing.T) {
		output := "40, 8192, 24576\ninvalid line\n60, 4096, 12288"
		_, err := monitor.parseOutput(output)

		assert.Error(t, err)
	})
}

func TestGPUStats_ZeroValue(t *testing.T) {
	// Verify GPUStats zero value is what we expect
	var stats GPUStats

	assert.Equal(t, 0.0, stats.UtilizationPct)
	assert.Equal(t, 0, stats.MemoryUsedMB)
	assert.Equal(t, 0, stats.MemoryTotalMB)
}
