// Package hooks provides a system for executing scripts on task events.
package hooks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/bborn/workflow/internal/db"
)

// Event types for hooks
const (
	EventTaskBlocked  = "task.blocked"
	EventTaskDone     = "task.done"
	EventTaskFailed   = "task.failed"
	EventTaskStarted  = "task.started"
	EventAuthRequired = "task.auth_required" // Executor session needs re-authentication
)

// Runner executes hooks for task events.
type Runner struct {
	hooksDir   string
	pluginsDir string
	plugins    []Plugin
	logger     *log.Logger
}

// New creates a new hook runner.
// hooksDir is typically ~/.config/task/hooks/
func New(hooksDir string) *Runner {
	return newRunner(hooksDir, DefaultPluginsDir(),
		log.NewWithOptions(os.Stderr, log.Options{Prefix: "hooks"}))
}

// NewSilent creates a hook runner without logging.
func NewSilent(hooksDir string) *Runner {
	return newRunner(hooksDir, DefaultPluginsDir(),
		log.NewWithOptions(os.Stderr, log.Options{Level: log.FatalLevel}))
}

// newRunner constructs a Runner and loads any plugins from pluginsDir. It is the
// shared constructor used by New/NewSilent and tests.
func newRunner(hooksDir, pluginsDir string, logger *log.Logger) *Runner {
	r := &Runner{hooksDir: hooksDir, pluginsDir: pluginsDir, logger: logger}
	plugins, warnings := LoadPlugins(pluginsDir)
	for _, w := range warnings {
		logger.Warn("plugin load", "detail", w)
	}
	for _, p := range plugins {
		logger.Debug("plugin loaded", "name", p.Name, "hooks", len(p.Hooks))
	}
	r.plugins = plugins
	return r
}

// Plugins returns the loaded plugins (read-only view for inspection/CLI).
func (r *Runner) Plugins() []Plugin { return r.plugins }

// Run executes every hook registered for the given event: the legacy
// single-script hook in hooksDir (named after the event), plus the matching
// hook from each loaded plugin. All run concurrently in the background.
func (r *Runner) Run(event string, task *db.Task, message string) {
	baseEnv := taskEnv(event, task, message)

	// Legacy single-script hook: ~/.config/task/hooks/<event>
	if r.hooksDir != "" {
		hookPath := filepath.Join(r.hooksDir, event)
		if fi, err := os.Stat(hookPath); err == nil && !fi.IsDir() {
			r.runScript(event, "", hookPath, r.hooksDir, baseEnv)
		}
	}

	// Plugin hooks: fan out to every plugin that handles this event.
	for _, p := range r.plugins {
		script, ok := p.ScriptFor(event)
		if !ok {
			continue
		}
		env := make([]string, 0, len(baseEnv)+2)
		env = append(env, baseEnv...)
		env = append(env,
			fmt.Sprintf("TASK_PLUGIN_NAME=%s", p.Name),
			fmt.Sprintf("TASK_PLUGIN_DIR=%s", p.Dir),
		)
		r.runScript(event, p.Name, script, p.Dir, env)
	}
}

// taskEnv builds the environment shared by every hook for an event.
func taskEnv(event string, task *db.Task, message string) []string {
	return append(os.Environ(),
		fmt.Sprintf("TASK_ID=%d", task.ID),
		fmt.Sprintf("TASK_TITLE=%s", task.Title),
		fmt.Sprintf("TASK_STATUS=%s", task.Status),
		fmt.Sprintf("TASK_PROJECT=%s", task.Project),
		fmt.Sprintf("TASK_TYPE=%s", task.Type),
		fmt.Sprintf("TASK_MESSAGE=%s", message),
		fmt.Sprintf("TASK_EVENT=%s", event),
		fmt.Sprintf("WORKTREE_PATH=%s", task.WorktreePath),
	)
}

// runScript launches a hook script in the background. The 30s timeout is owned
// by the goroutine (not the caller) so the context isn't cancelled the instant
// Run returns — which would otherwise kill the hook before it could do anything.
// plugin is "" for the legacy hook, or the plugin name for a plugin hook.
func (r *Runner) runScript(event, plugin, scriptPath, workDir string, env []string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, scriptPath)
		cmd.Dir = workDir
		cmd.Env = env

		output, err := cmd.CombinedOutput()
		if err != nil {
			r.logger.Error("Hook failed", "event", event, "plugin", plugin, "error", err,
				"output", strings.TrimSpace(string(output)))
		} else {
			r.logger.Debug("Hook executed", "event", event, "plugin", plugin)
		}
	}()
}

// OnStatusChange triggers appropriate hooks based on status transition.
func (r *Runner) OnStatusChange(task *db.Task, newStatus, message string) {
	switch newStatus {
	case db.StatusQueued, db.StatusProcessing:
		r.Run(EventTaskStarted, task, message)
	case db.StatusDone:
		r.Run(EventTaskDone, task, message)
	case db.StatusBlocked:
		// Differentiate between blocked (needs input) and failed
		if strings.Contains(strings.ToLower(message), "fail") ||
			strings.Contains(strings.ToLower(message), "error") {
			r.Run(EventTaskFailed, task, message)
		} else {
			r.Run(EventTaskBlocked, task, message)
		}
	}
}

// EnsureHooksDir creates the hooks directory if it doesn't exist.
func EnsureHooksDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	hooksDir := filepath.Join(configDir, "task", "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return "", err
	}

	return hooksDir, nil
}

// DefaultHooksDir returns the default hooks directory path.
func DefaultHooksDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "task", "hooks")
}
