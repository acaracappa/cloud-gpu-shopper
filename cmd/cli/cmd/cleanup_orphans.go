package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/config"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider/tensordock"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider/vastai"
	"github.com/spf13/cobra"
)

var (
	cleanupExecute  bool
	cleanupForce    bool
	cleanupProvider string
)

var cleanupOrphansCmd = &cobra.Command{
	Use:   "cleanup-orphans",
	Short: "Find and destroy orphan GPU instances",
	Long: `Find and destroy orphan GPU instances across all configured providers.

This command works WITHOUT the API server - it uses direct provider access
via environment variables (VASTAI_API_KEY, TENSORDOCK_AUTH_ID, TENSORDOCK_API_TOKEN).

By default, it runs in dry-run mode, showing what would be destroyed.
Use --execute to actually destroy the instances.

Examples:
  # Dry-run (show what would be destroyed)
  gpu-shopper cleanup-orphans

  # Actually destroy orphan instances
  gpu-shopper cleanup-orphans --execute

  # Target a specific provider
  gpu-shopper cleanup-orphans -p vastai

  # Destroy without confirmation prompt
  gpu-shopper cleanup-orphans --execute --force`,
	RunE: runCleanupOrphans,
}

func init() {
	rootCmd.AddCommand(cleanupOrphansCmd)

	cleanupOrphansCmd.Flags().BoolVar(&cleanupExecute, "execute", false, "Actually destroy instances (default is dry-run)")
	cleanupOrphansCmd.Flags().BoolVar(&cleanupForce, "force", false, "Skip confirmation prompt when destroying")
	cleanupOrphansCmd.Flags().StringVarP(&cleanupProvider, "provider", "p", "", "Target specific provider (vastai, tensordock)")
}

// OrphanInstance represents an instance found during cleanup scan
type OrphanInstance struct {
	Provider     string
	InstanceID   string
	Name         string
	Status       string
	PricePerHour float64
	StartedAt    time.Time
}

func runCleanupOrphans(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Load configuration from environment
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize providers based on config and filter
	providers, err := initializeProviders(cfg, cleanupProvider)
	if err != nil {
		return err
	}

	if len(providers) == 0 {
		return fmt.Errorf("no providers configured; set VASTAI_API_KEY or TENSORDOCK_AUTH_ID/TENSORDOCK_API_TOKEN")
	}

	// Collect all orphan instances from all providers
	var orphans []OrphanInstance
	var totalCost float64

	fmt.Println("Scanning for orphan instances...")
	fmt.Println()

	for _, p := range providers {
		fmt.Printf("Checking %s...\n", p.Name())
		instances, err := p.ListAllInstances(ctx)
		if err != nil {
			fmt.Printf("  Warning: failed to list instances from %s: %v\n", p.Name(), err)
			continue
		}

		for _, inst := range instances {
			orphan := OrphanInstance{
				Provider:     p.Name(),
				InstanceID:   inst.ID,
				Name:         inst.Name,
				Status:       inst.Status,
				PricePerHour: inst.PricePerHour,
				StartedAt:    inst.StartedAt,
			}
			orphans = append(orphans, orphan)
			totalCost += inst.PricePerHour
		}

		fmt.Printf("  Found %d shopper-managed instances\n", len(instances))
	}

	fmt.Println()

	// Output results
	if len(orphans) == 0 {
		fmt.Println("No shopper-managed instances found.")
		return nil
	}

	// Display instances in table or JSON format
	if outputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]interface{}{
			"instances":   orphans,
			"count":       len(orphans),
			"total_cost":  totalCost,
			"dry_run":     !cleanupExecute,
		})
	}

	// Table output
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tINSTANCE ID\tNAME\tSTATUS\tPRICE/HR\tSTARTED")
	fmt.Fprintln(w, "--------\t-----------\t----\t------\t--------\t-------")

	for _, orphan := range orphans {
		startedStr := "unknown"
		if !orphan.StartedAt.IsZero() {
			startedStr = orphan.StartedAt.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t$%.3f\t%s\n",
			orphan.Provider,
			orphan.InstanceID,
			truncateString(orphan.Name, 30),
			orphan.Status,
			orphan.PricePerHour,
			startedStr,
		)
	}
	w.Flush()

	fmt.Printf("\nTotal: %d instances, $%.3f/hr combined cost\n", len(orphans), totalCost)

	// If not executing, show dry-run message and exit
	if !cleanupExecute {
		fmt.Println()
		fmt.Println("This was a dry-run. To actually destroy these instances, run:")
		fmt.Println("  gpu-shopper cleanup-orphans --execute")
		return nil
	}

	// Executing - prompt for confirmation unless --force
	if !cleanupForce {
		fmt.Println()
		fmt.Printf("WARNING: You are about to destroy %d instance(s).\n", len(orphans))
		fmt.Print("Type 'yes' to confirm: ")

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read confirmation: %w", err)
		}

		if strings.TrimSpace(input) != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Destroy instances
	fmt.Println()
	fmt.Println("Destroying instances...")

	var destroyed, failed int
	for _, orphan := range orphans {
		// Find the provider for this instance
		var targetProvider provider.Provider
		for _, p := range providers {
			if p.Name() == orphan.Provider {
				targetProvider = p
				break
			}
		}

		if targetProvider == nil {
			fmt.Printf("  ERROR: provider %s not found for instance %s\n", orphan.Provider, orphan.InstanceID)
			failed++
			continue
		}

		fmt.Printf("  Destroying %s/%s...", orphan.Provider, orphan.InstanceID)
		err := targetProvider.DestroyInstance(ctx, orphan.InstanceID)
		if err != nil {
			fmt.Printf(" FAILED: %v\n", err)
			failed++
		} else {
			fmt.Println(" OK")
			destroyed++
		}
	}

	fmt.Println()
	fmt.Printf("Cleanup complete: %d destroyed, %d failed\n", destroyed, failed)

	if failed > 0 {
		return fmt.Errorf("%d instance(s) failed to destroy", failed)
	}

	return nil
}

// initializeProviders creates provider clients based on config and filter
func initializeProviders(cfg *config.Config, providerFilter string) ([]provider.Provider, error) {
	var providers []provider.Provider

	// Vast.ai
	if cfg.Providers.VastAI.APIKey != "" {
		if providerFilter == "" || providerFilter == "vastai" {
			client := vastai.NewClient(cfg.Providers.VastAI.APIKey)
			providers = append(providers, client)
		}
	}

	// TensorDock
	if cfg.Providers.TensorDock.AuthID != "" && cfg.Providers.TensorDock.APIToken != "" {
		if providerFilter == "" || providerFilter == "tensordock" {
			opts := []tensordock.ClientOption{}
			if cfg.Providers.TensorDock.DefaultImage != "" {
				opts = append(opts, tensordock.WithDefaultImage(cfg.Providers.TensorDock.DefaultImage))
			}
			client := tensordock.NewClient(
				cfg.Providers.TensorDock.AuthID,
				cfg.Providers.TensorDock.APIToken,
				opts...,
			)
			providers = append(providers, client)
		}
	}

	// Validate provider filter
	if providerFilter != "" && len(providers) == 0 {
		validProviders := []string{}
		if cfg.Providers.VastAI.APIKey != "" {
			validProviders = append(validProviders, "vastai")
		}
		if cfg.Providers.TensorDock.AuthID != "" && cfg.Providers.TensorDock.APIToken != "" {
			validProviders = append(validProviders, "tensordock")
		}
		if len(validProviders) == 0 {
			return nil, fmt.Errorf("provider %q not configured; no providers have credentials set", providerFilter)
		}
		return nil, fmt.Errorf("provider %q not configured; available providers: %s", providerFilter, strings.Join(validProviders, ", "))
	}

	return providers, nil
}

// truncateString truncates a string to maxLen characters, adding "..." if truncated
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
