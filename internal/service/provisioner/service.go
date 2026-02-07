package provisioner

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/logging"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	sshverify "github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/ssh"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/storage"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// Compile-time check that sshverify.Verifier satisfies SSHVerifier interface
var _ SSHVerifier = (*sshverify.Verifier)(nil)

const (
	// DefaultSSHVerifyTimeout is how long to wait for SSH verification
	// Increased to 8 minutes to accommodate TensorDock cloud-init delays
	DefaultSSHVerifyTimeout = 8 * time.Minute

	// DefaultSSHCheckInterval is the initial interval for SSH polling
	// This will increase with progressive backoff
	DefaultSSHCheckInterval = 15 * time.Second

	// DefaultSSHMaxInterval is the maximum interval between SSH poll attempts
	DefaultSSHMaxInterval = 60 * time.Second

	// DefaultSSHBackoffMultiplier is the multiplier for progressive backoff
	DefaultSSHBackoffMultiplier = 1.5

	// DefaultAPIVerifyTimeout is how long to wait for API verification (entrypoint mode)
	DefaultAPIVerifyTimeout = 10 * time.Minute

	// DefaultAPICheckInterval is how often to retry API health check
	DefaultAPICheckInterval = 15 * time.Second

	// DefaultDestroyTimeout is the max time to wait for destroy verification
	DefaultDestroyTimeout = 5 * time.Minute

	// DefaultDestroyRetries is the max number of destroy attempts
	DefaultDestroyRetries = 10

	// DefaultSSHKeyBits is the RSA key size
	DefaultSSHKeyBits = 4096

	// TensorDockCloudInitDelay is the time to wait for TensorDock cloud-init before SSH polling.
	// This needs to be long enough for:
	// 1. TensorDock's cloud-init SSH key setup (which writes 0 bytes to root)
	// 2. Our runcmd to run and install the actual SSH key
	// 3. TensorDock's scripts to restart the SSH service
	// Based on live testing, 90 seconds is sufficient.
	TensorDockCloudInitDelay = 90 * time.Second
)

// ProgressiveBackoff implements an exponential backoff strategy with a maximum cap.
// This reduces load on providers when instances take a long time to become ready.
// Bug #21 fix: Added mutex for thread safety
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
// Bug #21 fix: Thread-safe with mutex
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
// Bug #21 fix: Thread-safe with mutex
func (pb *ProgressiveBackoff) Reset() {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.current = pb.Initial
}

// Current returns the current interval without advancing
// Bug #21 fix: Thread-safe with mutex
func (pb *ProgressiveBackoff) Current() time.Duration {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	return pb.current
}

// SessionStore defines the interface for session persistence
type SessionStore interface {
	Create(ctx context.Context, session *models.Session) error
	Get(ctx context.Context, id string) (*models.Session, error)
	Update(ctx context.Context, session *models.Session) error
	GetActiveSessionByConsumerAndOffer(ctx context.Context, consumerID, offerID string) (*models.Session, error)
	List(ctx context.Context, filter models.SessionListFilter) ([]*models.Session, error)
}

// ProviderRegistry provides access to provider clients
type ProviderRegistry interface {
	Get(name string) (provider.Provider, error)
}

// InventoryFinder provides access to comparable offer lookup for auto-retry
// and global offer failure tracking
type InventoryFinder interface {
	FindComparableOffers(ctx context.Context, original *models.GPUOffer, scope string, excludeIDs []string) ([]models.GPUOffer, error)
	GetOffer(ctx context.Context, offerID string) (*models.GPUOffer, error)
	RecordOfferFailure(offerID, provider, gpuType, failureType, reason string)
}

// CostRecorder records final costs for terminated sessions.
type CostRecorder interface {
	RecordFinalCost(ctx context.Context, session *models.Session) error
}

// SSHVerifier defines the interface for SSH verification
type SSHVerifier interface {
	// VerifyOnce attempts a single SSH connection verification (no retries)
	VerifyOnce(ctx context.Context, host string, port int, user, privateKey string) error
}

// HTTPVerifier defines the interface for HTTP endpoint verification
type HTTPVerifier interface {
	// CheckHealth checks if an HTTP endpoint is responding
	CheckHealth(ctx context.Context, url string) error
}

// Service handles GPU session provisioning and destruction
type Service struct {
	store        SessionStore
	providers    ProviderRegistry
	inventory    InventoryFinder // Optional: needed for auto-retry
	costRecorder CostRecorder   // Optional: records final cost on session termination
	logger       *slog.Logger
	deploymentID string

	// SSH verification
	sshVerifier          SSHVerifier
	sshVerifyTimeout     time.Duration
	sshCheckInterval     time.Duration
	sshMaxInterval       time.Duration
	sshBackoffMultiplier float64

	// API verification (for entrypoint mode)
	httpVerifier     HTTPVerifier
	apiVerifyTimeout time.Duration
	apiCheckInterval time.Duration

	// Configuration
	destroyTimeout time.Duration
	destroyRetries int
	sshKeyBits     int

	// For time mocking in tests
	now func() time.Time

	// Verification goroutine tracking (for testing)
	verifyWg sync.WaitGroup

	// Bug #6 fix: Per-session destroy locks to prevent concurrent destroy operations
	destroyLocks   map[string]*sync.Mutex
	destroyLocksMu sync.Mutex
}

// Option configures the provisioner service
type Option func(*Service)

// WithLogger sets a custom logger
func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) {
		s.logger = logger
	}
}

// WithDeploymentID sets the deployment identifier for instance tagging
func WithDeploymentID(id string) Option {
	return func(s *Service) {
		s.deploymentID = id
	}
}

// WithSSHVerifyTimeout sets how long to wait for SSH verification
func WithSSHVerifyTimeout(d time.Duration) Option {
	return func(s *Service) {
		s.sshVerifyTimeout = d
	}
}

// WithSSHCheckInterval sets the initial interval for SSH connection retries
func WithSSHCheckInterval(d time.Duration) Option {
	return func(s *Service) {
		s.sshCheckInterval = d
	}
}

// WithSSHMaxInterval sets the maximum interval between SSH poll attempts
func WithSSHMaxInterval(d time.Duration) Option {
	return func(s *Service) {
		s.sshMaxInterval = d
	}
}

// WithSSHBackoffMultiplier sets the multiplier for progressive backoff
func WithSSHBackoffMultiplier(m float64) Option {
	return func(s *Service) {
		s.sshBackoffMultiplier = m
	}
}

// WithDestroyRetries sets the max number of destroy verification attempts
func WithDestroyRetries(n int) Option {
	return func(s *Service) {
		s.destroyRetries = n
	}
}

// WithSSHVerifier sets a custom SSH verifier (useful for testing)
func WithSSHVerifier(v SSHVerifier) Option {
	return func(s *Service) {
		s.sshVerifier = v
	}
}

// WithHTTPVerifier sets a custom HTTP verifier (useful for testing)
func WithHTTPVerifier(v HTTPVerifier) Option {
	return func(s *Service) {
		s.httpVerifier = v
	}
}

// WithAPIVerifyTimeout sets how long to wait for API verification
func WithAPIVerifyTimeout(d time.Duration) Option {
	return func(s *Service) {
		s.apiVerifyTimeout = d
	}
}

// WithAPICheckInterval sets how often to retry API health check
func WithAPICheckInterval(d time.Duration) Option {
	return func(s *Service) {
		s.apiCheckInterval = d
	}
}

// WithTimeFunc sets a custom time function (for testing)
func WithTimeFunc(fn func() time.Time) Option {
	return func(s *Service) {
		s.now = fn
	}
}

// WithInventory sets the inventory finder for auto-retry support
func WithInventory(inv InventoryFinder) Option {
	return func(s *Service) {
		s.inventory = inv
	}
}

// WithCostRecorder configures an optional cost recorder for the provisioner.
func WithCostRecorder(cr CostRecorder) Option {
	return func(s *Service) {
		s.costRecorder = cr
	}
}

// New creates a new provisioner service
func New(store SessionStore, providers ProviderRegistry, opts ...Option) *Service {
	s := &Service{
		store:                store,
		providers:            providers,
		logger:               slog.Default(),
		deploymentID:         uuid.New().String(),
		sshVerifyTimeout:     DefaultSSHVerifyTimeout,
		sshCheckInterval:     DefaultSSHCheckInterval,
		sshMaxInterval:       DefaultSSHMaxInterval,
		sshBackoffMultiplier: DefaultSSHBackoffMultiplier,
		apiVerifyTimeout:     DefaultAPIVerifyTimeout,
		apiCheckInterval:     DefaultAPICheckInterval,
		destroyTimeout:       DefaultDestroyTimeout,
		destroyRetries:       DefaultDestroyRetries,
		sshKeyBits:           DefaultSSHKeyBits,
		now:                  time.Now,
		destroyLocks:         make(map[string]*sync.Mutex),
	}

	for _, opt := range opts {
		opt(s)
	}

	// Create default SSH verifier if not provided
	if s.sshVerifier == nil {
		s.sshVerifier = sshverify.NewVerifier(
			sshverify.WithVerifyTimeout(s.sshVerifyTimeout),
			sshverify.WithCheckInterval(s.sshCheckInterval),
		)
	}

	// Create default HTTP verifier if not provided
	if s.httpVerifier == nil {
		s.httpVerifier = NewDefaultHTTPVerifier()
	}

	return s
}

// CreateSession provisions a new GPU session using two-phase provisioning.
// If auto_retry is enabled, it will automatically try comparable offers on failure.
func (s *Service) CreateSession(ctx context.Context, req models.CreateSessionRequest, offer *models.GPUOffer) (*models.Session, error) {
	// Validate and cap MaxRetries
	if req.AutoRetry && req.MaxRetries <= 0 {
		req.MaxRetries = 3
	}
	if req.MaxRetries > 5 {
		req.MaxRetries = 5
	}
	if req.RetryScope == "" {
		req.RetryScope = "same_gpu"
	}

	return s.createSessionWithRetry(ctx, req, offer, nil, 0, "")
}

// createSessionWithRetry is the internal implementation that supports retry.
// failedOfferIDs tracks offers that already failed, retryCount is the current attempt,
// retryParentID links back to the original session if this is a retry.
func (s *Service) createSessionWithRetry(ctx context.Context, req models.CreateSessionRequest, offer *models.GPUOffer, failedOfferIDs []string, retryCount int, retryParentID string) (*models.Session, error) {
	s.logger.Info("creating session",
		slog.String("consumer_id", req.ConsumerID),
		slog.String("offer_id", req.OfferID),
		slog.String("provider", offer.Provider),
		slog.Int("retry_count", retryCount))

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

	// Bug fix: Increment pending gauge when session is first created.
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

	// Build the provider request based on launch mode
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

	session.Status = models.StatusProvisioning
	if err := s.store.Update(ctx, session); err != nil {
		s.logger.Error("failed to update session to provisioning",
			slog.String("session_id", session.ID),
			slog.String("error", err.Error()))
	}
	// Bug #46 fix: Update metrics BEFORE CreateInstance so failSession can properly decrement
	metrics.UpdateSessionStatus(session.Provider, string(models.StatusPending), string(models.StatusProvisioning))

	instance, err := prov.CreateInstance(ctx, instanceReq)
	if err != nil {
		s.failSession(ctx, session, fmt.Sprintf("provider create failed: %s", err.Error()))

		// Record global offer failure for cross-session intelligence
		if s.inventory != nil {
			failType := "unknown"
			if provider.ShouldRetryWithDifferentOffer(err) {
				failType = "stale_inventory"
			}
			s.inventory.RecordOfferFailure(offer.ID, offer.Provider, offer.GPUType, failType, err.Error())
		}

		// Check if this is a retryable error and auto-retry is enabled
		if provider.ShouldRetryWithDifferentOffer(err) && req.AutoRetry && retryCount < req.MaxRetries && s.inventory != nil {
			metrics.RecordRetryAttempt(offer.Provider, req.RetryScope, "stale_inventory")

			newFailedOffers := append(failedOfferIDs, offer.ID)
			s.logger.Info("auto-retrying with different offer",
				slog.String("failed_offer", offer.ID),
				slog.Int("retry_count", retryCount+1),
				slog.Int("max_retries", req.MaxRetries),
				slog.String("scope", req.RetryScope))

			alternatives, findErr := s.inventory.FindComparableOffers(ctx, offer, req.RetryScope, newFailedOffers)
			if findErr != nil {
				s.logger.Warn("failed to find comparable offers for retry",
					slog.String("error", findErr.Error()))
			} else if len(alternatives) > 0 {
				nextOffer := &alternatives[0]
				req.OfferID = nextOffer.ID

				// Update the parent session's retry_child_id will be set after child creation
				retrySession, retryErr := s.createSessionWithRetry(ctx, req, nextOffer, newFailedOffers, retryCount+1, session.ID)
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
				// retryErr propagates the StaleInventoryError or final error
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

	// Record audit log and metrics
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
		// SSH mode: wait for SSH connectivity
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

// waitForSSHVerifyAsync waits for SSH verification in the background with default timeout.
// privateKey is passed directly because it's not stored in the database for security
func (s *Service) waitForSSHVerifyAsync(ctx context.Context, sessionID string, privateKey string, prov provider.Provider) {
	s.waitForSSHVerifyAsyncWithTimeout(ctx, sessionID, privateKey, prov, s.sshVerifyTimeout)
}

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

	// Get original offer info for comparison
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

	alternatives, err := s.inventory.FindComparableOffers(ctx, originalOffer, failedSession.RetryScope, failedOfferIDs)
	if err != nil || len(alternatives) == 0 {
		s.logger.Warn("async retry: no comparable offers found",
			slog.String("session_id", failedSession.ID),
			slog.String("scope", failedSession.RetryScope))
		metrics.RecordRetryExhausted(failedSession.Provider, failedSession.RetryScope)
		return
	}

	nextOffer := &alternatives[0]
	originalReq.OfferID = nextOffer.ID

	s.logger.Info("async retry: reprovisioning with new offer",
		slog.String("failed_session", failedSession.ID),
		slog.String("new_offer", nextOffer.ID),
		slog.Int("retry_count", failedSession.RetryCount+1))

	newSession, err := s.createSessionWithRetry(ctx, originalReq, nextOffer, failedOfferIDs, failedSession.RetryCount+1, failedSession.ID)
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

// waitForSSHVerifyAsyncWithTimeout waits for SSH verification with a custom timeout.
// BUG-005: Support template-specific timeouts for heavy images like vLLM.
func (s *Service) waitForSSHVerifyAsyncWithTimeout(ctx context.Context, sessionID string, privateKey string, prov provider.Provider, sshTimeout time.Duration) {
	logger := s.logger.With(slog.String("session_id", sessionID))
	logger.Info("waiting for SSH verification")

	start := time.Now()

	// TensorDock-specific: wait for cloud-init to complete before polling
	// TensorDock VMs need extra time for cloud-init runcmd to execute
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

	// Log warning about insecure host key verification once per session
	// This is intentional for commodity GPU instances where host keys are unknown
	logger.Warn("using insecure host key verification for commodity GPU instance",
		slog.String("reason", "host keys are unknown for spot instances"))

	// Create progressive backoff for SSH polling
	// This reduces provider API load when instances take a long time to be ready
	backoff := NewProgressiveBackoff(s.sshCheckInterval, s.sshMaxInterval, s.sshBackoffMultiplier)

	// Use a timer instead of ticker for progressive backoff
	nextInterval := backoff.Next()
	pollTimer := time.NewTimer(nextInterval)
	defer pollTimer.Stop()

	timeout := time.NewTimer(sshTimeout)
	defer timeout.Stop()

	attemptCount := 0
	lastErrorType := "none"
	lastError := ""
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
			// Bug #94 fix: Record session destroyed when SSH verification times out
			metrics.RecordSessionDestroyed(session.Provider, "ssh_verify_timeout")

			// Record global offer failure for cross-session intelligence
			if s.inventory != nil {
				s.inventory.RecordOfferFailure(session.OfferID, session.Provider, session.GPUType, "ssh_timeout", "SSH verification timeout")
			}
			return

		case <-pollTimer.C:
			attemptCount++

			// Log the current backoff interval
			currentInterval := backoff.Current()
			logger.Debug("SSH poll attempt",
				slog.Int("attempt", attemptCount),
				slog.Duration("next_interval", currentInterval))

			// Get current session state
			session, err := s.store.Get(ctx, sessionID)
			if err != nil {
				logger.Error("failed to get session", slog.String("error", err.Error()))
				// Schedule next poll with progressive backoff
				nextInterval = backoff.Next()
				pollTimer.Reset(nextInterval)
				continue
			}

			// Check if session is still in a valid state
			if session.IsTerminal() {
				logger.Info("session is terminal, stopping SSH verification")
				return
			}

			// Poll provider for SSH info if we don't have it yet
			if session.SSHHost == "" && session.ProviderID != "" {
				status, err := prov.GetInstanceStatus(ctx, session.ProviderID)
				if err != nil {
					logger.Debug("failed to get instance status", slog.String("error", err.Error()))
					// Schedule next poll with progressive backoff
					nextInterval = backoff.Next()
					pollTimer.Reset(nextInterval)
					continue
				}

				// BUG-011 fix: Fail fast if instance stopped unexpectedly
				// Don't fail for transient states like "creating", "starting", or "loading"
				if !status.Running && status.Status != "" &&
					status.Status != "creating" && status.Status != "starting" &&
					status.Status != "provisioning" && status.Status != "booting" &&
					status.Status != "loading" {
					logger.Error("instance stopped unexpectedly",
						slog.String("status", status.Status),
						slog.String("error_detail", status.Error),
						slog.String("provider_id", session.ProviderID))

					// Attempt to destroy the failed instance
					if err := prov.DestroyInstance(ctx, session.ProviderID); err != nil {
						logger.Error("failed to destroy stopped instance",
							slog.String("error", err.Error()))
					}

					failReason := classifyInstanceStopReason(status.Status, status.Error)
					s.failSession(ctx, session, failReason)
					metrics.RecordSessionDestroyed(session.Provider, "instance_stopped")

					// Record global offer failure for cross-session intelligence
					if s.inventory != nil {
						s.inventory.RecordOfferFailure(session.OfferID, session.Provider, session.GPUType, "instance_stopped", failReason)
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
						// Reset backoff when we get new SSH info
						backoff.Reset()
					}
				}
			}

			// Try SSH verification if we have connection info
			if session.SSHHost != "" && session.SSHPort > 0 {
				logger.Debug("attempting SSH verification",
					slog.String("host", session.SSHHost),
					slog.Int("port", session.SSHPort))

				// Try a single connection attempt using the private key passed to this function
				err := s.sshVerifier.VerifyOnce(ctx, session.SSHHost, session.SSHPort, session.SSHUser, privateKey)
				if err == nil {
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

					// Bug #46 fix: Update metrics gauge on state transition
					metrics.UpdateSessionStatus(session.Provider, string(oldStatus), string(models.StatusRunning))
					metrics.RecordSSHVerifyDuration(session.Provider, duration)
					metrics.RecordSSHVerifyAttempts(session.Provider, attemptCount)
					// Bug #57 fix: Record provisioning duration when session becomes running
					metrics.RecordProvisioningDuration(session.Provider, duration)

					// BUG-004: Validate CUDA version after SSH success (async, non-blocking)
					// This is informational - we don't fail the session on mismatch
					go s.validateCUDAVersionAsync(session, privateKey, logger)

					// Post-provision disk space check (async, non-blocking)
					go s.validateDiskSpaceAsync(session, privateKey, logger)

					return
				}

				lastErrorType = classifySSHError(err)
				lastError = err.Error()
				logger.Info("SSH verification attempt failed",
					slog.Int("attempt", attemptCount),
					slog.String("error_type", lastErrorType),
					slog.String("host", session.SSHHost),
					slog.Int("port", session.SSHPort),
					slog.String("error", lastError))
				metrics.RecordSSHVerifyError(session.Provider, lastErrorType)
			}

			// Schedule next poll with progressive backoff
			nextInterval = backoff.Next()
			pollTimer.Reset(nextInterval)

		case <-ctx.Done():
			logger.Warn("context cancelled while waiting for SSH verification")
			return
		}
	}
}

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

// GetSession retrieves a session by ID
func (s *Service) GetSession(ctx context.Context, sessionID string) (*models.Session, error) {
	return s.store.Get(ctx, sessionID)
}

// ListSessions returns sessions matching the filter criteria
func (s *Service) ListSessions(ctx context.Context, filter models.SessionListFilter) ([]*models.Session, error) {
	return s.store.List(ctx, filter)
}

// validateCUDAVersionAsync runs CUDA validation asynchronously after SSH verification.
// BUG-004: This is informational only - we log warnings but don't fail the session.
// The validation helps identify provider inventory mismatches.
func (s *Service) validateCUDAVersionAsync(session *models.Session, privateKey string, logger *slog.Logger) {
	// Use a short timeout for validation - we don't want to hold resources
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create SSH executor for CUDA check
	executor := sshverify.NewExecutor(
		sshverify.WithExecutorConnectTimeout(10*time.Second),
		sshverify.WithExecutorCommandTimeout(15*time.Second),
	)

	conn, err := executor.Connect(ctx, session.SSHHost, session.SSHPort, session.SSHUser, privateKey)
	if err != nil {
		logger.Debug("CUDA validation: failed to connect for validation",
			slog.String("error", err.Error()))
		return
	}
	defer conn.Close()

	cudaInfo, err := executor.GetCUDAVersion(ctx, conn)
	if err != nil {
		logger.Warn("CUDA validation: failed to get CUDA version",
			slog.String("error", err.Error()),
			slog.String("session_id", session.ID))
		return
	}

	logger.Info("CUDA validation: version detected",
		slog.String("cuda_version", cudaInfo.CUDAVersion),
		slog.String("driver_version", cudaInfo.DriverVersion),
		slog.String("session_id", session.ID),
		slog.String("provider", session.Provider))

	// TODO: Compare with expected CUDA version from offer/template if available
	// For now, we just log the detected version for observability
}

// validateDiskSpaceAsync checks available disk space after SSH verification.
// Logs warnings if disk is low. Informational only - does not fail the session.
func (s *Service) validateDiskSpaceAsync(session *models.Session, privateKey string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	executor := sshverify.NewExecutor(
		sshverify.WithExecutorConnectTimeout(10*time.Second),
		sshverify.WithExecutorCommandTimeout(15*time.Second),
	)

	conn, err := executor.Connect(ctx, session.SSHHost, session.SSHPort, session.SSHUser, privateKey)
	if err != nil {
		logger.Debug("disk check: failed to connect",
			slog.String("error", err.Error()))
		return
	}
	defer conn.Close()

	diskStatus, err := executor.GetDiskStatus(ctx, conn)
	if err != nil {
		logger.Warn("disk check: failed to get disk status",
			slog.String("error", err.Error()),
			slog.String("session_id", session.ID))
		return
	}

	availGB := diskStatus.AvailableGB()
	logger.Info("disk check: space available",
		slog.Float64("available_gb", availGB),
		slog.Bool("is_low", diskStatus.IsLow()),
		slog.String("session_id", session.ID),
		slog.String("provider", session.Provider),
		slog.String("detail", diskStatus.String()))

	metrics.RecordDiskAvailable(session.Provider, availGB)

	if diskStatus.IsLow() {
		logger.Warn("disk check: LOW DISK SPACE",
			slog.Float64("available_gb", availGB),
			slog.String("session_id", session.ID),
			slog.String("provider", session.Provider),
			slog.String("detail", diskStatus.String()))
	}

	// Also check for OOM events while we're connected
	oomStatus, err := executor.CheckOOM(ctx, conn)
	if err != nil {
		logger.Debug("OOM check: failed",
			slog.String("error", err.Error()))
		return
	}

	if oomStatus.OOMDetected {
		logger.Warn("OOM check: OOM events detected on instance",
			slog.String("session_id", session.ID),
			slog.String("provider", session.Provider),
			slog.String("detail", oomStatus.String()))
	} else {
		logger.Debug("OOM check: no OOM events",
			slog.String("session_id", session.ID))
	}
}

// classifyInstanceStopReason provides a more descriptive failure reason based on
// the instance status and error message from the provider.
func classifyInstanceStopReason(status, errorMsg string) string {
	base := fmt.Sprintf("instance stopped unexpectedly: %s", status)

	if errorMsg != "" {
		base += fmt.Sprintf(" (%s)", errorMsg)
	}

	// Add likely cause hints based on known patterns
	lower := strings.ToLower(status + " " + errorMsg)
	switch {
	case strings.Contains(lower, "loading"):
		base += " — likely cause: image pull failed, disk full, or driver incompatibility"
	case strings.Contains(lower, "error"):
		base += " — likely cause: runtime crash, OOM, or configuration error"
	case strings.Contains(lower, "exited"):
		base += " — likely cause: entrypoint failed or OOM kill"
	}

	return base
}

// failSession marks a session as failed
func (s *Service) failSession(ctx context.Context, session *models.Session, reason string) {
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

// generateSSHKeyPair generates an RSA SSH key pair
func (s *Service) generateSSHKeyPair() (privateKeyPEM, publicKeyOpenSSH string, err error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, s.sshKeyBits)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate RSA key: %w", err)
	}

	// Encode private key to PEM
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}
	privateKeyPEM = string(pem.EncodeToMemory(privateKeyBlock))

	// Generate public key in OpenSSH format
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to create public key: %w", err)
	}
	publicKeyOpenSSH = string(ssh.MarshalAuthorizedKey(publicKey))

	return privateKeyPEM, publicKeyOpenSSH, nil
}

// GetDeploymentID returns the deployment identifier
func (s *Service) GetDeploymentID() string {
	return s.deploymentID
}

// WaitForVerificationComplete waits for all pending verification goroutines to complete.
// This is primarily for testing to ensure no goroutine leaks.
// Returns true if all verifications completed within the timeout, false otherwise.
func (s *Service) WaitForVerificationComplete(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		s.verifyWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// buildWorkloadConfig builds a provider WorkloadConfig from session request
func (s *Service) buildWorkloadConfig(req models.CreateSessionRequest) *provider.WorkloadConfig {
	config := &provider.WorkloadConfig{
		ModelID:      req.ModelID,
		Quantization: req.Quantization,
	}

	// Determine workload type from session workload type
	switch req.WorkloadType {
	case models.WorkloadLLMVLLM:
		config.Type = provider.WorkloadTypeVLLM
	case models.WorkloadLLMTGI:
		config.Type = provider.WorkloadTypeTGI
	default:
		config.Type = provider.WorkloadTypeCustom
	}

	return config
}

// buildInstanceTags creates provider instance tags for session tracking and orphan detection
func (s *Service) buildInstanceTags(sessionID, consumerID string, expiresAt time.Time) models.InstanceTags {
	return models.InstanceTags{
		ShopperSessionID:    sessionID,
		ShopperDeploymentID: s.deploymentID,
		ShopperExpiresAt:    expiresAt,
		ShopperConsumerID:   consumerID,
	}
}

// waitForAPIVerifyAsync waits for API endpoint verification in the background
func (s *Service) waitForAPIVerifyAsync(ctx context.Context, sessionID string, prov provider.Provider) {
	logger := s.logger.With(slog.String("session_id", sessionID))
	logger.Info("waiting for API verification")

	start := time.Now()

	// Poll for API info and verify connectivity
	ticker := time.NewTicker(s.apiCheckInterval)
	defer ticker.Stop()

	timeout := time.NewTimer(s.apiVerifyTimeout)
	defer timeout.Stop()

	for {
		select {
		case <-timeout.C:
			// API verification timeout - destroy instance and fail session
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
			// Bug #94 fix: Record session destroyed when API verification times out
			metrics.RecordSessionDestroyed(session.Provider, "api_verify_timeout")
			return

		case <-ticker.C:
			// Get current session state
			session, err := s.store.Get(ctx, sessionID)
			if err != nil {
				logger.Error("failed to get session", slog.String("error", err.Error()))
				continue
			}

			// Check if session is still in a valid state
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

				// Try a health check
				err := s.httpVerifier.CheckHealth(ctx, apiURL)
				if err == nil {
					// API verified successfully
					duration := time.Since(start)
					logger.Info("API verification successful",
						slog.Duration("duration", duration))

					oldStatus := session.Status
					session.Status = models.StatusRunning
					session.APIEndpoint = fmt.Sprintf("http://%s:%d", session.SSHHost, session.APIPort)
					if err := s.store.Update(ctx, session); err != nil {
						logger.Error("failed to update session to running", slog.String("error", err.Error()))
					}

					// Bug #46 fix: Update metrics gauge on state transition
					metrics.UpdateSessionStatus(session.Provider, string(oldStatus), string(models.StatusRunning))
					metrics.RecordAPIVerifyDuration(session.Provider, duration)
					// Bug #57 fix: Record provisioning duration when session becomes running
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

// classifySSHError categorizes SSH connection errors for logging
// Returns: error_type (connection_refused, timeout, auth_failed, etc.)
func classifySSHError(err error) string {
	if err == nil {
		return "none"
	}

	errStr := err.Error()

	// Connection refused - instance not accepting connections yet
	if strings.Contains(errStr, "connection refused") {
		return "connection_refused"
	}

	// Connection timeout - network issues or firewall
	if strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "connection timed out") ||
		strings.Contains(errStr, "deadline exceeded") {
		return "timeout"
	}

	// No route to host - network unreachable
	if strings.Contains(errStr, "no route to host") ||
		strings.Contains(errStr, "network is unreachable") {
		return "network_unreachable"
	}

	// DNS resolution failure
	if strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "lookup") {
		return "dns_failed"
	}

	// SSH handshake failures (auth)
	if strings.Contains(errStr, "SSH handshake failed") ||
		strings.Contains(errStr, "unable to authenticate") ||
		strings.Contains(errStr, "permission denied") {
		return "auth_failed"
	}

	// Private key issues
	if strings.Contains(errStr, "failed to parse private key") {
		return "key_parse_failed"
	}

	// Session/command failures
	if strings.Contains(errStr, "failed to create session") ||
		strings.Contains(errStr, "verify command failed") {
		return "command_failed"
	}

	// EOF typically means the connection was closed
	if strings.Contains(errStr, "EOF") {
		return "connection_closed"
	}

	return "unknown"
}
