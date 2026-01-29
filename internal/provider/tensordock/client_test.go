package tensordock

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
	c := NewClient("test-key", "test-token")
	assert.Equal(t, "tensordock", c.Name())
}

func TestClient_SupportsFeature(t *testing.T) {
	c := NewClient("test-key", "test-token")

	tests := []struct {
		feature  provider.ProviderFeature
		expected bool
	}{
		{provider.FeatureCustomImages, true},
		{provider.FeatureInstanceTags, false},
		{provider.FeatureSpotPricing, false},
		{provider.FeatureIdleDetection, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.feature), func(t *testing.T) {
			assert.Equal(t, tt.expected, c.SupportsFeature(tt.feature))
		})
	}
}

func TestClient_ListOffers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/locations", r.URL.Path)
		// Check auth params
		assert.NotEmpty(t, r.URL.Query().Get("api_key"))
		assert.NotEmpty(t, r.URL.Query().Get("api_token"))

		resp := LocationsResponse{
			Data: LocationsData{
				Locations: []Location{
					{
						ID:            "loc-123",
						City:          "Chubbuck",
						StateProvince: "Idaho",
						Country:       "United States",
						Tier:          1,
						GPUs: []LocationGPU{
							{
								V0Name:      "geforcertx4090-pcie-24gb",
								DisplayName: "NVIDIA GeForce RTX 4090 PCIe 24GB",
								MaxCount:    4,
								PricePerHr:  0.40,
								Resources: GPUResources{
									MaxVCPUs:     56,
									MaxRAMGb:     331,
									MaxStorageGb: 30200,
								},
							},
						},
					},
					{
						ID:            "loc-456",
						City:          "Delaware",
						StateProvince: "Wilmington",
						Country:       "United States",
						Tier:          2,
						GPUs: []LocationGPU{
							{
								V0Name:      "rtxa100-pcie-80gb",
								DisplayName: "NVIDIA A100 PCIe 80GB",
								MaxCount:    2,
								PricePerHr:  1.50,
								Resources: GPUResources{
									MaxVCPUs:     32,
									MaxRAMGb:     256,
									MaxStorageGb: 10000,
								},
							},
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))

	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.NoError(t, err)
	assert.Len(t, offers, 2)

	// Check first offer
	assert.Equal(t, "tensordock-loc-123-geforcertx4090-pcie-24gb", offers[0].ID)
	assert.Equal(t, "tensordock", offers[0].Provider)
	assert.Equal(t, "RTX 4090", offers[0].GPUType)
	assert.Equal(t, 24, offers[0].VRAM)
	assert.Equal(t, 0.40, offers[0].PricePerHour)
	assert.Contains(t, offers[0].Location, "Chubbuck")

	// Check second offer
	assert.Equal(t, "A100", offers[1].GPUType)
	assert.Equal(t, 80, offers[1].VRAM)
}

func TestClient_ListOffers_WithFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := LocationsResponse{
			Data: LocationsData{
				Locations: []Location{
					{
						ID:   "loc-123",
						City: "Test",
						GPUs: []LocationGPU{
							{
								V0Name:      "rtx4090",
								DisplayName: "RTX 4090 24GB",
								MaxCount:    4,
								PricePerHr:  0.40,
							},
							{
								V0Name:      "rtx3080",
								DisplayName: "RTX 3080 10GB",
								MaxCount:    2,
								PricePerHr:  0.20,
							},
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))

	// Filter for >= 20GB VRAM
	filter := models.OfferFilter{
		MinVRAM: 20,
	}
	offers, err := client.ListOffers(context.Background(), filter)

	require.NoError(t, err)
	assert.Len(t, offers, 1)
	assert.Equal(t, 24, offers[0].VRAM)
}

func TestClient_ListAllInstances(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/instances", r.URL.Path)

		resp := InstancesResponse{
			Data: InstancesData{
				Instances: []Instance{
					{
						ID:           "inst-abc",
						Name:         "shopper-session123",
						Status:       "running",
						PricePerHour: 0.45,
					},
					{
						ID:     "inst-xyz",
						Name:   "other-vm", // Not ours
						Status: "running",
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))

	instances, err := client.ListAllInstances(context.Background())

	require.NoError(t, err)
	// Should only return our instances (with shopper- prefix)
	assert.Len(t, instances, 1)
	assert.Equal(t, "inst-abc", instances[0].ID)
	assert.Equal(t, "session123", instances[0].Tags.ShopperSessionID)
}

func TestClient_GetInstanceStatus_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))

	_, err := client.GetInstanceStatus(context.Background(), "nonexistent")

	require.Error(t, err)
	assert.ErrorIs(t, err, provider.ErrInstanceNotFound)
}

func TestNormalizeGPUName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"NVIDIA GeForce RTX 4090 PCIe 24GB", "RTX 4090"},
		{"NVIDIA A100 PCIe 80GB", "A100"},
		{"GeForce RTX 3090 PCIe 24GB", "RTX 3090"},
		{"Tesla V100 PCIe 32GB", "V100"},
		{"RTX 5090", "RTX 5090"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeGPUName(tt.input))
		})
	}
}

func TestParseVRAMFromName(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"NVIDIA GeForce RTX 4090 PCIe 24GB", 24},
		{"NVIDIA A100 PCIe 80GB", 80},
		{"RTX 3080 10GB", 10},
		{"Some GPU", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseVRAMFromName(tt.input))
		})
	}
}

func TestLocationGPUToOffer(t *testing.T) {
	loc := Location{
		ID:            "loc-123",
		City:          "TestCity",
		StateProvince: "TestState",
		Country:       "TestCountry",
		Tier:          2,
	}

	gpu := LocationGPU{
		V0Name:      "geforcertx4090-pcie-24gb",
		DisplayName: "NVIDIA GeForce RTX 4090 PCIe 24GB",
		MaxCount:    4,
		PricePerHr:  0.40,
	}

	offer := locationGPUToOffer(loc, gpu)

	assert.Equal(t, "tensordock-loc-123-geforcertx4090-pcie-24gb", offer.ID)
	assert.Equal(t, "tensordock", offer.Provider)
	assert.Equal(t, "loc-123", offer.ProviderID)
	assert.Equal(t, "RTX 4090", offer.GPUType)
	assert.Equal(t, 4, offer.GPUCount)
	assert.Equal(t, 24, offer.VRAM)
	assert.Equal(t, 0.40, offer.PricePerHour)
	assert.Contains(t, offer.Location, "TestCity")
	assert.InDelta(t, 0.67, offer.Reliability, 0.01) // Tier 2/3
}
