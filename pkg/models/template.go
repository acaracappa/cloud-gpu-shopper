package models

import (
	"encoding/json"
	"strings"
	"time"
)

// VastTemplate represents a Vast.ai template configuration.
// Templates are reusable configurations that can be referenced by hash_id when creating instances.
type VastTemplate struct {
	ID                   int       `json:"id"`                               // Numeric ID (for deletion only)
	HashID               string    `json:"hash_id"`                          // Content hash (for instance creation)
	Name                 string    `json:"name"`                             // Template name
	Image                string    `json:"image"`                            // Docker image
	Tag                  string    `json:"tag,omitempty"`                    // Image tag, defaults to "latest"
	Env                  string    `json:"env,omitempty"`                    // Docker flag format env vars
	OnStart              string    `json:"onstart,omitempty"`                // Startup commands
	RunType              string    `json:"runtype,omitempty"`                // "ssh", "jupyter", "args"
	ArgsStr              string    `json:"args_str,omitempty"`               // Entrypoint arguments
	UseSSH               bool      `json:"use_ssh"`                          // SSH access enabled
	SSHDirect            bool      `json:"ssh_direct"`                       // Direct SSH (vs proxy)
	Recommended          bool      `json:"recommended"`                      // Vast.ai recommended
	RecommendedDiskSpace int       `json:"recommended_disk_space,omitempty"` // Recommended disk in GB
	ExtraFilters         string    `json:"extra_filters,omitempty"`          // JSON string of machine filters
	CreatorID            int       `json:"creator_id,omitempty"`             // Template creator ID
	CreatedAt            time.Time `json:"created_at,omitempty"`             // Creation timestamp
	CountCreated         int       `json:"count_created,omitempty"`          // Popularity metric (instances created)
}

// TemplateFilter defines criteria for filtering templates
type TemplateFilter struct {
	Recommended bool   // Filter to recommended templates only
	UseSSH      bool   // Filter to SSH-enabled templates only
	Name        string // Filter by name (partial match)
	Image       string // Filter by image (partial match)
}

// MatchesFilter checks if the template matches the given filter criteria
func (t *VastTemplate) MatchesFilter(filter TemplateFilter) bool {
	// Check recommended filter
	if filter.Recommended && !t.Recommended {
		return false
	}

	// Check SSH filter
	if filter.UseSSH && !t.UseSSH {
		return false
	}

	// Check name filter (case-insensitive partial match)
	if filter.Name != "" {
		if !containsIgnoreCase(t.Name, filter.Name) {
			return false
		}
	}

	// Check image filter (case-insensitive partial match)
	if filter.Image != "" {
		if !containsIgnoreCase(t.Image, filter.Image) {
			return false
		}
	}

	return true
}

// containsIgnoreCase checks if s contains substr (case-insensitive)
func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(substr) == 0 ||
			findIgnoreCase(s, substr) >= 0)
}

// findIgnoreCase finds substr in s (case-insensitive), returns index or -1
func findIgnoreCase(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	if len(s) < len(substr) {
		return -1
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if matchesIgnoreCase(s[i:i+len(substr)], substr) {
			return i
		}
	}
	return -1
}

// matchesIgnoreCase compares two strings of equal length case-insensitively
func matchesIgnoreCase(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		// Convert to lowercase
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// ExtraFilter represents a single filter condition from a template's extra_filters.
// It supports comparison operators (eq, neq, gt, gte, lt, lte) and set operators (in, notin).
type ExtraFilter struct {
	Eq    interface{}   `json:"eq,omitempty"`
	Neq   interface{}   `json:"neq,omitempty"`
	Gt    *float64      `json:"gt,omitempty"`
	Gte   *float64      `json:"gte,omitempty"`
	Lt    *float64      `json:"lt,omitempty"`
	Lte   *float64      `json:"lte,omitempty"`
	In    []interface{} `json:"in,omitempty"`
	NotIn []interface{} `json:"notin,omitempty"`
}

// ExtraFilters maps property names to their filter conditions.
// Example: {"cuda_max_good": {"gte": 12.9}, "gpu_total_ram": {"gt": 21000}}
type ExtraFilters map[string]ExtraFilter

// ParseExtraFilters parses the template's ExtraFilters JSON string into ExtraFilters.
// Returns nil if the template has no extra_filters or if the field is empty/null.
func (t *VastTemplate) ParseExtraFilters() (ExtraFilters, error) {
	if t.ExtraFilters == "" || t.ExtraFilters == "null" {
		return nil, nil
	}

	var filters ExtraFilters
	if err := json.Unmarshal([]byte(t.ExtraFilters), &filters); err != nil {
		return nil, err
	}

	return filters, nil
}

// MatchesHost checks if the template's filters are compatible with the given host properties.
// A nil or empty filter set matches all hosts.
func (f ExtraFilters) MatchesHost(hostProps map[string]interface{}) bool {
	if len(f) == 0 {
		return true
	}

	for propName, filter := range f {
		hostValue, exists := hostProps[propName]
		if !exists {
			// If property doesn't exist on host, skip this filter
			// (conservative: don't reject if we don't have the data)
			continue
		}

		if !filter.matches(hostValue) {
			return false
		}
	}

	return true
}

// matches checks if a single filter condition matches a host value
func (f ExtraFilter) matches(hostValue interface{}) bool {
	// Check eq (equals)
	if f.Eq != nil {
		if !valuesEqual(hostValue, f.Eq) {
			return false
		}
	}

	// Check neq (not equals)
	if f.Neq != nil {
		if valuesEqual(hostValue, f.Neq) {
			return false
		}
	}

	// Check numeric comparisons
	hostNum, isNumeric := toFloat64(hostValue)

	if f.Gt != nil {
		if !isNumeric || hostNum <= *f.Gt {
			return false
		}
	}

	if f.Gte != nil {
		if !isNumeric || hostNum < *f.Gte {
			return false
		}
	}

	if f.Lt != nil {
		if !isNumeric || hostNum >= *f.Lt {
			return false
		}
	}

	if f.Lte != nil {
		if !isNumeric || hostNum > *f.Lte {
			return false
		}
	}

	// Check in (set membership)
	if len(f.In) > 0 {
		if !valueInSet(hostValue, f.In) {
			return false
		}
	}

	// Check notin (not in set)
	if len(f.NotIn) > 0 {
		if valueInSet(hostValue, f.NotIn) {
			return false
		}
	}

	return true
}

// valuesEqual compares two values for equality, handling type coercion
func valuesEqual(a, b interface{}) bool {
	// Try string comparison first
	aStr, aIsStr := toString(a)
	bStr, bIsStr := toString(b)
	if aIsStr && bIsStr {
		return aStr == bStr
	}

	// Try numeric comparison
	aNum, aIsNum := toFloat64(a)
	bNum, bIsNum := toFloat64(b)
	if aIsNum && bIsNum {
		return aNum == bNum
	}

	// Fall back to direct comparison
	return a == b
}

// valueInSet checks if a value is in a set of values
func valueInSet(value interface{}, set []interface{}) bool {
	for _, setVal := range set {
		if valuesEqual(value, setVal) {
			return true
		}
	}
	return false
}

// toFloat64 converts a value to float64 if possible
func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case int32:
		return float64(val), true
	case string:
		// Don't convert strings to numbers for numeric comparisons
		return 0, false
	default:
		return 0, false
	}
}

// toString converts a value to string if it's a string type
func toString(v interface{}) (string, bool) {
	switch val := v.(type) {
	case string:
		return val, true
	default:
		return "", false
	}
}

// DefaultSSHTimeout is the default SSH verification timeout for templates
const DefaultSSHTimeout = 10 * time.Minute

// heavyImageTimeouts maps image patterns to their recommended SSH timeouts.
// Heavy images (like vLLM, TGI) take longer to pull and start.
var heavyImageTimeouts = map[string]time.Duration{
	"vllm":      15 * time.Minute, // vLLM images are large (~20GB)
	"tgi":       12 * time.Minute, // Text Generation Inference
	"tensorrt":  12 * time.Minute, // TensorRT optimized images
	"deepspeed": 12 * time.Minute, // DeepSpeed framework
	"triton":    12 * time.Minute, // Triton Inference Server
	"nemo":      15 * time.Minute, // NVIDIA NeMo
	"megatron":  15 * time.Minute, // Megatron-LM
	"ollama":    12 * time.Minute, // Ollama models
}

// GetRecommendedSSHTimeout returns the recommended SSH timeout for this template.
// Heavy images like vLLM need longer timeouts due to image pull time.
// BUG-005: Templates with large images need longer SSH verification timeouts.
func (t *VastTemplate) GetRecommendedSSHTimeout() time.Duration {
	imageLower := strings.ToLower(t.Image)

	for pattern, timeout := range heavyImageTimeouts {
		if strings.Contains(imageLower, pattern) {
			return timeout
		}
	}

	return DefaultSSHTimeout
}

// IsHeavyImage returns true if the template uses a heavy (large) image
// that may take longer to pull and start.
func (t *VastTemplate) IsHeavyImage() bool {
	return t.GetRecommendedSSHTimeout() > DefaultSSHTimeout
}
