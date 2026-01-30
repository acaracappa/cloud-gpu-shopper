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
	costsConsumerID string
	costsSessionID  string
	costsPeriod     string
	costsStartDate  string
	costsEndDate    string
)

var costsCmd = &cobra.Command{
	Use:   "costs",
	Short: "View cost information",
	Long:  `View cost information for sessions and consumers.`,
	RunE:  runCosts,
}

var costsSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "View cost summary",
	RunE:  runCostsSummary,
}

func init() {
	rootCmd.AddCommand(costsCmd)
	costsCmd.AddCommand(costsSummaryCmd)

	costsCmd.Flags().StringVarP(&costsConsumerID, "consumer", "c", "", "Filter by consumer ID")
	costsCmd.Flags().StringVarP(&costsSessionID, "session", "s", "", "Get cost for specific session")
	costsCmd.Flags().StringVarP(&costsPeriod, "period", "p", "", "Time period (daily, monthly)")
	costsCmd.Flags().StringVar(&costsStartDate, "start", "", "Start date (YYYY-MM-DD)")
	costsCmd.Flags().StringVar(&costsEndDate, "end", "", "End date (YYYY-MM-DD)")

	costsSummaryCmd.Flags().StringVarP(&costsConsumerID, "consumer", "c", "", "Filter by consumer ID")
}

func runCosts(cmd *cobra.Command, args []string) error {
	params := url.Values{}
	if costsConsumerID != "" {
		params.Set("consumer_id", costsConsumerID)
	}
	if costsSessionID != "" {
		params.Set("session_id", costsSessionID)
	}
	if costsPeriod != "" {
		params.Set("period", costsPeriod)
	}
	if costsStartDate != "" {
		params.Set("start_date", costsStartDate)
	}
	if costsEndDate != "" {
		params.Set("end_date", costsEndDate)
	}

	reqURL := fmt.Sprintf("%s/api/v1/costs", serverURL)
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

	var result CostSummary
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if outputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	printCostSummary(result)
	return nil
}

func runCostsSummary(cmd *cobra.Command, args []string) error {
	params := url.Values{}
	if costsConsumerID != "" {
		params.Set("consumer_id", costsConsumerID)
	}

	reqURL := fmt.Sprintf("%s/api/v1/costs/summary", serverURL)
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

	var result CostSummary
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if outputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	printCostSummary(result)
	return nil
}

func printCostSummary(summary CostSummary) {
	fmt.Println("Cost Summary")
	fmt.Println("============")
	fmt.Println()

	if summary.ConsumerID != "" {
		fmt.Printf("Consumer:      %s\n", summary.ConsumerID)
	}

	fmt.Printf("Total Cost:    $%.2f\n", summary.TotalCost)
	fmt.Printf("Sessions:      %d\n", summary.SessionCount)
	fmt.Printf("Hours Used:    %.1f\n", summary.HoursUsed)

	if !summary.PeriodStart.IsZero() && !summary.PeriodEnd.IsZero() {
		fmt.Printf("Period:        %s to %s\n",
			summary.PeriodStart.Format("2006-01-02"),
			summary.PeriodEnd.Format("2006-01-02"))
	}

	if len(summary.ByProvider) > 0 {
		fmt.Println("\nBy Provider:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for provider, cost := range summary.ByProvider {
			fmt.Fprintf(w, "  %s\t$%.2f\n", provider, cost)
		}
		w.Flush()
	}

	if len(summary.ByGPUType) > 0 {
		fmt.Println("\nBy GPU Type:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for gpuType, cost := range summary.ByGPUType {
			fmt.Fprintf(w, "  %s\t$%.2f\n", gpuType, cost)
		}
		w.Flush()
	}
}

// CostSummary represents cost summary from the API
type CostSummary struct {
	ConsumerID   string             `json:"consumer_id,omitempty"`
	TotalCost    float64            `json:"total_cost"`
	SessionCount int                `json:"session_count"`
	HoursUsed    float64            `json:"hours_used"`
	ByProvider   map[string]float64 `json:"by_provider,omitempty"`
	ByGPUType    map[string]float64 `json:"by_gpu_type,omitempty"`
	PeriodStart  Time               `json:"period_start,omitempty"`
	PeriodEnd    Time               `json:"period_end,omitempty"`
}

// Time is a custom time type for JSON unmarshaling
type Time struct {
	time   string
	isZero bool
}

func (t *Time) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		t.isZero = true
		return nil
	}
	if s == "" || s == "0001-01-01T00:00:00Z" {
		t.isZero = true
		return nil
	}
	t.time = s
	return nil
}

func (t Time) IsZero() bool {
	return t.isZero || t.time == ""
}

func (t Time) Format(layout string) string {
	return t.time
}
