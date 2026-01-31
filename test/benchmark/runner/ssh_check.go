package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// SSHChecker provides SSH connectivity testing
type SSHChecker struct {
	keyPath string
	timeout time.Duration
}

// NewSSHChecker creates a new SSH checker
func NewSSHChecker(keyPath string) *SSHChecker {
	return &SSHChecker{
		keyPath: keyPath,
		timeout: 30 * time.Second,
	}
}

// CheckConnection verifies SSH connectivity to a host
func (s *SSHChecker) CheckConnection(ctx context.Context, host string, port int, user string) error {
	sshArgs := []string{
		"-i", s.keyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", int(s.timeout.Seconds())),
		"-p", fmt.Sprintf("%d", port),
		fmt.Sprintf("%s@%s", user, host),
		"echo", "SSH_OK",
	}

	execCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "ssh", sshArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("SSH connection failed: %w (output: %s)", err, string(output))
	}

	return nil
}

// RunCommand executes a command via SSH
func (s *SSHChecker) RunCommand(ctx context.Context, host string, port int, user string, command string) (string, error) {
	sshArgs := []string{
		"-i", s.keyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", int(s.timeout.Seconds())),
		"-p", fmt.Sprintf("%d", port),
		fmt.Sprintf("%s@%s", user, host),
		command,
	}

	execCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "ssh", sshArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("command failed: %w", err)
	}

	return string(output), nil
}

// CheckGPU verifies GPU access on the remote host
func (s *SSHChecker) CheckGPU(ctx context.Context, host string, port int, user string) (string, error) {
	output, err := s.RunCommand(ctx, host, port, user, "nvidia-smi --query-gpu=name,memory.total --format=csv,noheader")
	if err != nil {
		return "", fmt.Errorf("GPU check failed: %w", err)
	}
	return output, nil
}

// CheckDocker verifies Docker is available
func (s *SSHChecker) CheckDocker(ctx context.Context, host string, port int, user string) error {
	_, err := s.RunCommand(ctx, host, port, user, "docker --version")
	return err
}

// RunDiagnostics runs a full diagnostic check on a remote host
func (s *SSHChecker) RunDiagnostics(ctx context.Context, host string, port int, user string) (*Diagnostics, error) {
	diag := &Diagnostics{
		Host: host,
		Port: port,
		User: user,
	}

	// Check SSH connectivity
	fmt.Printf("  Testing SSH connectivity to %s@%s:%d...\n", user, host, port)
	if err := s.CheckConnection(ctx, host, port, user); err != nil {
		diag.SSHError = err.Error()
		return diag, err
	}
	diag.SSHConnected = true
	fmt.Printf("  SSH: OK\n")

	// Check GPU
	fmt.Printf("  Checking GPU...\n")
	gpuInfo, err := s.CheckGPU(ctx, host, port, user)
	if err != nil {
		diag.GPUError = err.Error()
		fmt.Printf("  GPU: FAILED - %v\n", err)
	} else {
		diag.GPUAvailable = true
		diag.GPUInfo = gpuInfo
		fmt.Printf("  GPU: %s\n", gpuInfo)
	}

	// Check Docker
	fmt.Printf("  Checking Docker...\n")
	if err := s.CheckDocker(ctx, host, port, user); err != nil {
		diag.DockerError = err.Error()
		fmt.Printf("  Docker: FAILED - %v\n", err)
	} else {
		diag.DockerAvailable = true
		fmt.Printf("  Docker: OK\n")
	}

	// Check disk space
	fmt.Printf("  Checking disk space...\n")
	diskInfo, err := s.RunCommand(ctx, host, port, user, "df -h /")
	if err == nil {
		diag.DiskInfo = diskInfo
		fmt.Printf("  Disk: OK\n")
	}

	return diag, nil
}

// Diagnostics holds the results of a diagnostic check
type Diagnostics struct {
	Host            string `json:"host"`
	Port            int    `json:"port"`
	User            string `json:"user"`
	SSHConnected    bool   `json:"ssh_connected"`
	SSHError        string `json:"ssh_error,omitempty"`
	GPUAvailable    bool   `json:"gpu_available"`
	GPUInfo         string `json:"gpu_info,omitempty"`
	GPUError        string `json:"gpu_error,omitempty"`
	DockerAvailable bool   `json:"docker_available"`
	DockerError     string `json:"docker_error,omitempty"`
	DiskInfo        string `json:"disk_info,omitempty"`
}

// IsReady returns true if the host is ready for benchmarking
func (d *Diagnostics) IsReady() bool {
	return d.SSHConnected && d.GPUAvailable && d.DockerAvailable
}

// SSHKeyExists checks if the SSH key file exists
func SSHKeyExists(keyPath string) bool {
	_, err := os.Stat(keyPath)
	return err == nil
}
