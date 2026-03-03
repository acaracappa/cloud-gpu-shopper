package provisioner

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// ProgressiveBackoff implements an exponential backoff strategy with a maximum cap.
// This reduces load on providers when instances take a long time to become ready.
type ProgressiveBackoff struct {
	Initial    time.Duration // Initial interval
	Max        time.Duration // Maximum interval cap
	Multiplier float64       // Multiplier for each step
	mu         sync.Mutex    // Protects current
	current    time.Duration // Current interval
}

// NewProgressiveBackoff creates a new progressive backoff with sensible defaults
func NewProgressiveBackoff(initial, max time.Duration, multiplier float64) *ProgressiveBackoff {
	return &ProgressiveBackoff{
		Initial:    initial,
		Max:        max,
		Multiplier: multiplier,
		current:    initial,
	}
}

// Next returns the current interval and advances to the next one
func (pb *ProgressiveBackoff) Next() time.Duration {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	current := pb.current

	// Calculate next interval
	next := time.Duration(float64(pb.current) * pb.Multiplier)
	if next > pb.Max {
		next = pb.Max
	}
	pb.current = next

	return current
}

// Reset resets the backoff to the initial interval
func (pb *ProgressiveBackoff) Reset() {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.current = pb.Initial
}

// Current returns the current interval without advancing
func (pb *ProgressiveBackoff) Current() time.Duration {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	return pb.current
}

// waitForSSHVerifyAsync waits for SSH verification in the background with default timeout.
// privateKey is passed directly because it's not stored in the database for security
func (s *Service) waitForSSHVerifyAsync(ctx context.Context, sessionID string, privateKey string, prov provider.Provider) {
	s.waitForSSHVerifyAsyncWithTimeout(ctx, sessionID, privateKey, prov, s.sshVerifyTimeout)
}

// waitForSSHVerifyAsyncWithTimeout waits for SSH verification with a custom timeout.
func (s *Service) waitForSSHVerifyAsyncWithTimeout(ctx context.Context, sessionID string, privateKey string, prov provider.Provider, sshTimeout time.Duration) {
	logger := s.logger.With(slog.String("session_id", sessionID))
	logger.Info("waiting for SSH verification")

	start := time.Now()

	// TensorDock-specific: wait for cloud-init to complete before polling
	session, err := s.store.Get(ctx, sessionID)
	if err == nil && session.Provider == "tensordock" {
		logger.Info("TensorDock: waiting for cloud-init before SSH polling",
			slog.Duration("delay", TensorDockCloudInitDelay))
		select {
		case <-time.After(TensorDockCloudInitDelay):
		case <-ctx.Done():
			return
		}
	}

	if err == nil && session.Provider == "bluelobster" {
		logger.Info("Blue Lobster: waiting for post-boot stabilization before SSH polling",
			slog.Duration("delay", BlueLobsterBootDelay))
		select {
		case <-time.After(BlueLobsterBootDelay):
		case <-ctx.Done():
			return
		}
	}

	// Log warning about insecure host key verification once per session
	logger.Warn("using insecure host key verification for commodity GPU instance",
		slog.String("reason", "host keys are unknown for spot instances"))

	backoff := NewProgressiveBackoff(s.sshCheckInterval, s.sshMaxInterval, s.sshBackoffMultiplier)

	nextInterval := backoff.Next()
	pollTimer := time.NewTimer(nextInterval)
	defer pollTimer.Stop()

	timeout := time.NewTimer(sshTimeout)
	defer timeout.Stop()

	attemptCount := 0
	lastErrorType := "none"
	lastError := ""
	consecutivePermanentErrors := 0
	consecutiveNeeded := 1
	if session != nil && session.Provider == "bluelobster" {
		consecutiveNeeded = 2
	}
	consecutiveOK := 0
	for {
		select {
		case <-timeout.C:
			// SSH verification timeout - destroy instance and fail session
			logger.Error("SSH verification timeout, destroying instance",
				slog.Int("attempts", attemptCount),
				slog.String("last_error_type", lastErrorType),
				slog.String("last_error", lastError),
				slog.Duration("elapsed", time.Since(start)))
			session, err := s.store.Get(ctx, sessionID)
			if err != nil {
				logger.Error("failed to get session", slog.String("error", err.Error()))
				return
			}

			if session.ProviderID != "" {
				if err := prov.DestroyInstance(ctx, session.ProviderID); err != nil {
					logger.Error("failed to destroy instance after SSH timeout",
						slog.String("error", err.Error()))
				}
			}

			s.failSession(ctx, session, "SSH verification timeout")
			metrics.RecordSSHVerifyFailure()
			metrics.RecordSessionDestroyed(session.Provider, "ssh_verify_timeout")

			if s.inventory != nil {
				s.inventory.RecordOfferFailure(session.OfferID, session.Provider, session.GPUType, "", "ssh_timeout", "SSH verification timeout")
				s.inventory.EvictOffer(session.OfferID)
			}
			return

		case <-pollTimer.C:
			attemptCount++

			currentInterval := backoff.Current()
			logger.Debug("SSH poll attempt",
				slog.Int("attempt", attemptCount),
				slog.Duration("next_interval", currentInterval))

			session, err := s.store.Get(ctx, sessionID)
			if err != nil {
				logger.Error("failed to get session", slog.String("error", err.Error()))
				nextInterval = backoff.Next()
				pollTimer.Reset(nextInterval)
				continue
			}

			if session.IsTerminal() {
				logger.Info("session is terminal, stopping SSH verification")
				return
			}

			// Poll provider for SSH info if we don't have it yet
			if session.SSHHost == "" && session.ProviderID != "" {
				status, err := prov.GetInstanceStatus(ctx, session.ProviderID)
				if err != nil {
					logger.Debug("failed to get instance status", slog.String("error", err.Error()))
					nextInterval = backoff.Next()
					pollTimer.Reset(nextInterval)
					continue
				}

				// BUG-011 fix: Fail fast if instance stopped unexpectedly
				if !status.Running && status.Status != "" &&
					status.Status != "creating" && status.Status != "starting" &&
					status.Status != "provisioning" && status.Status != "booting" &&
					status.Status != "loading" {
					logger.Error("instance stopped unexpectedly",
						slog.String("status", status.Status),
						slog.String("error_detail", status.Error),
						slog.String("provider_id", session.ProviderID))

					if err := prov.DestroyInstance(ctx, session.ProviderID); err != nil {
						logger.Error("failed to destroy stopped instance",
							slog.String("error", err.Error()))
					}

					failReason := classifyInstanceStopReason(status.Status, status.Error)
					s.failSession(ctx, session, failReason)
					metrics.RecordSessionDestroyed(session.Provider, "instance_stopped")

					if s.inventory != nil {
						s.inventory.RecordOfferFailure(session.OfferID, session.Provider, session.GPUType, "", "instance_stopped", failReason)
						s.inventory.EvictOffer(session.OfferID)
					}
					return
				}

				if status.SSHHost != "" {
					session.SSHHost = status.SSHHost
					if status.SSHPort != 0 {
						session.SSHPort = status.SSHPort
					}
					if status.SSHUser != "" {
						session.SSHUser = status.SSHUser
					}
					if err := s.store.Update(ctx, session); err != nil {
						logger.Error("failed to update SSH info", slog.String("error", err.Error()))
					} else {
						logger.Info("SSH info updated",
							slog.String("ssh_host", session.SSHHost),
							slog.Int("ssh_port", session.SSHPort))
						backoff.Reset()
					}
				}
			}

			// Try SSH verification if we have connection info
			if session.SSHHost != "" && session.SSHPort > 0 {
				logger.Debug("attempting SSH verification",
					slog.String("host", session.SSHHost),
					slog.Int("port", session.SSHPort))

				err := s.sshVerifier.VerifyOnce(ctx, session.SSHHost, session.SSHPort, session.SSHUser, privateKey)
				if err == nil {
					consecutiveOK++
					if consecutiveOK < consecutiveNeeded {
						logger.Info("SSH succeeded, verifying stability",
							slog.Int("consecutive", consecutiveOK),
							slog.Int("needed", consecutiveNeeded))
						nextInterval = 5 * time.Second
						pollTimer.Reset(nextInterval)
						continue
					}
					// SSH verified successfully
					duration := time.Since(start)
					logger.Info("SSH verification successful",
						slog.Duration("duration", duration),
						slog.Int("attempts", attemptCount))

					oldStatus := session.Status
					session.Status = models.StatusRunning
					if err := s.store.Update(ctx, session); err != nil {
						logger.Error("failed to update session to running", slog.String("error", err.Error()))
					}

					metrics.UpdateSessionStatus(session.Provider, string(oldStatus), string(models.StatusRunning))
					metrics.RecordSSHVerifyDuration(session.Provider, duration)
					metrics.RecordSSHVerifyAttempts(session.Provider, attemptCount)
					metrics.RecordProvisioningDuration(session.Provider, duration)

					go s.validateCUDAVersionAsync(session, privateKey, logger)
					go s.validateDiskSpaceAsync(session, privateKey, logger)

					return
				}

				lastErrorType = classifySSHError(err)
				consecutiveOK = 0
				lastError = err.Error()
				logger.Info("SSH verification attempt failed",
					slog.Int("attempt", attemptCount),
					slog.String("error_type", lastErrorType),
					slog.String("host", session.SSHHost),
					slog.Int("port", session.SSHPort),
					slog.String("error", lastError))
				metrics.RecordSSHVerifyError(session.Provider, lastErrorType)

				// Fail fast on permanent SSH errors (auth_failed, key_parse_failed)
				if lastErrorType == "auth_failed" || lastErrorType == "key_parse_failed" {
					consecutivePermanentErrors++
					if consecutivePermanentErrors >= 3 {
						logger.Error("permanent SSH error detected, failing session early",
							slog.String("error_type", lastErrorType),
							slog.Int("consecutive_errors", consecutivePermanentErrors))

						// Set FailedOffers BEFORE failSession so it's persisted atomically
						if session.OfferID != "" {
							if session.FailedOffers == "" {
								session.FailedOffers = session.OfferID
							} else if !strings.Contains(session.FailedOffers, session.OfferID) {
								session.FailedOffers = session.FailedOffers + "," + session.OfferID
							}
						}

						s.failSession(ctx, session, "permanent SSH error: "+lastErrorType)

						if s.inventory != nil {
							s.inventory.RecordOfferFailure(session.OfferID, session.Provider, session.GPUType, "", "auth_failed", "permanent SSH error: "+lastErrorType)
							s.inventory.EvictOffer(session.OfferID)
						}

						metrics.RecordSSHVerifyFailure()
						metrics.RecordSessionDestroyed(session.Provider, "permanent_ssh_error")
						return
					}
				} else {
					consecutivePermanentErrors = 0
				}
			}

			nextInterval = backoff.Next()
			pollTimer.Reset(nextInterval)

		case <-ctx.Done():
			logger.Warn("context cancelled while waiting for SSH verification")
			return
		}
	}
}

// waitForAPIVerifyAsync waits for API endpoint verification in the background
func (s *Service) waitForAPIVerifyAsync(ctx context.Context, sessionID string, prov provider.Provider) {
	logger := s.logger.With(slog.String("session_id", sessionID))
	logger.Info("waiting for API verification")

	start := time.Now()

	ticker := time.NewTicker(s.apiCheckInterval)
	defer ticker.Stop()

	timeout := time.NewTimer(s.apiVerifyTimeout)
	defer timeout.Stop()

	for {
		select {
		case <-timeout.C:
			logger.Error("API verification timeout, destroying instance")
			session, err := s.store.Get(ctx, sessionID)
			if err != nil {
				logger.Error("failed to get session", slog.String("error", err.Error()))
				return
			}

			if session.ProviderID != "" {
				if err := prov.DestroyInstance(ctx, session.ProviderID); err != nil {
					logger.Error("failed to destroy instance after API timeout",
						slog.String("error", err.Error()))
				}
			}

			s.failSession(ctx, session, "API verification timeout")
			metrics.RecordAPIVerifyFailure()
			metrics.RecordSessionDestroyed(session.Provider, "api_verify_timeout")
			return

		case <-ticker.C:
			session, err := s.store.Get(ctx, sessionID)
			if err != nil {
				logger.Error("failed to get session", slog.String("error", err.Error()))
				continue
			}

			if session.IsTerminal() {
				logger.Info("session is terminal, stopping API verification")
				return
			}

			// Poll provider for connection info if we don't have it yet
			if session.SSHHost == "" && session.ProviderID != "" {
				status, err := prov.GetInstanceStatus(ctx, session.ProviderID)
				if err != nil {
					logger.Debug("failed to get instance status", slog.String("error", err.Error()))
					continue
				}
				if status.SSHHost != "" {
					session.SSHHost = status.SSHHost
					if status.SSHPort != 0 {
						session.SSHPort = status.SSHPort
					}
					if status.SSHUser != "" {
						session.SSHUser = status.SSHUser
					}
					if err := s.store.Update(ctx, session); err != nil {
						logger.Error("failed to update connection info", slog.String("error", err.Error()))
					} else {
						logger.Info("connection info updated",
							slog.String("host", session.SSHHost))
					}
				}
			}

			// Try API verification if we have host info
			if session.SSHHost != "" && session.APIPort > 0 {
				apiURL := fmt.Sprintf("http://%s:%d/health", session.SSHHost, session.APIPort)
				logger.Debug("attempting API verification",
					slog.String("url", apiURL))

				err := s.httpVerifier.CheckHealth(ctx, apiURL)
				if err == nil {
					duration := time.Since(start)
					logger.Info("API verification successful",
						slog.Duration("duration", duration))

					oldStatus := session.Status
					session.Status = models.StatusRunning
					session.APIEndpoint = fmt.Sprintf("http://%s:%d", session.SSHHost, session.APIPort)
					if err := s.store.Update(ctx, session); err != nil {
						logger.Error("failed to update session to running", slog.String("error", err.Error()))
					}

					metrics.UpdateSessionStatus(session.Provider, string(oldStatus), string(models.StatusRunning))
					metrics.RecordAPIVerifyDuration(session.Provider, duration)
					metrics.RecordProvisioningDuration(session.Provider, duration)
					return
				}

				logger.Debug("API verification attempt failed", slog.String("error", err.Error()))
			}

		case <-ctx.Done():
			logger.Warn("context cancelled while waiting for API verification")
			return
		}
	}
}

// DefaultHTTPVerifier implements HTTPVerifier with standard HTTP client
type DefaultHTTPVerifier struct {
	client  *http.Client
	timeout time.Duration
}

// NewDefaultHTTPVerifier creates a new default HTTP verifier
func NewDefaultHTTPVerifier() *DefaultHTTPVerifier {
	return &DefaultHTTPVerifier{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		timeout: 10 * time.Second,
	}
}

// CheckHealth checks if an HTTP endpoint is responding
func (v *DefaultHTTPVerifier) CheckHealth(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// Also accept 404 as "alive" since some servers return that for /health
	if resp.StatusCode == 404 {
		return nil
	}

	return fmt.Errorf("unhealthy status: %d", resp.StatusCode)
}
