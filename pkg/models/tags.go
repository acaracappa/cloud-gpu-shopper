package models

import "time"

// InstanceTags are metadata attached to provider instances for identification
type InstanceTags struct {
	ShopperSessionID    string    `json:"shopper_session_id"`
	ShopperDeploymentID string    `json:"shopper_deployment_id"`
	ShopperExpiresAt    time.Time `json:"shopper_expires_at"`
	ShopperConsumerID   string    `json:"shopper_consumer_id"`
}

// ToMap converts tags to a string map for provider APIs
func (t InstanceTags) ToMap() map[string]string {
	return map[string]string{
		"shopper_session_id":    t.ShopperSessionID,
		"shopper_deployment_id": t.ShopperDeploymentID,
		"shopper_expires_at":    t.ShopperExpiresAt.Format(time.RFC3339),
		"shopper_consumer_id":   t.ShopperConsumerID,
	}
}

// ToLabel generates a single label string for providers that only support one label
func (t InstanceTags) ToLabel() string {
	return "shopper-" + t.ShopperSessionID
}

// ParseLabel extracts session ID from a label
func ParseLabel(label string) (sessionID string, ok bool) {
	const prefix = "shopper-"
	if len(label) > len(prefix) && label[:len(prefix)] == prefix {
		return label[len(prefix):], true
	}
	return "", false
}
