package provisioner

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// DiskEstimation contains the breakdown of estimated disk requirements.
type DiskEstimation struct {
	ModelWeightGB   float64 `json:"model_weight_gb"`
	DownloadBuffer  float64 `json:"download_buffer_gb"`
	DockerImageGB   float64 `json:"docker_image_gb"`
	SystemOverhead  float64 `json:"system_overhead_gb"`
	MinimumGB       int     `json:"minimum_gb"`
	RecommendedGB   int     `json:"recommended_gb"`
	TemplateFloorGB int     `json:"template_floor_gb,omitempty"`
	ModelID         string  `json:"model_id,omitempty"`
	Quantization    string  `json:"quantization,omitempty"`
	ParamCount      float64 `json:"param_count_b,omitempty"`
}

const (
	dockerImageVLLM    = 15.0 // GB for vLLM Docker image
	dockerImageDefault = 10.0 // GB for other Docker images
	systemOverhead     = 5.0  // GB for OS/runtime
)

// knownModels maps model IDs (lowercased) to param counts in billions
// for models whose names don't contain parseable param counts.
var knownModels = map[string]float64{
	"deepseek-ai/deepseek-r1":           671,
	"deepseek-ai/deepseek-v3":           671,
	"deepseek-ai/deepseek-v2":           236,
	"deepseek-ai/deepseek-coder-v2":     236,
	"mistralai/mixtral-8x22b-v0.1":      176,
	"mistralai/mixtral-8x22b-instruct":  176,
	"meta-llama/llama-2-70b-hf":         70,
	"meta-llama/meta-llama-3-70b":       70,
	"meta-llama/meta-llama-3.1-70b":     70,
	"meta-llama/meta-llama-3.1-405b":    405,
	"meta-llama/meta-llama-3.1-405b-fp8": 405,
	"01-ai/yi-34b":                      34,
	"tiiuae/falcon-180b":                180,
}

// paramRegex matches parameter counts like "70B", "8x7B", "1.1B", "0.5B"
// Captures: optional multiplier (e.g. "8x"), param number (e.g. "70", "1.1"), and the B suffix
var paramRegex = regexp.MustCompile(`(?i)[\-_](\d+)x(\d+(?:\.\d+)?)b(?:[\-_.]|$)`)
var simpleParamRegex = regexp.MustCompile(`(?i)[\-_](\d+(?:\.\d+)?)b(?:[\-_.]|$)`)

// parseParamCount extracts parameter count in billions from a model ID string.
// Handles formats: "70B", "8x7B", "1.1B", and known model overrides.
// Returns 0 if no param count can be determined.
func parseParamCount(modelID string) float64 {
	if modelID == "" {
		return 0
	}

	// Check known model overrides first (case-insensitive)
	lower := strings.ToLower(modelID)
	if count, ok := knownModels[lower]; ok {
		return count
	}

	// Try MoE pattern first: "8x7B" -> 8 * 7 = 56
	if matches := paramRegex.FindStringSubmatch(modelID); len(matches) == 3 {
		multiplier, err1 := strconv.ParseFloat(matches[1], 64)
		base, err2 := strconv.ParseFloat(matches[2], 64)
		if err1 == nil && err2 == nil {
			return multiplier * base
		}
	}

	// Try simple pattern: "70B", "1.1B"
	if matches := simpleParamRegex.FindStringSubmatch(modelID); len(matches) == 2 {
		count, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			return count
		}
	}

	return 0
}

// bytesPerParam returns the bytes per parameter for a given quantization.
func bytesPerParam(quantization string) float64 {
	switch strings.ToUpper(quantization) {
	case "FP32":
		return 4
	case "FP16", "BF16", "":
		// Default to FP16 when no quantization specified
		return 2
	case "FP8":
		return 1
	case "INT8":
		return 1
	case "AWQ", "GPTQ":
		return 0.5625 // 4-bit with group quantization overhead (~4.5 bits)
	case "INT4", "GGUF-Q4":
		return 0.5
	default:
		return 2 // Default to FP16
	}
}

// inferQuantization attempts to detect quantization from the model ID string.
// Returns empty string if no quantization is detected.
func inferQuantization(modelID string) string {
	upper := strings.ToUpper(modelID)
	switch {
	case strings.Contains(upper, "-AWQ") || strings.Contains(upper, "_AWQ"):
		return "AWQ"
	case strings.Contains(upper, "-GPTQ") || strings.Contains(upper, "_GPTQ"):
		return "GPTQ"
	case strings.Contains(upper, "-FP8") || strings.Contains(upper, "_FP8"):
		return "FP8"
	case strings.Contains(upper, "-INT8") || strings.Contains(upper, "_INT8"):
		return "INT8"
	case strings.Contains(upper, "-INT4") || strings.Contains(upper, "_INT4"):
		return "INT4"
	default:
		return ""
	}
}

// EstimateDiskRequirements calculates disk space needed for a model.
// Returns nil if no estimation can be made (no model ID and no template recommendation).
func EstimateDiskRequirements(modelID, quantization, templateHashID string, templateRecommendedDiskGB int) *DiskEstimation {
	paramCount := parseParamCount(modelID)

	// If no model params and no template recommendation, nothing to estimate
	if paramCount == 0 && templateRecommendedDiskGB == 0 {
		return nil
	}

	// Infer quantization from model name if not explicitly provided
	if quantization == "" {
		quantization = inferQuantization(modelID)
	}

	// Determine docker image size based on template
	dockerImage := dockerImageDefault
	if templateHashID != "" {
		// Assume vLLM-class image for template-based provisioning
		dockerImage = dockerImageVLLM
	}

	est := &DiskEstimation{
		ModelID:         modelID,
		Quantization:    quantization,
		ParamCount:      paramCount,
		DockerImageGB:   dockerImage,
		SystemOverhead:  systemOverhead,
		TemplateFloorGB: templateRecommendedDiskGB,
	}

	if paramCount > 0 {
		bpp := bytesPerParam(quantization)
		est.ModelWeightGB = paramCount * bpp // params in billions * bytes = GB
		est.DownloadBuffer = est.ModelWeightGB * 0.5
	}

	// Calculate minimum
	minimum := est.ModelWeightGB + est.DownloadBuffer + est.DockerImageGB + est.SystemOverhead
	est.MinimumGB = int(math.Ceil(minimum))

	// Calculate recommended = minimum * 1.2, rounded up to nearest 5GB
	recommended := minimum * 1.2
	est.RecommendedGB = roundUpTo5(int(math.Ceil(recommended)))

	// Apply template floor
	if templateRecommendedDiskGB > est.RecommendedGB {
		est.RecommendedGB = templateRecommendedDiskGB
	}
	if templateRecommendedDiskGB > est.MinimumGB {
		est.MinimumGB = templateRecommendedDiskGB
	}

	return est
}

// ValidateDiskSpace checks if the requested disk space is sufficient.
// Returns an InsufficientDiskError if disk is too small, nil otherwise.
// If requestedDiskGB is 0, validation passes (auto-calculate will apply).
func ValidateDiskSpace(requestedDiskGB int, estimation *DiskEstimation) error {
	if estimation == nil || requestedDiskGB == 0 {
		return nil
	}

	if requestedDiskGB < estimation.MinimumGB {
		return &InsufficientDiskError{
			RequestedGB: requestedDiskGB,
			MinimumGB:   estimation.MinimumGB,
			RecommendedGB: estimation.RecommendedGB,
			Estimation:  estimation,
		}
	}

	return nil
}

// roundUpTo5 rounds up to the nearest multiple of 5.
func roundUpTo5(n int) int {
	if n%5 == 0 {
		return n
	}
	return n + (5 - n%5)
}

// FormatBreakdown returns a human-readable breakdown of disk requirements.
func (e *DiskEstimation) FormatBreakdown() string {
	var parts []string
	if e.ModelWeightGB > 0 {
		parts = append(parts, fmt.Sprintf("model weights: %.1f GB", e.ModelWeightGB))
	}
	if e.DownloadBuffer > 0 {
		parts = append(parts, fmt.Sprintf("download buffer: %.1f GB", e.DownloadBuffer))
	}
	parts = append(parts, fmt.Sprintf("docker image: %.0f GB", e.DockerImageGB))
	parts = append(parts, fmt.Sprintf("system overhead: %.0f GB", e.SystemOverhead))
	if e.TemplateFloorGB > 0 {
		parts = append(parts, fmt.Sprintf("template recommendation: %d GB", e.TemplateFloorGB))
	}
	return strings.Join(parts, ", ")
}
