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
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/logging"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/storage"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

const (
	// DefaultHeartbeatTimeout is how long to wait for agent heartbeat
	DefaultHeartbeatTimeout = 5 * time.Minute

	// DefaultHeartbeatCheckInterval is how often to check for heartbeat
	DefaultHeartbeatCheckInterval = 10 * time.Second

	// DefaultDestroyTimeout is the max time to wait for destroy verification
	DefaultDestroyTimeout = 5 * time.Minute

	// DefaultDestroyRetries is the max number of destroy attempts
	DefaultDestroyRetries = 10

	// DefaultSSHKeyBits is the RSA key size
	DefaultSSHKeyBits = 4096

	// DefaultAgentPort is the default port for the agent endpoint
	DefaultAgentPort = 8081

	// DefaultAgentBinaryURL is the URL to download the agent binary from GitHub releases
	DefaultAgentBinaryURL = "https://github.com/acaracappa/cloud-gpu-shopper/releases/latest/download/gpu-shopper-agent-linux-amd64"
)

// SessionStore defines the interface for session persistence
type SessionStore interface {
	Create(ctx context.Context, session *models.Session) error
	Get(ctx context.Context, id string) (*models.Session, error)
	Update(ctx context.Context, session *models.Session) error
	UpdateHeartbeat(ctx context.Context, id string, t time.Time) error
	UpdateHeartbeatWithIdle(ctx context.Context, id string, t time.Time, idleSeconds int) error
	GetActiveSessionByConsumerAndOffer(ctx context.Context, consumerID, offerID string) (*models.Session, error)
}

// ProviderRegistry provides access to provider clients
type ProviderRegistry interface {
	Get(name string) (provider.Provider, error)
}

// Service handles GPU session provisioning and destruction
type Service struct {
	store        SessionStore
	providers    ProviderRegistry
	logger       *slog.Logger
	deploymentID string
	agentImage   string
	agentPort    int
	shopperURL   string // Public URL for agents to reach this server
	agentBinURL  string // URL to download agent binary (for non-Docker providers)

	// Configuration
	heartbeatTimeout       time.Duration
	heartbeatCheckInterval time.Duration
	destroyTimeout         time.Duration
	destroyRetries         int
	sshKeyBits             int

	// Active sessions waiting for heartbeat
	heartbeatWaiters sync.Map // sessionID -> chan struct{}
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

// WithAgentImage sets the Docker image for the node agent
func WithAgentImage(image string) Option {
	return func(s *Service) {
		s.agentImage = image
	}
}

// WithAgentPort sets the port for the agent endpoint
func WithAgentPort(port int) Option {
	return func(s *Service) {
		s.agentPort = port
	}
}

// WithShopperURL sets the public URL for agents to reach the shopper server
func WithShopperURL(url string) Option {
	return func(s *Service) {
		s.shopperURL = url
	}
}

// WithAgentBinaryURL sets the URL to download agent binary from
func WithAgentBinaryURL(url string) Option {
	return func(s *Service) {
		s.agentBinURL = url
	}
}

// WithHeartbeatTimeout sets how long to wait for agent heartbeat
func WithHeartbeatTimeout(d time.Duration) Option {
	return func(s *Service) {
		s.heartbeatTimeout = d
	}
}

// WithDestroyRetries sets the max number of destroy verification attempts
func WithDestroyRetries(n int) Option {
	return func(s *Service) {
		s.destroyRetries = n
	}
}

// New creates a new provisioner service
func New(store SessionStore, providers ProviderRegistry, opts ...Option) *Service {
	s := &Service{
		store:                  store,
		providers:              providers,
		logger:                 slog.Default(),
		deploymentID:           uuid.New().String(),
		agentImage:             "nvidia/cuda:12.1.0-runtime-ubuntu22.04",
		agentPort:              DefaultAgentPort,
		agentBinURL:            DefaultAgentBinaryURL,
		heartbeatTimeout:       DefaultHeartbeatTimeout,
		heartbeatCheckInterval: DefaultHeartbeatCheckInterval,
		destroyTimeout:         DefaultDestroyTimeout,
		destroyRetries:         DefaultDestroyRetries,
		sshKeyBits:             DefaultSSHKeyBits,
	}

	for _, opt := range opts {
		opt(s)
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

	// Generate agent token
	agentToken := uuid.New().String()

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
		AgentToken:     agentToken,
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

	instanceReq := provider.CreateInstanceRequest{
		OfferID:      offer.ID,
		SessionID:    session.ID,
		SSHPublicKey: publicKey,
		DockerImage:  s.agentImage,
		EnvVars:      s.buildAgentEnv(session, tags),
		Tags:         tags,
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

	// PHASE 4: Wait for agent heartbeat (async - don't block API)
	// The caller can poll session status to check when it's running
	go s.waitForHeartbeatAsync(context.Background(), session.ID, prov)

	return session, nil
}

// waitForHeartbeatAsync waits for agent heartbeat in the background
func (s *Service) waitForHeartbeatAsync(ctx context.Context, sessionID string, prov provider.Provider) {
	logger := s.logger.With(slog.String("session_id", sessionID))
	logger.Info("waiting for agent heartbeat")

	// Create done channel for this session
	done := make(chan struct{})
	s.heartbeatWaiters.Store(sessionID, done)
	defer s.heartbeatWaiters.Delete(sessionID)

	timeout := time.NewTimer(s.heartbeatTimeout)
	defer timeout.Stop()

	ticker := time.NewTicker(s.heartbeatCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			// Heartbeat received
			logger.Info("agent heartbeat received")
			session, err := s.store.Get(ctx, sessionID)
			if err != nil {
				logger.Error("failed to get session", slog.String("error", err.Error()))
				return
			}

			// Fetch SSH info if we don't have it yet
			if session.SSHHost == "" && session.ProviderID != "" {
				status, err := prov.GetInstanceStatus(ctx, session.ProviderID)
				if err != nil {
					logger.Warn("failed to get SSH info on heartbeat", slog.String("error", err.Error()))
				} else if status.SSHHost != "" {
					session.SSHHost = status.SSHHost
					if status.SSHPort != 0 {
						session.SSHPort = status.SSHPort
					}
					if status.SSHUser != "" {
						session.SSHUser = status.SSHUser
					}
					logger.Info("SSH info updated on heartbeat",
						slog.String("ssh_host", session.SSHHost),
						slog.Int("ssh_port", session.SSHPort))
				}
			}

			session.Status = models.StatusRunning
			if err := s.store.Update(ctx, session); err != nil {
				logger.Error("failed to update session to running", slog.String("error", err.Error()))
			}
			return

		case <-timeout.C:
			// Heartbeat timeout - destroy instance and fail session
			logger.Error("agent heartbeat timeout, destroying instance")
			session, err := s.store.Get(ctx, sessionID)
			if err != nil {
				logger.Error("failed to get session", slog.String("error", err.Error()))
				return
			}

			if session.ProviderID != "" {
				if err := prov.DestroyInstance(ctx, session.ProviderID); err != nil {
					logger.Error("failed to destroy instance after heartbeat timeout",
						slog.String("error", err.Error()))
				}
			}

			s.failSession(ctx, session, "agent failed to start: heartbeat timeout")
			return

		case <-ticker.C:
			// Get current session state
			session, err := s.store.Get(ctx, sessionID)
			if err != nil {
				logger.Error("failed to get session", slog.String("error", err.Error()))
				continue
			}

			// Poll provider for SSH info if we don't have it yet
			if session.SSHHost == "" && session.ProviderID != "" {
				status, err := prov.GetInstanceStatus(ctx, session.ProviderID)
				if err != nil {
					logger.Debug("failed to get instance status", slog.String("error", err.Error()))
				} else if status.SSHHost != "" {
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
					}
				}
			}

			// Check if session received heartbeat
			if !session.LastHeartbeat.IsZero() {
				close(done)
			}

		case <-ctx.Done():
			logger.Warn("context cancelled while waiting for heartbeat")
			return
		}
	}
}

// RecordHeartbeat records a heartbeat from an agent with idle tracking
func (s *Service) RecordHeartbeat(ctx context.Context, sessionID string, idleSeconds int) error {
	if err := s.store.UpdateHeartbeatWithIdle(ctx, sessionID, time.Now(), idleSeconds); err != nil {
		return fmt.Errorf("failed to update heartbeat: %w", err)
	}

	// Signal waiter if exists
	if done, ok := s.heartbeatWaiters.Load(sessionID); ok {
		select {
		case <-done.(chan struct{}):
			// Already closed
		default:
			close(done.(chan struct{}))
		}
	}

	return nil
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
	metrics.RemoveHeartbeatAge(session.ID)

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

// buildAgentEnv creates environment variables for the node agent
func (s *Service) buildAgentEnv(session *models.Session, tags models.InstanceTags) map[string]string {
	env := map[string]string{
		"SHOPPER_URL":           s.shopperURL,
		"SHOPPER_SESSION_ID":    session.ID,
		"SHOPPER_DEPLOYMENT_ID": s.deploymentID,
		"SHOPPER_EXPIRES_AT":    tags.ShopperExpiresAt.Format(time.RFC3339),
		"SHOPPER_CONSUMER_ID":   session.ConsumerID,
		"SHOPPER_AGENT_TOKEN":   session.AgentToken,
		"SHOPPER_AGENT_PORT":    fmt.Sprintf("%d", s.agentPort),
	}
	// Include agent binary URL if configured (for non-Docker providers)
	if s.agentBinURL != "" {
		env["SHOPPER_AGENT_URL"] = s.agentBinURL
	}
	return env
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
