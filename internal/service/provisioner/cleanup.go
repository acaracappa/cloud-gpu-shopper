package provisioner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/logging"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// getDestroyLock returns a per-session mutex for destroy operations
// Bug #6 fix: Ensures only one destroy operation runs per session
func (s *Service) getDestroyLock(sessionID string) *sync.Mutex {
	s.destroyLocksMu.Lock()
	defer s.destroyLocksMu.Unlock()

	if lock, exists := s.destroyLocks[sessionID]; exists {
		return lock
	}

	lock := &sync.Mutex{}
	s.destroyLocks[sessionID] = lock
	return lock
}

// cleanupDestroyLock removes the lock for a session after destroy completes
func (s *Service) cleanupDestroyLock(sessionID string) {
	s.destroyLocksMu.Lock()
	defer s.destroyLocksMu.Unlock()
	delete(s.destroyLocks, sessionID)
}

// DestroySession destroys a session with verification
func (s *Service) DestroySession(ctx context.Context, sessionID string) error {
	// Bug #6 fix: Acquire per-session lock to prevent concurrent destroy operations
	lock := s.getDestroyLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	defer s.cleanupDestroyLock(sessionID)

	session, err := s.store.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// Check for terminal OR stopping state (another destroy may have started)
	if session.IsTerminal() || session.Status == models.StatusStopping {
		s.logger.Debug("session already terminal or stopping, skipping destroy",
			slog.String("session_id", sessionID),
			slog.String("status", string(session.Status)))
		return nil // Already terminated or being terminated
	}

	s.logger.Info("destroying session",
		slog.String("session_id", sessionID),
		slog.String("provider_id", session.ProviderID))

	// Bug #46 fix: Track old status for metrics update
	preDestroyStatus := session.Status
	session.Status = models.StatusStopping
	if err := s.store.Update(ctx, session); err != nil {
		s.logger.Error("failed to update session to stopping",
			slog.String("session_id", sessionID),
			slog.String("error", err.Error()))
	}
	// Bug #46 fix: Update metrics gauge on state transition to stopping
	metrics.UpdateSessionStatus(session.Provider, string(preDestroyStatus), string(models.StatusStopping))

	// Get provider
	prov, err := s.providers.Get(session.Provider)
	if err != nil {
		return fmt.Errorf("provider not found: %w", err)
	}

	// Destroy with verification
	if err := s.destroyWithVerification(ctx, session, prov); err != nil {
		return err
	}

	oldStatus := session.Status
	session.Status = models.StatusStopped
	session.StoppedAt = s.now()
	if err := s.store.Update(ctx, session); err != nil {
		s.logger.Error("failed to update session to stopped",
			slog.String("session_id", sessionID),
			slog.String("error", err.Error()))
	}

	s.logger.Info("session destroyed",
		slog.String("session_id", sessionID))

	// Record audit log and metrics
	logging.Audit(ctx, "session_destroyed",
		"session_id", session.ID,
		"consumer_id", session.ConsumerID,
		"provider", session.Provider,
		"provider_id", session.ProviderID,
		"reason", "user_requested")

	metrics.RecordSessionDestroyed(session.Provider, "user_requested")
	metrics.UpdateSessionStatus(session.Provider, string(oldStatus), string(models.StatusStopped))

	return nil
}

// destroyWithVerification destroys an instance and verifies destruction
func (s *Service) destroyWithVerification(ctx context.Context, session *models.Session, prov provider.Provider) error {
	if session.ProviderID == "" {
		// No provider instance to destroy
		return nil
	}

	for attempt := 0; attempt < s.destroyRetries; attempt++ {
		s.logger.Debug("destroy attempt",
			slog.String("session_id", session.ID),
			slog.Int("attempt", attempt+1))

		// Call destroy
		if err := prov.DestroyInstance(ctx, session.ProviderID); err != nil {
			s.logger.Warn("destroy call failed",
				slog.String("session_id", session.ID),
				slog.String("error", err.Error()))
			// Continue to verification - instance might still be gone
		}

		// Bug #4 fix: Use select with context to respect cancellation during wait
		delay := time.Duration(attempt+1) * 5 * time.Second
		select {
		case <-time.After(delay):
			// Continue to verification
		case <-ctx.Done():
			return ctx.Err()
		}

		// Verify destruction
		status, err := prov.GetInstanceStatus(ctx, session.ProviderID)
		if err != nil {
			// Instance not found = successfully destroyed
			if errors.Is(err, provider.ErrInstanceNotFound) {
				return nil
			}
			s.logger.Warn("status check failed",
				slog.String("session_id", session.ID),
				slog.Int("attempt", attempt+1),
				slog.String("error", err.Error()))
			continue
		}

		if !status.Running {
			return nil
		}

		s.logger.Warn("instance still running",
			slog.String("session_id", session.ID),
			slog.Int("attempt", attempt+1))
	}

	// Failed to verify destruction
	s.logger.Error("CRITICAL: failed to verify instance destruction",
		slog.String("session_id", session.ID),
		slog.String("provider_id", session.ProviderID),
		slog.Int("attempts", s.destroyRetries))

	// Record metrics for destroy failure
	metrics.RecordDestroyFailure()

	return &DestroyVerificationError{
		SessionID:  session.ID,
		ProviderID: session.ProviderID,
		Attempts:   s.destroyRetries,
	}
}

// failSession marks a session as failed
func (s *Service) failSession(ctx context.Context, session *models.Session, reason string) {
	// Destroy provider instance if one exists (defensive — prevents orphans)
	if session.ProviderID != "" {
		if prov, err := s.providers.Get(session.Provider); err == nil {
			if err := prov.DestroyInstance(ctx, session.ProviderID); err != nil {
				// 404 is expected if caller already destroyed — only log other errors
				if !errors.Is(err, provider.ErrInstanceNotFound) {
					s.logger.Warn("failed to destroy instance during session failure",
						slog.String("session_id", session.ID),
						slog.String("provider_id", session.ProviderID),
						slog.String("error", err.Error()))
				}
			} else {
				s.logger.Info("AUDIT",
					slog.Bool("audit", true),
					slog.String("operation", "instance_destroyed_on_fail"),
					slog.String("session_id", session.ID),
					slog.String("provider_id", session.ProviderID),
					slog.String("provider", session.Provider))
			}
		}
	}

	oldStatus := session.Status
	session.Status = models.StatusFailed
	session.Error = reason
	session.StoppedAt = s.now()
	if err := s.store.Update(ctx, session); err != nil {
		s.logger.Error("failed to update session to failed",
			slog.String("session_id", session.ID),
			slog.String("error", err.Error()))
	}

	// Bug #46 fix: Update metrics gauge on state transition
	metrics.UpdateSessionStatus(session.Provider, string(oldStatus), string(models.StatusFailed))

	// Record final cost so short-lived failed sessions are captured
	if s.costRecorder != nil {
		if err := s.costRecorder.RecordFinalCost(ctx, session); err != nil {
			s.logger.Error("failed to record final cost for failed session",
				slog.String("session_id", session.ID),
				slog.String("error", err.Error()))
		}
	}
}
