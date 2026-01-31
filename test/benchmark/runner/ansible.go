package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AnsibleExecutor handles execution of Ansible playbooks
type AnsibleExecutor struct {
	playbookDir string
	sshKeyPath  string
	sshUser     string
	timeout     time.Duration
	verbose     bool
}

// NewAnsibleExecutor creates a new Ansible executor
func NewAnsibleExecutor(playbookDir, sshKeyPath, sshUser string) *AnsibleExecutor {
	return &AnsibleExecutor{
		playbookDir: playbookDir,
		sshKeyPath:  sshKeyPath,
		sshUser:     sshUser,
		timeout:     30 * time.Minute,
		verbose:     false,
	}
}

// SetTimeout sets the maximum execution time for playbooks
func (a *AnsibleExecutor) SetTimeout(timeout time.Duration) {
	a.timeout = timeout
}

// SetVerbose enables verbose output
func (a *AnsibleExecutor) SetVerbose(verbose bool) {
	a.verbose = verbose
}

// RunPlaybook executes an Ansible playbook against a target host
func (a *AnsibleExecutor) RunPlaybook(ctx context.Context, playbook string, host string, port int, vars map[string]string) error {
	return a.RunPlaybookWithKey(ctx, playbook, host, port, vars, "")
}

// RunPlaybookWithKey executes an Ansible playbook with a specific SSH key
func (a *AnsibleExecutor) RunPlaybookWithKey(ctx context.Context, playbook string, host string, port int, vars map[string]string, sshKeyPath string) error {
	playbookPath := filepath.Join(a.playbookDir, "playbooks", playbook)

	// Verify playbook exists
	if _, err := os.Stat(playbookPath); os.IsNotExist(err) {
		return fmt.Errorf("playbook not found: %s", playbookPath)
	}

	// Use provided key or fall back to default
	keyPath := sshKeyPath
	if keyPath == "" {
		keyPath = a.sshKeyPath
	}

	// Build inventory string
	var inventory string
	if port != 0 && port != 22 {
		inventory = fmt.Sprintf("%s ansible_port=%d", host, port)
	} else {
		inventory = host + ","
	}

	// Build command arguments
	args := []string{
		playbookPath,
		"-i", inventory,
		"-u", a.sshUser,
		"--private-key", keyPath,
		"-e", "ansible_ssh_common_args='-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'",
	}

	// Add extra variables
	for key, value := range vars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	// Add verbosity
	if a.verbose {
		args = append(args, "-vv")
	}

	// Create timeout context
	execCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	// Execute ansible-playbook
	cmd := exec.CommandContext(execCtx, "ansible-playbook", args...)
	cmd.Dir = a.playbookDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set environment
	cmd.Env = append(os.Environ(),
		"ANSIBLE_HOST_KEY_CHECKING=False",
		"ANSIBLE_RETRY_FILES_ENABLED=False",
	)

	fmt.Printf("Running: ansible-playbook %s\n", strings.Join(args, " "))

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime)

	// Log output
	if a.verbose || err != nil {
		if stdout.Len() > 0 {
			fmt.Printf("STDOUT:\n%s\n", stdout.String())
		}
		if stderr.Len() > 0 {
			fmt.Printf("STDERR:\n%s\n", stderr.String())
		}
	}

	if execCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("playbook timed out after %s", a.timeout)
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("playbook failed with exit code %d: %s",
				exitErr.ExitCode(), stderr.String())
		}
		return fmt.Errorf("playbook execution failed: %w", err)
	}

	fmt.Printf("Playbook %s completed in %s\n", playbook, duration.Round(time.Second))
	return nil
}

// RunPlaybookWithInventory executes a playbook using the dynamic inventory
func (a *AnsibleExecutor) RunPlaybookWithInventory(ctx context.Context, playbook string, vars map[string]string) error {
	playbookPath := filepath.Join(a.playbookDir, "playbooks", playbook)
	inventoryPath := filepath.Join(a.playbookDir, "inventory", "dynamic.py")

	// Verify files exist
	if _, err := os.Stat(playbookPath); os.IsNotExist(err) {
		return fmt.Errorf("playbook not found: %s", playbookPath)
	}
	if _, err := os.Stat(inventoryPath); os.IsNotExist(err) {
		return fmt.Errorf("inventory not found: %s", inventoryPath)
	}

	// Build command arguments
	args := []string{
		playbookPath,
		"-i", inventoryPath,
	}

	// Add extra variables
	for key, value := range vars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	if a.verbose {
		args = append(args, "-vv")
	}

	// Create timeout context
	execCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "ansible-playbook", args...)
	cmd.Dir = a.playbookDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	cmd.Env = append(os.Environ(),
		"ANSIBLE_HOST_KEY_CHECKING=False",
		"ANSIBLE_RETRY_FILES_ENABLED=False",
	)

	fmt.Printf("Running: ansible-playbook %s\n", strings.Join(args, " "))

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime)

	if a.verbose || err != nil {
		if stdout.Len() > 0 {
			fmt.Printf("STDOUT:\n%s\n", stdout.String())
		}
		if stderr.Len() > 0 {
			fmt.Printf("STDERR:\n%s\n", stderr.String())
		}
	}

	if execCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("playbook timed out after %s", a.timeout)
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("playbook failed with exit code %d", exitErr.ExitCode())
		}
		return fmt.Errorf("playbook execution failed: %w", err)
	}

	fmt.Printf("Playbook %s completed in %s\n", playbook, duration.Round(time.Second))
	return nil
}

// CheckAnsibleInstalled verifies that ansible-playbook is available
func CheckAnsibleInstalled() error {
	cmd := exec.Command("ansible-playbook", "--version")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ansible-playbook not found: %w", err)
	}

	// Parse version from output
	lines := strings.Split(string(output), "\n")
	if len(lines) > 0 {
		fmt.Printf("Ansible: %s\n", strings.TrimSpace(lines[0]))
	}

	return nil
}

// ValidatePlaybook checks if a playbook has valid syntax
func (a *AnsibleExecutor) ValidatePlaybook(playbook string) error {
	playbookPath := filepath.Join(a.playbookDir, "playbooks", playbook)

	cmd := exec.Command("ansible-playbook", playbookPath, "--syntax-check")
	cmd.Dir = a.playbookDir

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("syntax check failed: %s", stderr.String())
	}

	return nil
}

// ListPlaybooks returns all available playbooks
func (a *AnsibleExecutor) ListPlaybooks() ([]string, error) {
	playbooksDir := filepath.Join(a.playbookDir, "playbooks")

	entries, err := os.ReadDir(playbooksDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read playbooks directory: %w", err)
	}

	var playbooks []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yml") {
			playbooks = append(playbooks, entry.Name())
		}
	}

	return playbooks, nil
}
