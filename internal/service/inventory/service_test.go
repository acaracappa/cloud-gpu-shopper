package inventory

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProvider implements provider.Provider for testing
type mockProvider struct {
	name      string
	offers    []models.GPUOffer
	err       error
	callCount atomic.Int32
	delay     time.Duration
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	m.callCount.Add(1)

	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.delay):
		}
	}

	if m.err != nil {
		return nil, m.err
	}
	return m.offers, nil
}

func (m *mockProvider) ListAllInstances(ctx context.Context) ([]provider.ProviderInstance, error) {
	return nil, nil
}

func (m *mockProvider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
	return nil, nil
}

func (m *mockProvider) DestroyInstance(ctx context.Context, instanceID string) error {
	return nil
}

func (m *mockProvider) GetInstanceStatus(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
	return nil, nil
}

func (m *mockProvider) SupportsFeature(feature provider.ProviderFeature) bool {
	return false
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestService_New(t *testing.T) {
	p1 := &mockProvider{name: "vastai"}
	p2 := &mockProvider{name: "tensordock"}

	svc := New([]provider.Provider{p1, p2})

	assert.Equal(t, 2, svc.ProviderCount())
	assert.ElementsMatch(t, []string{"vastai", "tensordock"}, svc.ProviderNames())
}

func TestService_ListOffers_SingleProvider(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.50, Available: true},
		{ID: "offer-2", Provider: "vastai", GPUType: "A100", PricePerHour: 1.50, Available: true},
	}

	p := &mockProvider{name: "vastai", offers: offers}
	svc := New([]provider.Provider{p}, WithLogger(newTestLogger()))

	ctx := context.Background()
	result, err := svc.ListOffers(ctx, models.OfferFilter{})

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, int32(1), p.callCount.Load())
}

func TestService_ListOffers_MultipleProviders(t *testing.T) {
	vastaiOffers := []models.GPUOffer{
		{ID: "vastai-1", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.50, Available: true},
	}
	tensordockOffers := []models.GPUOffer{
		{ID: "tensordock-1", Provider: "tensordock", GPUType: "A100", PricePerHour: 1.20, Available: true},
	}

	p1 := &mockProvider{name: "vastai", offers: vastaiOffers}
	p2 := &mockProvider{name: "tensordock", offers: tensordockOffers}

	svc := New([]provider.Provider{p1, p2}, WithLogger(newTestLogger()))

	ctx := context.Background()
	result, err := svc.ListOffers(ctx, models.OfferFilter{})

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, int32(1), p1.callCount.Load())
	assert.Equal(t, int32(1), p2.callCount.Load())
}

func TestService_ListOffers_FilterByProvider(t *testing.T) {
	vastaiOffers := []models.GPUOffer{
		{ID: "vastai-1", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.50, Available: true},
	}
	tensordockOffers := []models.GPUOffer{
		{ID: "tensordock-1", Provider: "tensordock", GPUType: "A100", PricePerHour: 1.20, Available: true},
	}

	p1 := &mockProvider{name: "vastai", offers: vastaiOffers}
	p2 := &mockProvider{name: "tensordock", offers: tensordockOffers}

	svc := New([]provider.Provider{p1, p2}, WithLogger(newTestLogger()))

	ctx := context.Background()
	result, err := svc.ListOffers(ctx, models.OfferFilter{Provider: "vastai"})

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "vastai-1", result[0].ID)
	assert.Equal(t, int32(1), p1.callCount.Load())
	assert.Equal(t, int32(0), p2.callCount.Load()) // TensorDock should not be called
}

func TestService_ListOffers_FilterByGPUType(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.50, Available: true},
		{ID: "offer-2", Provider: "vastai", GPUType: "A100", PricePerHour: 1.50, Available: true},
		{ID: "offer-3", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.60, Available: true},
	}

	p := &mockProvider{name: "vastai", offers: offers}
	svc := New([]provider.Provider{p}, WithLogger(newTestLogger()))

	ctx := context.Background()
	result, err := svc.ListOffers(ctx, models.OfferFilter{GPUType: "RTX4090"})

	require.NoError(t, err)
	assert.Len(t, result, 2)
	for _, o := range result {
		assert.Equal(t, "RTX4090", o.GPUType)
	}
}

func TestService_ListOffers_FilterByMaxPrice(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.50, Available: true},
		{ID: "offer-2", Provider: "vastai", GPUType: "A100", PricePerHour: 1.50, Available: true},
		{ID: "offer-3", Provider: "vastai", GPUType: "RTX3090", PricePerHour: 0.30, Available: true},
	}

	p := &mockProvider{name: "vastai", offers: offers}
	svc := New([]provider.Provider{p}, WithLogger(newTestLogger()))

	ctx := context.Background()
	result, err := svc.ListOffers(ctx, models.OfferFilter{MaxPrice: 0.50})

	require.NoError(t, err)
	assert.Len(t, result, 2)
	for _, o := range result {
		assert.LessOrEqual(t, o.PricePerHour, 0.50)
	}
}

func TestService_ListOffers_SortedByPrice(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUType: "A100", PricePerHour: 1.50, Available: true},
		{ID: "offer-2", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.50, Available: true},
		{ID: "offer-3", Provider: "vastai", GPUType: "RTX3090", PricePerHour: 0.30, Available: true},
	}

	p := &mockProvider{name: "vastai", offers: offers}
	svc := New([]provider.Provider{p}, WithLogger(newTestLogger()))

	ctx := context.Background()
	result, err := svc.ListOffers(ctx, models.OfferFilter{})

	require.NoError(t, err)
	assert.Len(t, result, 3)
	assert.Equal(t, "offer-3", result[0].ID) // 0.30
	assert.Equal(t, "offer-2", result[1].ID) // 0.50
	assert.Equal(t, "offer-1", result[2].ID) // 1.50
}

func TestService_ListOffers_ExcludesUnavailable(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.50, Available: true},
		{ID: "offer-2", Provider: "vastai", GPUType: "A100", PricePerHour: 1.50, Available: false},
	}

	p := &mockProvider{name: "vastai", offers: offers}
	svc := New([]provider.Provider{p}, WithLogger(newTestLogger()))

	ctx := context.Background()
	result, err := svc.ListOffers(ctx, models.OfferFilter{})

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "offer-1", result[0].ID)
}

func TestService_ListOffers_Caching(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.50, Available: true},
	}

	p := &mockProvider{name: "vastai", offers: offers}
	svc := New([]provider.Provider{p},
		WithCacheTTL(100*time.Millisecond),
		WithLogger(newTestLogger()))

	ctx := context.Background()

	// First call - fetches from provider
	_, err := svc.ListOffers(ctx, models.OfferFilter{})
	require.NoError(t, err)
	assert.Equal(t, int32(1), p.callCount.Load())

	// Second call - should use cache
	_, err = svc.ListOffers(ctx, models.OfferFilter{})
	require.NoError(t, err)
	assert.Equal(t, int32(1), p.callCount.Load()) // Still 1

	// Wait for cache to expire
	time.Sleep(150 * time.Millisecond)

	// Third call - should fetch again
	_, err = svc.ListOffers(ctx, models.OfferFilter{})
	require.NoError(t, err)
	assert.Equal(t, int32(2), p.callCount.Load())
}

func TestService_ListOffers_BackoffOnError(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	p := &mockProvider{name: "vastai", err: providerErr}
	svc := New([]provider.Provider{p},
		WithCacheTTL(50*time.Millisecond),
		WithBackoffTTL(200*time.Millisecond),
		WithLogger(newTestLogger()))

	ctx := context.Background()

	// First call - fetches and gets error
	_, err := svc.ListOffers(ctx, models.OfferFilter{})
	require.Error(t, err)
	assert.Equal(t, int32(1), p.callCount.Load())

	// Second call within backoff - should return cached error
	_, err = svc.ListOffers(ctx, models.OfferFilter{})
	require.Error(t, err)
	assert.Equal(t, int32(1), p.callCount.Load()) // Still 1 due to backoff

	// Wait for backoff to expire
	time.Sleep(250 * time.Millisecond)

	// Third call - should fetch again
	_, err = svc.ListOffers(ctx, models.OfferFilter{})
	require.Error(t, err)
	assert.Equal(t, int32(2), p.callCount.Load())
}

func TestService_ListOffers_PartialFailure(t *testing.T) {
	vastaiOffers := []models.GPUOffer{
		{ID: "vastai-1", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.50, Available: true},
	}

	p1 := &mockProvider{name: "vastai", offers: vastaiOffers}
	p2 := &mockProvider{name: "tensordock", err: errors.New("tensordock down")}

	svc := New([]provider.Provider{p1, p2}, WithLogger(newTestLogger()))

	ctx := context.Background()
	result, err := svc.ListOffers(ctx, models.OfferFilter{})

	// Should succeed with partial results
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "vastai-1", result[0].ID)
}

func TestService_ListOffers_AllProvidersFailed(t *testing.T) {
	p1 := &mockProvider{name: "vastai", err: errors.New("vastai down")}
	p2 := &mockProvider{name: "tensordock", err: errors.New("tensordock down")}

	svc := New([]provider.Provider{p1, p2}, WithLogger(newTestLogger()))

	ctx := context.Background()
	_, err := svc.ListOffers(ctx, models.OfferFilter{})

	require.Error(t, err)
	var allFailed *AllProvidersFailed
	assert.True(t, errors.As(err, &allFailed))
	assert.Len(t, allFailed.Errors, 2)
}

func TestService_ListOffers_ProviderNotFound(t *testing.T) {
	p := &mockProvider{name: "vastai"}
	svc := New([]provider.Provider{p}, WithLogger(newTestLogger()))

	ctx := context.Background()
	_, err := svc.ListOffers(ctx, models.OfferFilter{Provider: "nonexistent"})

	require.Error(t, err)
	var notFound *ProviderNotFoundError
	assert.True(t, errors.As(err, &notFound))
	assert.Equal(t, "nonexistent", notFound.Name)
}

func TestService_GetOffer_Found(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.50, Available: true},
		{ID: "offer-2", Provider: "vastai", GPUType: "A100", PricePerHour: 1.50, Available: true},
	}

	p := &mockProvider{name: "vastai", offers: offers}
	svc := New([]provider.Provider{p}, WithLogger(newTestLogger()))

	ctx := context.Background()

	// First populate cache
	_, err := svc.ListOffers(ctx, models.OfferFilter{})
	require.NoError(t, err)

	// Now get specific offer
	offer, err := svc.GetOffer(ctx, "offer-2")
	require.NoError(t, err)
	assert.Equal(t, "offer-2", offer.ID)
	assert.Equal(t, "A100", offer.GPUType)
}

func TestService_GetOffer_NotFound(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.50, Available: true},
	}

	p := &mockProvider{name: "vastai", offers: offers}
	svc := New([]provider.Provider{p}, WithLogger(newTestLogger()))

	ctx := context.Background()
	_, err := svc.GetOffer(ctx, "nonexistent")

	require.Error(t, err)
	var notFound *OfferNotFoundError
	assert.True(t, errors.As(err, &notFound))
}

func TestService_InvalidateCache(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUType: "RTX4090", PricePerHour: 0.50, Available: true},
	}

	p := &mockProvider{name: "vastai", offers: offers}
	svc := New([]provider.Provider{p},
		WithCacheTTL(time.Hour), // Long TTL
		WithLogger(newTestLogger()))

	ctx := context.Background()

	// First call
	_, err := svc.ListOffers(ctx, models.OfferFilter{})
	require.NoError(t, err)
	assert.Equal(t, int32(1), p.callCount.Load())

	// Second call - uses cache
	_, err = svc.ListOffers(ctx, models.OfferFilter{})
	require.NoError(t, err)
	assert.Equal(t, int32(1), p.callCount.Load())

	// Invalidate cache
	svc.InvalidateCache("vastai")

	// Third call - should fetch again
	_, err = svc.ListOffers(ctx, models.OfferFilter{})
	require.NoError(t, err)
	assert.Equal(t, int32(2), p.callCount.Load())
}

func TestService_InvalidateCacheAll(t *testing.T) {
	p1 := &mockProvider{name: "vastai", offers: []models.GPUOffer{
		{ID: "v-1", Provider: "vastai", Available: true},
	}}
	p2 := &mockProvider{name: "tensordock", offers: []models.GPUOffer{
		{ID: "t-1", Provider: "tensordock", Available: true},
	}}

	svc := New([]provider.Provider{p1, p2},
		WithCacheTTL(time.Hour),
		WithLogger(newTestLogger()))

	ctx := context.Background()

	// Populate cache
	_, err := svc.ListOffers(ctx, models.OfferFilter{})
	require.NoError(t, err)
	assert.Equal(t, int32(1), p1.callCount.Load())
	assert.Equal(t, int32(1), p2.callCount.Load())

	// Invalidate all caches
	svc.InvalidateCache("")

	// Both should be fetched again
	_, err = svc.ListOffers(ctx, models.OfferFilter{})
	require.NoError(t, err)
	assert.Equal(t, int32(2), p1.callCount.Load())
	assert.Equal(t, int32(2), p2.callCount.Load())
}

func TestService_GetCacheStatus(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUType: "RTX4090", Available: true},
		{ID: "offer-2", Provider: "vastai", GPUType: "A100", Available: true},
	}

	p := &mockProvider{name: "vastai", offers: offers}
	svc := New([]provider.Provider{p},
		WithCacheTTL(time.Hour),
		WithLogger(newTestLogger()))

	ctx := context.Background()

	// Initially empty
	status := svc.GetCacheStatus()
	assert.Empty(t, status)

	// After fetching
	_, err := svc.ListOffers(ctx, models.OfferFilter{})
	require.NoError(t, err)

	status = svc.GetCacheStatus()
	assert.Len(t, status, 1)
	assert.Contains(t, status, "vastai")
	assert.Equal(t, 2, status["vastai"].OfferCount)
	assert.False(t, status["vastai"].IsExpired)
	assert.False(t, status["vastai"].InBackoff)
	assert.False(t, status["vastai"].HasError)
}

func TestService_GetCacheStatus_WithError(t *testing.T) {
	p := &mockProvider{name: "vastai", err: errors.New("provider error")}
	svc := New([]provider.Provider{p},
		WithBackoffTTL(time.Hour),
		WithLogger(newTestLogger()))

	ctx := context.Background()

	// Fetch and get error
	_, _ = svc.ListOffers(ctx, models.OfferFilter{})

	status := svc.GetCacheStatus()
	assert.Len(t, status, 1)
	assert.True(t, status["vastai"].InBackoff)
	assert.True(t, status["vastai"].HasError)
	assert.Equal(t, 0, status["vastai"].OfferCount)
}

func TestService_ConcurrentAccess(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUType: "RTX4090", Available: true},
	}

	p := &mockProvider{name: "vastai", offers: offers, delay: 10 * time.Millisecond}
	svc := New([]provider.Provider{p},
		WithCacheTTL(time.Hour),
		WithLogger(newTestLogger()))

	ctx := context.Background()
	const numGoroutines = 10

	errCh := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			_, err := svc.ListOffers(ctx, models.OfferFilter{})
			errCh <- err
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		err := <-errCh
		require.NoError(t, err)
	}

	// Due to concurrent initial access, multiple fetches may happen
	// but after cache is populated, subsequent calls should use cache
	_, err := svc.ListOffers(ctx, models.OfferFilter{})
	require.NoError(t, err)
}

func TestService_ContextCancellation(t *testing.T) {
	p := &mockProvider{name: "vastai", delay: time.Second}
	svc := New([]provider.Provider{p}, WithLogger(newTestLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := svc.ListOffers(ctx, models.OfferFilter{})
	require.Error(t, err)
}

func TestService_FilterByMinVRAM(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUType: "RTX4090", VRAM: 24, PricePerHour: 0.50, Available: true},
		{ID: "offer-2", Provider: "vastai", GPUType: "A100", VRAM: 80, PricePerHour: 1.50, Available: true},
		{ID: "offer-3", Provider: "vastai", GPUType: "RTX3080", VRAM: 10, PricePerHour: 0.30, Available: true},
	}

	p := &mockProvider{name: "vastai", offers: offers}
	svc := New([]provider.Provider{p}, WithLogger(newTestLogger()))

	ctx := context.Background()
	result, err := svc.ListOffers(ctx, models.OfferFilter{MinVRAM: 20})

	require.NoError(t, err)
	assert.Len(t, result, 2)
	for _, o := range result {
		assert.GreaterOrEqual(t, o.VRAM, 20)
	}
}

func TestService_FilterByMinGPUCount(t *testing.T) {
	offers := []models.GPUOffer{
		{ID: "offer-1", Provider: "vastai", GPUCount: 1, PricePerHour: 0.50, Available: true},
		{ID: "offer-2", Provider: "vastai", GPUCount: 4, PricePerHour: 2.00, Available: true},
		{ID: "offer-3", Provider: "vastai", GPUCount: 8, PricePerHour: 4.00, Available: true},
	}

	p := &mockProvider{name: "vastai", offers: offers}
	svc := New([]provider.Provider{p}, WithLogger(newTestLogger()))

	ctx := context.Background()
	result, err := svc.ListOffers(ctx, models.OfferFilter{MinGPUCount: 4})

	require.NoError(t, err)
	assert.Len(t, result, 2)
	for _, o := range result {
		assert.GreaterOrEqual(t, o.GPUCount, 4)
	}
}
