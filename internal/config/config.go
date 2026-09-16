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
	SettingProjectsDir      = "projects_dir"
	SettingTheme            = "theme"
	SettingDetailPaneHeight = "detail_pane_height"
	// SettingShellPaneWidth is the fallback shell pane width, used for tasks
	// that have never been resized. A task's own width lives under
	// ShellPaneWidthKey; see that function.
	SettingShellPaneWidth        = "shell_pane_width"
	SettingShellPaneHidden       = "shell_pane_hidden"
	SettingIdleSuspendTimeout    = "idle_suspend_timeout"
	SettingServerURL             = "server_url"
	SettingWorktreeCleanupMaxAge = "worktree_cleanup_max_age"
	// SettingTrashRetention is how long a soft-deleted (trashed) task stays
	// recoverable before the daemon sweep hard-deletes it. Value is a Go duration
	// string (e.g. "336h"); "0" or "disabled" turns the sweep off (trash kept
	// forever). See DefaultTrashRetention.
	SettingTrashRetention = "trash_retention"
	// SettingHTTPAPIPort is the port the daemon-hosted HTTP API listens on.
	SettingHTTPAPIPort = "http_api_port"
	// SettingHTTPAPIDisabled, when "true", stops the daemon from hosting the
	// HTTP API (for headless/security-sensitive boxes). The API is on by default.
	SettingHTTPAPIDisabled = "http_api_disabled"

	// SettingReapBlockedIdle is how long a task must show NO activity before
	// `ty sessions cleanup` will reap the side processes (dev servers, watchers)
	// running out of its worktree. Deliberately much longer than the done-task
	// grace: in ty, "blocked" usually means "waiting for a human to come look",
	// not "dead". Go duration string; "0"/"disabled" never reaps on staleness.
	// See reaper.DefaultBlockedIdle for the default and its rationale.
	SettingReapBlockedIdle = "reap_blocked_idle"

	// SettingReapOrphanMinAge is the minimum age for the no-worktree orphan
	// heuristic (a known dev server reparented to init with no terminal, which
	// nothing can tie back to a task). Go duration string.
	SettingReapOrphanMinAge = "reap_orphan_min_age"

	// SettingReapOrphanDevServers, when "false", disables that no-worktree
	// heuristic entirely, leaving the sweep to only touch processes it can map
	// to a task worktree. Enabled by default.
	SettingReapOrphanDevServers = "reap_orphan_dev_servers"

	// SettingBoardDisplayMode is how the board draws itself: BoardDisplayList for
	// one flat line per task, anything else for the kanban columns.
	SettingBoardDisplayMode = "board_display_mode"
	// SettingBoardFilter is the filter query the board had when it was last
	// closed, restored on launch so a filtered view actually persists.
	SettingBoardFilter = "board_filter"
	// SettingBoardView is the name of the saved view that filter came from, ""
	// when the filter was typed ad hoc. Only used to label the filter bar.
	SettingBoardView = "board_view"
	// SettingListGroupBy and SettingListSort are how the list view is arranged:
	// which field it breaks into sections on, and how tasks order inside a
	// section. See ui.ListOptions for the accepted values. Row height is not a
	// setting — see the note in listopts.go.
	SettingListGroupBy = "list_group_by"
	SettingListSort    = "list_sort"
)

// Board display modes for SettingBoardDisplayMode.
const (
	BoardDisplayBoard = "board"
	BoardDisplayList  = "list"
)

// ShellPaneWidthKey is the settings key holding one task's shell pane width.
// Widths are per task: dragging the agent/shell split in one task must not move
// it in every other task. SettingShellPaneWidth remains the fallback for tasks
// with no width of their own.
func ShellPaneWidthKey(taskID int64) string {
	return db.TaskSettingKey(SettingShellPaneWidth, taskID)
}

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
