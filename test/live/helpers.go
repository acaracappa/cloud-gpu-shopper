//go:build live
// +build live

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// LiveTestEnv provides helpers for live testing
type LiveTestEnv struct {
	Config      *TestConfig
	Watchdog    *Watchdog
	Diagnostics *DiagnosticsManager
	client      *http.Client
}

// NewLiveTestEnv creates a new live test environment
func NewLiveTestEnv(config *TestConfig, watchdog *Watchdog) *LiveTestEnv {
	env := &LiveTestEnv{
		Config:   config,
		Watchdog: watchdog,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}

	// Initialize diagnostics manager
	diagConfig := DefaultDiagnosticsConfig()
	env.Diagnostics = NewDiagnosticsManager(diagConfig, env)

	// Set diagnostics manager on watchdog
	if watchdog != nil {
		watchdog.SetDiagnosticsManager(env.Diagnostics)
	}

	return env
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
	Session       Session `json:"session"`
	AgentToken    string  `json:"agent_token"`
	SSHPrivateKey string  `json:"ssh_private_key,omitempty"`
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
// Filters out very cheap instances (< $0.05/hr) which tend to be unreliable
func (e *LiveTestEnv) FindCheapestFromProvider(t *testing.T, provider Provider) *GPUOffer {
	cfg, ok := e.Config.Providers[provider]
	require.True(t, ok, "Provider %s not configured", provider)
	require.True(t, cfg.Enabled, "Provider %s not enabled", provider)

	offers := e.ListInventory(t, provider, cfg.MaxPriceHour)
	require.NotEmpty(t, offers, "No cheap GPUs available from %s", provider)

	// Filter out very cheap instances which tend to be unreliable
	minPrice := 0.05 // Minimum $0.05/hr for reliability
	var filtered []GPUOffer
	for _, o := range offers {
		if o.PricePerHour >= minPrice {
			filtered = append(filtered, o)
		}
	}

	// Fall back to all offers if no offers above minimum price
	if len(filtered) == 0 {
		filtered = offers
	}

	// Sort by price
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].PricePerHour < filtered[j].PricePerHour
	})

	cheapest := filtered[0]
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

// CreateSessionWithError provisions a new GPU session and returns error on failure
func (e *LiveTestEnv) CreateSessionWithError(t *testing.T, req CreateSessionRequest) (*CreateSessionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/v1/sessions", e.Config.ServerURL)
	resp, err := e.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create session failed: %d - %s", resp.StatusCode, string(respBody))
	}

	var result CreateSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

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

	return &result, nil
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

// ConnectSSH creates and connects an SSH helper for a session
func (e *LiveTestEnv) ConnectSSH(t *testing.T, session *Session, privateKey string) *SSHHelper {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ssh := NewSSHHelperFromSession(session, privateKey)
	err := ssh.Connect(ctx)
	require.NoError(t, err, "Failed to connect SSH to %s:%d", session.SSHHost, session.SSHPort)

	t.Logf("SSH connected to %s@%s:%d", session.SSHUser, session.SSHHost, session.SSHPort)
	return ssh
}

// WaitForAgentReady waits for the agent to be ready and responding
func (e *LiveTestEnv) WaitForAgentReady(t *testing.T, ssh *SSHHelper, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check if agent process is running
		running, err := ssh.ProcessRunning(ctx, "gpu-agent")
		if err == nil && running {
			// Try to hit the health endpoint
			output, err := ssh.CurlEndpoint(ctx, "http://localhost:8081/health")
			if err == nil && output != "" {
				t.Log("Agent is ready and responding")
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			// Continue waiting
		}
	}

	return fmt.Errorf("timeout waiting for agent to be ready")
}

// SendHeartbeat sends a manual heartbeat for testing
func (e *LiveTestEnv) SendHeartbeat(t *testing.T, sessionID, agentToken string, req HeartbeatRequest) {
	body, err := json.Marshal(req)
	require.NoError(t, err)

	url := fmt.Sprintf("%s/api/v1/sessions/%s/heartbeat", e.Config.ServerURL, sessionID)
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(httpReq)
	require.NoError(t, err)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("Heartbeat failed: %d - %s", resp.StatusCode, string(respBody))
	}
}

// HeartbeatRequest represents a heartbeat request
type HeartbeatRequest struct {
	SessionID    string  `json:"session_id"`
	AgentToken   string  `json:"agent_token"`
	Status       string  `json:"status"`
	IdleSeconds  int     `json:"idle_seconds,omitempty"`
	GPUUtilPct   float64 `json:"gpu_util_pct,omitempty"`
	MemoryUsedMB int     `json:"memory_used_mb,omitempty"`
}

// GetDiagnosticsCollector returns or creates a diagnostics collector for a session
func (e *LiveTestEnv) GetDiagnosticsCollector(sessionID string) *DiagnosticsCollector {
	if e.Diagnostics == nil {
		return nil
	}
	return e.Diagnostics.GetCollector(sessionID)
}

// CollectDiagnostics collects a diagnostic snapshot for a session
func (e *LiveTestEnv) CollectDiagnostics(t *testing.T, sessionID, label string) {
	if e.Diagnostics == nil || !e.Diagnostics.IsEnabled() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	collector := e.GetDiagnosticsCollector(sessionID)
	if collector == nil {
		return
	}

	snapshot, err := collector.CollectSnapshot(ctx, label)
	if err != nil {
		t.Logf("Warning: Failed to collect diagnostics: %v", err)
		return
	}

	t.Logf("Collected diagnostics snapshot: %s (errors: %d)", label, len(snapshot.Errors))
}

// FindReliableOffer finds a GPU offer with better availability characteristics
func (e *LiveTestEnv) FindReliableOffer(t *testing.T, provider Provider) *GPUOffer {
	cfg, ok := e.Config.Providers[provider]
	require.True(t, ok, "Provider %s not configured", provider)
	require.True(t, cfg.Enabled, "Provider %s not enabled", provider)

	offers := e.ListInventory(t, provider, cfg.MaxPriceHour)
	require.NotEmpty(t, offers, "No GPUs available from %s under $%.2f/hr", provider, cfg.MaxPriceHour)

	// Sort by availability score, then price
	sort.Slice(offers, func(i, j int) bool {
		scoreI := availabilityScore(offers[i].GPUType)
		scoreJ := availabilityScore(offers[j].GPUType)
		if scoreI != scoreJ {
			return scoreI > scoreJ
		}
		return offers[i].PricePerHour < offers[j].PricePerHour
	})

	selected := offers[0]
	t.Logf("Selected reliable offer: %s (%s) @ $%.4f/hr (availability score: %d)",
		selected.GPUType, selected.ID, selected.PricePerHour, availabilityScore(selected.GPUType))

	return &selected
}

// availabilityScore returns a score for GPU availability (higher = more available)
func availabilityScore(gpuType string) int {
	gpuLower := strings.ToLower(gpuType)

	// Lower demand = higher availability score
	switch {
	case strings.Contains(gpuLower, "3080"):
		return 4 // Good availability
	case strings.Contains(gpuLower, "3090"):
		return 3 // Good availability
	case strings.Contains(gpuLower, "4080"):
		return 2 // Moderate
	case strings.Contains(gpuLower, "4090"):
		return 1 // High demand
	case strings.Contains(gpuLower, "a100"):
		return 0 // Very high demand
	case strings.Contains(gpuLower, "h100"):
		return 0 // Very high demand
	default:
		return 2 // Default moderate
	}
}

// ProvisionReliableGPU provisions a GPU with better availability characteristics
func (e *LiveTestEnv) ProvisionReliableGPU(t *testing.T, provider Provider) *CreateSessionResponse {
	var offer *GPUOffer

	if provider == "" {
		// Find cheapest across all providers
		offer, provider = e.FindCheapestGPU(t)
	} else {
		// Use reliability scoring for specific provider
		offer = e.FindReliableOffer(t, provider)
	}

	return e.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        offer.ID,
		WorkloadType:   "live-test",
		ReservationHrs: 1,
	})
}

// FindMidRangeOffer finds a GPU offer from the middle price range.
// This strategy picks GPUs that are less likely to be claimed by others:
// - Avoids cheapest offers (high competition)
// - Avoids most expensive offers (wasteful)
// - Prefers higher GPU counts (less likely to be fully utilized)
// - Adds randomness to avoid always picking the same offer
func (e *LiveTestEnv) FindMidRangeOffer(t *testing.T, provider Provider) *GPUOffer {
	cfg, ok := e.Config.Providers[provider]
	require.True(t, ok, "Provider %s not configured", provider)
	require.True(t, cfg.Enabled, "Provider %s not enabled", provider)

	offers := e.ListInventory(t, provider, cfg.MaxPriceHour)
	require.NotEmpty(t, offers, "No GPUs available from %s under $%.2f/hr", provider, cfg.MaxPriceHour)

	// If only 1-2 offers, just return the first one with some preference for GPU count
	if len(offers) <= 2 {
		sort.Slice(offers, func(i, j int) bool {
			return offers[i].GPUCount > offers[j].GPUCount
		})
		t.Logf("Limited offers (%d), selecting: %s (%s) @ $%.4f/hr, %d GPUs",
			len(offers), offers[0].GPUType, offers[0].ID, offers[0].PricePerHour, offers[0].GPUCount)
		return &offers[0]
	}

	// Sort by price ascending
	sort.Slice(offers, func(i, j int) bool {
		return offers[i].PricePerHour < offers[j].PricePerHour
	})

	// Calculate the middle 50% range (25th to 75th percentile)
	startIdx := len(offers) / 4         // 25th percentile
	endIdx := (len(offers) * 3) / 4     // 75th percentile
	if endIdx <= startIdx {
		endIdx = startIdx + 1
	}
	if endIdx > len(offers) {
		endIdx = len(offers)
	}

	midRangeOffers := offers[startIdx:endIdx]
	t.Logf("Mid-range selection: using offers %d-%d of %d (price range $%.4f - $%.4f)",
		startIdx, endIdx-1, len(offers),
		midRangeOffers[0].PricePerHour,
		midRangeOffers[len(midRangeOffers)-1].PricePerHour)

	// Score each mid-range offer: prefer higher GPU counts and good availability
	type scoredOffer struct {
		offer *GPUOffer
		score float64
	}
	scored := make([]scoredOffer, len(midRangeOffers))
	for i := range midRangeOffers {
		// Score based on:
		// - GPU count (more = better, as multi-GPU is less common demand)
		// - Availability score (from existing function)
		// - Small random factor to avoid always picking the same one
		gpuCountScore := float64(midRangeOffers[i].GPUCount) * 10.0
		availScore := float64(availabilityScore(midRangeOffers[i].GPUType)) * 5.0
		randomFactor := rand.Float64() * 3.0 // Random 0-3 points

		scored[i] = scoredOffer{
			offer: &midRangeOffers[i],
			score: gpuCountScore + availScore + randomFactor,
		}
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	selected := scored[0].offer
	t.Logf("Selected mid-range offer: %s (%s) @ $%.4f/hr, %d GPUs, score=%.2f",
		selected.GPUType, selected.ID, selected.PricePerHour, selected.GPUCount, scored[0].score)

	return selected
}

// FindMidRangeGPU finds a mid-range priced GPU across enabled providers
func (e *LiveTestEnv) FindMidRangeGPU(t *testing.T) (*GPUOffer, Provider) {
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

	require.NotEmpty(t, allOffers, "No GPUs available from any provider")

	// If only 1-2 offers, just return the first one with preference for GPU count
	if len(allOffers) <= 2 {
		sort.Slice(allOffers, func(i, j int) bool {
			return allOffers[i].GPUCount > allOffers[j].GPUCount
		})
		selected := allOffers[0]
		t.Logf("Limited offers (%d), selecting: %s (%s) @ $%.4f/hr from %s",
			len(allOffers), selected.GPUType, selected.ID, selected.PricePerHour, selected.Provider)
		return &selected, Provider(selected.Provider)
	}

	// Sort by price ascending
	sort.Slice(allOffers, func(i, j int) bool {
		return allOffers[i].PricePerHour < allOffers[j].PricePerHour
	})

	// Calculate the middle 50% range
	startIdx := len(allOffers) / 4
	endIdx := (len(allOffers) * 3) / 4
	if endIdx <= startIdx {
		endIdx = startIdx + 1
	}
	if endIdx > len(allOffers) {
		endIdx = len(allOffers)
	}

	midRangeOffers := allOffers[startIdx:endIdx]

	// Score and select
	type scoredOffer struct {
		offer *GPUOffer
		score float64
	}
	scored := make([]scoredOffer, len(midRangeOffers))
	for i := range midRangeOffers {
		gpuCountScore := float64(midRangeOffers[i].GPUCount) * 10.0
		availScore := float64(availabilityScore(midRangeOffers[i].GPUType)) * 5.0
		randomFactor := rand.Float64() * 3.0

		scored[i] = scoredOffer{
			offer: &midRangeOffers[i],
			score: gpuCountScore + availScore + randomFactor,
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	selected := scored[0].offer
	t.Logf("Selected mid-range GPU: %s (%s) @ $%.4f/hr, %d GPUs from %s",
		selected.GPUType, selected.ID, selected.PricePerHour, selected.GPUCount, selected.Provider)

	return selected, Provider(selected.Provider)
}

// ProvisionMidRangeGPU provisions a GPU from the middle price range
func (e *LiveTestEnv) ProvisionMidRangeGPU(t *testing.T, provider Provider) *CreateSessionResponse {
	var offer *GPUOffer

	if provider == "" {
		offer, provider = e.FindMidRangeGPU(t)
	} else {
		offer = e.FindMidRangeOffer(t, provider)
	}

	return e.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        offer.ID,
		WorkloadType:   "live-test",
		ReservationHrs: 1,
	})
}
