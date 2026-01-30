package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	sessionsConsumerID string
	sessionsStatus     string
)

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List and manage sessions",
	Long:  `List and manage GPU sessions.`,
}

var sessionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List sessions",
	RunE:  runSessionsList,
}

var sessionsGetCmd = &cobra.Command{
	Use:   "get [session-id]",
	Short: "Get session details",
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionsGet,
}

var sessionsDoneCmd = &cobra.Command{
	Use:   "done [session-id]",
	Short: "Signal session completion",
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionsDone,
}

var sessionsExtendCmd = &cobra.Command{
	Use:   "extend [session-id]",
	Short: "Extend session reservation",
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionsExtend,
}

var sessionsDeleteCmd = &cobra.Command{
	Use:   "delete [session-id]",
	Short: "Force delete a session",
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionsDelete,
}

var extendHours int

func init() {
	rootCmd.AddCommand(sessionsCmd)

	sessionsCmd.AddCommand(sessionsListCmd)
	sessionsCmd.AddCommand(sessionsGetCmd)
	sessionsCmd.AddCommand(sessionsDoneCmd)
	sessionsCmd.AddCommand(sessionsExtendCmd)
	sessionsCmd.AddCommand(sessionsDeleteCmd)

	sessionsListCmd.Flags().StringVarP(&sessionsConsumerID, "consumer", "c", "", "Filter by consumer ID")
	sessionsListCmd.Flags().StringVarP(&sessionsStatus, "status", "s", "", "Filter by status")

	sessionsExtendCmd.Flags().IntVarP(&extendHours, "hours", "t", 1, "Additional hours (1-12)")
}

func runSessionsList(cmd *cobra.Command, args []string) error {
	params := url.Values{}
	if sessionsConsumerID != "" {
		params.Set("consumer_id", sessionsConsumerID)
	}
	if sessionsStatus != "" {
		params.Set("status", sessionsStatus)
	}

	reqURL := fmt.Sprintf("%s/api/v1/sessions", serverURL)
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	resp, err := http.Get(reqURL)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error: %s", string(body))
	}

	var result struct {
		Sessions []Session `json:"sessions"`
		Count    int       `json:"count"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if outputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	if len(result.Sessions) == 0 {
		fmt.Println("No sessions found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tCONSUMER\tPROVIDER\tGPU\tSTATUS\tPRICE/HR\tEXPIRES")
	fmt.Fprintln(w, "--\t--------\t--------\t---\t------\t--------\t-------")

	for _, session := range result.Sessions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t$%.2f\t%s\n",
			session.ID,
			session.ConsumerID,
			session.Provider,
			session.GPUType,
			session.Status,
			session.PricePerHour,
			session.ExpiresAt,
		)
	}
	w.Flush()

	fmt.Printf("\nTotal: %d sessions\n", result.Count)
	return nil
}

func runSessionsGet(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	reqURL := fmt.Sprintf("%s/api/v1/sessions/%s", serverURL, sessionID)
	resp, err := http.Get(reqURL)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error: %s", string(body))
	}

	var session Session
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if outputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(session)
	}

	fmt.Printf("Session ID:     %s\n", session.ID)
	fmt.Printf("Consumer ID:    %s\n", session.ConsumerID)
	fmt.Printf("Provider:       %s\n", session.Provider)
	fmt.Printf("GPU Type:       %s\n", session.GPUType)
	fmt.Printf("GPU Count:      %d\n", session.GPUCount)
	fmt.Printf("Status:         %s\n", session.Status)
	fmt.Printf("Workload Type:  %s\n", session.WorkloadType)
	fmt.Printf("Price/Hour:     $%.2f\n", session.PricePerHour)
	fmt.Printf("Created At:     %s\n", session.CreatedAt)
	fmt.Printf("Expires At:     %s\n", session.ExpiresAt)

	if session.SSHHost != "" {
		fmt.Println("\nSSH Connection:")
		fmt.Printf("  ssh -p %d %s@%s\n", session.SSHPort, session.SSHUser, session.SSHHost)
	}

	if session.AgentEndpoint != "" {
		fmt.Printf("\nAgent Endpoint: %s\n", session.AgentEndpoint)
	}

	if session.Error != "" {
		fmt.Printf("\nError: %s\n", session.Error)
	}

	return nil
}

func runSessionsDone(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	reqURL := fmt.Sprintf("%s/api/v1/sessions/%s/done", serverURL, sessionID)
	req, _ := http.NewRequest("POST", reqURL, nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to signal done: %s", string(body))
	}

	fmt.Printf("Session %s shutdown initiated.\n", sessionID)
	return nil
}

func runSessionsExtend(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	reqBody := map[string]interface{}{
		"additional_hours": extendHours,
	}
	jsonBody, _ := json.Marshal(reqBody)

	reqURL := fmt.Sprintf("%s/api/v1/sessions/%s/extend", serverURL, sessionID)
	resp, err := http.Post(reqURL, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to extend session: %s", string(body))
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	fmt.Printf("Session %s extended by %d hours.\n", sessionID, extendHours)
	if expiresAt, ok := result["new_expires_at"]; ok {
		fmt.Printf("New expiration: %s\n", expiresAt)
	}
	return nil
}

func runSessionsDelete(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	reqURL := fmt.Sprintf("%s/api/v1/sessions/%s", serverURL, sessionID)
	req, _ := http.NewRequest("DELETE", reqURL, nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete session: %s", string(body))
	}

	fmt.Printf("Session %s destroyed.\n", sessionID)
	return nil
}
