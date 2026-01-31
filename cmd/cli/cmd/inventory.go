package cmd

import (
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
	inventoryProvider    string
	inventoryGPUType     string
	inventoryMaxPrice    float64
	inventoryMinVRAM     int
	inventoryMinGPUCount int
)

var inventoryCmd = &cobra.Command{
	Use:   "inventory",
	Short: "List available GPU offers",
	Long:  `Display available GPU offers from all configured providers.`,
	RunE:  runInventory,
}

func init() {
	rootCmd.AddCommand(inventoryCmd)

	inventoryCmd.Flags().StringVarP(&inventoryProvider, "provider", "p", "", "Filter by provider (vastai, tensordock)")
	inventoryCmd.Flags().StringVarP(&inventoryGPUType, "gpu", "g", "", "Filter by GPU type (e.g., RTX4090, A100)")
	inventoryCmd.Flags().Float64Var(&inventoryMaxPrice, "max-price", 0, "Maximum price per hour (USD)")
	inventoryCmd.Flags().IntVar(&inventoryMinVRAM, "min-vram", 0, "Minimum VRAM in GB")
	inventoryCmd.Flags().IntVar(&inventoryMinGPUCount, "min-gpus", 0, "Minimum GPU count")
}

func runInventory(cmd *cobra.Command, args []string) error {
	// Build query parameters
	params := url.Values{}
	if inventoryProvider != "" {
		params.Set("provider", inventoryProvider)
	}
	if inventoryGPUType != "" {
		params.Set("gpu_type", inventoryGPUType)
	}
	if inventoryMaxPrice > 0 {
		params.Set("max_price", fmt.Sprintf("%.2f", inventoryMaxPrice))
	}
	if inventoryMinVRAM > 0 {
		params.Set("min_vram", fmt.Sprintf("%d", inventoryMinVRAM))
	}
	if inventoryMinGPUCount > 0 {
		params.Set("min_gpu_count", fmt.Sprintf("%d", inventoryMinGPUCount))
	}

	// Make request
	reqURL := fmt.Sprintf("%s/api/v1/inventory", serverURL)
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
		Offers []GPUOffer `json:"offers"`
		Count  int        `json:"count"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if outputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	// Table output
	if len(result.Offers) == 0 {
		fmt.Println("No offers found matching criteria.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPROVIDER\tGPU\tCOUNT\tVRAM\tPRICE/HR\tLOCATION")
	fmt.Fprintln(w, "--\t--------\t---\t-----\t----\t--------\t--------")

	for _, offer := range result.Offers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%dGB\t$%.2f\t%s\n",
			offer.ID,
			offer.Provider,
			offer.GPUType,
			offer.GPUCount,
			offer.VRAM,
			offer.PricePerHour,
			offer.Location,
		)
	}
	w.Flush()

	fmt.Printf("\nTotal: %d offers\n", result.Count)
	return nil
}
