package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/api"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/config"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/logging"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider/tensordock"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider/vastai"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/cost"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/inventory"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/lifecycle"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/provisioner"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/storage"
)

func main() {
	// Load configuration
	cfg, err := config.LoadFromEnv()
	if err != nil {
		slog.Error("failed to load config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Initialize logging
	logger := logging.Setup(logging.Config{
		Level:  cfg.Logging.Level,
		Format: cfg.Logging.Format,
	})

	logger.Info("starting GPU Shopper server",
		slog.String("version", "0.1.0"),
		slog.Int("port", cfg.Server.Port))

	// Initialize database
	db, err := storage.New(cfg.Database.Path)
	if err != nil {
		logger.Error("failed to initialize database", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		logger.Error("failed to run migrations", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Initialize stores
	sessionStore := storage.NewSessionStore(db)
	costStore := storage.NewCostStore(db)

	// Initialize providers
	var providers []provider.Provider

	if cfg.Providers.VastAI.APIKey != "" {
		vastaiClient := vastai.NewClient(cfg.Providers.VastAI.APIKey)
		providers = append(providers, vastaiClient)
		logger.Info("initialized Vast.ai provider")
	}

	if cfg.Providers.TensorDock.AuthID != "" && cfg.Providers.TensorDock.APIToken != "" {
		tensordockClient := tensordock.NewClient(
			cfg.Providers.TensorDock.AuthID,
			cfg.Providers.TensorDock.APIToken,
			tensordock.WithDefaultImage(cfg.Providers.TensorDock.DefaultImage),
		)
		providers = append(providers, tensordockClient)
		logger.Info("initialized TensorDock provider",
			slog.String("default_image", cfg.Providers.TensorDock.DefaultImage))
	}

	if len(providers) == 0 {
		logger.Warn("no providers configured, running in demo mode")
	}

	if cfg.Server.PublicURL != "" {
		logger.Info("public URL configured", slog.String("url", cfg.Server.PublicURL))
	}

	// Initialize services
	invService := inventory.New(providers, inventory.WithLogger(logger))

	registry := provisioner.NewSimpleProviderRegistry(providers)
	provService := provisioner.New(sessionStore, registry,
		provisioner.WithLogger(logger),
		provisioner.WithSSHVerifyTimeout(cfg.SSH.VerifyTimeout),
		provisioner.WithSSHCheckInterval(cfg.SSH.CheckInterval))

	lifecycleManager := lifecycle.New(sessionStore, provService,
		lifecycle.WithLogger(logger),
		lifecycle.WithCheckInterval(cfg.Lifecycle.CheckInterval),
		lifecycle.WithHardMaxHours(cfg.Lifecycle.HardMaxHours),
		lifecycle.WithOrphanGracePeriod(cfg.Lifecycle.OrphanGracePeriod))

	costTracker := cost.New(costStore, sessionStore, nil,
		cost.WithLogger(logger))

	// Create reconciler with auto-destroy orphans enabled
	reconciler := lifecycle.NewReconciler(sessionStore, registry,
		lifecycle.WithReconcileLogger(logger),
		lifecycle.WithReconcileInterval(cfg.Lifecycle.ReconciliationInterval),
		lifecycle.WithAutoDestroyOrphans(true))

	// Create startup/shutdown manager
	startupManager := lifecycle.NewStartupShutdownManager(
		sessionStore,
		reconciler,
		registry,
		lifecycle.WithStartupLogger(logger),
		lifecycle.WithStartupSweepTimeout(cfg.Lifecycle.StartupSweepTimeout),
		lifecycle.WithShutdownTimeout(cfg.Lifecycle.ShutdownTimeout))

	// Initialize API server (not ready yet)
	server := api.New(invService, provService, lifecycleManager, costTracker,
		api.WithLogger(logger),
		api.WithPort(cfg.Server.Port))

	// Run startup sweep before accepting traffic (if enabled)
	if cfg.Lifecycle.StartupSweepEnabled {
		logger.Info("running startup sweep to clean up orphaned instances")
		if err := startupManager.RunStartupSweep(ctx); err != nil {
			logger.Error("startup sweep failed", slog.String("error", err.Error()))
			// Continue startup even if sweep fails - we don't want to prevent
			// the server from starting due to sweep issues
		}
	} else {
		logger.Info("startup sweep disabled, skipping")
	}

	// Mark server as ready
	server.SetReady(true)

	// Start background services
	if err := lifecycleManager.Start(ctx); err != nil {
		logger.Error("failed to start lifecycle manager", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if err := costTracker.Start(ctx); err != nil {
		logger.Error("failed to start cost tracker", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Start reconciler for ongoing checks
	if err := reconciler.Start(ctx); err != nil {
		logger.Error("failed to start reconciler", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Handle shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		logger.Info("shutting down...")

		// Mark server as not ready to stop accepting new requests
		server.SetReady(false)

		// Run graceful shutdown to destroy active sessions BEFORE stopping server
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Lifecycle.ShutdownTimeout+10*time.Second)
		defer cancel()

		if err := startupManager.GracefulShutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown error", slog.String("error", err.Error()))
		}

		// Stop background services
		reconciler.Stop()
		lifecycleManager.Stop()
		costTracker.Stop()

		// Shutdown HTTP server
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown error", slog.String("error", err.Error()))
		}
	}()

	// Start server
	if err := server.Start(); err != nil {
		logger.Error("server error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
