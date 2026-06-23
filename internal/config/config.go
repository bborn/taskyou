// Package config provides application configuration from database.
package config

import (
	"os"
	"path/filepath"

	"github.com/bborn/workflow/internal/db"
)

// Config holds application configuration loaded from database.
type Config struct {
	db          *db.DB
	ProjectsDir string
}

// Setting keys
const (
	SettingProjectsDir           = "projects_dir"
	SettingTheme                 = "theme"
	SettingDetailPaneHeight      = "detail_pane_height"
	SettingShellPaneWidth        = "shell_pane_width"
	SettingShellPaneHidden       = "shell_pane_hidden"
	SettingIdleSuspendTimeout    = "idle_suspend_timeout"
	SettingServerURL             = "server_url"
	SettingWorktreeCleanupMaxAge = "worktree_cleanup_max_age"
	// SettingHTTPAPIPort is the port the daemon-hosted HTTP API listens on.
	SettingHTTPAPIPort = "http_api_port"
	// SettingHTTPAPIDisabled, when "true", stops the daemon from hosting the
	// HTTP API (for headless/security-sensitive boxes). The API is on by default.
	SettingHTTPAPIDisabled = "http_api_disabled"

	// Push notification settings. OFF by default — nothing is sent unless
	// SettingNotifyEnabled is "true" AND a provider is configured. See
	// internal/notify for the delivery logic.

	// SettingNotifyEnabled, when "true", turns on push notifications for task
	// lifecycle events (blocked/needs-input, auth-required, completed, failed).
	SettingNotifyEnabled = "notify_enabled"
	// SettingNotifyBaseURL is the externally reachable base URL of the daemon
	// HTTP API, used to build one-tap action links in notifications (e.g.
	// "https://ty.my-tailnet.ts.net:8080"). Falls back to http://localhost:<port>.
	SettingNotifyBaseURL = "notify_base_url"
	// SettingNotifyUnblockReply is the canned reply sent to a blocked task when
	// the user taps the one-tap action button. Defaults to "continue".
	SettingNotifyUnblockReply = "notify_unblock_reply"

	// ntfy (https://ntfy.sh) provider.
	// SettingNtfyServer is the ntfy server base URL (default https://ntfy.sh).
	SettingNtfyServer = "notify_ntfy_server"
	// SettingNtfyTopic is the ntfy topic to publish to. A bare topic name or a
	// full topic URL. Empty disables the ntfy provider.
	SettingNtfyTopic = "notify_ntfy_topic"
	// SettingNtfyToken is an optional ntfy access token for protected topics.
	// Treated as a secret (hidden from `ty settings` and the settings API).
	SettingNtfyToken = "notify_ntfy_token" //nolint:gosec // G101: this is a settings-key name, not a credential

	// Telegram bot provider.
	// SettingTelegramToken is the Telegram bot token. Secret. Empty disables it.
	SettingTelegramToken = "notify_telegram_token" //nolint:gosec // G101: this is a settings-key name, not a credential
	// SettingTelegramChatID is the Telegram chat ID to deliver messages to.
	SettingTelegramChatID = "notify_telegram_chat_id"
)

// DefaultNtfyServer is the ntfy server used when SettingNtfyServer is unset.
const DefaultNtfyServer = "https://ntfy.sh"

// DefaultUnblockReply is the canned reply sent on a one-tap unblock action.
const DefaultUnblockReply = "continue"

// DefaultHTTPAPIPort is the port the daemon-hosted HTTP API binds by default.
// Matches the standalone `ty serve` default so existing clients (ty-web, the
// ty-chrome extension) keep working without reconfiguration.
const DefaultHTTPAPIPort = 8080

// DefaultServerURL is the default base URL for opening tasks in the browser.
const DefaultServerURL = "http://localhost"

// New creates a config from database.
func New(database *db.DB) *Config {
	cfg := &Config{db: database}
	cfg.load()
	return cfg
}

func (c *Config) load() {
	// Load projects_dir or use default
	if dir, err := c.db.GetSetting(SettingProjectsDir); err == nil && dir != "" {
		c.ProjectsDir = expandPath(dir)
	} else {
		home, _ := os.UserHomeDir()
		c.ProjectsDir = filepath.Join(home, "Projects")
	}
}

// GetProjectDir returns the directory for a project name.
func (c *Config) GetProjectDir(project string) string {
	if project == "" {
		return c.ProjectsDir
	}

	// Look up project in database
	p, err := c.db.GetProjectByName(project)
	if err == nil && p != nil {
		return expandPath(p.Path)
	}

	// Default: projects_dir/project
	return filepath.Join(c.ProjectsDir, project)
}

// ProjectUsesWorktrees returns whether a project uses git worktrees for task isolation.
// Returns true by default (for backward compatibility and unknown projects).
func (c *Config) ProjectUsesWorktrees(project string) bool {
	if project == "" {
		return true
	}
	p, err := c.db.GetProjectByName(project)
	if err == nil && p != nil {
		return p.UsesWorktrees()
	}
	return true
}

// SetProjectsDir sets the default projects directory.
func (c *Config) SetProjectsDir(dir string) error {
	if err := c.db.SetSetting(SettingProjectsDir, dir); err != nil {
		return err
	}
	c.ProjectsDir = expandPath(dir)
	return nil
}

func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[1:])
	}
	return path
}
