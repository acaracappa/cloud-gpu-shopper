package models

import "time"

// CostRecord represents a cost entry for a session
type CostRecord struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	ConsumerID string    `json:"consumer_id"`
	Provider   string    `json:"provider"`
	GPUType    string    `json:"gpu_type"`
	Hour       time.Time `json:"hour"`     // Truncated to hour
	Amount     float64   `json:"amount"`   // Cost in USD
	Currency   string    `json:"currency"` // Always "USD" for now
}

// CostSummary provides aggregated cost information
type CostSummary struct {
	ConsumerID   string             `json:"consumer_id,omitempty"`
	TotalCost    float64            `json:"total_cost"`
	SessionCount int                `json:"session_count"`
	HoursUsed    float64            `json:"hours_used"`
	ByProvider   map[string]float64 `json:"by_provider,omitempty"`
	ByGPUType    map[string]float64 `json:"by_gpu_type,omitempty"`
	PeriodStart  time.Time          `json:"period_start,omitempty"`
	PeriodEnd    time.Time          `json:"period_end,omitempty"`
}

// CostQuery defines criteria for querying costs
type CostQuery struct {
	ConsumerID string    `json:"consumer_id,omitempty"`
	SessionID  string    `json:"session_id,omitempty"`
	Provider   string    `json:"provider,omitempty"`
	StartTime  time.Time `json:"start_time,omitempty"`
	EndTime    time.Time `json:"end_time,omitempty"`
}

// Consumer represents a registered API consumer
type Consumer struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	APIKey       string    `json:"api_key,omitempty"` // Only shown on creation
	BudgetLimit  float64   `json:"budget_limit"`      // Monthly budget limit in USD, 0 = unlimited
	WebhookURL   string    `json:"webhook_url,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	CurrentSpend float64   `json:"current_spend"` // Current month spend
	AlertSent    bool      `json:"alert_sent"`    // Budget alert already sent this period
}

// BudgetAlert is sent when a consumer approaches or exceeds their budget
type BudgetAlert struct {
	ConsumerID   string    `json:"consumer_id"`
	ConsumerName string    `json:"consumer_name"`
	BudgetLimit  float64   `json:"budget_limit"`
	CurrentSpend float64   `json:"current_spend"`
	Percentage   float64   `json:"percentage"`
	AlertType    string    `json:"alert_type"` // "warning" (80%) or "exceeded" (100%)
	Timestamp    time.Time `json:"timestamp"`
}
