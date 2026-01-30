// Package gpumon provides GPU monitoring functionality for the node agent.
package gpumon

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	// commandTimeout is the timeout for nvidia-smi execution
	commandTimeout = 5 * time.Second
)

// GPUStats contains current GPU statistics
type GPUStats struct {
	UtilizationPct float64 // 0-100
	MemoryUsedMB   int
	MemoryTotalMB  int
}

// Monitor queries GPU statistics via nvidia-smi
type Monitor struct {
	logger *slog.Logger
}

// NewMonitor creates a new GPU monitor
func NewMonitor(logger *slog.Logger) *Monitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Monitor{
		logger: logger,
	}
}

// GetStats retrieves current GPU statistics
// If nvidia-smi is not available or fails, returns empty GPUStats with nil error
func (m *Monitor) GetStats(ctx context.Context) (GPUStats, error) {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	// Execute nvidia-smi
	cmd := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=utilization.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits")

	output, err := cmd.Output()
	if err != nil {
		// Check if nvidia-smi is not found
		var exitErr *exec.ExitError
		if errors.Is(err, exec.ErrNotFound) {
			m.logger.Warn("nvidia-smi not found, GPU monitoring unavailable")
			return GPUStats{}, nil
		}
		if errors.As(err, &exitErr) {
			m.logger.Warn("nvidia-smi failed",
				slog.String("error", err.Error()),
				slog.String("stderr", string(exitErr.Stderr)))
			return GPUStats{}, nil
		}
		if ctx.Err() != nil {
			m.logger.Warn("nvidia-smi timed out",
				slog.Duration("timeout", commandTimeout))
			return GPUStats{}, nil
		}
		m.logger.Warn("nvidia-smi execution failed",
			slog.String("error", err.Error()))
		return GPUStats{}, nil
	}

	// Parse output - may contain multiple lines for multiple GPUs
	stats, err := m.parseOutput(string(output))
	if err != nil {
		m.logger.Warn("failed to parse nvidia-smi output",
			slog.String("error", err.Error()),
			slog.String("output", string(output)))
		return GPUStats{}, nil
	}

	return stats, nil
}

// parseOutput parses nvidia-smi CSV output
// Handles multiple GPUs by averaging utilization and summing memory
func (m *Monitor) parseOutput(output string) (GPUStats, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return GPUStats{}, errors.New("empty output")
	}

	var totalUtil float64
	var totalMemUsed, totalMemTotal int
	var gpuCount int

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, ",")
		if len(parts) != 3 {
			return GPUStats{}, errors.New("unexpected csv format: expected 3 fields")
		}

		// Parse utilization
		util, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil {
			return GPUStats{}, errors.New("failed to parse utilization: " + err.Error())
		}

		// Parse memory used
		memUsed, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return GPUStats{}, errors.New("failed to parse memory used: " + err.Error())
		}

		// Parse memory total
		memTotal, err := strconv.Atoi(strings.TrimSpace(parts[2]))
		if err != nil {
			return GPUStats{}, errors.New("failed to parse memory total: " + err.Error())
		}

		totalUtil += util
		totalMemUsed += memUsed
		totalMemTotal += memTotal
		gpuCount++
	}

	if gpuCount == 0 {
		return GPUStats{}, errors.New("no GPU data found")
	}

	return GPUStats{
		UtilizationPct: totalUtil / float64(gpuCount), // Average utilization
		MemoryUsedMB:   totalMemUsed,                  // Sum memory
		MemoryTotalMB:  totalMemTotal,                 // Sum memory
	}, nil
}
