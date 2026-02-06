package ssh

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseOOMOutput(t *testing.T) {
	tests := []struct {
		name         string
		output       string
		wantDetected bool
		wantProcs    []string
		wantTime     string
	}{
		{
			name:         "empty output - no OOM",
			output:       "",
			wantDetected: false,
			wantProcs:    nil,
			wantTime:     "",
		},
		{
			name:         "no OOM events",
			output:       "some random dmesg output",
			wantDetected: false,
			wantProcs:    nil,
			wantTime:     "",
		},
		{
			name:         "single OOM kill with timestamp",
			output:       `[Thu Feb  6 12:34:56 2026] Out of memory: Killed process 1234 (python3) total-vm:12345678kB, anon-rss:8765432kB`,
			wantDetected: true,
			wantProcs:    []string{"python3"},
			wantTime:     "Thu Feb  6 12:34:56 2026",
		},
		{
			name: "multiple OOM kills",
			output: `[Thu Feb  6 12:30:00 2026] Out of memory: Killed process 1000 (ollama) total-vm:50000000kB
[Thu Feb  6 12:34:56 2026] Out of memory: Killed process 1234 (python3) total-vm:12345678kB`,
			wantDetected: true,
			wantProcs:    []string{"ollama", "python3"},
			wantTime:     "Thu Feb  6 12:34:56 2026",
		},
		{
			name:         "oom invoked line",
			output:       `[Thu Feb  6 12:34:56 2026] python3 invoked oom-killer: gfp_mask=0xcc0, order=0`,
			wantDetected: true,
			wantProcs:    nil,
			wantTime:     "Thu Feb  6 12:34:56 2026",
		},
		{
			name: "duplicate process names deduplicated",
			output: `[Thu Feb  6 12:30:00 2026] Killed process 1000 (python3) total-vm:50000000kB
[Thu Feb  6 12:31:00 2026] Killed process 1001 (python3) total-vm:50000000kB`,
			wantDetected: true,
			wantProcs:    []string{"python3"},
			wantTime:     "Thu Feb  6 12:31:00 2026",
		},
		{
			name:         "kernel numeric timestamp format",
			output:       `[12345.678] Killed process 999 (vllm_worker)`,
			wantDetected: true,
			wantProcs:    []string{"vllm_worker"},
			wantTime:     "", // no human-readable timestamp
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := ParseOOMOutput(tt.output)

			assert.Equal(t, tt.wantDetected, status.OOMDetected, "OOMDetected")
			assert.Equal(t, tt.wantProcs, status.KilledProcs, "KilledProcs")
			assert.Equal(t, tt.wantTime, status.LastOOMTime, "LastOOMTime")

			if tt.output != "" {
				assert.Equal(t, tt.output, status.RawOutput, "RawOutput preserved")
			}
		})
	}
}

func TestOOMStatus_String(t *testing.T) {
	tests := []struct {
		name   string
		status OOMStatus
		want   string
	}{
		{
			name:   "no OOM",
			status: OOMStatus{OOMDetected: false},
			want:   "no OOM events detected",
		},
		{
			name: "OOM with processes",
			status: OOMStatus{
				OOMDetected: true,
				KilledProcs: []string{"python3", "ollama"},
				LastOOMTime: "Thu Feb  6 12:34:56 2026",
			},
			want: "OOM detected: killed python3, ollama (last: Thu Feb  6 12:34:56 2026)",
		},
		{
			name: "OOM without process details",
			status: OOMStatus{
				OOMDetected: true,
			},
			want: "OOM detected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.String())
		})
	}
}
