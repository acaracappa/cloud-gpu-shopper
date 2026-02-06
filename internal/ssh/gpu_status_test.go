package ssh

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCUDAVersion(t *testing.T) {
	tests := []struct {
		name          string
		output        string
		wantVersion   string
		wantMajor     int
		wantMinor     int
		wantDriver    string
		wantErr       bool
		errContains   string
	}{
		{
			name: "standard nvidia-smi output",
			output: `Thu Feb  6 10:15:00 2026
+-----------------------------------------------------------------------------------------+
| NVIDIA-SMI 580.126.09        Driver Version: 580.126.09     CUDA Version: 12.9         |
|-----------------------------------------+------------------------+----------------------+
| GPU  Name                 Persistence-M | Bus-Id          Disp.A | Volatile Uncorr. ECC |
| Fan  Temp   Perf          Pwr:Usage/Cap |           Memory-Usage | GPU-Util  Compute M. |
|                                         |                        |               MIG M. |
|=========================================+========================+======================|
|   0  NVIDIA GeForce RTX 4090        Off |   00000000:01:00.0 Off |                  Off |
| 30%   42C    P8             15W /  450W |       1MiB /  24564MiB |      0%      Default |
+-----------------------------------------------------------------------------------------+`,
			wantVersion: "12.9",
			wantMajor:   12,
			wantMinor:   9,
			wantDriver:  "580.126.09",
			wantErr:     false,
		},
		{
			name: "CUDA 13.0 output",
			output: `| NVIDIA-SMI 600.10.00        Driver Version: 600.10.00     CUDA Version: 13.0      |`,
			wantVersion: "13.0",
			wantMajor:   13,
			wantMinor:   0,
			wantDriver:  "600.10.00",
			wantErr:     false,
		},
		{
			name:        "simple version format",
			output:      "12.9",
			wantVersion: "12.9",
			wantMajor:   12,
			wantMinor:   9,
			wantDriver:  "",
			wantErr:     false,
		},
		{
			name:        "simple version format older CUDA",
			output:      "11.8",
			wantVersion: "11.8",
			wantMajor:   11,
			wantMinor:   8,
			wantDriver:  "",
			wantErr:     false,
		},
		{
			name:        "empty output",
			output:      "",
			wantErr:     true,
			errContains: "empty",
		},
		{
			name:        "no CUDA version in output",
			output:      "GPU 0: NVIDIA GeForce RTX 4090",
			wantErr:     true,
			errContains: "could not parse CUDA version",
		},
		{
			name:        "whitespace only",
			output:      "   \n\t  ",
			wantErr:     true,
			errContains: "empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseCUDAVersion(tt.output)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, info)
			assert.Equal(t, tt.wantVersion, info.CUDAVersion)
			assert.Equal(t, tt.wantMajor, info.CUDAMajor)
			assert.Equal(t, tt.wantMinor, info.CUDAMinor)
			assert.Equal(t, tt.wantDriver, info.DriverVersion)

			// Test CUDAVersionFloat
			expectedFloat := float64(tt.wantMajor) + float64(tt.wantMinor)/10.0
			assert.InDelta(t, expectedFloat, info.CUDAVersionFloat(), 0.01)
		})
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantName    string
		wantMemUsed int64
		wantMemTotal int64
		wantErr     bool
	}{
		{
			name:         "standard output",
			output:       "NVIDIA GeForce RTX 4090, 1234, 24576, 45, 65, 250",
			wantName:     "NVIDIA GeForce RTX 4090",
			wantMemUsed:  1234,
			wantMemTotal: 24576,
			wantErr:      false,
		},
		{
			name:    "empty output",
			output:  "",
			wantErr: true,
		},
		{
			name:    "insufficient fields",
			output:  "GPU, 1234",
			wantErr: true,
		},
		{
			name:         "with N/A values",
			output:       "NVIDIA A100, 0, 81920, [N/A], 35, [N/A]",
			wantName:     "NVIDIA A100",
			wantMemUsed:  0,
			wantMemTotal: 81920,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, err := ParseNvidiaSMI(tt.output)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, status)
			assert.Equal(t, tt.wantName, status.Name)
			assert.Equal(t, tt.wantMemUsed, status.MemoryUsedMB)
			assert.Equal(t, tt.wantMemTotal, status.MemoryTotalMB)
		})
	}
}

func TestGPUStatus_MemoryUsedPct(t *testing.T) {
	tests := []struct {
		name     string
		used     int64
		total    int64
		expected float64
	}{
		{"0% usage", 0, 24576, 0.0},
		{"50% usage", 12288, 24576, 50.0},
		{"100% usage", 24576, 24576, 100.0},
		{"zero total (edge case)", 0, 0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &GPUStatus{
				MemoryUsedMB:  tt.used,
				MemoryTotalMB: tt.total,
			}
			assert.InDelta(t, tt.expected, status.MemoryUsedPct(), 0.01)
		})
	}
}

func TestGPUStatus_IsHealthy(t *testing.T) {
	tests := []struct {
		name     string
		temp     int
		memUsed  int64
		memTotal int64
		expected bool
	}{
		{"healthy - normal", 65, 12288, 24576, true},
		{"unhealthy - high temp", 95, 12288, 24576, false},
		{"unhealthy - memory full", 65, 24576, 24576, false},
		{"edge case - temp at limit", 89, 12288, 24576, true},
		{"edge case - temp over limit", 90, 12288, 24576, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &GPUStatus{
				TemperatureC:  tt.temp,
				MemoryUsedMB:  tt.memUsed,
				MemoryTotalMB: tt.memTotal,
			}
			assert.Equal(t, tt.expected, status.IsHealthy())
		})
	}
}
