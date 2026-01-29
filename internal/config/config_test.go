package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFromEnv_Defaults(t *testing.T) {
	// Clear environment
	os.Unsetenv("VASTAI_API_KEY")
	os.Unsetenv("TENSORDOCK_AUTH_ID")
	os.Unsetenv("TENSORDOCK_API_TOKEN")

	cfg, err := LoadFromEnv()
	require.NoError(t, err)

	// Check defaults
	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "./data/gpu-shopper.db", cfg.Database.Path)
	assert.Equal(t, time.Minute, cfg.Inventory.DefaultCacheTTL)
	assert.Equal(t, 5*time.Minute, cfg.Inventory.BackoffCacheTTL)
	assert.Equal(t, 12, cfg.Lifecycle.HardMaxHours)
	assert.Equal(t, "info", cfg.Logging.Level)
}

func TestLoadFromEnv_WithEnvVars(t *testing.T) {
	// Set environment variables
	os.Setenv("VASTAI_API_KEY", "test-vast-key")
	os.Setenv("TENSORDOCK_AUTH_ID", "test-auth-id")
	os.Setenv("TENSORDOCK_API_TOKEN", "test-api-token")
	os.Setenv("SERVER_PORT", "9090")
	defer func() {
		os.Unsetenv("VASTAI_API_KEY")
		os.Unsetenv("TENSORDOCK_AUTH_ID")
		os.Unsetenv("TENSORDOCK_API_TOKEN")
		os.Unsetenv("SERVER_PORT")
	}()

	cfg, err := LoadFromEnv()
	require.NoError(t, err)

	assert.Equal(t, "test-vast-key", cfg.Providers.VastAI.APIKey)
	assert.Equal(t, "test-auth-id", cfg.Providers.TensorDock.AuthID)
	assert.Equal(t, "test-api-token", cfg.Providers.TensorDock.APIToken)
	assert.Equal(t, 9090, cfg.Server.Port)
}

func TestConfig_Validate_NoProviders(t *testing.T) {
	cfg := &Config{
		Providers: ProvidersConfig{
			VastAI:     VastAIConfig{Enabled: false},
			TensorDock: TensorDockConfig{Enabled: false},
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one provider must be enabled")
}

func TestConfig_Validate_VastAIMissingKey(t *testing.T) {
	cfg := &Config{
		Providers: ProvidersConfig{
			VastAI:     VastAIConfig{Enabled: true, APIKey: ""},
			TensorDock: TensorDockConfig{Enabled: false},
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "VASTAI_API_KEY")
}

func TestConfig_Validate_TensorDockMissingCreds(t *testing.T) {
	cfg := &Config{
		Providers: ProvidersConfig{
			VastAI:     VastAIConfig{Enabled: false},
			TensorDock: TensorDockConfig{Enabled: true, AuthID: "", APIToken: ""},
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "TENSORDOCK_AUTH_ID")
}

func TestConfig_Validate_Success(t *testing.T) {
	cfg := &Config{
		Providers: ProvidersConfig{
			VastAI:     VastAIConfig{Enabled: true, APIKey: "test-key"},
			TensorDock: TensorDockConfig{Enabled: true, AuthID: "auth-id", APIToken: "token"},
		},
	}

	err := cfg.Validate()
	assert.NoError(t, err)
}
