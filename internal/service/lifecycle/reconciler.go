package lifecycle

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/logging"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

const (
	// DefaultReconcileInterval is how often to run reconciliation
	DefaultReconcileInterval = 5 * time.Minute
)

// ProviderRegistry provides access to provider clients
type ProviderRegistry interface {
	List() []string
	Get(name string) (provider.Provider, error)
}

// ReconcileStore defines the interface for session persistence needed by reconciler
type ReconcileStore interface {
	GetActiveSessionsByProvider(ctx context.Context, provider string) ([]*models.Session, error)
	GetSessionsByStatus(ctx context.Context, statuses ...models.SessionStatus) ([]*models.Session, error)
	Get(ctx context.Context, id string) (*models.Session, error)
	Update(ctx context.Context, session *models.Session) error
}

// ReconcileEventHandler receives reconciliation events
type ReconcileEventHandler interface {
	OnOrphanFound(providerName, instanceID, sessionID string)
	OnGhostFound(session *models.Session)
	OnReconcileError(providerName string, err error)
}

// noopReconcileHandler is a default handler that does nothing
type noopReconcileHandler struct{}

func (n *noopReconcileHandler) OnOrphanFound(providerName, instanceID, sessionID string) {}
func (n *noopReconcileHandler) OnGhostFound(session *models.Session)                     {}
func (n *noopReconcileHandler) OnReconcileError(providerName string, err error)          {}

// Reconciler compares provider state with database state
type Reconciler struct {
	store        ReconcileStore
	providers    ProviderRegistry
	handler      ReconcileEventHandler
	logger       *slog.Logger
	deploymentID string

	// Configuration
	reconcileInterval  time.Duration
	autoDestroyOrphans bool

	// For time mocking in tests
	now func() time.Time

	// Shutdown coordination
	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}

	// Metrics
	metrics *ReconcileMetrics
}

// ReconcileMetrics tracks reconciliation statistics
type ReconcileMetrics struct {
	mu                 sync.RWMutex
	ReconciliationsRun int64
	OrphansFound       int64
	OrphansDestroyed   int64
	GhostsFound        int64
	GhostsFixed        int64
	Errors             int64
}

// ReconcilerOption configures the reconciler
type ReconcilerOption func(*Reconciler)

// WithReconcileLogger sets a custom logger
func WithReconcileLogger(logger *slog.Logger) ReconcilerOption {
	return func(r *Reconciler) {
		r.logger = logger
	}
}

// WithReconcileInterval sets how often to run reconciliation
func WithReconcileInterval(d time.Duration) ReconcilerOption {
	return func(r *Reconciler) {
		r.reconcileInterval = d
	}
}

// WithDeploymentID sets the deployment ID for filtering instances
func WithDeploymentID(id string) ReconcilerOption {
	return func(r *Reconciler) {
		r.deploymentID = id
	}
}

// WithAutoDestroyOrphans enables automatic destruction of orphan instances
func WithAutoDestroyOrphans(enabled bool) ReconcilerOption {
	return func(r *Reconciler) {
		r.autoDestroyOrphans = enabled
	}
}

// WithReconcileEventHandler sets a custom event handler
func WithReconcileEventHandler(handler ReconcileEventHandler) ReconcilerOption {
	return func(r *Reconciler) {
		r.handler = handler
	}
}

// WithReconcileTimeFunc sets a custom time function (for testing)
func WithReconcileTimeFunc(fn func() time.Time) ReconcilerOption {
	return func(r *Reconciler) {
		r.now = fn
	}
}

// NewReconciler creates a new reconciler
func NewReconciler(store ReconcileStore, providers ProviderRegistry, opts ...ReconcilerOption) *Reconciler {
	r := &Reconciler{
		store:              store,
		providers:          providers,
		handler:            &noopReconcileHandler{},
		logger:             slog.Default(),
		reconcileInterval:  DefaultReconcileInterval,
		autoDestroyOrphans: true,
		now:                time.Now,
		stopCh:             make(chan struct{}),
		doneCh:             make(chan struct{}),
		metrics:            &ReconcileMetrics{},
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Start begins the reconciliation loop
func (r *Reconciler) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil
	}
	r.running = true
	r.stopCh = make(chan struct{})
	r.doneCh = make(chan struct{})
	r.mu.Unlock()

	r.logger.Info("reconciler starting",
		slog.Duration("interval", r.reconcileInterval))

	go r.run(ctx)
	return nil
}

// Stop gracefully stops the reconciler
func (r *Reconciler) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	// Bug #3 fix: Capture channel references while holding lock to prevent race with Start()
	stopCh := r.stopCh
	doneCh := r.doneCh
	r.mu.Unlock()

	r.logger.Info("reconciler stopping")
	close(stopCh)
	<-doneCh
	// Bug #14/16 fix: running flag is now set to false by the run() goroutine
	// before closing doneCh, similar to Manager.Stop()

	r.logger.Info("reconciler stopped")
}

// run is the main reconciliation loop
func (r *Reconciler) run(ctx context.Context) {
	// Bug #14/16 fix: Use defer pattern similar to Manager.run() to ensure
	// running flag is set to false when exiting due to context cancellation
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
		close(r.doneCh)
	}()

	ticker := time.NewTicker(r.reconcileInterval)
	defer ticker.Stop()

	// Run initial reconciliation
	r.RunReconciliation(ctx)

	for {
		select {
		case <-ticker.C:
			r.RunReconciliation(ctx)
		case <-r.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// RunReconciliation executes a single reconciliation pass
func (r *Reconciler) RunReconciliation(ctx context.Context) {
	r.logger.Debug("running reconciliation")

	r.metrics.mu.Lock()
	r.metrics.ReconciliationsRun++
	r.metrics.mu.Unlock()

	providerNames := r.providers.List()
	for _, providerName := range providerNames {
		if err := r.reconcileProvider(ctx, providerName); err != nil {
			r.logger.Error("reconciliation failed for provider",
				slog.String("provider", providerName),
				slog.String("error", err.Error()))

			r.metrics.mu.Lock()
			r.metrics.Errors++
			r.metrics.mu.Unlock()

			r.handler.OnReconcileError(providerName, err)
		}
	}
}

// reconcileProvider reconciles state for a single provider
func (r *Reconciler) reconcileProvider(ctx context.Context, providerName string) error {
	prov, err := r.providers.Get(providerName)
	if err != nil {
		return err
	}

	// Get all instances from provider with our tags
	providerInstances, err := prov.ListAllInstances(ctx)
	if err != nil {
		return err
	}

	// Get all active sessions from DB for this provider
	localSessions, err := r.store.GetActiveSessionsByProvider(ctx, providerName)
	if err != nil {
		return err
	}

	// Build maps for comparison
	localMap := make(map[string]*models.Session)
	for _, s := range localSessions {
		if s.ProviderID != "" {
			localMap[s.ProviderID] = s
		}
	}

	providerMap := make(map[string]provider.ProviderInstance)
	for _, p := range providerInstances {
		// Only include instances from our deployment.
		// If deploymentID is empty, we include ALL instances which may lead to
		// false positive orphan detection for instances from other deployments.
		// This is intentional for single-deployment environments but operators
		// should configure a deploymentID in multi-deployment scenarios.
		if r.deploymentID == "" {
			r.logger.Warn("deploymentID is empty; all provider instances will be considered ours",
				slog.String("instance_id", p.ID))
		} else if !p.IsOurs(r.deploymentID) {
			continue
		}
		providerMap[p.ID] = p
	}

	// Find orphans: exist on provider but not in DB
	for providerID, instance := range providerMap {
		if _, exists := localMap[providerID]; !exists {
			r.handleOrphan(ctx, prov, providerID, instance)
		}
	}

	// Find ghosts: exist in DB but not on provider
	for providerID, session := range localMap {
		if _, exists := providerMap[providerID]; !exists {
			r.handleGhost(ctx, session)
		}
	}

	return nil
}

// handleOrphan handles an orphan instance (exists on provider but not in DB)
func (r *Reconciler) handleOrphan(ctx context.Context, prov provider.Provider, providerID string, instance provider.ProviderInstance) {
	r.logger.Warn("ORPHAN DETECTED: Instance exists on provider but not in local DB",
		slog.String("provider", prov.Name()),
		slog.String("provider_id", providerID),
		slog.String("session_id", instance.Tags.ShopperSessionID),
		slog.Time("started_at", instance.StartedAt))

	r.metrics.mu.Lock()
	r.metrics.OrphansFound++
	r.metrics.mu.Unlock()

	// Record audit log and metrics
	logging.Audit(ctx, "orphan_detected",
		"provider", prov.Name(),
		"provider_id", providerID,
		"session_id", instance.Tags.ShopperSessionID,
		"started_at", instance.StartedAt)
	metrics.RecordOrphanDetected()

	r.handler.OnOrphanFound(prov.Name(), providerID, instance.Tags.ShopperSessionID)

	if r.autoDestroyOrphans {
		r.logger.Info("auto-destroying orphan instance",
			slog.String("provider_id", providerID))

		if err := prov.DestroyInstance(ctx, providerID); err != nil {
			r.logger.Error("failed to destroy orphan",
				slog.String("provider_id", providerID),
				slog.String("error", err.Error()))
			metrics.RecordDestroyFailure()
		} else {
			r.logger.Info("orphan destroyed",
				slog.String("provider_id", providerID))

			// Record audit log for orphan destruction
			logging.Audit(ctx, "orphan_destroyed",
				"provider", prov.Name(),
				"provider_id", providerID,
				"session_id", instance.Tags.ShopperSessionID)

			r.metrics.mu.Lock()
			r.metrics.OrphansDestroyed++
			r.metrics.mu.Unlock()
		}
	}
}

// handleGhost handles a ghost session (exists in DB but not on provider)
func (r *Reconciler) handleGhost(ctx context.Context, session *models.Session) {
	// Only handle running/provisioning sessions as ghosts
	if session.Status != models.StatusRunning && session.Status != models.StatusProvisioning {
		return
	}

	r.logger.Warn("GHOST DETECTED: Session in DB but instance not on provider",
		slog.String("session_id", session.ID),
		slog.String("provider_id", session.ProviderID),
		slog.String("status", string(session.Status)))

	r.metrics.mu.Lock()
	r.metrics.GhostsFound++
	r.metrics.mu.Unlock()

	// Record audit log and metrics
	logging.Audit(ctx, "ghost_detected",
		"session_id", session.ID,
		"consumer_id", session.ConsumerID,
		"provider", session.Provider,
		"provider_id", session.ProviderID,
		"status", string(session.Status))
	metrics.RecordGhostDetected()

	r.handler.OnGhostFound(session)

	// Update session to stopped
	oldStatus := session.Status
	session.Status = models.StatusStopped
	session.Error = "Instance not found on provider during reconciliation"
	session.StoppedAt = r.now()

	if err := r.store.Update(ctx, session); err != nil {
		r.logger.Error("failed to update ghost session",
			slog.String("session_id", session.ID),
			slog.String("error", err.Error()))
	} else {
		// Record audit log for ghost fix
		logging.Audit(ctx, "ghost_fixed",
			"session_id", session.ID,
			"consumer_id", session.ConsumerID,
			"provider", session.Provider)

		metrics.UpdateSessionStatus(session.Provider, string(oldStatus), string(models.StatusStopped))

		r.metrics.mu.Lock()
		r.metrics.GhostsFixed++
		r.metrics.mu.Unlock()
	}
}

// RecoverStuckSessions recovers sessions stuck in transitional states
func (r *Reconciler) RecoverStuckSessions(ctx context.Context) error {
	r.logger.Info("recovering stuck sessions")

	// Get sessions stuck in transitional states
	stuckSessions, err := r.store.GetSessionsByStatus(ctx,
		models.StatusProvisioning,
		models.StatusStopping)
	if err != nil {
		return err
	}

	for _, session := range stuckSessions {
		r.logger.Warn("found stuck session",
			slog.String("session_id", session.ID),
			slog.String("status", string(session.Status)))

		prov, err := r.providers.Get(session.Provider)
		if err != nil {
			r.logger.Error("provider not found for stuck session",
				slog.String("session_id", session.ID),
				slog.String("provider", session.Provider))
			continue
		}

		// Check actual provider status
		if session.ProviderID == "" {
			// Never got a provider ID - mark as failed
			session.Status = models.StatusFailed
			session.Error = "Provisioning failed - no provider instance ID"
			session.StoppedAt = r.now()
			r.store.Update(ctx, session)
			continue
		}

		status, err := prov.GetInstanceStatus(ctx, session.ProviderID)
		if err != nil {
			// Instance not found - mark as stopped/failed
			if session.Status == models.StatusProvisioning {
				session.Status = models.StatusFailed
				session.Error = "Instance not found after restart"
			} else {
				session.Status = models.StatusStopped
			}
			session.StoppedAt = r.now()
			r.store.Update(ctx, session)
			continue
		}

		if status.Running {
			if session.Status == models.StatusProvisioning {
				// Instance is running - update to running with SSH info
				session.Status = models.StatusRunning
				if status.SSHHost != "" {
					session.SSHHost = status.SSHHost
				}
				if status.SSHPort != 0 {
					session.SSHPort = status.SSHPort
				}
				if status.SSHUser != "" {
					session.SSHUser = status.SSHUser
				}
				r.store.Update(ctx, session)
			} else {
				// Was stopping but still running - retry destroy
				r.logger.Info("retrying destroy for stuck stopping session",
					slog.String("session_id", session.ID))
				prov.DestroyInstance(ctx, session.ProviderID)
			}
		} else {
			// Instance not running
			session.Status = models.StatusStopped
			session.StoppedAt = r.now()
			r.store.Update(ctx, session)
		}
	}

	return nil
}

// GetMetrics returns current reconciliation metrics
func (r *Reconciler) GetMetrics() ReconcileMetrics {
	r.metrics.mu.RLock()
	defer r.metrics.mu.RUnlock()

	return ReconcileMetrics{
		ReconciliationsRun: r.metrics.ReconciliationsRun,
		OrphansFound:       r.metrics.OrphansFound,
		OrphansDestroyed:   r.metrics.OrphansDestroyed,
		GhostsFound:        r.metrics.GhostsFound,
		GhostsFixed:        r.metrics.GhostsFixed,
		Errors:             r.metrics.Errors,
	}
}

// IsRunning returns whether the reconciler is currently running
func (r *Reconciler) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}
