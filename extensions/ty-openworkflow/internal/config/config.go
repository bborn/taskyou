// Package config handles ty-openworkflow configuration.
package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the complete configuration.
type Config struct {
	// DataDir is where state and workflow files are stored
	DataDir string `yaml:"data_dir"`

	// DefaultAdapter is the default compute adapter to use
	DefaultAdapter string `yaml:"default_adapter"`

	// Adapters holds configuration for each compute adapter
	Adapters AdaptersConfig `yaml:"adapters"`

	// Webhook configuration for receiving callbacks
	Webhook WebhookConfig `yaml:"webhook"`

	// TaskYou configuration
	TaskYou TaskYouConfig `yaml:"taskyou"`

	// Polling configuration
	PollInterval time.Duration `yaml:"poll_interval"`
}

// AdaptersConfig holds configuration for all adapters.
type AdaptersConfig struct {
	Exec       ExecAdapterConfig       `yaml:"exec"`
	Docker     DockerAdapterConfig     `yaml:"docker"`
	Cloudflare CloudflareAdapterConfig `yaml:"cloudflare"`
}

// ExecAdapterConfig holds local exec adapter configuration.
type ExecAdapterConfig struct {
	Enabled bool   `yaml:"enabled"`
	WorkDir string `yaml:"work_dir"`
}

// DockerAdapterConfig holds Docker adapter configuration.
type DockerAdapterConfig struct {
	Enabled bool   `yaml:"enabled"`
	WorkDir string `yaml:"work_dir"`
	Network string `yaml:"network"`
}

// CloudflareAdapterConfig holds Cloudflare Workers configuration.
type CloudflareAdapterConfig struct {
	Enabled     bool   `yaml:"enabled"`
	AccountID   string `yaml:"account_id"`
	APIToken    string `yaml:"api_token"`
	APITokenCmd string `yaml:"api_token_cmd"` // Command to retrieve API token
	Namespace   string `yaml:"namespace"`     // KV namespace for state
}

// WebhookConfig holds webhook server configuration.
type WebhookConfig struct {
	Enabled bool   `yaml:"enabled"`
	Port    int    `yaml:"port"`
	Host    string `yaml:"host"`
	Path    string `yaml:"path"`
	// External URL for compute platforms to callback to
	ExternalURL string `yaml:"external_url"`
}

// TaskYouConfig holds TaskYou integration configuration.
type TaskYouConfig struct {
	// Path to ty CLI binary
	CLI string `yaml:"cli"`
	// Default project for tasks
	Project string `yaml:"project"`
	// Whether to auto-create tasks for workflow runs
	AutoCreateTasks bool `yaml:"auto_create_tasks"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, ".config", "ty-openworkflow")

	return &Config{
		DataDir:        dataDir,
		DefaultAdapter: "exec",
		Adapters: AdaptersConfig{
			Exec: ExecAdapterConfig{
				Enabled: true,
				WorkDir: filepath.Join(dataDir, "exec"),
			},
			Docker: DockerAdapterConfig{
				Enabled: false,
				WorkDir: filepath.Join(dataDir, "docker"),
			},
			Cloudflare: CloudflareAdapterConfig{
				Enabled: false,
			},
		},
		Webhook: WebhookConfig{
			Enabled: false,
			Port:    8765,
			Host:    "localhost",
			Path:    "/webhook",
		},
		TaskYou: TaskYouConfig{
			CLI:             "ty",
			AutoCreateTasks: true,
		},
		PollInterval: 30 * time.Second,
	}
}

// ConfigPath returns the default configuration file path.
func ConfigPath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".config", "ty-openworkflow", "config.yaml")
}

// Load loads configuration from the given path.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // Use defaults if no config file
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Resolve API tokens from commands if specified
	if cfg.Adapters.Cloudflare.APITokenCmd != "" && cfg.Adapters.Cloudflare.APIToken == "" {
		token, err := runCommand(cfg.Adapters.Cloudflare.APITokenCmd)
		if err != nil {
			return nil, fmt.Errorf("get cloudflare api token: %w", err)
		}
		cfg.Adapters.Cloudflare.APIToken = token
	}

	return cfg, nil
}

// Save saves configuration to the given path.
func Save(cfg *Config, path string) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// runCommand executes a shell command and returns its output.
func runCommand(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	// Check that at least one adapter is enabled
	hasAdapter := c.Adapters.Exec.Enabled || c.Adapters.Docker.Enabled || c.Adapters.Cloudflare.Enabled
	if !hasAdapter {
		return fmt.Errorf("at least one compute adapter must be enabled")
	}

	// Validate Cloudflare config if enabled
	if c.Adapters.Cloudflare.Enabled {
		if c.Adapters.Cloudflare.AccountID == "" {
			return fmt.Errorf("cloudflare account_id is required when cloudflare adapter is enabled")
		}
		if c.Adapters.Cloudflare.APIToken == "" && c.Adapters.Cloudflare.APITokenCmd == "" {
			return fmt.Errorf("cloudflare api_token or api_token_cmd is required when cloudflare adapter is enabled")
		}
	}

	return nil
}

// WebhookURL returns the full webhook URL for compute platforms to call back to.
func (c *Config) WebhookURL() string {
	if c.Webhook.ExternalURL != "" {
		return c.Webhook.ExternalURL
	}
	if !c.Webhook.Enabled {
		return ""
	}
	return fmt.Sprintf("http://%s:%d%s", c.Webhook.Host, c.Webhook.Port, c.Webhook.Path)
}
