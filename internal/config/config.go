package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all application configuration
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Providers ProvidersConfig `mapstructure:"providers"`
	Inventory InventoryConfig `mapstructure:"inventory"`
	Lifecycle LifecycleConfig `mapstructure:"lifecycle"`
	SSH       SSHConfig       `mapstructure:"ssh"`
	Logging   LoggingConfig   `mapstructure:"logging"`
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Path string `mapstructure:"path"`
}

// ProvidersConfig holds configuration for GPU providers
type ProvidersConfig struct {
	VastAI     VastAIConfig     `mapstructure:"vastai"`
	TensorDock TensorDockConfig `mapstructure:"tensordock"`
}

// VastAIConfig holds Vast.ai specific configuration
type VastAIConfig struct {
	APIKey  string `mapstructure:"api_key"`
	Enabled bool   `mapstructure:"enabled"`
}

// TensorDockConfig holds TensorDock specific configuration
type TensorDockConfig struct {
	AuthID       string `mapstructure:"auth_id"`
	APIToken     string `mapstructure:"api_token"`
	Enabled      bool   `mapstructure:"enabled"`
	DefaultImage string `mapstructure:"default_image"` // Default OS image (e.g., "ubuntu2404")
}

// InventoryConfig holds inventory cache configuration
type InventoryConfig struct {
	DefaultCacheTTL   time.Duration `mapstructure:"default_cache_ttl"`
	BackoffCacheTTL   time.Duration `mapstructure:"backoff_cache_ttl"`
	TensorDockCacheTTL time.Duration `mapstructure:"tensordock_cache_ttl"` // Shorter TTL for volatile TensorDock inventory
}

// LifecycleConfig holds lifecycle management configuration
type LifecycleConfig struct {
	CheckInterval          time.Duration `mapstructure:"check_interval"`
	HardMaxHours           int           `mapstructure:"hard_max_hours"`
	OrphanGracePeriod      time.Duration `mapstructure:"orphan_grace_period"`
	ReconciliationInterval time.Duration `mapstructure:"reconciliation_interval"`
	StartupSweepEnabled    bool          `mapstructure:"startup_sweep_enabled"`
	StartupSweepTimeout    time.Duration `mapstructure:"startup_sweep_timeout"`
	ShutdownTimeout        time.Duration `mapstructure:"shutdown_timeout"`
	DeploymentID           string        `mapstructure:"deployment_id"`
}

// SSHConfig holds SSH verification configuration
type SSHConfig struct {
	VerifyTimeout time.Duration `mapstructure:"verify_timeout"`
	CheckInterval time.Duration `mapstructure:"check_interval"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"` // "json" or "text"
}

// Load loads configuration from file and environment
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Read from config file if provided
	if configPath != "" {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			// Config file is optional
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				return nil, fmt.Errorf("failed to read config file: %w", err)
			}
		}
	}

	// Read from environment variables
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Bind specific environment variables
	bindEnvVars(v)

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}

// LoadFromEnv loads configuration primarily from environment variables
func LoadFromEnv() (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Read from .env file if it exists
	v.SetConfigFile(".env")
	v.SetConfigType("env")
	_ = v.ReadInConfig() // Ignore error if .env doesn't exist

	// Read from environment variables
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Bind specific environment variables
	bindEnvVars(v)

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	// Server defaults
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)

	// Database defaults
	v.SetDefault("database.path", "./data/gpu-shopper.db")

	// Provider defaults
	v.SetDefault("providers.vastai.enabled", true)
	v.SetDefault("providers.tensordock.enabled", true)
	v.SetDefault("providers.tensordock.default_image", "ubuntu2404")

	// Inventory defaults
	v.SetDefault("inventory.default_cache_ttl", time.Minute)
	v.SetDefault("inventory.backoff_cache_ttl", 5*time.Minute)
	v.SetDefault("inventory.tensordock_cache_ttl", 30*time.Second) // Shorter TTL for volatile TensorDock inventory

	// Lifecycle defaults
	v.SetDefault("lifecycle.check_interval", time.Minute)
	v.SetDefault("lifecycle.hard_max_hours", 12)
	v.SetDefault("lifecycle.orphan_grace_period", 15*time.Minute)
	v.SetDefault("lifecycle.reconciliation_interval", 5*time.Minute)
	v.SetDefault("lifecycle.startup_sweep_enabled", true)
	v.SetDefault("lifecycle.startup_sweep_timeout", 2*time.Minute)
	v.SetDefault("lifecycle.shutdown_timeout", 60*time.Second)

	// SSH verification defaults
	v.SetDefault("ssh.verify_timeout", 5*time.Minute)
	v.SetDefault("ssh.check_interval", 15*time.Second)

	// Logging defaults
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
}

func bindEnvVars(v *viper.Viper) {
	// Helper to bind and log errors (BindEnv errors are non-fatal but should be logged)
	bindEnv := func(key string, envVar string) {
		if err := v.BindEnv(key, envVar); err != nil {
			slog.Warn("failed to bind environment variable",
				slog.String("key", key),
				slog.String("env_var", envVar),
				slog.String("error", err.Error()))
		}
	}

	// Provider credentials from environment
	bindEnv("providers.vastai.api_key", "VASTAI_API_KEY")
	bindEnv("providers.tensordock.auth_id", "TENSORDOCK_AUTH_ID")
	bindEnv("providers.tensordock.api_token", "TENSORDOCK_API_TOKEN")
	bindEnv("providers.tensordock.default_image", "TENSORDOCK_DEFAULT_IMAGE")

	// Database path
	bindEnv("database.path", "DATABASE_PATH")

	// Server config
	bindEnv("server.host", "SERVER_HOST")
	bindEnv("server.port", "SERVER_PORT")

	// Logging
	bindEnv("logging.level", "LOG_LEVEL")
	bindEnv("logging.format", "LOG_FORMAT")

	// Lifecycle
	bindEnv("lifecycle.deployment_id", "DEPLOYMENT_ID")
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	// Check that at least one provider is configured
	if !c.Providers.VastAI.Enabled && !c.Providers.TensorDock.Enabled {
		return fmt.Errorf("at least one provider must be enabled")
	}

	// Check Vast.ai config if enabled
	if c.Providers.VastAI.Enabled && c.Providers.VastAI.APIKey == "" {
		return fmt.Errorf("VASTAI_API_KEY is required when Vast.ai is enabled")
	}

	// Check TensorDock config if enabled
	if c.Providers.TensorDock.Enabled {
		if c.Providers.TensorDock.AuthID == "" {
			return fmt.Errorf("TENSORDOCK_AUTH_ID is required when TensorDock is enabled")
		}
		if c.Providers.TensorDock.APIToken == "" {
			return fmt.Errorf("TENSORDOCK_API_TOKEN is required when TensorDock is enabled")
		}
	}

	return nil
}
