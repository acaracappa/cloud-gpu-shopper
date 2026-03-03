package provisioner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/logging"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/storage"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// createSessionWithRetry is the internal implementation that supports retry.
// failedOfferIDs tracks offers that already failed, retryCount is the current attempt,
// retryParentID links back to the original session if this is a retry.
func (s *Service) createSessionWithRetry(ctx context.Context, req models.CreateSessionRequest, offer *models.GPUOffer, failedOfferIDs []string, failedMachineIDs []string, retryCount int, retryParentID string) (*models.Session, error) {
	s.logger.Info("creating session",
		slog.String("consumer_id", req.ConsumerID),
		slog.String("offer_id", req.OfferID),
		slog.String("provider", offer.Provider),
		slog.Int("retry_count", retryCount))

	// Check provider balance (warn-only)
	if prov, err := s.providers.Get(offer.Provider); err == nil {
		if bp, ok := prov.(provider.BalanceProvider); ok {
			if balance, err := bp.GetAccountBalance(ctx); err == nil {
				if balance.Balance < s.lowBalanceThreshold {
					s.logger.Warn("LOW BALANCE: provider account balance is below threshold",
						slog.String("provider", offer.Provider),
						slog.Float64("balance", balance.Balance),
						slog.Float64("threshold", s.lowBalanceThreshold),
						slog.String("currency", balance.Currency))
				}
			} else {
				s.logger.Debug("could not check provider balance",
					slog.String("provider", offer.Provider),
					slog.String("error", err.Error()))
			}
		}
	}

	// Check for existing active session for this consumer and offer
	// Skip this check for retry attempts (the consumer already has a failed session for a different offer)
	if retryCount == 0 {
		existing, err := s.store.GetActiveSessionByConsumerAndOffer(ctx, req.ConsumerID, req.OfferID)
		if err == nil && existing != nil {
			return nil, &DuplicateSessionError{
				ConsumerID: req.ConsumerID,
				OfferID:    req.OfferID,
				SessionID:  existing.ID,
				Status:     existing.Status,
			}
		}
		// Only fail on unexpected errors (not ErrNotFound which is expected)
		if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, storage.ErrNotFound) {
			return nil, fmt.Errorf("failed to check for existing session: %w", err)
		}
	}

	// Generate SSH key pair
	privateKey, publicKey, err := s.generateSSHKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate SSH key: %w", err)
	}

	now := s.now()
	expiresAt := now.Add(time.Duration(req.ReservationHrs) * time.Hour)

	// Set defaults
	storagePolicy := req.StoragePolicy
	if storagePolicy == "" {
		storagePolicy = models.StorageDestroy
	}

	// Build failed offers string
	failedOffersStr := strings.Join(failedOfferIDs, ",")

	// PHASE 1: Create session record in database (survives crashes)
	session := &models.Session{
		ID:             uuid.New().String(),
		ConsumerID:     req.ConsumerID,
		Provider:       offer.Provider,
		OfferID:        req.OfferID,
		GPUType:        offer.GPUType,
		GPUCount:       offer.GPUCount,
		Status:         models.StatusPending,
		SSHPublicKey:   publicKey,
		SSHPrivateKey:  privateKey,
		WorkloadType:   req.WorkloadType,
		ReservationHrs: req.ReservationHrs,
		IdleThreshold:  req.IdleThreshold,
		StoragePolicy:  storagePolicy,
		PricePerHour:   offer.PricePerHour,
		CreatedAt:      now,
		ExpiresAt:      expiresAt,
		AutoRetry:      req.AutoRetry,
		MaxRetries:     req.MaxRetries,
		RetryScope:     req.RetryScope,
		RetryCount:     retryCount,
		RetryParentID:  retryParentID,
		FailedOffers:   failedOffersStr,
	}

	if err := s.store.Create(ctx, session); err != nil {
		// Bug #47 fix: Handle race condition where another request created the session
		// The database unique constraint catches this race at the DB level
		if errors.Is(err, storage.ErrAlreadyExists) {
			// Try to find the existing session to return proper error
			existing, findErr := s.store.GetActiveSessionByConsumerAndOffer(ctx, req.ConsumerID, req.OfferID)
			if findErr == nil && existing != nil {
				return nil, &DuplicateSessionError{
					ConsumerID: req.ConsumerID,
					OfferID:    req.OfferID,
					SessionID:  existing.ID,
					Status:     existing.Status,
				}
			}
			// If we can't find it, still return a duplicate error
			return nil, &DuplicateSessionError{
				ConsumerID: req.ConsumerID,
				OfferID:    req.OfferID,
			}
		}
		return nil, fmt.Errorf("failed to create session record: %w", err)
	}

	metrics.UpdateSessionStatus(session.Provider, "", string(models.StatusPending))

	s.logger.Info("session record created",
		slog.String("session_id", session.ID),
		slog.String("status", string(session.Status)))

	// PHASE 2: Call provider to create instance
	prov, err := s.providers.Get(offer.Provider)
	if err != nil {
		s.failSession(ctx, session, fmt.Sprintf("provider not found: %s", offer.Provider))
		return nil, err
	}

	tags := s.buildInstanceTags(session.ID, req.ConsumerID, expiresAt)

	instanceReq := provider.CreateInstanceRequest{
		OfferID:      offer.ID,
		SessionID:    session.ID,
		SSHPublicKey: publicKey,
		Tags:         tags,
	}

	// Template-based provisioning (Vast.ai)
	if req.TemplateHashID != "" {
		instanceReq.TemplateHashID = req.TemplateHashID
		session.TemplateHashID = req.TemplateHashID
	}

	// Storage configuration with disk estimation
	s.logger.Info("storage configuration",
		slog.Int("request_disk_gb", req.DiskGB),
		slog.String("model_id", req.ModelID),
		slog.String("quantization", req.Quantization))

	diskEstimation := EstimateDiskRequirements(req.ModelID, req.Quantization, req.TemplateHashID, req.TemplateRecommendedDiskGB)

	if diskEstimation != nil {
		s.logger.Info("disk estimation",
			slog.Int("minimum_gb", diskEstimation.MinimumGB),
			slog.Int("recommended_gb", diskEstimation.RecommendedGB),
			slog.Float64("model_weight_gb", diskEstimation.ModelWeightGB))

		if req.DiskGB > 0 {
			if err := ValidateDiskSpace(req.DiskGB, diskEstimation); err != nil {
				return nil, err
			}
			instanceReq.DiskGB = req.DiskGB
			session.DiskGB = req.DiskGB
			s.logger.Info("disk configured (user-specified)", slog.Int("disk_gb", req.DiskGB))
		} else {
			instanceReq.DiskGB = diskEstimation.RecommendedGB
			session.DiskGB = diskEstimation.RecommendedGB
			s.logger.Info("disk auto-calculated", slog.Int("disk_gb", diskEstimation.RecommendedGB))
		}
	} else if req.DiskGB > 0 {
		instanceReq.DiskGB = req.DiskGB
		session.DiskGB = req.DiskGB
		s.logger.Info("disk configured (no estimation)", slog.Int("disk_gb", req.DiskGB))
	}

	// Configure for entrypoint mode if specified
	if req.LaunchMode == models.LaunchModeEntrypoint {
		instanceReq.LaunchMode = provider.LaunchModeEntrypoint
		instanceReq.DockerImage = req.DockerImage
		instanceReq.ExposedPorts = req.ExposedPorts
		instanceReq.WorkloadConfig = s.buildWorkloadConfig(req)
	}

	// Auto-inject benchmark script for benchmark workload type
	if req.WorkloadType == models.WorkloadBenchmark && req.OnStartCmd == "" {
		instanceReq.OnStartCmd = buildBenchmarkOnStart(session.ID, offer)
		s.logger.Info("auto-injected benchmark script",
			slog.String("session_id", session.ID))
	}

	// Pass through on-start command if specified (overrides auto-inject)
	if req.OnStartCmd != "" {
		instanceReq.OnStartCmd = req.OnStartCmd
	}

	session.Status = models.StatusProvisioning
	if err := s.store.Update(ctx, session); err != nil {
		s.logger.Error("failed to update session to provisioning",
			slog.String("session_id", session.ID),
			slog.String("error", err.Error()))
	}
	metrics.UpdateSessionStatus(session.Provider, string(models.StatusPending), string(models.StatusProvisioning))

	instance, err := prov.CreateInstance(ctx, instanceReq)
	if err != nil {
		s.failSession(ctx, session, fmt.Sprintf("provider create failed: %s", err.Error()))

		// Record global offer failure and evict from cache for cross-session intelligence
		if s.inventory != nil {
			failType := "unknown"
			if provider.ShouldRetryWithDifferentOffer(err) {
				failType = "stale_inventory"
			}
			s.inventory.RecordOfferFailure(offer.ID, offer.Provider, offer.GPUType, offer.MachineID, failType, err.Error())
			s.inventory.EvictOffer(offer.ID)
		}

		// Check if this is a retryable error and auto-retry is enabled
		if provider.ShouldRetryWithDifferentOffer(err) && req.AutoRetry && retryCount < req.MaxRetries && s.inventory != nil {
			metrics.RecordRetryAttempt(offer.Provider, req.RetryScope, "stale_inventory")

			newFailedOffers := append(failedOfferIDs, offer.ID)
			newFailedMachines := failedMachineIDs
			if offer.MachineID != "" {
				newFailedMachines = append(append([]string{}, failedMachineIDs...), offer.MachineID)
			}
			s.logger.Info("auto-retrying with different offer",
				slog.String("failed_offer", offer.ID),
				slog.Int("retry_count", retryCount+1),
				slog.Int("max_retries", req.MaxRetries),
				slog.String("scope", req.RetryScope))

			alternatives, findErr := s.inventory.FindComparableOffers(ctx, offer, req.RetryScope, newFailedOffers, newFailedMachines)
			if findErr != nil {
				s.logger.Warn("failed to find comparable offers for retry",
					slog.String("error", findErr.Error()))
			} else if len(alternatives) > 0 {
				nextOffer := models.SelectFromTopN(alternatives, 3, 1.3)
				req.OfferID = nextOffer.ID

				retrySession, retryErr := s.createSessionWithRetry(ctx, req, nextOffer, newFailedOffers, newFailedMachines, retryCount+1, session.ID)
				if retryErr == nil {
					// Success — link parent to child
					session.RetryChildID = retrySession.ID
					session.FailedOffers = strings.Join(newFailedOffers, ",")
					_ = s.store.Update(ctx, session)

					metrics.RecordRetrySuccess(offer.Provider, req.RetryScope)
					return retrySession, nil
				}

				s.logger.Warn("retry attempt failed",
					slog.Int("retry_count", retryCount+1),
					slog.String("error", retryErr.Error()))
				return nil, retryErr
			} else {
				s.logger.Warn("no comparable offers found for retry",
					slog.String("scope", req.RetryScope))
			}

			// All retries exhausted or no alternatives found
			metrics.RecordRetryExhausted(offer.Provider, req.RetryScope)
		}

		// Return stale inventory error if applicable
		if provider.ShouldRetryWithDifferentOffer(err) {
			return nil, &StaleInventoryError{
				OfferID:     offer.ID,
				Provider:    offer.Provider,
				OriginalErr: err,
			}
		}

		return nil, fmt.Errorf("failed to create instance: %w", err)
	}

	// PHASE 3: Update session with provider instance info
	session.ProviderID = instance.ProviderInstanceID
	session.SSHHost = instance.SSHHost
	session.SSHPort = instance.SSHPort
	session.SSHUser = instance.SSHUser
	if session.SSHUser == "" {
		session.SSHUser = "root"
	}
	if instance.ActualPricePerHour > 0 {
		session.PricePerHour = instance.ActualPricePerHour
	}

	if err := s.store.Update(ctx, session); err != nil {
		// Critical: Instance exists but we failed to record it
		s.logger.Error("CRITICAL: failed to update session after provision, attempting cleanup",
			slog.String("session_id", session.ID),
			slog.String("provider_id", instance.ProviderInstanceID),
			slog.String("error", err.Error()))

		destroyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if destroyErr := prov.DestroyInstance(destroyCtx, instance.ProviderInstanceID); destroyErr != nil {
			s.logger.Error("CRITICAL: failed to destroy orphaned instance after DB failure",
				slog.String("session_id", session.ID),
				slog.String("provider_id", instance.ProviderInstanceID),
				slog.String("provider", session.Provider),
				slog.String("destroy_error", destroyErr.Error()),
				slog.String("db_error", err.Error()))
			metrics.RecordOrphanDetected()
		} else {
			s.logger.Info("successfully destroyed orphaned instance after DB failure",
				slog.String("session_id", session.ID),
				slog.String("provider_id", instance.ProviderInstanceID))
		}

		return nil, fmt.Errorf("failed to update session: %w", err)
	}

	s.logger.Info("instance created",
		slog.String("session_id", session.ID),
		slog.String("provider_id", instance.ProviderInstanceID))

	logging.Audit(ctx, "session_provisioned",
		"session_id", session.ID,
		"consumer_id", session.ConsumerID,
		"provider", session.Provider,
		"provider_id", instance.ProviderInstanceID,
		"gpu_type", session.GPUType,
		"gpu_count", session.GPUCount,
		"price_per_hour", session.PricePerHour,
		"reservation_hours", session.ReservationHrs)

	metrics.RecordSessionCreated(session.Provider)

	// PHASE 4: Wait for verification (async - don't block API)
	if req.LaunchMode == models.LaunchModeEntrypoint {
		verifyCtx, cancel := context.WithTimeout(context.Background(), s.apiVerifyTimeout+5*time.Second)
		s.verifyWg.Add(1)
		go func() {
			defer s.verifyWg.Done()
			defer cancel()
			s.waitForAPIVerifyAsync(verifyCtx, session.ID, prov)
		}()
	} else {
		sshTimeout := s.sshVerifyTimeout
		if req.TemplateRecommendedSSHTimeout > 0 {
			sshTimeout = req.TemplateRecommendedSSHTimeout
			s.logger.Info("using template-recommended SSH timeout",
				slog.Duration("timeout", sshTimeout))
		}
		verifyCtx, cancel := context.WithTimeout(context.Background(), sshTimeout+5*time.Second)
		s.verifyWg.Add(1)
		go func() {
			defer s.verifyWg.Done()
			defer cancel()
			s.waitForSSHVerifyAsyncWithRetry(verifyCtx, session.ID, privateKey, prov, sshTimeout, req)
		}()
	}

	return session, nil
}
