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
)

// ProgressiveBackoff implements an exponential backoff strategy with a maximum cap.
// This reduces load on providers when instances take a long time to become ready.
type ProgressiveBackoff struct {
	Initial    time.Duration // Initial interval
	Max        time.Duration // Maximum interval cap
	Multiplier float64       // Multiplier for each step
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
	pb.current = pb.Initial
}

// Current returns the current interval without advancing
func (pb *ProgressiveBackoff) Current() time.Duration {
	return pb.current
}

// SessionStore defines the interface for session persistence
type SessionStore interface {
	Create(ctx context.Context, session *models.Session) error
	Get(ctx context.Context, id string) (*models.Session, error)
	Update(ctx context.Context, session *models.Session) error
	GetActiveSessionByConsumerAndOffer(ctx context.Context, consumerID, offerID string) (*models.Session, error)
}

// ProviderRegistry provides access to provider clients
type ProviderRegistry interface {
	Get(name string) (provider.Provider, error)
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

// CreateSession provisions a new GPU session using two-phase provisioning
func (s *Service) CreateSession(ctx context.Context, req models.CreateSessionRequest, offer *models.GPUOffer) (*models.Session, error) {
	s.logger.Info("creating session",
		slog.String("consumer_id", req.ConsumerID),
		slog.String("offer_id", req.OfferID),
		slog.String("provider", offer.Provider))

	// Check for existing active session for this consumer and offer
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

	// Generate SSH key pair
	privateKey, publicKey, err := s.generateSSHKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate SSH key: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(time.Duration(req.ReservationHrs) * time.Hour)

	// Set defaults
	storagePolicy := req.StoragePolicy
	if storagePolicy == "" {
		storagePolicy = models.StorageDestroy
	}

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
	}

	if err := s.store.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to create session record: %w", err)
	}

	s.logger.Info("session record created",
		slog.String("session_id", session.ID),
		slog.String("status", string(session.Status)))

	// PHASE 2: Call provider to create instance
	prov, err := s.providers.Get(offer.Provider)
	if err != nil {
		s.failSession(ctx, session, fmt.Sprintf("provider not found: %s", offer.Provider))
		return nil, err
	}

	tags := models.InstanceTags{
		ShopperSessionID:    session.ID,
		ShopperDeploymentID: s.deploymentID,
		ShopperExpiresAt:    expiresAt,
		ShopperConsumerID:   req.ConsumerID,
	}

	// Build the provider request based on launch mode
	instanceReq := provider.CreateInstanceRequest{
		OfferID:      offer.ID, // Full offer ID (e.g., tensordock-{uuid}-{gpu} for TensorDock)
		SessionID:    session.ID,
		SSHPublicKey: publicKey,
		Tags:         tags,
	}

	// Configure for entrypoint mode if specified
	if req.LaunchMode == models.LaunchModeEntrypoint {
		instanceReq.LaunchMode = provider.LaunchModeEntrypoint
		instanceReq.DockerImage = req.DockerImage
		instanceReq.ExposedPorts = req.ExposedPorts

		// Build workload config from request
		instanceReq.WorkloadConfig = s.buildWorkloadConfig(req)
	}

	session.Status = models.StatusProvisioning
	if err := s.store.Update(ctx, session); err != nil {
		s.logger.Error("failed to update session to provisioning",
			slog.String("session_id", session.ID),
			slog.String("error", err.Error()))
	}

	instance, err := prov.CreateInstance(ctx, instanceReq)
	if err != nil {
		s.failSession(ctx, session, fmt.Sprintf("provider create failed: %s", err.Error()))

		// Check if this is a stale inventory error - wrap it so callers can
		// identify it and potentially retry with a different offer
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
		// Log error but return success - reconciliation will catch orphans
		s.logger.Error("CRITICAL: failed to update session after provision",
			slog.String("session_id", session.ID),
			slog.String("provider_id", instance.ProviderInstanceID),
			slog.String("error", err.Error()))
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
	metrics.UpdateSessionStatus(session.Provider, "", string(models.StatusProvisioning))

	// PHASE 4: Wait for verification (async - don't block API)
	// The caller can poll session status to check when it's running
	if req.LaunchMode == models.LaunchModeEntrypoint {
		// Entrypoint mode: wait for API endpoint to be ready
		go s.waitForAPIVerifyAsync(context.Background(), session.ID, prov)
	} else {
		// SSH mode: wait for SSH connectivity
		// Note: We pass the private key directly because it's not stored in the database
		go s.waitForSSHVerifyAsync(context.Background(), session.ID, privateKey, prov)
	}

	return session, nil
}

// waitForSSHVerifyAsync waits for SSH verification in the background
// privateKey is passed directly because it's not stored in the database for security
func (s *Service) waitForSSHVerifyAsync(ctx context.Context, sessionID string, privateKey string, prov provider.Provider) {
	logger := s.logger.With(slog.String("session_id", sessionID))
	logger.Info("waiting for SSH verification")

	start := time.Now()

	// TensorDock-specific: wait for cloud-init to complete before polling
	// TensorDock VMs need extra time for cloud-init runcmd to execute
	session, err := s.store.Get(ctx, sessionID)
	if err == nil && session.Provider == "tensordock" {
		logger.Info("TensorDock: waiting 45s for cloud-init before SSH polling")
		select {
		case <-time.After(45 * time.Second):
		case <-ctx.Done():
			return
		}
	}

	// Create progressive backoff for SSH polling
	// This reduces provider API load when instances take a long time to be ready
	backoff := NewProgressiveBackoff(s.sshCheckInterval, s.sshMaxInterval, s.sshBackoffMultiplier)

	// Use a timer instead of ticker for progressive backoff
	nextInterval := backoff.Next()
	pollTimer := time.NewTimer(nextInterval)
	defer pollTimer.Stop()

	timeout := time.NewTimer(s.sshVerifyTimeout)
	defer timeout.Stop()

	attemptCount := 0
	for {
		select {
		case <-timeout.C:
			// SSH verification timeout - destroy instance and fail session
			logger.Error("SSH verification timeout, destroying instance",
				slog.Int("attempts", attemptCount))
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

					session.Status = models.StatusRunning
					if err := s.store.Update(ctx, session); err != nil {
						logger.Error("failed to update session to running", slog.String("error", err.Error()))
					}

					metrics.RecordSSHVerifyDuration(session.Provider, duration)
					return
				}

				logger.Debug("SSH verification attempt failed", slog.String("error", err.Error()))
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

// DestroySession destroys a session with verification
func (s *Service) DestroySession(ctx context.Context, sessionID string) error {
	session, err := s.store.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	if session.IsTerminal() {
		return nil // Already terminated
	}

	s.logger.Info("destroying session",
		slog.String("session_id", sessionID),
		slog.String("provider_id", session.ProviderID))

	session.Status = models.StatusStopping
	if err := s.store.Update(ctx, session); err != nil {
		s.logger.Error("failed to update session to stopping",
			slog.String("session_id", sessionID),
			slog.String("error", err.Error()))
	}

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
	session.StoppedAt = time.Now()
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

		// Wait before checking status
		time.Sleep(time.Duration(attempt+1) * 5 * time.Second)

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

// failSession marks a session as failed
func (s *Service) failSession(ctx context.Context, session *models.Session, reason string) {
	session.Status = models.StatusFailed
	session.Error = reason
	session.StoppedAt = time.Now()
	if err := s.store.Update(ctx, session); err != nil {
		s.logger.Error("failed to update session to failed",
			slog.String("session_id", session.ID),
			slog.String("error", err.Error()))
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

					session.Status = models.StatusRunning
					session.APIEndpoint = fmt.Sprintf("http://%s:%d", session.SSHHost, session.APIPort)
					if err := s.store.Update(ctx, session); err != nil {
						logger.Error("failed to update session to running", slog.String("error", err.Error()))
					}

					metrics.RecordAPIVerifyDuration(session.Provider, duration)
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
