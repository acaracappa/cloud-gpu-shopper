package config

import (
	"fmt"
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
	Agent     AgentConfig     `mapstructure:"agent"`
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
	AuthID   string `mapstructure:"auth_id"`
	APIToken string `mapstructure:"api_token"`
	Enabled  bool   `mapstructure:"enabled"`
}

// InventoryConfig holds inventory cache configuration
type InventoryConfig struct {
	DefaultCacheTTL time.Duration `mapstructure:"default_cache_ttl"`
	BackoffCacheTTL time.Duration `mapstructure:"backoff_cache_ttl"`
}

// LifecycleConfig holds lifecycle management configuration
type LifecycleConfig struct {
	CheckInterval          time.Duration `mapstructure:"check_interval"`
	HardMaxHours           int           `mapstructure:"hard_max_hours"`
	DefaultIdleThreshold   int           `mapstructure:"default_idle_threshold"`
	HeartbeatTimeout       time.Duration `mapstructure:"heartbeat_timeout"`
	OrphanGracePeriod      time.Duration `mapstructure:"orphan_grace_period"`
	ReconciliationInterval time.Duration `mapstructure:"reconciliation_interval"`
}

// AgentConfig holds node agent configuration
type AgentConfig struct {
	DockerImage       string        `mapstructure:"docker_image"`
	DefaultPort       int           `mapstructure:"default_port"`
	SelfDestructGrace time.Duration `mapstructure:"self_destruct_grace"`
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

	// Inventory defaults
	v.SetDefault("inventory.default_cache_ttl", time.Minute)
	v.SetDefault("inventory.backoff_cache_ttl", 5*time.Minute)

	// Lifecycle defaults
	v.SetDefault("lifecycle.check_interval", time.Minute)
	v.SetDefault("lifecycle.hard_max_hours", 12)
	v.SetDefault("lifecycle.default_idle_threshold", 0)
	v.SetDefault("lifecycle.heartbeat_timeout", 5*time.Minute)
	v.SetDefault("lifecycle.orphan_grace_period", 15*time.Minute)
	v.SetDefault("lifecycle.reconciliation_interval", 5*time.Minute)

	// Agent defaults
	v.SetDefault("agent.docker_image", "ghcr.io/cloud-gpu-shopper/agent:latest")
	v.SetDefault("agent.default_port", 8081)
	v.SetDefault("agent.self_destruct_grace", 30*time.Minute)

	// Logging defaults
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
}

func bindEnvVars(v *viper.Viper) {
	// Provider credentials from environment
	v.BindEnv("providers.vastai.api_key", "VASTAI_API_KEY")
	v.BindEnv("providers.tensordock.auth_id", "TENSORDOCK_AUTH_ID")
	v.BindEnv("providers.tensordock.api_token", "TENSORDOCK_API_TOKEN")

	// Database path
	v.BindEnv("database.path", "DATABASE_PATH")

	// Server config
	v.BindEnv("server.host", "SERVER_HOST")
	v.BindEnv("server.port", "SERVER_PORT")

	// Logging
	v.BindEnv("logging.level", "LOG_LEVEL")
	v.BindEnv("logging.format", "LOG_FORMAT")
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
