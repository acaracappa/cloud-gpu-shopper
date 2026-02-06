package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestVastTemplate_MatchesFilter(t *testing.T) {
	template := VastTemplate{
		ID:          1,
		HashID:      "abc123",
		Name:        "vLLM Template",
		Image:       "vllm/vllm-openai",
		UseSSH:      true,
		Recommended: true,
	}

	tests := []struct {
		name     string
		filter   TemplateFilter
		expected bool
	}{
		{
			name:     "empty filter matches all",
			filter:   TemplateFilter{},
			expected: true,
		},
		{
			name:     "recommended filter matches recommended template",
			filter:   TemplateFilter{Recommended: true},
			expected: true,
		},
		{
			name:     "use_ssh filter matches ssh-enabled template",
			filter:   TemplateFilter{UseSSH: true},
			expected: true,
		},
		{
			name:     "name filter matches (case-insensitive)",
			filter:   TemplateFilter{Name: "vllm"},
			expected: true,
		},
		{
			name:     "name filter matches (case-insensitive uppercase)",
			filter:   TemplateFilter{Name: "VLLM"},
			expected: true,
		},
		{
			name:     "image filter matches",
			filter:   TemplateFilter{Image: "vllm"},
			expected: true,
		},
		{
			name:     "name filter does not match",
			filter:   TemplateFilter{Name: "ollama"},
			expected: false,
		},
		{
			name:     "combined filters match",
			filter:   TemplateFilter{Recommended: true, UseSSH: true, Name: "vllm"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := template.MatchesFilter(tt.filter)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestVastTemplate_MatchesFilter_NotRecommended(t *testing.T) {
	template := VastTemplate{
		ID:          1,
		HashID:      "abc123",
		Name:        "Test Template",
		UseSSH:      true,
		Recommended: false,
	}

	// Recommended filter should not match non-recommended template
	filter := TemplateFilter{Recommended: true}
	assert.False(t, template.MatchesFilter(filter))
}

func TestVastTemplate_MatchesFilter_NotSSH(t *testing.T) {
	template := VastTemplate{
		ID:          1,
		HashID:      "abc123",
		Name:        "Test Template",
		UseSSH:      false,
		Recommended: true,
	}

	// UseSSH filter should not match non-SSH template
	filter := TemplateFilter{UseSSH: true}
	assert.False(t, template.MatchesFilter(filter))
}

func TestContainsIgnoreCase(t *testing.T) {
	tests := []struct {
		s       string
		substr  string
		matches bool
	}{
		{"vLLM Template", "vllm", true},
		{"vLLM Template", "VLLM", true},
		{"vLLM Template", "template", true},
		{"vLLM Template", "Template", true},
		{"vLLM Template", "ollama", false},
		{"", "", true},
		{"abc", "", true},
		{"", "abc", false},
		{"ab", "abc", false},
	}

	for _, tt := range tests {
		t.Run(tt.s+"_"+tt.substr, func(t *testing.T) {
			result := containsIgnoreCase(tt.s, tt.substr)
			assert.Equal(t, tt.matches, result)
		})
	}
}

func TestVastTemplate_ParseExtraFilters(t *testing.T) {
	tests := []struct {
		name        string
		extraFilter string
		wantNil     bool
		wantErr     bool
		wantLen     int
	}{
		{
			name:        "empty string returns nil",
			extraFilter: "",
			wantNil:     true,
			wantErr:     false,
		},
		{
			name:        "null string returns nil",
			extraFilter: "null",
			wantNil:     true,
			wantErr:     false,
		},
		{
			name:        "simple filter parses",
			extraFilter: `{"cuda_max_good": {"gte": 12.9}}`,
			wantNil:     false,
			wantErr:     false,
			wantLen:     1,
		},
		{
			name:        "multiple filters parse",
			extraFilter: `{"cuda_max_good": {"gte": 12.9}, "compute_cap": {"gte": 750}}`,
			wantNil:     false,
			wantErr:     false,
			wantLen:     2,
		},
		{
			name:        "complex vLLM filter parses",
			extraFilter: `{"gpu_total_ram": {"gt": 21000}, "cuda_max_good": {"gte": 12.9}, "compute_cap": {"gte": 750}, "num_gpus": {"in": ["1", "2", "4", "8"]}}`,
			wantNil:     false,
			wantErr:     false,
			wantLen:     4,
		},
		{
			name:        "invalid JSON returns error",
			extraFilter: `{invalid json}`,
			wantNil:     true,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := VastTemplate{ExtraFilters: tt.extraFilter}
			filters, err := template.ParseExtraFilters()

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.wantNil {
				assert.Nil(t, filters)
			} else {
				assert.NotNil(t, filters)
				assert.Len(t, filters, tt.wantLen)
			}
		})
	}
}

func TestExtraFilters_MatchesHost_Gte(t *testing.T) {
	filters := ExtraFilters{
		"cuda_max_good": {Gte: floatPtr(12.9)},
	}

	tests := []struct {
		name      string
		hostProps map[string]interface{}
		want      bool
	}{
		{
			name:      "matches when value equals threshold",
			hostProps: map[string]interface{}{"cuda_max_good": 12.9},
			want:      true,
		},
		{
			name:      "matches when value above threshold",
			hostProps: map[string]interface{}{"cuda_max_good": 13.0},
			want:      true,
		},
		{
			name:      "does not match when value below threshold",
			hostProps: map[string]interface{}{"cuda_max_good": 12.5},
			want:      false,
		},
		{
			name:      "matches when property missing (skip unknown)",
			hostProps: map[string]interface{}{},
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filters.MatchesHost(tt.hostProps)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestExtraFilters_MatchesHost_Gt(t *testing.T) {
	filters := ExtraFilters{
		"gpu_total_ram": {Gt: floatPtr(21000)},
	}

	tests := []struct {
		name      string
		hostProps map[string]interface{}
		want      bool
	}{
		{
			name:      "does not match when value equals threshold",
			hostProps: map[string]interface{}{"gpu_total_ram": 21000.0},
			want:      false,
		},
		{
			name:      "matches when value above threshold",
			hostProps: map[string]interface{}{"gpu_total_ram": 24000.0},
			want:      true,
		},
		{
			name:      "does not match when value below threshold",
			hostProps: map[string]interface{}{"gpu_total_ram": 16000.0},
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filters.MatchesHost(tt.hostProps)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestExtraFilters_MatchesHost_Lt(t *testing.T) {
	filters := ExtraFilters{
		"compute_cap": {Lt: floatPtr(900)},
	}

	tests := []struct {
		name      string
		hostProps map[string]interface{}
		want      bool
	}{
		{
			name:      "matches when value below threshold",
			hostProps: map[string]interface{}{"compute_cap": 860},
			want:      true,
		},
		{
			name:      "does not match when value equals threshold",
			hostProps: map[string]interface{}{"compute_cap": 900},
			want:      false,
		},
		{
			name:      "does not match when value above threshold",
			hostProps: map[string]interface{}{"compute_cap": 950},
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filters.MatchesHost(tt.hostProps)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestExtraFilters_MatchesHost_Lte(t *testing.T) {
	filters := ExtraFilters{
		"compute_cap": {Lte: floatPtr(900)},
	}

	tests := []struct {
		name      string
		hostProps map[string]interface{}
		want      bool
	}{
		{
			name:      "matches when value below threshold",
			hostProps: map[string]interface{}{"compute_cap": 860},
			want:      true,
		},
		{
			name:      "matches when value equals threshold",
			hostProps: map[string]interface{}{"compute_cap": 900},
			want:      true,
		},
		{
			name:      "does not match when value above threshold",
			hostProps: map[string]interface{}{"compute_cap": 950},
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filters.MatchesHost(tt.hostProps)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestExtraFilters_MatchesHost_Eq(t *testing.T) {
	tests := []struct {
		name      string
		filters   ExtraFilters
		hostProps map[string]interface{}
		want      bool
	}{
		{
			name:      "string equality matches",
			filters:   ExtraFilters{"cpu_arch": {Eq: "amd64"}},
			hostProps: map[string]interface{}{"cpu_arch": "amd64"},
			want:      true,
		},
		{
			name:      "string equality does not match",
			filters:   ExtraFilters{"cpu_arch": {Eq: "amd64"}},
			hostProps: map[string]interface{}{"cpu_arch": "arm64"},
			want:      false,
		},
		{
			name:      "numeric equality matches",
			filters:   ExtraFilters{"num_gpus": {Eq: 4.0}},
			hostProps: map[string]interface{}{"num_gpus": 4},
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.filters.MatchesHost(tt.hostProps)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestExtraFilters_MatchesHost_Neq(t *testing.T) {
	tests := []struct {
		name      string
		filters   ExtraFilters
		hostProps map[string]interface{}
		want      bool
	}{
		{
			name:      "not equals matches when different",
			filters:   ExtraFilters{"gpu_name": {Neq: "A100"}},
			hostProps: map[string]interface{}{"gpu_name": "RTX 3090"},
			want:      true,
		},
		{
			name:      "not equals does not match when same",
			filters:   ExtraFilters{"gpu_name": {Neq: "A100"}},
			hostProps: map[string]interface{}{"gpu_name": "A100"},
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.filters.MatchesHost(tt.hostProps)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestExtraFilters_MatchesHost_In(t *testing.T) {
	filters := ExtraFilters{
		"num_gpus": {In: []interface{}{"1", "2", "4", "8"}},
	}

	tests := []struct {
		name      string
		hostProps map[string]interface{}
		want      bool
	}{
		{
			name:      "matches when value in set",
			hostProps: map[string]interface{}{"num_gpus": "4"},
			want:      true,
		},
		{
			name:      "does not match when value not in set",
			hostProps: map[string]interface{}{"num_gpus": "3"},
			want:      false,
		},
		{
			name:      "matches first item in set",
			hostProps: map[string]interface{}{"num_gpus": "1"},
			want:      true,
		},
		{
			name:      "matches last item in set",
			hostProps: map[string]interface{}{"num_gpus": "8"},
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filters.MatchesHost(tt.hostProps)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestExtraFilters_MatchesHost_NotIn(t *testing.T) {
	filters := ExtraFilters{
		"cpu_arch": {NotIn: []interface{}{"arm32", "mips"}},
	}

	tests := []struct {
		name      string
		hostProps map[string]interface{}
		want      bool
	}{
		{
			name:      "matches when value not in excluded set",
			hostProps: map[string]interface{}{"cpu_arch": "amd64"},
			want:      true,
		},
		{
			name:      "does not match when value in excluded set",
			hostProps: map[string]interface{}{"cpu_arch": "arm32"},
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filters.MatchesHost(tt.hostProps)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestExtraFilters_MatchesHost_MultipleFilters(t *testing.T) {
	// vLLM template filters
	filters := ExtraFilters{
		"gpu_total_ram": {Gt: floatPtr(21000)},
		"cuda_max_good": {Gte: floatPtr(12.9)},
		"compute_cap":   {Gte: floatPtr(750)},
		"num_gpus":      {In: []interface{}{"1", "2", "4", "8"}},
	}

	tests := []struct {
		name      string
		hostProps map[string]interface{}
		want      bool
	}{
		{
			name: "RTX 4090 matches all filters",
			hostProps: map[string]interface{}{
				"gpu_total_ram": 24576.0, // 24GB
				"cuda_max_good": 12.9,
				"compute_cap":   890,
				"num_gpus":      "1",
			},
			want: true,
		},
		{
			name: "RTX 3090 fails gpu_total_ram",
			hostProps: map[string]interface{}{
				"gpu_total_ram": 24576.0, // 24GB (passes)
				"cuda_max_good": 12.5,    // fails
				"compute_cap":   860,
				"num_gpus":      "1",
			},
			want: false,
		},
		{
			name: "Host with 3 GPUs fails num_gpus filter",
			hostProps: map[string]interface{}{
				"gpu_total_ram": 81920.0, // 80GB
				"cuda_max_good": 12.9,
				"compute_cap":   900,
				"num_gpus":      "3",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filters.MatchesHost(tt.hostProps)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestExtraFilters_MatchesHost_NilFilters(t *testing.T) {
	var filters ExtraFilters = nil
	hostProps := map[string]interface{}{
		"gpu_total_ram": 24576.0,
		"cuda_max_good": 12.9,
	}

	// Nil filters should match all hosts
	assert.True(t, filters.MatchesHost(hostProps))
}

func TestExtraFilters_MatchesHost_EmptyFilters(t *testing.T) {
	filters := ExtraFilters{}
	hostProps := map[string]interface{}{
		"gpu_total_ram": 24576.0,
		"cuda_max_good": 12.9,
	}

	// Empty filters should match all hosts
	assert.True(t, filters.MatchesHost(hostProps))
}

// Helper function to create float64 pointers
func floatPtr(f float64) *float64 {
	return &f
}

// BUG-005: Test GetRecommendedSSHTimeout for various image types
func TestVastTemplate_GetRecommendedSSHTimeout(t *testing.T) {
	tests := []struct {
		name         string
		image        string
		expectedTime time.Duration
		isHeavy      bool
	}{
		{
			name:         "vLLM image gets extended timeout",
			image:        "vllm/vllm-openai:latest",
			expectedTime: 15 * time.Minute,
			isHeavy:      true,
		},
		{
			name:         "vLLM case-insensitive",
			image:        "vastai/VLLM:v0.15.0",
			expectedTime: 15 * time.Minute,
			isHeavy:      true,
		},
		{
			name:         "TGI image gets extended timeout",
			image:        "ghcr.io/huggingface/tgi:latest",
			expectedTime: 12 * time.Minute,
			isHeavy:      true,
		},
		{
			name:         "TensorRT image gets extended timeout",
			image:        "nvcr.io/nvidia/tensorrt:23.04-py3",
			expectedTime: 12 * time.Minute,
			isHeavy:      true,
		},
		{
			name:         "Ollama image gets extended timeout",
			image:        "ollama/ollama:latest",
			expectedTime: 12 * time.Minute,
			isHeavy:      true,
		},
		{
			name:         "Triton image gets extended timeout",
			image:        "nvcr.io/nvidia/tritonserver:24.01-py3",
			expectedTime: 12 * time.Minute,
			isHeavy:      true,
		},
		{
			name:         "Regular image gets default timeout",
			image:        "pytorch/pytorch:2.0-cuda12.0",
			expectedTime: 10 * time.Minute,
			isHeavy:      false,
		},
		{
			name:         "Ubuntu base image gets default timeout",
			image:        "ubuntu:22.04",
			expectedTime: 10 * time.Minute,
			isHeavy:      false,
		},
		{
			name:         "Empty image gets default timeout",
			image:        "",
			expectedTime: 10 * time.Minute,
			isHeavy:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := VastTemplate{Image: tt.image}
			timeout := template.GetRecommendedSSHTimeout()
			assert.Equal(t, tt.expectedTime, timeout, "timeout should match expected")
			assert.Equal(t, tt.isHeavy, template.IsHeavyImage(), "isHeavy should match expected")
		})
	}
}
