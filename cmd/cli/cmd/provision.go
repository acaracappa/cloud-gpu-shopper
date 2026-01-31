package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/spf13/cobra"
)

var (
	provisionConsumerID  string
	provisionOfferID     string
	provisionWorkload    string
	provisionHours       int
	provisionIdleTimeout int
	provisionStorage     string
	provisionSaveKey     string
	provisionGPUType     string
)

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision a new GPU session",
	Long:  `Provision a new GPU session from an available offer.`,
	RunE:  runProvision,
}

func init() {
	rootCmd.AddCommand(provisionCmd)

	provisionCmd.Flags().StringVarP(&provisionConsumerID, "consumer", "c", "", "Consumer ID (required)")
	provisionCmd.Flags().StringVarP(&provisionOfferID, "offer", "i", "", "Offer ID to provision")
	provisionCmd.Flags().StringVarP(&provisionGPUType, "gpu", "g", "", "GPU type to auto-select cheapest offer (e.g., RTX4090, A100)")
	provisionCmd.Flags().StringVarP(&provisionWorkload, "workload", "w", "llm", "Workload type (llm, llm_vllm, llm_tgi, training, batch, interactive)")
	provisionCmd.Flags().IntVarP(&provisionHours, "hours", "t", 2, "Reservation hours (1-12)")
	provisionCmd.Flags().IntVar(&provisionIdleTimeout, "idle-timeout", 0, "Idle timeout in minutes (0 = disabled)")
	provisionCmd.Flags().StringVar(&provisionStorage, "storage", "destroy", "Storage policy (destroy, preserve)")
	provisionCmd.Flags().StringVar(&provisionSaveKey, "save-key", "", "Save SSH private key to file")

	provisionCmd.MarkFlagRequired("consumer")
}

func runProvision(cmd *cobra.Command, args []string) error {
	// Validate workload type
	validWorkloads := map[string]bool{
		"llm": true, "llm_vllm": true, "llm_tgi": true,
		"training": true, "batch": true, "interactive": true,
	}
	if !validWorkloads[provisionWorkload] {
		return fmt.Errorf("invalid workload type %q, valid types: llm, llm_vllm, llm_tgi, training, batch, interactive", provisionWorkload)
	}

	// If --gpu provided but not --offer, auto-select cheapest matching offer
	if provisionOfferID == "" && provisionGPUType != "" {
		offer, err := selectCheapestOffer(provisionGPUType)
		if err != nil {
			return fmt.Errorf("failed to auto-select offer: %w", err)
		}
		provisionOfferID = offer.ID
		fmt.Printf("Auto-selected offer %s (%s, $%.2f/hr)\n", offer.ID, offer.GPUType, offer.PricePerHour)
	}

	if provisionOfferID == "" {
		return fmt.Errorf("either --offer or --gpu must be provided")
	}

	reqBody := map[string]interface{}{
		"consumer_id":       provisionConsumerID,
		"offer_id":          provisionOfferID,
		"workload_type":     provisionWorkload,
		"reservation_hours": provisionHours,
		"storage_policy":    provisionStorage,
	}

	if provisionIdleTimeout > 0 {
		reqBody["idle_threshold_minutes"] = provisionIdleTimeout
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	reqURL := fmt.Sprintf("%s/api/v1/sessions", serverURL)
	resp, err := http.Post(reqURL, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("provisioning failed: %s", string(body))
	}

	var result SessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if outputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	// Display session details
	fmt.Println("Session provisioned successfully!")
	fmt.Println()
	fmt.Printf("Session ID:    %s\n", result.Session.ID)
	fmt.Printf("Provider:      %s\n", result.Session.Provider)
	fmt.Printf("GPU Type:      %s\n", result.Session.GPUType)
	fmt.Printf("Status:        %s\n", result.Session.Status)
	fmt.Printf("Price/Hour:    $%.2f\n", result.Session.PricePerHour)
	fmt.Printf("Expires At:    %s\n", result.Session.ExpiresAt)
	fmt.Println()

	if result.Session.SSHHost != "" {
		fmt.Println("SSH Connection:")
		fmt.Printf("  Host: %s\n", result.Session.SSHHost)
		fmt.Printf("  Port: %d\n", result.Session.SSHPort)
		fmt.Printf("  User: %s\n", result.Session.SSHUser)
		fmt.Println()
	}

	if result.SSHPrivateKey != "" {
		if provisionSaveKey != "" {
			if err := os.WriteFile(provisionSaveKey, []byte(result.SSHPrivateKey), 0600); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save SSH key: %v\n", err)
			} else {
				fmt.Printf("SSH private key saved to: %s\n", provisionSaveKey)
			}
		} else {
			fmt.Println("SSH Private Key (save this, shown only once):")
			fmt.Println("---BEGIN---")
			fmt.Println(result.SSHPrivateKey)
			fmt.Println("---END---")
		}
		fmt.Println()
	}

	fmt.Println("Note: The session is provisioning. Check status with:")
	fmt.Printf("  gpu-shopper sessions get %s\n", result.Session.ID)

	return nil
}

// selectCheapestOffer queries the inventory API for the cheapest available offer matching the GPU type
func selectCheapestOffer(gpuType string) (*GPUOffer, error) {
	params := url.Values{}
	params.Set("gpu_type", gpuType)

	reqURL := fmt.Sprintf("%s/api/v1/inventory?%s", serverURL, params.Encode())
	resp, err := http.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("inventory request failed: %s", string(body))
	}

	var result struct {
		Offers []GPUOffer `json:"offers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Offers) == 0 {
		return nil, fmt.Errorf("no offers found for GPU type %s", gpuType)
	}

	// Offers are already sorted by price from API
	return &result.Offers[0], nil
}
