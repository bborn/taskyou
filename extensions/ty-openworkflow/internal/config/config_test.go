package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.DefaultAdapter != "exec" {
		t.Errorf("expected default adapter 'exec', got '%s'", cfg.DefaultAdapter)
	}

	if !cfg.Adapters.Exec.Enabled {
		t.Error("exec adapter should be enabled by default")
	}

	if cfg.Adapters.Docker.Enabled {
		t.Error("docker adapter should be disabled by default")
	}

	if cfg.Adapters.Cloudflare.Enabled {
		t.Error("cloudflare adapter should be disabled by default")
	}

	if cfg.TaskYou.CLI != "ty" {
		t.Errorf("expected ty CLI 'ty', got '%s'", cfg.TaskYou.CLI)
	}

	if cfg.PollInterval != 30*time.Second {
		t.Errorf("expected poll interval 30s, got %v", cfg.PollInterval)
	}
}

func TestLoadDefault(t *testing.T) {
	// Load from non-existent file should return defaults
	cfg, err := Load("/non/existent/path/config.yaml")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if cfg.DefaultAdapter != "exec" {
		t.Errorf("expected default adapter 'exec', got '%s'", cfg.DefaultAdapter)
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yaml")

	cfg := DefaultConfig()
	cfg.DefaultAdapter = "docker"
	cfg.Adapters.Docker.Enabled = true
	cfg.Adapters.Docker.Network = "my-network"
	cfg.TaskYou.Project = "my-project"

	// Save
	if err := Save(cfg, path); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("config file was not created")
	}

	// Load
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if loaded.DefaultAdapter != "docker" {
		t.Errorf("expected default adapter 'docker', got '%s'", loaded.DefaultAdapter)
	}
	if !loaded.Adapters.Docker.Enabled {
		t.Error("docker adapter should be enabled")
	}
	if loaded.Adapters.Docker.Network != "my-network" {
		t.Errorf("expected network 'my-network', got '%s'", loaded.Adapters.Docker.Network)
	}
	if loaded.TaskYou.Project != "my-project" {
		t.Errorf("expected project 'my-project', got '%s'", loaded.TaskYou.Project)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		expectErr bool
	}{
		{
			name:      "valid default config",
			cfg:       DefaultConfig(),
			expectErr: false,
		},
		{
			name: "no adapters enabled",
			cfg: &Config{
				Adapters: AdaptersConfig{
					Exec:       ExecAdapterConfig{Enabled: false},
					Docker:     DockerAdapterConfig{Enabled: false},
					Cloudflare: CloudflareAdapterConfig{Enabled: false},
				},
			},
			expectErr: true,
		},
		{
			name: "cloudflare without account_id",
			cfg: &Config{
				Adapters: AdaptersConfig{
					Cloudflare: CloudflareAdapterConfig{
						Enabled:   true,
						AccountID: "",
						APIToken:  "token",
					},
				},
			},
			expectErr: true,
		},
		{
			name: "cloudflare without api_token",
			cfg: &Config{
				Adapters: AdaptersConfig{
					Cloudflare: CloudflareAdapterConfig{
						Enabled:   true,
						AccountID: "account",
						APIToken:  "",
					},
				},
			},
			expectErr: true,
		},
		{
			name: "valid cloudflare config",
			cfg: &Config{
				Adapters: AdaptersConfig{
					Cloudflare: CloudflareAdapterConfig{
						Enabled:   true,
						AccountID: "account",
						APIToken:  "token",
					},
				},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.expectErr && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestWebhookURL(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *Config
		expected string
	}{
		{
			name: "webhook disabled",
			cfg: &Config{
				Webhook: WebhookConfig{
					Enabled: false,
				},
			},
			expected: "",
		},
		{
			name: "external URL set",
			cfg: &Config{
				Webhook: WebhookConfig{
					Enabled:     true,
					ExternalURL: "https://example.com/webhook",
				},
			},
			expected: "https://example.com/webhook",
		},
		{
			name: "local webhook",
			cfg: &Config{
				Webhook: WebhookConfig{
					Enabled: true,
					Host:    "localhost",
					Port:    8765,
					Path:    "/webhook",
				},
			},
			expected: "http://localhost:8765/webhook",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tt.cfg.WebhookURL()
			if url != tt.expected {
				t.Errorf("expected URL '%s', got '%s'", tt.expected, url)
			}
		})
	}
}

func TestConfigPath(t *testing.T) {
	path := ConfigPath()
	if path == "" {
		t.Error("config path should not be empty")
	}

	// Should contain ty-openworkflow
	if filepath.Base(filepath.Dir(path)) != "ty-openworkflow" {
		t.Errorf("config path should be in ty-openworkflow directory, got %s", path)
	}
}
