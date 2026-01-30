package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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
		tensordockClient := tensordock.NewClient(cfg.Providers.TensorDock.AuthID, cfg.Providers.TensorDock.APIToken)
		providers = append(providers, tensordockClient)
		logger.Info("initialized TensorDock provider")
	}

	if len(providers) == 0 {
		logger.Warn("no providers configured, running in demo mode")
	}

	// Initialize services
	invService := inventory.New(providers, inventory.WithLogger(logger))

	registry := provisioner.NewSimpleProviderRegistry(providers)
	provService := provisioner.New(sessionStore, registry,
		provisioner.WithLogger(logger))

	lifecycleManager := lifecycle.New(sessionStore, provService,
		lifecycle.WithLogger(logger))

	costTracker := cost.New(costStore, sessionStore, nil,
		cost.WithLogger(logger))

	// Start background services
	if err := lifecycleManager.Start(ctx); err != nil {
		logger.Error("failed to start lifecycle manager", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if err := costTracker.Start(ctx); err != nil {
		logger.Error("failed to start cost tracker", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Initialize API server
	server := api.New(invService, provService, lifecycleManager, costTracker,
		api.WithLogger(logger),
		api.WithPort(cfg.Server.Port))

	// Handle shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		logger.Info("shutting down...")

		lifecycleManager.Stop()
		costTracker.Stop()

		shutdownCtx := context.Background()
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
