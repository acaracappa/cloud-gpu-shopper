//go:build live
// +build live

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// LiveTestEnv provides helpers for live testing
type LiveTestEnv struct {
	Config   *TestConfig
	Watchdog *Watchdog
	client   *http.Client
}

// NewLiveTestEnv creates a new live test environment
func NewLiveTestEnv(config *TestConfig, watchdog *Watchdog) *LiveTestEnv {
	return &LiveTestEnv{
		Config:   config,
		Watchdog: watchdog,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// GPUOffer represents an available GPU from the inventory
type GPUOffer struct {
	ID           string  `json:"id"`
	Provider     string  `json:"provider"`
	GPUType      string  `json:"gpu_type"`
	GPUCount     int     `json:"gpu_count"`
	VRAM         int     `json:"vram"`
	PricePerHour float64 `json:"price_per_hour"`
	Available    bool    `json:"available"`
	Location     string  `json:"location"`
}

// Session represents a provisioned session
type Session struct {
	ID                 string    `json:"id"`
	ConsumerID         string    `json:"consumer_id"`
	Provider           string    `json:"provider"`
	ProviderInstanceID string    `json:"provider_instance_id"`
	Status             string    `json:"status"`
	GPUType            string    `json:"gpu_type"`
	GPUCount           int       `json:"gpu_count"`
	PricePerHour       float64   `json:"price_per_hour"`
	SSHHost            string    `json:"ssh_host"`
	SSHPort            int       `json:"ssh_port"`
	SSHUser            string    `json:"ssh_user"`
	ReservationHrs     int       `json:"reservation_hours"`
	ExpiresAt          time.Time `json:"expires_at"`
	LastHeartbeat      time.Time `json:"last_heartbeat"`
	Error              string    `json:"error,omitempty"`
}

// CreateSessionRequest represents a session creation request
type CreateSessionRequest struct {
	ConsumerID     string `json:"consumer_id"`
	OfferID        string `json:"offer_id"`
	WorkloadType   string `json:"workload_type"`
	ReservationHrs int    `json:"reservation_hours"`
	IdleThreshold  int    `json:"idle_threshold,omitempty"`
}

// CreateSessionResponse represents a session creation response
type CreateSessionResponse struct {
	Session    Session `json:"session"`
	AgentToken string  `json:"agent_token"`
}

// ListInventory fetches available GPUs, optionally filtered by provider and max price
func (e *LiveTestEnv) ListInventory(t *testing.T, provider Provider, maxPrice float64) []GPUOffer {
	url := fmt.Sprintf("%s/api/v1/inventory?available=true", e.Config.ServerURL)
	if provider != "" {
		url += fmt.Sprintf("&provider=%s", provider)
	}
	if maxPrice > 0 {
		url += fmt.Sprintf("&max_price=%.2f", maxPrice)
	}

	resp, err := e.client.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "Failed to list inventory")

	var result struct {
		Offers []GPUOffer `json:"offers"`
	}
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	return result.Offers
}

// FindCheapestGPU finds the cheapest GPU across enabled providers
func (e *LiveTestEnv) FindCheapestGPU(t *testing.T) (*GPUOffer, Provider) {
	var allOffers []GPUOffer

	for prov, cfg := range e.Config.Providers {
		if !cfg.Enabled {
			continue
		}

		offers := e.ListInventory(t, prov, cfg.MaxPriceHour)
		for i := range offers {
			offers[i].Provider = string(prov) // Ensure provider is set
		}
		allOffers = append(allOffers, offers...)
	}

	require.NotEmpty(t, allOffers, "No cheap GPUs available from any provider")

	// Sort by price
	sort.Slice(allOffers, func(i, j int) bool {
		return allOffers[i].PricePerHour < allOffers[j].PricePerHour
	})

	cheapest := allOffers[0]
	t.Logf("Cheapest GPU: %s (%s) @ $%.2f/hr from %s",
		cheapest.GPUType, cheapest.ID, cheapest.PricePerHour, cheapest.Provider)

	return &cheapest, Provider(cheapest.Provider)
}

// FindCheapestFromProvider finds the cheapest GPU from a specific provider
func (e *LiveTestEnv) FindCheapestFromProvider(t *testing.T, provider Provider) *GPUOffer {
	cfg, ok := e.Config.Providers[provider]
	require.True(t, ok, "Provider %s not configured", provider)
	require.True(t, cfg.Enabled, "Provider %s not enabled", provider)

	offers := e.ListInventory(t, provider, cfg.MaxPriceHour)
	require.NotEmpty(t, offers, "No cheap GPUs available from %s", provider)

	// Sort by price
	sort.Slice(offers, func(i, j int) bool {
		return offers[i].PricePerHour < offers[j].PricePerHour
	})

	cheapest := offers[0]
	t.Logf("Cheapest %s GPU: %s @ $%.2f/hr",
		provider, cheapest.GPUType, cheapest.PricePerHour)

	return &cheapest
}

// CreateSession provisions a new GPU session
func (e *LiveTestEnv) CreateSession(t *testing.T, req CreateSessionRequest) *CreateSessionResponse {
	body, err := json.Marshal(req)
	require.NoError(t, err)

	url := fmt.Sprintf("%s/api/v1/sessions", e.Config.ServerURL)
	resp, err := e.client.Post(url, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to create session: %d - %s", resp.StatusCode, string(respBody))
	}

	var result CreateSessionResponse
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	// Track with watchdog
	if e.Watchdog != nil {
		e.Watchdog.TrackInstance(InstanceInfo{
			InstanceID: result.Session.ProviderInstanceID,
			SessionID:  result.Session.ID,
			Provider:   Provider(result.Session.Provider),
			StartTime:  time.Now(),
			PriceHour:  result.Session.PricePerHour,
		})
	}

	t.Logf("Created session %s (provider=%s, instance=%s)",
		result.Session.ID, result.Session.Provider, result.Session.ProviderInstanceID)

	return &result
}

// GetSession retrieves a session by ID
func (e *LiveTestEnv) GetSession(t *testing.T, sessionID string) *Session {
	url := fmt.Sprintf("%s/api/v1/sessions/%s", e.Config.ServerURL, sessionID)
	resp, err := e.client.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var session Session
	err = json.NewDecoder(resp.Body).Decode(&session)
	require.NoError(t, err)

	return &session
}

// WaitForStatus waits for a session to reach a specific status
func (e *LiveTestEnv) WaitForStatus(t *testing.T, sessionID, status string, timeout time.Duration) *Session {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		session := e.GetSession(t, sessionID)
		if session.Status == status {
			return session
		}
		if session.Status == "failed" || session.Status == "stopped" {
			if status != "failed" && status != "stopped" {
				t.Fatalf("Session %s reached terminal status %s (expected %s): %s",
					sessionID, session.Status, status, session.Error)
			}
		}
		time.Sleep(5 * time.Second)
	}

	t.Fatalf("Timeout waiting for session %s to reach status %s", sessionID, status)
	return nil
}

// ExtendSession extends a session by additional hours
func (e *LiveTestEnv) ExtendSession(t *testing.T, sessionID string, hours int) {
	url := fmt.Sprintf("%s/api/v1/sessions/%s/extend", e.Config.ServerURL, sessionID)
	body, _ := json.Marshal(map[string]int{"hours": hours})

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "Failed to extend session")
	t.Logf("Extended session %s by %d hours", sessionID, hours)
}

// SignalDone signals that a session is complete
func (e *LiveTestEnv) SignalDone(t *testing.T, sessionID string) {
	url := fmt.Sprintf("%s/api/v1/sessions/%s/done", e.Config.ServerURL, sessionID)

	req, err := http.NewRequest(http.MethodPost, url, nil)
	require.NoError(t, err)

	resp, err := e.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "Failed to signal done")
	t.Logf("Signaled done for session %s", sessionID)
}

// DestroySession destroys a session
func (e *LiveTestEnv) DestroySession(t *testing.T, sessionID string) {
	url := fmt.Sprintf("%s/api/v1/sessions/%s", e.Config.ServerURL, sessionID)

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	require.NoError(t, err)

	resp, err := e.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Logf("Warning: destroy returned status %d for session %s", resp.StatusCode, sessionID)
	}

	// Untrack from watchdog
	if e.Watchdog != nil {
		session := e.GetSession(t, sessionID)
		e.Watchdog.UntrackInstance(session.ProviderInstanceID)
	}

	t.Logf("Destroyed session %s", sessionID)
}

// VerifySSH verifies SSH connectivity to an instance
func (e *LiveTestEnv) VerifySSH(t *testing.T, host string, port int, user string) {
	// For now, just verify the connection info is present
	require.NotEmpty(t, host, "SSH host is empty")
	require.Greater(t, port, 0, "SSH port is invalid")
	require.NotEmpty(t, user, "SSH user is empty")

	t.Logf("SSH info: %s@%s:%d", user, host, port)

	// TODO: Optionally attempt actual SSH connection
	// This would require the SSH private key from session creation
}

// GenerateConsumerID generates a unique consumer ID for testing
func GenerateConsumerID() string {
	return fmt.Sprintf("live-test-%d", time.Now().UnixNano())
}

// ProvisionCheapGPU is a convenience method to provision the cheapest available GPU
func (e *LiveTestEnv) ProvisionCheapGPU(t *testing.T, provider Provider) *CreateSessionResponse {
	var offer *GPUOffer

	if provider == "" {
		offer, provider = e.FindCheapestGPU(t)
	} else {
		offer = e.FindCheapestFromProvider(t, provider)
	}

	return e.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        offer.ID,
		WorkloadType:   "live-test",
		ReservationHrs: 1,
	})
}

// Cleanup destroys a session and handles errors gracefully
func (e *LiveTestEnv) Cleanup(t *testing.T, sessionID string) {
	if sessionID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/api/v1/sessions/%s", e.Config.ServerURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		t.Logf("Cleanup: Failed to create request for %s: %v", sessionID, err)
		return
	}

	resp, err := e.client.Do(req)
	if err != nil {
		t.Logf("Cleanup: Failed to destroy %s: %v", sessionID, err)
		return
	}
	resp.Body.Close()

	t.Logf("Cleanup: Destroyed session %s", sessionID)
}
