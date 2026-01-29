package vastai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Name(t *testing.T) {
	c := NewClient("test-key")
	assert.Equal(t, "vastai", c.Name())
}

func TestClient_SupportsFeature(t *testing.T) {
	c := NewClient("test-key")

	tests := []struct {
		feature  provider.ProviderFeature
		expected bool
	}{
		{provider.FeatureInstanceTags, true},
		{provider.FeatureSpotPricing, true},
		{provider.FeatureCustomImages, true},
		{provider.FeatureIdleDetection, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.feature), func(t *testing.T) {
			assert.Equal(t, tt.expected, c.SupportsFeature(tt.feature))
		})
	}
}

func TestClient_ListOffers(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/bundles/", r.URL.Path)
		assert.Contains(t, r.Header.Get("Authorization"), "Bearer")

		resp := BundlesResponse{
			Offers: []Bundle{
				{
					ID:          12345,
					GPUName:     "RTX 4090",
					GPURam:      24576, // 24GB in MB
					NumGPUs:     1,
					DphTotal:    0.45,
					Geolocation: "California, US",
					Reliability: 0.95,
					Rentable:    true,
					Rented:      false,
				},
				{
					ID:          12346,
					GPUName:     "A100",
					GPURam:      81920, // 80GB in MB
					NumGPUs:     1,
					DphTotal:    1.50,
					Geolocation: "Virginia, US",
					Reliability: 0.99,
					Rentable:    true,
					Rented:      false,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.NoError(t, err)
	assert.Len(t, offers, 2)

	// Check first offer
	assert.Equal(t, "vastai-12345", offers[0].ID)
	assert.Equal(t, "vastai", offers[0].Provider)
	assert.Equal(t, "RTX 4090", offers[0].GPUType)
	assert.Equal(t, 24, offers[0].VRAM)
	assert.Equal(t, 0.45, offers[0].PricePerHour)
	assert.True(t, offers[0].Available)
}

func TestClient_ListOffers_WithFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that query contains filter params
		q := r.URL.Query().Get("q")
		assert.Contains(t, q, "rentable")

		resp := BundlesResponse{
			Offers: []Bundle{
				{
					ID:          12345,
					GPUName:     "RTX 4090",
					GPURam:      24576,
					NumGPUs:     1,
					DphTotal:    0.45,
					Geolocation: "California, US",
					Reliability: 0.95,
					Rentable:    true,
				},
				{
					ID:          12346,
					GPUName:     "RTX 3080",
					GPURam:      10240, // 10GB - should be filtered out
					NumGPUs:     1,
					DphTotal:    0.25,
					Geolocation: "Texas, US",
					Reliability: 0.90,
					Rentable:    true,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	// Filter for >= 20GB VRAM
	filter := models.OfferFilter{
		MinVRAM: 20,
	}
	offers, err := client.ListOffers(context.Background(), filter)

	require.NoError(t, err)
	assert.Len(t, offers, 1)
	assert.Equal(t, "RTX 4090", offers[0].GPUType)
}

func TestClient_ListAllInstances(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/instances/", r.URL.Path)

		resp := InstancesResponse{
			Instances: []Instance{
				{
					ID:           123,
					Label:        "shopper-abc123",
					ActualStatus: "running",
					DphTotal:     0.50,
					StartDate:    1706500000,
				},
				{
					ID:           124,
					Label:        "other-instance", // Not ours
					ActualStatus: "running",
					DphTotal:     0.30,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	instances, err := client.ListAllInstances(context.Background())

	require.NoError(t, err)
	// Should only return our instances (with shopper- prefix)
	assert.Len(t, instances, 1)
	assert.Equal(t, "123", instances[0].ID)
	assert.Equal(t, "abc123", instances[0].Tags.ShopperSessionID)
}

func TestClient_GetInstanceStatus_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	_, err := client.GetInstanceStatus(context.Background(), "99999")

	require.Error(t, err)
	assert.ErrorIs(t, err, provider.ErrInstanceNotFound)
}

func TestClient_HandleError_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	_, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.Error(t, err)
	assert.True(t, provider.IsRateLimitError(err))
}

func TestBundle_ToGPUOffer(t *testing.T) {
	bundle := Bundle{
		ID:          12345,
		GPUName:     "GeForce RTX 4090",
		GPURam:      24576,
		NumGPUs:     2,
		DphTotal:    0.90,
		Geolocation: "California, US",
		Reliability: 0.95,
		Rentable:    true,
		Rented:      false,
	}

	offer := bundle.ToGPUOffer()

	assert.Equal(t, "vastai-12345", offer.ID)
	assert.Equal(t, "vastai", offer.Provider)
	assert.Equal(t, "12345", offer.ProviderID)
	assert.Equal(t, "RTX 4090", offer.GPUType) // Normalized
	assert.Equal(t, 2, offer.GPUCount)
	assert.Equal(t, 24, offer.VRAM) // Converted from MB to GB
	assert.Equal(t, 0.90, offer.PricePerHour)
	assert.True(t, offer.Available)
}

func TestNormalizeGPUName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"RTX 4090", "RTX 4090"},
		{"GeForce RTX 4090", "RTX 4090"},
		{"NVIDIA A100", "A100"},
		{"Tesla V100", "V100"},
		{"RTX 5090", "RTX 5090"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeGPUName(tt.input))
		})
	}
}
