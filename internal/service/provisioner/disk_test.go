package provisioner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseParamCount(t *testing.T) {
	tests := []struct {
		name     string
		modelID  string
		expected float64
	}{
		{"simple 8B", "meta-llama/Meta-Llama-3.1-8B", 8},
		{"simple 70B", "meta-llama/Meta-Llama-3.1-70B", 70},
		{"decimal 1.1B", "TinyLlama/TinyLlama-1.1B-Chat", 1.1},
		{"MoE 8x7B", "mistralai/Mixtral-8x7B-v0.1", 56},
		{"MoE 8x22B", "mistralai/Mixtral-8x22B-v0.1", 176},
		{"AWQ suffix 70B", "TheBloke/Llama-2-70B-AWQ", 70},
		{"GPTQ suffix 13B", "TheBloke/Llama-2-13B-GPTQ", 13},
		{"known override DeepSeek-R1", "deepseek-ai/DeepSeek-R1", 671},
		{"known override DeepSeek-V3", "deepseek-ai/DeepSeek-V3", 671},
		{"known override 405B", "meta-llama/Meta-Llama-3.1-405B", 405},
		{"known override 405B-FP8", "meta-llama/Meta-Llama-3.1-405B-FP8", 405},
		{"no params", "openai/whisper-large", 0},
		{"empty string", "", 0},
		{"405B with instruct", "meta-llama/Meta-Llama-3.1-405B-Instruct", 405},
		{"0.5B model", "Qwen/Qwen2-0.5B", 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseParamCount(tt.modelID)
			assert.InDelta(t, tt.expected, result, 0.01, "modelID=%q", tt.modelID)
		})
	}
}

func TestBytesPerParam(t *testing.T) {
	tests := []struct {
		quant    string
		expected float64
	}{
		{"FP32", 4},
		{"FP16", 2},
		{"BF16", 2},
		{"", 2}, // default to FP16
		{"FP8", 1},
		{"INT8", 1},
		{"AWQ", 0.5625},
		{"GPTQ", 0.5625},
		{"INT4", 0.5},
		{"unknown", 2}, // fallback to FP16
	}

	for _, tt := range tests {
		t.Run(tt.quant, func(t *testing.T) {
			result := bytesPerParam(tt.quant)
			assert.InDelta(t, tt.expected, result, 0.001)
		})
	}
}

func TestInferQuantization(t *testing.T) {
	tests := []struct {
		modelID  string
		expected string
	}{
		{"TheBloke/Llama-2-70B-AWQ", "AWQ"},
		{"TheBloke/Llama-2-70B-GPTQ", "GPTQ"},
		{"meta-llama/Meta-Llama-3.1-405B-FP8", "FP8"},
		{"some-model-INT8", "INT8"},
		{"some-model_INT4", "INT4"},
		{"meta-llama/Meta-Llama-3.1-70B", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			assert.Equal(t, tt.expected, inferQuantization(tt.modelID))
		})
	}
}

func TestEstimateDiskRequirements(t *testing.T) {
	t.Run("70B FP16 model", func(t *testing.T) {
		est := EstimateDiskRequirements("meta-llama/Meta-Llama-3.1-70B", "", "", 0)
		require.NotNil(t, est)
		// 70B * 2 bytes = 140GB model weight
		assert.InDelta(t, 140.0, est.ModelWeightGB, 0.1)
		// Download buffer = 140 * 0.5 = 70GB
		assert.InDelta(t, 70.0, est.DownloadBuffer, 0.1)
		// Docker = 10GB (no template)
		assert.Equal(t, 10.0, est.DockerImageGB)
		// System = 5GB
		assert.Equal(t, 5.0, est.SystemOverhead)
		// Minimum = ceil(140 + 70 + 10 + 5) = 225
		assert.Equal(t, 225, est.MinimumGB)
		// Recommended = roundUpTo5(ceil(225 * 1.2)) = roundUpTo5(270) = 270
		assert.Equal(t, 270, est.RecommendedGB)
	})

	t.Run("8B FP16 model", func(t *testing.T) {
		est := EstimateDiskRequirements("meta-llama/Meta-Llama-3.1-8B", "", "", 0)
		require.NotNil(t, est)
		// 8B * 2 = 16GB
		assert.InDelta(t, 16.0, est.ModelWeightGB, 0.1)
		// Minimum = ceil(16 + 8 + 10 + 5) = 39
		assert.Equal(t, 39, est.MinimumGB)
		// Recommended = roundUpTo5(ceil(39 * 1.2)) = roundUpTo5(47) = 50
		assert.Equal(t, 50, est.RecommendedGB)
	})

	t.Run("70B AWQ model", func(t *testing.T) {
		est := EstimateDiskRequirements("TheBloke/Llama-2-70B-AWQ", "", "", 0)
		require.NotNil(t, est)
		// Should infer AWQ from model name
		assert.Equal(t, "AWQ", est.Quantization)
		// 70B * 0.5625 = 39.375GB
		assert.InDelta(t, 39.375, est.ModelWeightGB, 0.1)
	})

	t.Run("explicit quantization overrides inferred", func(t *testing.T) {
		est := EstimateDiskRequirements("meta-llama/Meta-Llama-3.1-70B", "AWQ", "", 0)
		require.NotNil(t, est)
		assert.Equal(t, "AWQ", est.Quantization)
		// 70B * 0.5625 = 39.375GB
		assert.InDelta(t, 39.375, est.ModelWeightGB, 0.1)
	})

	t.Run("vLLM template increases docker overhead", func(t *testing.T) {
		est := EstimateDiskRequirements("meta-llama/Meta-Llama-3.1-8B", "", "some-template-hash", 0)
		require.NotNil(t, est)
		assert.Equal(t, 15.0, est.DockerImageGB) // vLLM-class image
	})

	t.Run("template floor overrides lower estimation", func(t *testing.T) {
		est := EstimateDiskRequirements("TinyLlama/TinyLlama-1.1B-Chat", "", "", 100)
		require.NotNil(t, est)
		// Model is small but template wants 100GB
		assert.GreaterOrEqual(t, est.RecommendedGB, 100)
		assert.GreaterOrEqual(t, est.MinimumGB, 100)
	})

	t.Run("template-only (no model)", func(t *testing.T) {
		est := EstimateDiskRequirements("", "", "", 50)
		require.NotNil(t, est)
		assert.Equal(t, 50, est.RecommendedGB)
		assert.Equal(t, 50, est.MinimumGB)
		assert.Equal(t, float64(0), est.ModelWeightGB)
	})

	t.Run("no model and no template returns nil", func(t *testing.T) {
		est := EstimateDiskRequirements("", "", "", 0)
		assert.Nil(t, est)
	})

	t.Run("unparseable model with no template returns nil", func(t *testing.T) {
		est := EstimateDiskRequirements("openai/whisper-large", "", "", 0)
		assert.Nil(t, est)
	})

	t.Run("known override DeepSeek-R1 FP8", func(t *testing.T) {
		est := EstimateDiskRequirements("deepseek-ai/DeepSeek-R1", "FP8", "", 0)
		require.NotNil(t, est)
		// 671B * 1 = 671GB
		assert.InDelta(t, 671.0, est.ModelWeightGB, 0.1)
	})

	t.Run("MoE 8x7B", func(t *testing.T) {
		est := EstimateDiskRequirements("mistralai/Mixtral-8x7B-v0.1", "", "", 0)
		require.NotNil(t, est)
		// 56B * 2 = 112GB
		assert.InDelta(t, 112.0, est.ModelWeightGB, 0.1)
	})
}

func TestValidateDiskSpace(t *testing.T) {
	t.Run("sufficient disk passes", func(t *testing.T) {
		est := &DiskEstimation{MinimumGB: 50, RecommendedGB: 60}
		err := ValidateDiskSpace(100, est)
		assert.NoError(t, err)
	})

	t.Run("exact minimum passes", func(t *testing.T) {
		est := &DiskEstimation{MinimumGB: 50, RecommendedGB: 60}
		err := ValidateDiskSpace(50, est)
		assert.NoError(t, err)
	})

	t.Run("insufficient disk fails", func(t *testing.T) {
		est := &DiskEstimation{MinimumGB: 50, RecommendedGB: 60}
		err := ValidateDiskSpace(30, est)
		require.Error(t, err)

		var diskErr *InsufficientDiskError
		require.ErrorAs(t, err, &diskErr)
		assert.Equal(t, 30, diskErr.RequestedGB)
		assert.Equal(t, 50, diskErr.MinimumGB)
		assert.Equal(t, 60, diskErr.RecommendedGB)
	})

	t.Run("zero disk passes (auto-calculate)", func(t *testing.T) {
		est := &DiskEstimation{MinimumGB: 50, RecommendedGB: 60}
		err := ValidateDiskSpace(0, est)
		assert.NoError(t, err)
	})

	t.Run("nil estimation passes", func(t *testing.T) {
		err := ValidateDiskSpace(10, nil)
		assert.NoError(t, err)
	})
}

func TestRoundUpTo5(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{0, 0},
		{1, 5},
		{5, 5},
		{6, 10},
		{10, 10},
		{11, 15},
		{47, 50},
		{50, 50},
		{270, 270},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, roundUpTo5(tt.input), "roundUpTo5(%d)", tt.input)
	}
}

func TestDiskEstimationFormatBreakdown(t *testing.T) {
	est := &DiskEstimation{
		ModelWeightGB:   140.0,
		DownloadBuffer:  70.0,
		DockerImageGB:   15,
		SystemOverhead:  5,
		TemplateFloorGB: 50,
	}

	breakdown := est.FormatBreakdown()
	assert.Contains(t, breakdown, "model weights: 140.0 GB")
	assert.Contains(t, breakdown, "download buffer: 70.0 GB")
	assert.Contains(t, breakdown, "docker image: 15 GB")
	assert.Contains(t, breakdown, "system overhead: 5 GB")
	assert.Contains(t, breakdown, "template recommendation: 50 GB")
}

func TestInsufficientDiskErrorMessage(t *testing.T) {
	est := &DiskEstimation{
		ModelWeightGB:  16.0,
		DownloadBuffer: 8.0,
		DockerImageGB:  10,
		SystemOverhead: 5,
	}
	err := &InsufficientDiskError{
		RequestedGB:   20,
		MinimumGB:     39,
		RecommendedGB: 50,
		Estimation:    est,
	}

	msg := err.Error()
	assert.Contains(t, msg, "20 GB requested")
	assert.Contains(t, msg, "minimum 39 GB required")
	assert.Contains(t, msg, "recommended: 50 GB")
	assert.Contains(t, msg, "model weights: 16.0 GB")
}
