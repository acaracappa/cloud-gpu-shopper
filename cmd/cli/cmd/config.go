package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "View and manage CLI configuration",
	Long:  `View and manage GPU Shopper CLI configuration.`,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	RunE:  runConfigShow,
}

var configSetCmd = &cobra.Command{
	Use:   "set [key] [value]",
	Short: "Set a configuration value",
	Long: `Set a configuration value. Supported keys:
  server  - GPU Shopper server URL`,
	Args: cobra.ExactArgs(2),
	RunE: runConfigSet,
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	fmt.Println("GPU Shopper CLI Configuration")
	fmt.Println("==============================")
	fmt.Println()
	fmt.Printf("Server URL:     %s\n", serverURL)
	fmt.Printf("Output Format:  %s\n", outputFormat)
	fmt.Println()

	fmt.Println("Environment Variables:")
	if url := os.Getenv("GPU_SHOPPER_URL"); url != "" {
		fmt.Printf("  GPU_SHOPPER_URL=%s\n", url)
	} else {
		fmt.Println("  GPU_SHOPPER_URL (not set, using default)")
	}

	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	key := args[0]
	value := args[1]

	switch key {
	case "server":
		fmt.Printf("To set the server URL, use the environment variable:\n")
		fmt.Printf("  export GPU_SHOPPER_URL=%s\n", value)
		fmt.Println()
		fmt.Println("Or use the --server flag with each command.")
	default:
		return fmt.Errorf("unknown configuration key: %s", key)
	}

	return nil
}
