package hooks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// ActionTimeout bounds how long a user-invoked action may run. Actions are
// synchronous (their output is shown to the user), so this is more generous than
// the fire-and-forget hook timeout.
const ActionTimeout = 60 * time.Second

// ActionEnv builds the environment for a plugin action. task may be nil when an
// action is invoked without a task in context; the TASK_* vars are then omitted.
func ActionEnv(p Plugin, task *db.Task) []string {
	env := os.Environ()
	if task != nil {
		env = append(env,
			fmt.Sprintf("TASK_ID=%d", task.ID),
			fmt.Sprintf("TASK_TITLE=%s", task.Title),
			fmt.Sprintf("TASK_STATUS=%s", task.Status),
			fmt.Sprintf("TASK_PROJECT=%s", task.Project),
			fmt.Sprintf("TASK_TYPE=%s", task.Type),
			fmt.Sprintf("WORKTREE_PATH=%s", task.WorktreePath),
		)
	}
	env = append(env,
		"TASK_PLUGIN_NAME="+p.Name,
		"TASK_PLUGIN_DIR="+p.Dir,
	)
	return env
}

// RunAction runs a plugin action to completion and returns its combined output.
// It is synchronous by design: actions are user-triggered and their result is
// surfaced back to the user (CLI stdout, a TUI banner, etc.).
func RunAction(ctx context.Context, p Plugin, a Action, task *db.Task) ([]byte, error) {
	script := filepath.Join(p.Dir, a.Command)
	if !isExecutableFile(script) {
		return nil, fmt.Errorf("action %q: script %s not found", a.ID, a.Command)
	}

	ctx, cancel := context.WithTimeout(ctx, ActionTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, script)
	cmd.Dir = p.Dir
	cmd.Env = ActionEnv(p, task)
	return cmd.CombinedOutput()
}

// FindAction locates a plugin and action by name/id across the given plugins.
func FindAction(plugins []Plugin, pluginName, actionID string) (Plugin, Action, error) {
	for _, p := range plugins {
		if p.Name != pluginName {
			continue
		}
		if a, ok := p.Action(actionID); ok {
			return p, a, nil
		}
		return Plugin{}, Action{}, fmt.Errorf("plugin %q has no action %q", pluginName, actionID)
	}
	return Plugin{}, Action{}, fmt.Errorf("no plugin named %q", pluginName)
}
