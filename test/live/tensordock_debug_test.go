//go:build live

package live

import (
	"context"
	"os"
	"testing"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider/tensordock"
)

func TestDebugTensorDockCreate(t *testing.T) {
	authID := os.Getenv("TENSORDOCK_AUTH_ID")
	token := os.Getenv("TENSORDOCK_API_TOKEN")
	if token == "" || authID == "" {
		t.Skip("TENSORDOCK credentials not set")
	}

	// Enable debug mode to see full API request/response
	client := tensordock.NewClient(authID, token, tensordock.WithDebug(true))

	req := provider.CreateInstanceRequest{
		OfferID:      "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx3090-pcie-24gb",
		SessionID:    "test-123",
		SSHPublicKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQDHJu9m/oyqtRfvGqkzYeBkmqjmyGsC6Ldq7seWXJtNsklOd3wFtJ+NDrzzmZr6D1EYZR6mqesWhBXlijSmmSqgM0MxE4SUsfAH2XV1cZISHyii8rufFUuLIxQ54lYrqqgJcvpu1OM8qi/jCnlU1/Yg6YnSTpp7yTcCLFdimNdsB+gSo9gGOSaII8Mu5dAaN+NNixEgqG3Bi1uuVmEefiBLhsdhZYP3z63V32LZwftvoYyvzmj5mX6QrYC/S0ieP4GRcqkre3MccBU28XqWDXXL7zJ4/Z7omBGbJ9Hw8cVaGR66k86+JDFIzZ/snQfAdph3TLnbI2QYOc0mYtbqV/Av51q+KQtAJTa6qk7XI/G4v5iSsMyz2xY9v1Kj5fQ35TnTNG+yNyqbL4b2mEUHW6VnTA/8PhCF4URoDB8DvKJtVi8OlEbfL2f5KQaqPEhmq3N9qi3ZzEHVu+/b3Zmk+kHHKZNcQzo5BhMosJx62UyrPFnn3xCiIu0m/pfQEkPvYlZIYtFuLiapXC3gYYfgYN3cT0X9NApTsU4QU8C8bVqcE6yasO1qdTsCnaWIm+x+HmrU8y1HVShADBUfeAZaK/hmAT3MUL/ytusMRqUXHPNGcofd1YoRVI0uP0b7CSiA+ir/TlSkavUWPJ+ROVr8TBhJ+UkQdiASpKVVuVHAKF3/Ow== test@local",
		EnvVars: map[string]string{
			"SHOPPER_AGENT_URL":   "https://github.com/acaracappa/cloud-gpu-shopper/releases/latest/download/gpu-shopper-agent-linux-amd64",
			"SHOPPER_URL":         "https://example.com",
			"SHOPPER_SESSION_ID":  "test-123",
			"SHOPPER_AGENT_TOKEN": "token-456",
		},
	}

	ctx := context.Background()
	result, err := client.CreateInstance(ctx, req)
	if err != nil {
		t.Fatalf("CreateInstance failed: %v", err)
	}

	t.Logf("Instance created: %+v", result)

	// Clean up
	defer client.DestroyInstance(ctx, result.ProviderInstanceID)
}
