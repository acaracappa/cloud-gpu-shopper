package provisioner

import (
	"context"
	"log/slog"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	sshverify "github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/ssh"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

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
