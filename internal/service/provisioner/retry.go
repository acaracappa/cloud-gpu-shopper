package provisioner

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// waitForSSHVerifyAsyncWithRetry wraps SSH verification with auto-retry support.
// On failure (timeout or instance stopped), if auto_retry is enabled, it triggers
// a new session with a comparable offer.
func (s *Service) waitForSSHVerifyAsyncWithRetry(ctx context.Context, sessionID string, privateKey string, prov provider.Provider, sshTimeout time.Duration, req models.CreateSessionRequest) {
	s.waitForSSHVerifyAsyncWithTimeout(ctx, sessionID, privateKey, prov, sshTimeout)

	// After SSH verification completes (success or failure), check if we need to retry
	session, err := s.store.Get(context.Background(), sessionID)
	if err != nil {
		return
	}

	// Only trigger async retry if session failed and auto-retry is enabled
	if session.Status == models.StatusFailed && session.AutoRetry && session.RetryCount < session.MaxRetries && s.inventory != nil {
		s.triggerAsyncRetry(session, req)
	}
}

// triggerAsyncRetry finds a comparable offer and creates a new session after async failure.
func (s *Service) triggerAsyncRetry(failedSession *models.Session, originalReq models.CreateSessionRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	reason := "ssh_timeout"
	if strings.Contains(failedSession.Error, "instance stopped") {
		reason = "instance_stopped"
	}
	metrics.RecordRetryAttempt(failedSession.Provider, failedSession.RetryScope, reason)

	// Build exclusion list from previously failed offers
	var failedOfferIDs []string
	if failedSession.FailedOffers != "" {
		failedOfferIDs = strings.Split(failedSession.FailedOffers, ",")
	}
	failedOfferIDs = append(failedOfferIDs, failedSession.OfferID)

	// Get original offer info for comparison and build machine ID exclusion list
	var failedMachineIDs []string
	originalOffer, err := s.inventory.GetOffer(ctx, failedSession.OfferID)
	if err != nil {
		// Build a synthetic offer from session data for comparison
		originalOffer = &models.GPUOffer{
			ID:           failedSession.OfferID,
			Provider:     failedSession.Provider,
			GPUType:      failedSession.GPUType,
			GPUCount:     failedSession.GPUCount,
			PricePerHour: failedSession.PricePerHour,
		}
	}
	if originalOffer.MachineID != "" {
		failedMachineIDs = append(failedMachineIDs, originalOffer.MachineID)
	}

	alternatives, err := s.inventory.FindComparableOffers(ctx, originalOffer, failedSession.RetryScope, failedOfferIDs, failedMachineIDs)
	if err != nil || len(alternatives) == 0 {
		s.logger.Warn("async retry: no comparable offers found",
			slog.String("session_id", failedSession.ID),
			slog.String("scope", failedSession.RetryScope))
		metrics.RecordRetryExhausted(failedSession.Provider, failedSession.RetryScope)
		return
	}

	nextOffer := models.SelectFromTopN(alternatives, 3, 1.3)
	originalReq.OfferID = nextOffer.ID

	s.logger.Info("async retry: reprovisioning with new offer",
		slog.String("failed_session", failedSession.ID),
		slog.String("new_offer", nextOffer.ID),
		slog.Int("retry_count", failedSession.RetryCount+1))

	newSession, err := s.createSessionWithRetry(ctx, originalReq, nextOffer, failedOfferIDs, failedMachineIDs, failedSession.RetryCount+1, failedSession.ID)
	if err != nil {
		s.logger.Warn("async retry failed",
			slog.String("session_id", failedSession.ID),
			slog.String("error", err.Error()))
		return
	}

	// Link parent to child
	failedSession.RetryChildID = newSession.ID
	failedSession.FailedOffers = strings.Join(failedOfferIDs, ",")
	_ = s.store.Update(context.Background(), failedSession)

	metrics.RecordRetrySuccess(failedSession.Provider, failedSession.RetryScope)
	s.logger.Info("async retry: successfully reprovisioned",
		slog.String("old_session", failedSession.ID),
		slog.String("new_session", newSession.ID))
}
