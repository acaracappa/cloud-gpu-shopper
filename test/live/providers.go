//go:build live
// +build live

package live

import (
	"fmt"
	"os"
	"sync"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider/tensordock"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider/vastai"
)

// ProviderFactory creates and caches provider instances for direct API access.
// This is used for test cleanup operations that need to bypass the shopper API.
type ProviderFactory struct {
	config    *TestConfig
	mu        sync.Mutex
	providers map[Provider]provider.Provider
}

// NewProviderFactory creates a new provider factory from test configuration.
func NewProviderFactory(config *TestConfig) *ProviderFactory {
	return &ProviderFactory{
		config:    config,
		providers: make(map[Provider]provider.Provider),
	}
}

// GetProvider returns a provider instance, creating it if necessary.
// Provider instances are cached for reuse.
func (f *ProviderFactory) GetProvider(name Provider) (provider.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Return cached provider if available
	if p, ok := f.providers[name]; ok {
		return p, nil
	}

	// Create provider based on name
	var p provider.Provider
	var err error

	switch name {
	case ProviderVastAI:
		p, err = f.createVastAIClient()
	case ProviderTensorDock:
		p, err = f.createTensorDockClient()
	default:
		return nil, fmt.Errorf("unknown provider: %s", name)
	}

	if err != nil {
		return nil, err
	}

	// Cache for reuse
	f.providers[name] = p
	return p, nil
}

// createVastAIClient creates a Vast.ai provider client.
func (f *ProviderFactory) createVastAIClient() (provider.Provider, error) {
	cfg, ok := f.config.Providers[ProviderVastAI]
	if !ok || !cfg.Enabled {
		return nil, fmt.Errorf("vastai provider not enabled")
	}

	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("VASTAI_API_KEY")
	}

	if apiKey == "" {
		return nil, fmt.Errorf("VASTAI_API_KEY not configured")
	}

	return vastai.NewClient(apiKey), nil
}

// createTensorDockClient creates a TensorDock provider client.
func (f *ProviderFactory) createTensorDockClient() (provider.Provider, error) {
	cfg, ok := f.config.Providers[ProviderTensorDock]
	if !ok || !cfg.Enabled {
		return nil, fmt.Errorf("tensordock provider not enabled")
	}

	// TensorDock requires both auth ID and API token
	authID := os.Getenv("TENSORDOCK_AUTH_ID")
	apiToken := os.Getenv("TENSORDOCK_API_TOKEN")

	// Fall back to config if environment variables aren't set
	if apiToken == "" {
		apiToken = cfg.APIKey
	}

	if authID == "" || apiToken == "" {
		return nil, fmt.Errorf("TENSORDOCK_AUTH_ID and TENSORDOCK_API_TOKEN must be set")
	}

	return tensordock.NewClient(authID, apiToken), nil
}

// GetEnabledProviders returns provider instances for all enabled providers.
func (f *ProviderFactory) GetEnabledProviders() ([]provider.Provider, error) {
	var providers []provider.Provider

	for name, cfg := range f.config.Providers {
		if !cfg.Enabled {
			continue
		}

		p, err := f.GetProvider(name)
		if err != nil {
			// Log but don't fail - provider might not be configured
			continue
		}

		providers = append(providers, p)
	}

	return providers, nil
}

// HasProvider checks if a provider is available and can be instantiated.
func (f *ProviderFactory) HasProvider(name Provider) bool {
	_, err := f.GetProvider(name)
	return err == nil
}
