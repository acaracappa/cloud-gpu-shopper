package ssh

import (
	"fmt"
	"strconv"
	"strings"
)

// GPUStatus represents parsed nvidia-smi output
type GPUStatus struct {
	Name           string
	MemoryUsedMB   int64
	MemoryTotalMB  int64
	UtilizationPct int
	TemperatureC   int
	PowerDrawW     int
}

// MemoryUsedPct returns the percentage of GPU memory in use
func (g *GPUStatus) MemoryUsedPct() float64 {
	if g.MemoryTotalMB == 0 {
		return 0
	}
	return float64(g.MemoryUsedMB) / float64(g.MemoryTotalMB) * 100
}

// IsHealthy returns true if the GPU appears to be functioning normally
func (g *GPUStatus) IsHealthy() bool {
	// Consider GPU healthy if:
	// - Temperature is below 90C (throttling threshold for most GPUs)
	// - Memory is not fully exhausted
	return g.TemperatureC < 90 && g.MemoryUsedMB < g.MemoryTotalMB
}

// String returns a human-readable representation of the GPU status
func (g *GPUStatus) String() string {
	return fmt.Sprintf("%s: %dMB/%dMB (%.1f%%), %d%% util, %dC, %dW",
		g.Name,
		g.MemoryUsedMB,
		g.MemoryTotalMB,
		g.MemoryUsedPct(),
		g.UtilizationPct,
		g.TemperatureC,
		g.PowerDrawW,
	)
}

// ParseNvidiaSMI parses nvidia-smi output into GPUStatus
// Expected format from: nvidia-smi --query-gpu=name,memory.used,memory.total,utilization.gpu,temperature.gpu,power.draw --format=csv,noheader,nounits
// Example output: "NVIDIA GeForce RTX 3090, 1234, 24576, 45, 65, 250"
func ParseNvidiaSMI(output string) (*GPUStatus, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, fmt.Errorf("empty nvidia-smi output")
	}

	// Handle multi-GPU systems by taking the first GPU
	lines := strings.Split(output, "\n")
	line := strings.TrimSpace(lines[0])

	// Split by comma
	parts := strings.Split(line, ",")
	if len(parts) < 6 {
		return nil, fmt.Errorf("invalid nvidia-smi output format: expected 6 fields, got %d (output: %q)", len(parts), line)
	}

	status := &GPUStatus{}

	// Field 0: GPU name
	status.Name = strings.TrimSpace(parts[0])
	if status.Name == "" {
		return nil, fmt.Errorf("empty GPU name in nvidia-smi output")
	}

	// Field 1: memory.used (MB)
	memUsed, err := parseIntField(parts[1], "memory.used")
	if err != nil {
		return nil, err
	}
	status.MemoryUsedMB = int64(memUsed)

	// Field 2: memory.total (MB)
	memTotal, err := parseIntField(parts[2], "memory.total")
	if err != nil {
		return nil, err
	}
	status.MemoryTotalMB = int64(memTotal)

	// Field 3: utilization.gpu (%)
	util, err := parseIntField(parts[3], "utilization.gpu")
	if err != nil {
		return nil, err
	}
	status.UtilizationPct = util

	// Field 4: temperature.gpu (C)
	temp, err := parseIntField(parts[4], "temperature.gpu")
	if err != nil {
		return nil, err
	}
	status.TemperatureC = temp

	// Field 5: power.draw (W) - may contain decimal
	power, err := parseFloatAsInt(parts[5], "power.draw")
	if err != nil {
		return nil, err
	}
	status.PowerDrawW = power

	return status, nil
}

// parseIntField parses a trimmed string field as an integer
func parseIntField(s, fieldName string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "[N/A]" || s == "N/A" {
		return 0, nil
	}

	val, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("failed to parse %s %q: %w", fieldName, s, err)
	}
	return val, nil
}

// parseFloatAsInt parses a trimmed string field as a float and returns it as an integer
func parseFloatAsInt(s, fieldName string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "[N/A]" || s == "N/A" {
		return 0, nil
	}

	// Try parsing as float first (handles "250.00")
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		// Fall back to int parsing
		intVal, intErr := strconv.Atoi(s)
		if intErr != nil {
			return 0, fmt.Errorf("failed to parse %s %q: %w", fieldName, s, err)
		}
		return intVal, nil
	}
	return int(val), nil
}

// ParseMultiGPUNvidiaSMI parses nvidia-smi output for multiple GPUs
// Returns a slice of GPUStatus, one for each GPU
func ParseMultiGPUNvidiaSMI(output string) ([]*GPUStatus, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, fmt.Errorf("empty nvidia-smi output")
	}

	lines := strings.Split(output, "\n")
	statuses := make([]*GPUStatus, 0, len(lines))

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		status, err := ParseNvidiaSMI(line)
		if err != nil {
			return nil, fmt.Errorf("failed to parse GPU %d: %w", i, err)
		}
		statuses = append(statuses, status)
	}

	if len(statuses) == 0 {
		return nil, fmt.Errorf("no GPUs found in nvidia-smi output")
	}

	return statuses, nil
}
