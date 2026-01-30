package provisioner

import (
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
)

// SimpleProviderRegistry is a basic in-memory provider registry
type SimpleProviderRegistry struct {
	providers map[string]provider.Provider
}

// NewSimpleProviderRegistry creates a new provider registry
func NewSimpleProviderRegistry(providers []provider.Provider) *SimpleProviderRegistry {
	r := &SimpleProviderRegistry{
		providers: make(map[string]provider.Provider),
	}
	for _, p := range providers {
		r.providers[p.Name()] = p
	}
	return r
}

// Get returns a provider by name
func (r *SimpleProviderRegistry) Get(name string) (provider.Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, &ProviderNotFoundError{Name: name}
	}
	return p, nil
}

// List returns all registered provider names
func (r *SimpleProviderRegistry) List() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}
