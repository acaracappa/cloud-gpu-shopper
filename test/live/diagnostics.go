//go:build live
// +build live

package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// DiagnosticsCollector collects diagnostic data from GPU instances
type DiagnosticsCollector struct {
	sessionID string
	outputDir string
	startTime time.Time
	sshHelper *SSHHelper
	env       *LiveTestEnv
	snapshots []string
}

// DiagnosticSnapshot contains all collected diagnostic data
type DiagnosticSnapshot struct {
	Timestamp    time.Time `json:"timestamp"`
	Label        string    `json:"label"`
	SessionID    string    `json:"session_id"`
	SessionState *Session  `json:"session_state,omitempty"`
	Processes    string    `json:"processes,omitempty"`
	Environment  string    `json:"environment,omitempty"`
	NvidiaSMI    string    `json:"nvidia_smi,omitempty"`
	Network      string    `json:"network,omitempty"`
	Errors       []string  `json:"errors,omitempty"`
}

// NewDiagnosticsCollector creates a new diagnostics collector
func NewDiagnosticsCollector(sessionID string, outputDir string, env *LiveTestEnv) *DiagnosticsCollector {
	// Create session-specific output directory
	sessionDir := filepath.Join(outputDir, sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		log.Printf("Warning: failed to create diagnostics dir %s: %v", sessionDir, err)
	}

	return &DiagnosticsCollector{
		sessionID: sessionID,
		outputDir: sessionDir,
		startTime: time.Now(),
		env:       env,
		snapshots: make([]string, 0),
	}
}

// SetSSHHelper sets the SSH helper for remote diagnostics collection
func (d *DiagnosticsCollector) SetSSHHelper(ssh *SSHHelper) {
	d.sshHelper = ssh
}

// CollectSnapshot collects a full diagnostic snapshot with a label
func (d *DiagnosticsCollector) CollectSnapshot(ctx context.Context, label string) (*DiagnosticSnapshot, error) {
	snapshot := &DiagnosticSnapshot{
		Timestamp: time.Now(),
		Label:     label,
		SessionID: d.sessionID,
		Errors:    make([]string, 0),
	}

	// Collect session state from shopper
	if d.env != nil {
		session, err := d.getSessionSafe(ctx)
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, fmt.Sprintf("session state: %v", err))
		} else {
			snapshot.SessionState = session
		}
	}

	// Collect from instance via SSH if connected
	if d.sshHelper != nil && d.sshHelper.IsConnected() {
		// Process list
		procs, err := d.sshHelper.GetProcessList(ctx)
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, fmt.Sprintf("processes: %v", err))
		} else {
			snapshot.Processes = procs
		}

		// Environment
		env, err := d.sshHelper.GetEnvironment(ctx)
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, fmt.Sprintf("environment: %v", err))
		} else {
			snapshot.Environment = env
		}

		// nvidia-smi
		nvsmi, err := d.sshHelper.GetNvidiaSMI(ctx)
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, fmt.Sprintf("nvidia-smi: %v", err))
		} else {
			snapshot.NvidiaSMI = nvsmi
		}

		// Network status
		net, err := d.sshHelper.GetNetworkStatus(ctx)
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, fmt.Sprintf("network: %v", err))
		} else {
			snapshot.Network = net
		}
	}

	// Save snapshot to file
	if err := d.saveSnapshot(snapshot); err != nil {
		log.Printf("Failed to save snapshot: %v", err)
	}

	return snapshot, nil
}

func (d *DiagnosticsCollector) getSessionSafe(ctx context.Context) (*Session, error) {
	// Use a simple HTTP client call to avoid testing assertions
	if d.env == nil || d.env.Config == nil {
		return nil, fmt.Errorf("environment not configured")
	}

	url := fmt.Sprintf("%s/api/v1/sessions/%s", d.env.Config.ServerURL, d.sessionID)
	resp, err := d.env.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var session Session
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (d *DiagnosticsCollector) saveSnapshot(snapshot *DiagnosticSnapshot) error {
	filename := fmt.Sprintf("%s_%s.json", snapshot.Timestamp.Format("20060102_150405"), snapshot.Label)
	path := filepath.Join(d.outputDir, filename)

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	d.snapshots = append(d.snapshots, path)
	return os.WriteFile(path, data, 0644)
}

// SaveTextFile saves a text file in the diagnostics directory
func (d *DiagnosticsCollector) SaveTextFile(filename, content string) error {
	path := filepath.Join(d.outputDir, filename)
	return os.WriteFile(path, []byte(content), 0644)
}

// CollectProvisionData saves provision request/response data
func (d *DiagnosticsCollector) CollectProvisionData(request *CreateSessionRequest, response *CreateSessionResponse) error {
	data := map[string]interface{}{
		"timestamp": time.Now(),
		"request":   request,
		"response":  response,
	}

	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return d.SaveTextFile("provision.json", string(content))
}

// GenerateSummary creates a summary file for the test
func (d *DiagnosticsCollector) GenerateSummary(testName string, passed bool, err error) error {
	summary := map[string]interface{}{
		"test_name":      testName,
		"session_id":     d.sessionID,
		"start_time":     d.startTime,
		"end_time":       time.Now(),
		"duration":       time.Since(d.startTime).String(),
		"passed":         passed,
		"snapshot_count": len(d.snapshots),
		"snapshots":      d.snapshots,
	}

	if err != nil {
		summary["error"] = err.Error()
	}

	content, jsonErr := json.MarshalIndent(summary, "", "  ")
	if jsonErr != nil {
		return jsonErr
	}

	return d.SaveTextFile("summary.json", string(content))
}

// GetOutputDir returns the diagnostics output directory
func (d *DiagnosticsCollector) GetOutputDir() string {
	return d.outputDir
}

// GetSnapshots returns the list of saved snapshot paths
func (d *DiagnosticsCollector) GetSnapshots() []string {
	return d.snapshots
}

// DiagnosticsConfig holds configuration for diagnostics collection
type DiagnosticsConfig struct {
	OutputDir            string
	Enabled              bool
	CollectOnFailure     bool
	CollectOnTimeout     bool
	CollectOnLimitExceed bool
}

// DefaultDiagnosticsConfig returns default diagnostics configuration
func DefaultDiagnosticsConfig() *DiagnosticsConfig {
	outputDir := os.Getenv("DIAG_OUTPUT_DIR")
	if outputDir == "" {
		outputDir = "./diagnostics"
	}

	return &DiagnosticsConfig{
		OutputDir:            outputDir,
		Enabled:              true,
		CollectOnFailure:     true,
		CollectOnTimeout:     true,
		CollectOnLimitExceed: true,
	}
}

// DiagnosticsManager manages multiple diagnostic collectors
type DiagnosticsManager struct {
	config     *DiagnosticsConfig
	collectors map[string]*DiagnosticsCollector
	env        *LiveTestEnv
}

// NewDiagnosticsManager creates a new diagnostics manager
func NewDiagnosticsManager(config *DiagnosticsConfig, env *LiveTestEnv) *DiagnosticsManager {
	if err := os.MkdirAll(config.OutputDir, 0755); err != nil {
		log.Printf("Warning: failed to create diagnostics root dir: %v", err)
	}

	return &DiagnosticsManager{
		config:     config,
		collectors: make(map[string]*DiagnosticsCollector),
		env:        env,
	}
}

// GetCollector returns or creates a collector for a session
func (m *DiagnosticsManager) GetCollector(sessionID string) *DiagnosticsCollector {
	if collector, ok := m.collectors[sessionID]; ok {
		return collector
	}

	collector := NewDiagnosticsCollector(sessionID, m.config.OutputDir, m.env)
	m.collectors[sessionID] = collector
	return collector
}

// CollectAllSnapshots collects snapshots from all tracked sessions
func (m *DiagnosticsManager) CollectAllSnapshots(ctx context.Context, label string) {
	for _, collector := range m.collectors {
		if _, err := collector.CollectSnapshot(ctx, label); err != nil {
			log.Printf("Failed to collect snapshot for %s: %v", collector.sessionID, err)
		}
	}
}

// IsEnabled returns whether diagnostics collection is enabled
func (m *DiagnosticsManager) IsEnabled() bool {
	return m.config.Enabled
}
