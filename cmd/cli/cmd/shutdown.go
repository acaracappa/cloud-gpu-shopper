package cmd

import (
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

var shutdownForce bool

var shutdownCmd = &cobra.Command{
	Use:   "shutdown [session-id]",
	Short: "Shutdown a GPU session",
	Long: `Shutdown a GPU session gracefully or forcefully.

By default, this signals the session to shut down gracefully.
Use --force to immediately destroy the session.`,
	Args: cobra.ExactArgs(1),
	RunE: runShutdown,
}

func init() {
	rootCmd.AddCommand(shutdownCmd)

	shutdownCmd.Flags().BoolVarP(&shutdownForce, "force", "f", false, "Force immediate shutdown")
}

func runShutdown(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	var reqURL string
	var method string

	if shutdownForce {
		// Force delete
		reqURL = fmt.Sprintf("%s/api/v1/sessions/%s", serverURL, sessionID)
		method = "DELETE"
	} else {
		// Graceful shutdown
		reqURL = fmt.Sprintf("%s/api/v1/sessions/%s/done", serverURL, sessionID)
		method = "POST"
	}

	req, _ := http.NewRequest(method, reqURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("shutdown failed: %s", string(body))
	}

	if shutdownForce {
		fmt.Printf("Session %s forcefully destroyed.\n", sessionID)
	} else {
		fmt.Printf("Session %s shutdown initiated.\n", sessionID)
		fmt.Println("The session will terminate gracefully.")
	}

	return nil
}
