//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/api"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/cost"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/inventory"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/lifecycle"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/provisioner"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/storage"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/test/mockprovider"
)

var (
	testServer       *httptest.Server
	testMockProvider *httptest.Server
	testEnv          *TestEnv
	testReconciler   *lifecycle.Reconciler
)

// TestMain sets up the test environment
func TestMain(m *testing.M) {
	// Check if external servers are configured
	if os.Getenv(EnvServerURL) != "" && os.Getenv(EnvMockProviderURL) != "" {
		// Use external servers
		log.Println("Using external servers for E2E tests")
		os.Exit(m.Run())
	}

	// Run tests with in-process servers
	// Using a wrapper function ensures defers run before os.Exit
	os.Exit(runTestsWithServers(m))
}

// runTestsWithServers sets up in-process servers and runs tests.
// This wrapper ensures all cleanup runs via defers before os.Exit is called.
func runTestsWithServers(m *testing.M) (exitCode int) {
	log.Println("Starting in-process servers for E2E tests")

	// Track resources for cleanup
	var (
		db     *storage.DB
		dbPath string
		lm     *lifecycle.Manager
	)

	// Deferred cleanup runs in LIFO order when this function returns
	// This ensures cleanup happens even on panic, and before os.Exit
	defer func() {
		// Recover from any panic to ensure cleanup completes
		if r := recover(); r != nil {
			log.Printf("PANIC during E2E tests: %v", r)
			exitCode = 1
		}

		log.Println("Cleaning up E2E test environment...")

		// 1. Stop lifecycle manager (stops background goroutines)
		if lm != nil {
			lm.Stop()
			log.Println("Lifecycle manager stopped")
		}

		// 2. Close test servers
		if testServer != nil {
			testServer.Close()
			log.Println("API server closed")
		}
		if testMockProvider != nil {
			testMockProvider.Close()
			log.Println("Mock provider closed")
		}

		// 3. Close database connection
		if db != nil {
			if err := db.Close(); err != nil {
				log.Printf("Warning: failed to close database: %v", err)
			} else {
				log.Println("Database closed")
			}
		}

		// 4. Remove temp database file (after db is closed)
		if dbPath != "" {
			if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
				log.Printf("Warning: failed to remove temp database: %v", err)
			} else {
				log.Println("Temp database removed")
			}
		}

		// 5. Clean up environment variables
		os.Unsetenv(EnvServerURL)
		os.Unsetenv(EnvMockProviderURL)

		log.Println("E2E cleanup complete")
	}()

	// Start mock provider
	mockState := mockprovider.NewState()
	mockServer := mockprovider.NewServer(mockState)
	testMockProvider = httptest.NewServer(mockServer)
	log.Printf("Mock provider started at %s", testMockProvider.URL)

	// Create temp database
	tmpDB, err := os.CreateTemp("", "e2e-test-*.db")
	if err != nil {
		log.Printf("Failed to create temp database: %v", err)
		return 1
	}
	tmpDB.Close()
	dbPath = tmpDB.Name()

	// Initialize database
	db, err = storage.New(dbPath)
	if err != nil {
		log.Printf("Failed to open database: %v", err)
		return 1
	}
	if err := db.Migrate(context.Background()); err != nil {
		log.Printf("Failed to migrate database: %v", err)
		return 1
	}

	// Create mock provider adapter that talks to our mock server
	mockProviderAdapter := NewMockProviderAdapter(testMockProvider.URL)

	// Create services
	sessionStore := storage.NewSessionStore(db)
	costStore := storage.NewCostStore(db)

	inv := inventory.New([]provider.Provider{mockProviderAdapter})
	registry := provisioner.NewSimpleProviderRegistry([]provider.Provider{mockProviderAdapter})

	// Use mock SSH verifier for E2E tests since we don't have real SSH servers
	mockSSHVerifier := &provisioner.AlwaysSucceedSSHVerifier{}
	prov := provisioner.New(sessionStore, registry,
		provisioner.WithSSHVerifier(mockSSHVerifier),
		provisioner.WithSSHVerifyTimeout(5*time.Second),
		provisioner.WithSSHCheckInterval(500*time.Millisecond))
	lm = lifecycle.New(sessionStore, prov)
	ct := cost.New(costStore, sessionStore, nil)

	// Create reconciler for orphan/ghost detection tests
	testReconciler = lifecycle.NewReconciler(
		sessionStore,
		registry,
		lifecycle.WithReconcileInterval(1*time.Hour), // Don't auto-run, tests will trigger manually
		lifecycle.WithAutoDestroyOrphans(true),
	)

	// Start lifecycle manager
	lm.Start(context.Background())

	// Create API server
	apiServer := api.New(inv, prov, lm, ct)
	apiServer.SetReady(true)
	testServer = httptest.NewServer(apiServer.Router())
	log.Printf("API server started at %s", testServer.URL)

	// Set environment variables for tests
	os.Setenv(EnvServerURL, testServer.URL)
	os.Setenv(EnvMockProviderURL, testMockProvider.URL)

	// Run tests - deferred cleanup will run after this returns
	return m.Run()
}

// MockProviderAdapter adapts the mock provider HTTP server to the provider.Provider interface
type MockProviderAdapter struct {
	baseURL string
	client  *http.Client
}

// NewMockProviderAdapter creates a new mock provider adapter
func NewMockProviderAdapter(baseURL string) *MockProviderAdapter {
	return &MockProviderAdapter{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (m *MockProviderAdapter) Name() string {
	return "vastai"
}

func (m *MockProviderAdapter) ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	// The mock provider returns offers via HTTP
	// For simplicity in testing, we return hardcoded offers that match the mock
	return []models.GPUOffer{
		{
			ID:           "offer-rtx4090-1",
			Provider:     "vastai",
			ProviderID:   "offer-rtx4090-1",
			GPUType:      "RTX 4090",
			GPUCount:     1,
			VRAM:         24,
			PricePerHour: 0.40,
			Available:    true,
			Location:     "US",
		},
		{
			ID:           "offer-rtx4090-2",
			Provider:     "vastai",
			ProviderID:   "offer-rtx4090-2",
			GPUType:      "RTX 4090",
			GPUCount:     2,
			VRAM:         24,
			PricePerHour: 0.75,
			Available:    true,
			Location:     "US",
		},
		{
			ID:           "offer-a100-1",
			Provider:     "vastai",
			ProviderID:   "offer-a100-1",
			GPUType:      "A100 SXM4",
			GPUCount:     1,
			VRAM:         80,
			PricePerHour: 1.50,
			Available:    true,
			Location:     "US",
		},
		{
			ID:           "offer-h100-1",
			Provider:     "vastai",
			ProviderID:   "offer-h100-1",
			GPUType:      "H100 SXM5",
			GPUCount:     1,
			VRAM:         80,
			PricePerHour: 3.50,
			Available:    true,
			Location:     "US",
		},
	}, nil
}

func (m *MockProviderAdapter) ListAllInstances(ctx context.Context) ([]provider.ProviderInstance, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.baseURL+"/instances/", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list instances: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Instances []struct {
			ID           int     `json:"id"`
			MachineID    int     `json:"machine_id"`
			ActualStatus string  `json:"actual_status"`
			SSHHost      string  `json:"ssh_host"`
			SSHPort      int     `json:"ssh_port"`
			Label        string  `json:"label"`
			GPUName      string  `json:"gpu_name"`
			NumGPUs      int     `json:"num_gpus"`
			DPHTotal     float64 `json:"dph_total"`
			StartDate    float64 `json:"start_date"`
		} `json:"instances"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	instances := make([]provider.ProviderInstance, len(result.Instances))
	for i, inst := range result.Instances {
		instances[i] = provider.ProviderInstance{
			ID:        fmt.Sprintf("%d", inst.ID),
			Status:    inst.ActualStatus,
			StartedAt: time.Unix(int64(inst.StartDate), 0),
			Tags: models.InstanceTags{
				ShopperSessionID: inst.Label, // Label contains session ID
			},
		}
	}

	return instances, nil
}

func (m *MockProviderAdapter) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
	// Create request body matching mock provider API
	createReq := map[string]interface{}{
		"client_id": "test-client",
		"image":     req.DockerImage,
		"env":       req.EnvVars,
		"disk":      50.0,
		"label":     req.SessionID, // Use session ID as label for reconciliation
		"onstart":   req.OnStartCmd,
		"runtype":   "ssh",
		"ssh_key":   req.SSHPublicKey,
	}

	body, err := json.Marshal(createReq)
	if err != nil {
		return nil, err
	}

	// Call mock provider to create instance
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, m.baseURL+"/asks/"+req.OfferID+"/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Success     bool   `json:"success"`
		NewContract int    `json:"new_contract"`
		Error       string `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if !result.Success {
		return nil, fmt.Errorf("failed to create instance: %s", result.Error)
	}

	return &provider.InstanceInfo{
		ProviderInstanceID: fmt.Sprintf("%d", result.NewContract),
		SSHHost:            "192.168.1.100",
		SSHPort:            22,
		SSHUser:            "root",
		Status:             "running",
	}, nil
}

func (m *MockProviderAdapter) DestroyInstance(ctx context.Context, instanceID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, m.baseURL+"/instances/"+instanceID+"/", nil)
	if err != nil {
		return err
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to destroy instance: status %d", resp.StatusCode)
	}

	return nil
}

func (m *MockProviderAdapter) GetInstanceStatus(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
	return nil, provider.ErrInstanceNotFound
}

func (m *MockProviderAdapter) SupportsFeature(feature provider.ProviderFeature) bool {
	return false
}
