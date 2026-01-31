package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// APIClient communicates with the GPU Shopper server
type APIClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewAPIClient creates a new API client
func NewAPIClient(baseURL string) *APIClient {
	return &APIClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// InventoryOffer represents an available GPU offer
type InventoryOffer struct {
	ID           string  `json:"id"`
	Provider     string  `json:"provider"`
	GPUType      string  `json:"gpu_type"`
	GPUCount     int     `json:"gpu_count"`
	VRAMGB       int     `json:"vram_gb"`
	PricePerHour float64 `json:"price_per_hour"`
	Available    bool    `json:"available"`
	Region       string  `json:"region,omitempty"`
}

// InventoryResponse is the response from /api/v1/inventory
type InventoryResponse struct {
	Offers []InventoryOffer `json:"offers"`
	Count  int              `json:"count"`
}

// SessionRequest is the request to create a session
type SessionRequest struct {
	ConsumerID       string `json:"consumer_id"`
	OfferID          string `json:"offer_id"`
	WorkloadType     string `json:"workload_type"`
	ReservationHours int    `json:"reservation_hours"`
	SSHPublicKey     string `json:"ssh_public_key,omitempty"`
}

// SessionResponse represents a session from the API
type SessionResponse struct {
	ID           string    `json:"id"`
	ConsumerID   string    `json:"consumer_id"`
	Provider     string    `json:"provider"`
	ProviderID   string    `json:"provider_instance_id"`
	OfferID      string    `json:"offer_id"`
	GPUType      string    `json:"gpu_type"`
	GPUCount     int       `json:"gpu_count"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	SSHHost      string    `json:"ssh_host"`
	SSHPort      int       `json:"ssh_port"`
	SSHUser      string    `json:"ssh_user"`
	PricePerHour float64   `json:"price_per_hour"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// CreateSessionResponse wraps the session creation response
type CreateSessionResponse struct {
	Session       SessionResponse `json:"session"`
	SSHPrivateKey string          `json:"ssh_private_key,omitempty"`
}

// GetSessionResponse wraps the session get response
type GetSessionResponse struct {
	Session SessionResponse `json:"session"`
}

// GetInventory fetches available GPU offers
func (c *APIClient) GetInventory(ctx context.Context, gpuType string, minVRAM int) ([]InventoryOffer, error) {
	apiURL := fmt.Sprintf("%s/api/v1/inventory", c.baseURL)
	if gpuType != "" {
		apiURL += fmt.Sprintf("?gpu_type=%s", url.QueryEscape(gpuType))
	}

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch inventory: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("inventory request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var inventory InventoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&inventory); err != nil {
		return nil, fmt.Errorf("failed to decode inventory: %w", err)
	}

	// Filter by VRAM if specified
	if minVRAM > 0 {
		var filtered []InventoryOffer
		for _, offer := range inventory.Offers {
			if offer.VRAMGB >= minVRAM {
				filtered = append(filtered, offer)
			}
		}
		return filtered, nil
	}

	return inventory.Offers, nil
}

// CreateSession provisions a new GPU session
// Returns the full CreateSessionResponse which includes the SSH private key (only available at creation time)
func (c *APIClient) CreateSession(ctx context.Context, req *SessionRequest) (*CreateSessionResponse, error) {
	url := fmt.Sprintf("%s/api/v1/sessions", c.baseURL)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("session creation failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var createResp CreateSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return nil, fmt.Errorf("failed to decode session: %w", err)
	}

	return &createResp, nil
}

// GetSession retrieves session details
func (c *APIClient) GetSession(ctx context.Context, sessionID string) (*SessionResponse, error) {
	url := fmt.Sprintf("%s/api/v1/sessions/%s", c.baseURL, sessionID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get session failed with status %d: %s", resp.StatusCode, string(body))
	}

	var session SessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("failed to decode session: %w", err)
	}

	return &session, nil
}

// WaitForSession polls until the session is ready or fails
func (c *APIClient) WaitForSession(ctx context.Context, sessionID string, timeout time.Duration) (*SessionResponse, error) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("timeout waiting for session to be ready")
			}

			session, err := c.GetSession(ctx, sessionID)
			if err != nil {
				return nil, err
			}

			switch session.Status {
			case "running":
				return session, nil
			case "failed", "stopped", "terminated":
				return nil, fmt.Errorf("session failed: %s - %s", session.Status, session.Error)
			default:
				// Still provisioning, continue waiting
				fmt.Printf("  Session status: %s...\n", session.Status)
			}
		}
	}
}

// DeleteSession terminates a session
func (c *APIClient) DeleteSession(ctx context.Context, sessionID string) error {
	url := fmt.Sprintf("%s/api/v1/sessions/%s", c.baseURL, sessionID)

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete session failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// MarkSessionDone signals that work is complete
func (c *APIClient) MarkSessionDone(ctx context.Context, sessionID string) error {
	url := fmt.Sprintf("%s/api/v1/sessions/%s/done", c.baseURL, sessionID)

	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to mark session done: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mark done failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// HealthCheck verifies the server is reachable
func (c *APIClient) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/health", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server unhealthy: status %d", resp.StatusCode)
	}

	return nil
}
