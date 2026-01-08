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

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/log"
)

// Event types for hooks
const (
	EventTaskBlocked = "task.blocked"
	EventTaskDone    = "task.done"
	EventTaskFailed  = "task.failed"
	EventTaskStarted = "task.started"
)

// Runner executes hooks for task events.
type Runner struct {
	hooksDir string
	logger   *log.Logger
}

// New creates a new hook runner.
// hooksDir is typically ~/.config/task/hooks/
func New(hooksDir string) *Runner {
	return &Runner{
		hooksDir: hooksDir,
		logger:   log.NewWithOptions(os.Stderr, log.Options{Prefix: "hooks"}),
	}
}

// NewSilent creates a hook runner without logging.
func NewSilent(hooksDir string) *Runner {
	return &Runner{
		hooksDir: hooksDir,
		logger:   log.NewWithOptions(os.Stderr, log.Options{Level: log.FatalLevel}),
	}
}

// Run executes hooks for the given event.
// Hooks are scripts in hooksDir named after the event (e.g., task.blocked)
func (r *Runner) Run(event string, task *db.Task, message string) {
	if r.hooksDir == "" {
		return
	}

	// Look for hook script
	hookPath := filepath.Join(r.hooksDir, event)
	if _, err := os.Stat(hookPath); os.IsNotExist(err) {
		return
	}

	// Execute hook with environment variables
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, hookPath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("TASK_ID=%d", task.ID),
		fmt.Sprintf("TASK_TITLE=%s", task.Title),
		fmt.Sprintf("TASK_STATUS=%s", task.Status),
		fmt.Sprintf("TASK_PROJECT=%s", task.Project),
		fmt.Sprintf("TASK_TYPE=%s", task.Type),
		fmt.Sprintf("TASK_PRIORITY=%s", task.Priority),
		fmt.Sprintf("TASK_MESSAGE=%s", message),
		fmt.Sprintf("TASK_EVENT=%s", event),
	)

	// Run in background, don't block
	go func() {
		output, err := cmd.CombinedOutput()
		if err != nil {
			r.logger.Error("Hook failed", "event", event, "error", err, "output", strings.TrimSpace(string(output)))
		} else {
			r.logger.Debug("Hook executed", "event", event)
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
