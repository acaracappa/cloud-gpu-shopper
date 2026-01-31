package inventory

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

const (
	// DefaultCacheTTL is the default cache duration
	DefaultCacheTTL = 1 * time.Minute

	// BackoffCacheTTL is used when a provider returns an error
	BackoffCacheTTL = 5 * time.Minute

	// MaxConcurrentFetches limits parallel provider requests
	MaxConcurrentFetches = 5

	// StaleInventoryThreshold is how old inventory can be before we start
	// degrading availability confidence. Offers older than this threshold
	// will have their confidence reduced to warn users of potential staleness.
	StaleInventoryThreshold = 2 * time.Minute

	// MaxStaleAge is the maximum age before inventory is considered very stale
	// and confidence is reduced to a minimum
	MaxStaleAge = 5 * time.Minute

	// StaleConfidenceMultiplier is applied to offers when inventory is stale
	StaleConfidenceMultiplier = 0.5

	// DefaultProviderTimeout is the default timeout for provider API calls
	DefaultProviderTimeout = 30 * time.Second
)

// Service aggregates GPU offers from multiple providers with caching
type Service struct {
	providers []provider.Provider
	logger    *slog.Logger

	mu              sync.RWMutex
	cache           map[string]*providerCache
	cacheTTL        time.Duration
	backoffTTL      time.Duration
	providerTimeout time.Duration
}

// providerCache holds cached offers for a single provider
type providerCache struct {
	offers    []models.GPUOffer
	fetchedAt time.Time
	expiresAt time.Time
	err       error
	inBackoff bool
}

// Option configures the inventory service
type Option func(*Service)

// WithCacheTTL sets a custom cache TTL
func WithCacheTTL(d time.Duration) Option {
	return func(s *Service) {
		s.cacheTTL = d
	}
}

// WithBackoffTTL sets a custom backoff TTL for error cases
func WithBackoffTTL(d time.Duration) Option {
	return func(s *Service) {
		s.backoffTTL = d
	}
}

// WithLogger sets a custom logger
func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) {
		s.logger = logger
	}
}

// WithProviderTimeout sets a custom timeout for provider API calls
func WithProviderTimeout(d time.Duration) Option {
	return func(s *Service) {
		s.providerTimeout = d
	}
}

// New creates a new inventory service
func New(providers []provider.Provider, opts ...Option) *Service {
	s := &Service{
		providers:       providers,
		logger:          slog.Default(),
		cache:           make(map[string]*providerCache),
		cacheTTL:        DefaultCacheTTL,
		backoffTTL:      BackoffCacheTTL,
		providerTimeout: DefaultProviderTimeout,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// ListOffers returns aggregated GPU offers from all providers
func (s *Service) ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	// If filtering by specific provider, only fetch from that one
	if filter.Provider != "" {
		return s.fetchFromProvider(ctx, filter.Provider, filter)
	}

	// Fetch from all providers concurrently
	return s.fetchFromAllProviders(ctx, filter)
}

// fetchFromProvider fetches offers from a single provider
func (s *Service) fetchFromProvider(ctx context.Context, providerName string, filter models.OfferFilter) ([]models.GPUOffer, error) {
	var targetProvider provider.Provider
	for _, p := range s.providers {
		if p.Name() == providerName {
			targetProvider = p
			break
		}
	}

	if targetProvider == nil {
		return nil, &ProviderNotFoundError{Name: providerName}
	}

	offers, err := s.getOffersWithCache(ctx, targetProvider, filter)
	if err != nil {
		return nil, err
	}

	return s.filterAndSort(offers, filter), nil
}

// fetchFromAllProviders fetches offers from all providers concurrently
func (s *Service) fetchFromAllProviders(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	type result struct {
		offers []models.GPUOffer
		err    error
		name   string
	}

	results := make(chan result, len(s.providers))
	var wg sync.WaitGroup

	for _, p := range s.providers {
		wg.Add(1)
		go func(prov provider.Provider) {
			defer wg.Done()

			offers, err := s.getOffersWithCache(ctx, prov, filter)
			results <- result{
				offers: offers,
				err:    err,
				name:   prov.Name(),
			}
		}(p)
	}

	// Close results channel when all goroutines complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var allOffers []models.GPUOffer
	var errors []error

	for r := range results {
		if r.err != nil {
			s.logger.Warn("provider fetch failed",
				slog.String("provider", r.name),
				slog.String("error", r.err.Error()))
			errors = append(errors, r.err)
			continue
		}
		allOffers = append(allOffers, r.offers...)
	}

	// If all providers failed, return an error
	if len(errors) == len(s.providers) {
		return nil, &AllProvidersFailed{Errors: errors}
	}

	return s.filterAndSort(allOffers, filter), nil
}

// getOffersWithCache returns cached offers or fetches fresh ones
func (s *Service) getOffersWithCache(ctx context.Context, p provider.Provider, filter models.OfferFilter) ([]models.GPUOffer, error) {
	providerName := p.Name()

	// Check cache first
	s.mu.RLock()
	cached, exists := s.cache[providerName]
	s.mu.RUnlock()

	if exists && time.Now().Before(cached.expiresAt) {
		s.logger.Debug("using cached offers",
			slog.String("provider", providerName),
			slog.Int("count", len(cached.offers)),
			slog.Bool("in_backoff", cached.inBackoff))

		if cached.err != nil {
			return nil, cached.err
		}
		return cached.offers, nil
	}

	// Fetch fresh offers with timeout
	s.logger.Debug("fetching offers from provider", slog.String("provider", providerName))

	fetchCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()

	offers, err := p.ListOffers(fetchCtx, models.OfferFilter{}) // Fetch all, filter locally for better caching
	now := time.Now()

	// Update cache
	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		s.logger.Warn("provider fetch error, entering backoff",
			slog.String("provider", providerName),
			slog.String("error", err.Error()))

		s.cache[providerName] = &providerCache{
			offers:    nil,
			fetchedAt: now,
			expiresAt: now.Add(s.backoffTTL),
			err:       err,
			inBackoff: true,
		}
		return nil, err
	}

	s.cache[providerName] = &providerCache{
		offers:    offers,
		fetchedAt: now,
		expiresAt: now.Add(s.cacheTTL),
		err:       nil,
		inBackoff: false,
	}

	s.logger.Debug("cached offers from provider",
		slog.String("provider", providerName),
		slog.Int("count", len(offers)))

	return offers, nil
}

// filterAndSort applies filters and sorts offers by price
func (s *Service) filterAndSort(offers []models.GPUOffer, filter models.OfferFilter) []models.GPUOffer {
	filtered := make([]models.GPUOffer, 0, len(offers))

	for _, offer := range offers {
		// Apply staleness degradation to availability confidence
		adjustedOffer := s.applyStalenessDegradation(offer)
		if adjustedOffer.MatchesFilter(filter) && adjustedOffer.Available {
			filtered = append(filtered, adjustedOffer)
		}
	}

	// Sort by price (lowest first)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].PricePerHour < filtered[j].PricePerHour
	})

	return filtered
}

// applyStalenessDegradation reduces availability confidence for stale offers
func (s *Service) applyStalenessDegradation(offer models.GPUOffer) models.GPUOffer {
	age := time.Since(offer.FetchedAt)

	// If inventory is fresh, no degradation needed
	if age < StaleInventoryThreshold {
		return offer
	}

	// Calculate degradation factor based on staleness
	// Linear degradation from 1.0 at StaleInventoryThreshold to StaleConfidenceMultiplier at MaxStaleAge
	var degradationFactor float64
	if age >= MaxStaleAge {
		degradationFactor = StaleConfidenceMultiplier
	} else {
		// Linear interpolation
		progress := float64(age-StaleInventoryThreshold) / float64(MaxStaleAge-StaleInventoryThreshold)
		degradationFactor = 1.0 - (progress * (1.0 - StaleConfidenceMultiplier))
	}

	// Get the effective confidence and apply degradation
	baseConfidence := offer.GetEffectiveAvailabilityConfidence()
	offer.AvailabilityConfidence = baseConfidence * degradationFactor

	return offer
}

// GetOffer retrieves a specific offer by ID
func (s *Service) GetOffer(ctx context.Context, offerID string) (*models.GPUOffer, error) {
	// Search through all cached offers first
	s.mu.RLock()
	for _, cached := range s.cache {
		if cached.err == nil {
			for _, offer := range cached.offers {
				if offer.ID == offerID {
					s.mu.RUnlock()
					return &offer, nil
				}
			}
		}
	}
	s.mu.RUnlock()

	// If not found in cache, refresh all providers and search again
	allOffers, err := s.ListOffers(ctx, models.OfferFilter{})
	if err != nil {
		return nil, err
	}

	for _, offer := range allOffers {
		if offer.ID == offerID {
			return &offer, nil
		}
	}

	return nil, &OfferNotFoundError{ID: offerID}
}

// InvalidateCache clears the cache for a specific provider or all providers
func (s *Service) InvalidateCache(providerName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if providerName == "" {
		s.cache = make(map[string]*providerCache)
		s.logger.Debug("invalidated all provider caches")
	} else {
		delete(s.cache, providerName)
		s.logger.Debug("invalidated provider cache", slog.String("provider", providerName))
	}
}

// GetCacheStatus returns cache status for monitoring
func (s *Service) GetCacheStatus() map[string]CacheStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := make(map[string]CacheStatus)
	now := time.Now()

	for name, cached := range s.cache {
		age := now.Sub(cached.fetchedAt)
		status[name] = CacheStatus{
			OfferCount: len(cached.offers),
			FetchedAt:  cached.fetchedAt,
			ExpiresAt:  cached.expiresAt,
			IsExpired:  now.After(cached.expiresAt),
			InBackoff:  cached.inBackoff,
			HasError:   cached.err != nil,
			AgeSeconds: age.Seconds(),
			IsStale:    age >= StaleInventoryThreshold,
		}
	}

	return status
}

// CacheStatus represents the cache state for a provider
type CacheStatus struct {
	OfferCount int
	FetchedAt  time.Time
	ExpiresAt  time.Time
	IsExpired  bool
	InBackoff  bool
	HasError   bool
	AgeSeconds float64 // How old the cache is in seconds
	IsStale    bool    // True if older than StaleInventoryThreshold
}

// ProviderCount returns the number of registered providers
func (s *Service) ProviderCount() int {
	return len(s.providers)
}

// ProviderNames returns the names of all registered providers
func (s *Service) ProviderNames() []string {
	names := make([]string, len(s.providers))
	for i, p := range s.providers {
		names[i] = p.Name()
	}
	return names
}
