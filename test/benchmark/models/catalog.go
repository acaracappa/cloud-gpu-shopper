package models

// ModelTier represents a size category for LLM models
type ModelTier string

const (
	TierSmall  ModelTier = "small"
	TierMedium ModelTier = "medium"
	TierLarge  ModelTier = "large"
)

// Model represents an LLM model configuration for benchmarking
type Model struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	HuggingFaceID  string    `json:"huggingface_id"`
	ParametersB    float64   `json:"parameters_b"`
	Tier           ModelTier `json:"tier"`
	MinVRAMGB      int       `json:"min_vram_gb"`
	RecommendedGPU []string  `json:"recommended_gpu"`
	MaxModelLen    int       `json:"max_model_len"`
	TensorParallel int       `json:"tensor_parallel"`
}

// GPU represents a GPU type available from providers
type GPU struct {
	Type           string   `json:"type"`
	VRAMGB         int      `json:"vram_gb"`
	TypicalPriceHr float64  `json:"typical_price_hr"`
	Providers      []string `json:"providers"`
}

// Catalog holds all available models and GPUs for benchmarking
type Catalog struct {
	Models map[string]*Model
	GPUs   map[string]*GPU
}

// NewCatalog creates a new catalog with default models and GPUs
func NewCatalog() *Catalog {
	c := &Catalog{
		Models: make(map[string]*Model),
		GPUs:   make(map[string]*GPU),
	}
	c.initModels()
	c.initGPUs()
	return c
}

func (c *Catalog) initModels() {
	models := []*Model{
		// Small tier (7B models) - ~16GB VRAM
		{
			ID:             "mistral-7b",
			Name:           "Mistral 7B Instruct v0.3",
			HuggingFaceID:  "mistralai/Mistral-7B-Instruct-v0.3",
			ParametersB:    7.0,
			Tier:           TierSmall,
			MinVRAMGB:      16,
			RecommendedGPU: []string{"RTX 4090", "RTX 3090", "L4"},
			MaxModelLen:    4096,
			TensorParallel: 1,
		},
		{
			ID:             "qwen2.5-7b",
			Name:           "Qwen 2.5 7B Instruct",
			HuggingFaceID:  "Qwen/Qwen2.5-7B-Instruct",
			ParametersB:    7.0,
			Tier:           TierSmall,
			MinVRAMGB:      16,
			RecommendedGPU: []string{"RTX 4090", "RTX 3090", "L4"},
			MaxModelLen:    4096,
			TensorParallel: 1,
		},

		// Medium tier (32-33B models) - 24-48GB VRAM
		{
			ID:             "qwen2.5-32b",
			Name:           "Qwen 2.5 32B Instruct",
			HuggingFaceID:  "Qwen/Qwen2.5-32B-Instruct",
			ParametersB:    32.0,
			Tier:           TierMedium,
			MinVRAMGB:      48,
			RecommendedGPU: []string{"RTX A6000", "L40S"},
			MaxModelLen:    4096,
			TensorParallel: 1,
		},
		{
			ID:             "deepseek-33b",
			Name:           "DeepSeek LLM 33B Chat",
			HuggingFaceID:  "deepseek-ai/deepseek-llm-7b-chat",
			ParametersB:    33.0,
			Tier:           TierMedium,
			MinVRAMGB:      48,
			RecommendedGPU: []string{"RTX A6000", "L40S"},
			MaxModelLen:    4096,
			TensorParallel: 1,
		},

		// Large tier (67-72B models) - 80GB+ VRAM
		{
			ID:             "qwen2.5-72b",
			Name:           "Qwen 2.5 72B Instruct",
			HuggingFaceID:  "Qwen/Qwen2.5-72B-Instruct",
			ParametersB:    72.0,
			Tier:           TierLarge,
			MinVRAMGB:      80,
			RecommendedGPU: []string{"A100 SXM4 80GB", "H100 SXM"},
			MaxModelLen:    4096,
			TensorParallel: 2,
		},
		{
			ID:             "deepseek-67b",
			Name:           "DeepSeek LLM 67B Chat",
			HuggingFaceID:  "deepseek-ai/deepseek-llm-67b-chat",
			ParametersB:    67.0,
			Tier:           TierLarge,
			MinVRAMGB:      80,
			RecommendedGPU: []string{"A100 SXM4 80GB", "H100 SXM"},
			MaxModelLen:    4096,
			TensorParallel: 2,
		},
	}

	for _, m := range models {
		c.Models[m.ID] = m
	}
}

func (c *Catalog) initGPUs() {
	gpus := []*GPU{
		// Consumer GPUs (using actual names from providers)
		{
			Type:           "RTX 4090",
			VRAMGB:         24,
			TypicalPriceHr: 0.45,
			Providers:      []string{"tensordock", "vastai"},
		},
		{
			Type:           "RTX 3090",
			VRAMGB:         24,
			TypicalPriceHr: 0.10,
			Providers:      []string{"vastai"},
		},

		// Professional GPUs
		{
			Type:           "L4",
			VRAMGB:         24,
			TypicalPriceHr: 0.40,
			Providers:      []string{"tensordock", "vastai"},
		},
		{
			Type:           "RTX A6000",
			VRAMGB:         48,
			TypicalPriceHr: 0.75,
			Providers:      []string{"tensordock", "vastai"},
		},
		{
			Type:           "L40S",
			VRAMGB:         48,
			TypicalPriceHr: 0.90,
			Providers:      []string{"vastai"},
		},

		// Data center GPUs
		{
			Type:           "A100 SXM4 80GB",
			VRAMGB:         80,
			TypicalPriceHr: 2.00,
			Providers:      []string{"vastai"},
		},
		{
			Type:           "H100 SXM",
			VRAMGB:         80,
			TypicalPriceHr: 3.50,
			Providers:      []string{"vastai"},
		},
	}

	for _, g := range gpus {
		c.GPUs[g.Type] = g
	}
}

// GetModel returns a model by ID
func (c *Catalog) GetModel(id string) (*Model, bool) {
	m, ok := c.Models[id]
	return m, ok
}

// GetGPU returns a GPU by type
func (c *Catalog) GetGPU(gpuType string) (*GPU, bool) {
	g, ok := c.GPUs[gpuType]
	return g, ok
}

// GetModelsByTier returns all models in a given tier
func (c *Catalog) GetModelsByTier(tier ModelTier) []*Model {
	var result []*Model
	for _, m := range c.Models {
		if m.Tier == tier {
			result = append(result, m)
		}
	}
	return result
}

// GetCompatibleGPUs returns GPUs that can run a given model
func (c *Catalog) GetCompatibleGPUs(modelID string) []*GPU {
	model, ok := c.Models[modelID]
	if !ok {
		return nil
	}

	var result []*GPU
	for _, g := range c.GPUs {
		if g.VRAMGB >= model.MinVRAMGB {
			result = append(result, g)
		}
	}
	return result
}

// GetModelsForGPU returns models that can run on a given GPU
func (c *Catalog) GetModelsForGPU(gpuType string) []*Model {
	gpu, ok := c.GPUs[gpuType]
	if !ok {
		return nil
	}

	var result []*Model
	for _, m := range c.Models {
		if gpu.VRAMGB >= m.MinVRAMGB {
			result = append(result, m)
		}
	}
	return result
}

// AllModels returns all models in the catalog
func (c *Catalog) AllModels() []*Model {
	result := make([]*Model, 0, len(c.Models))
	for _, m := range c.Models {
		result = append(result, m)
	}
	return result
}

// AllGPUs returns all GPUs in the catalog
func (c *Catalog) AllGPUs() []*GPU {
	result := make([]*GPU, 0, len(c.GPUs))
	for _, g := range c.GPUs {
		result = append(result, g)
	}
	return result
}

// EstimateCost estimates the cost for running a benchmark
func (c *Catalog) EstimateCost(gpuType string, durationMinutes int) float64 {
	gpu, ok := c.GPUs[gpuType]
	if !ok {
		return 0
	}
	return gpu.TypicalPriceHr * float64(durationMinutes) / 60.0
}

// ModelList returns model IDs in a stable order for CLI output
func (c *Catalog) ModelList() []string {
	order := []string{
		"mistral-7b",
		"qwen2.5-7b",
		"qwen2.5-32b",
		"deepseek-33b",
		"qwen2.5-72b",
		"deepseek-67b",
	}
	var result []string
	for _, id := range order {
		if _, ok := c.Models[id]; ok {
			result = append(result, id)
		}
	}
	return result
}

// GPUList returns GPU types in a stable order for CLI output
func (c *Catalog) GPUList() []string {
	order := []string{
		"RTX 4090",
		"RTX 3090",
		"L4",
		"RTX A6000",
		"L40S",
		"A100 SXM4 80GB",
		"H100 SXM",
	}
	var result []string
	for _, t := range order {
		if _, ok := c.GPUs[t]; ok {
			result = append(result, t)
		}
	}
	return result
}
