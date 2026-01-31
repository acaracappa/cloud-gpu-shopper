package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	serverURL    string
	outputFormat string
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "gpu-shopper",
	Short: "GPU Shopper CLI - manage GPU cloud resources",
	Long: `GPU Shopper is a unified inventory and orchestration service
for commodity GPU cloud providers.

This CLI tool allows you to:
- Browse available GPU offers from multiple providers
- Provision GPU sessions
- Monitor session status and costs
- Manage session lifecycle`,
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&serverURL, "server", getEnvOrDefault("GPU_SHOPPER_URL", "http://localhost:8080"), "GPU Shopper server URL")
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "table", "Output format (table, json)")
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
