//go:build live
// +build live

package live

import (
	"os"
	"time"
)

// Provider represents a cloud GPU provider for live testing
type Provider string

const (
	ProviderVastAI     Provider = "vastai"
	ProviderTensorDock Provider = "tensordock"
)

// ProviderConfig holds configuration for a single provider
type ProviderConfig struct {
	Name         Provider
	APIKey       string
	BaseURL      string
	MaxPriceHour float64 // Maximum price per hour for test instances
	Enabled      bool
}

// TestConfig holds overall test configuration
type TestConfig struct {
	// Global limits
	MaxTotalSpendUSD   float64
	MaxTotalRuntime    time.Duration
	MaxPerProviderUSD  float64
	MaxPerProviderTime time.Duration

	// Server configuration
	ServerURL string

	// Provider configurations
	Providers map[Provider]ProviderConfig
}

// DefaultTestConfig returns the default configuration for live tests
func DefaultTestConfig() *TestConfig {
	cfg := &TestConfig{
		MaxTotalSpendUSD:   3.00,
		MaxTotalRuntime:    60 * time.Minute,
		MaxPerProviderUSD:  1.50,
		MaxPerProviderTime: 30 * time.Minute,
		ServerURL:          getEnvOrDefault("SHOPPER_URL", "http://localhost:8080"),
		Providers:          make(map[Provider]ProviderConfig),
	}

	// Vast.ai configuration
	vastKey := os.Getenv("VASTAI_API_KEY")
	cfg.Providers[ProviderVastAI] = ProviderConfig{
		Name:         ProviderVastAI,
		APIKey:       vastKey,
		BaseURL:      "https://console.vast.ai/api/v0",
		MaxPriceHour: 0.15, // Max $0.15/hr for cheap test GPUs
		Enabled:      vastKey != "",
	}

	// TensorDock configuration
	tensorKey := os.Getenv("TENSORDOCK_API_KEY")
	tensorOrg := os.Getenv("TENSORDOCK_ORG_ID")
	cfg.Providers[ProviderTensorDock] = ProviderConfig{
		Name:         ProviderTensorDock,
		APIKey:       tensorKey,
		BaseURL:      "https://api.tensordock.com/api/v2",
		MaxPriceHour: 0.20, // Max $0.20/hr for TensorDock
		Enabled:      tensorKey != "" && tensorOrg != "",
	}

	return cfg
}

// EnabledProviders returns a list of providers that have valid API keys
func (c *TestConfig) EnabledProviders() []Provider {
	var enabled []Provider
	for name, cfg := range c.Providers {
		if cfg.Enabled {
			enabled = append(enabled, name)
		}
	}
	return enabled
}

// HasProvider checks if a specific provider is enabled
func (c *TestConfig) HasProvider(p Provider) bool {
	cfg, ok := c.Providers[p]
	return ok && cfg.Enabled
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
