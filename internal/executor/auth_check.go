package executor

import (
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/hooks"
)

// authPattern maps a distinctive substring found in an executor's terminal
// output to a human-readable explanation of why the session needs attention.
type authPattern struct {
	needle string // lowercased substring to search for in pane content
	reason string // human-readable explanation surfaced to the user
}

// authRequiredPatterns are phrases Claude Code prints when its login/cloud
// session has expired or is otherwise unauthenticated. Multi-word phrases are
// used deliberately to avoid false positives from ordinary task output (e.g. a
// diff that happens to mention "login").
var authRequiredPatterns = []authPattern{
	{"please run /login", "Claude session expired — run /login to re-authenticate"},
	{"run `/login`", "Claude session expired — run /login to re-authenticate"},
	{"oauth token has expired", "Claude OAuth token expired — run /login to re-authenticate"},
	{"oauth token expired", "Claude OAuth token expired — run /login to re-authenticate"},
	{"session has expired", "Claude session expired — run /login to re-authenticate"},
	{"invalid api key", "Claude reported an invalid API key — re-authentication required"},
	{"select login method", "Claude is showing the login screen — re-authentication required"},
	{"log in with your claude account", "Claude is showing the login screen — re-authentication required"},
	{"you are not logged in", "Claude is not logged in — run /login to re-authenticate"},
	{"authentication_error", "Claude returned an authentication error — re-authentication required"},
}

// DetectAuthPrompt scans captured pane content for signs that the executor's
// session has been logged out. It returns a human-readable reason and true when
// a known re-authentication prompt is present.
func DetectAuthPrompt(content string) (string, bool) {
	if content == "" {
		return "", false
	}
	lower := strings.ToLower(content)
	for _, p := range authRequiredPatterns {
		if strings.Contains(lower, p.needle) {
			return p.reason, true
		}
	}
	return "", false
}

// checkAuthStuckTasks scans tasks that should be executing and detects ones that
// are silently stalled because their executor session is logged out. Detected
// tasks are moved to blocked (surfacing them on the board and firing the
// task.blocked hook) and additionally fire the dedicated task.auth_required
// event/hook so re-authentication can be notified separately from generic input.
func (e *Executor) checkAuthStuckTasks() {
	tasks, err := e.db.ListTasks(db.ListTasksOptions{Status: db.StatusProcessing, Limit: 100})
	if err != nil {
		return
	}

	for _, task := range tasks {
		// Capture the executor pane. Prefer the stable pane ID, which survives
		// tmux join-pane moving the pane between the daemon and task-ui sessions;
		// fall back to the daemon window target.
		captureTarget := task.ClaudePaneID
		if captureTarget == "" {
			captureTarget = TmuxSessionName(task.ID)
		}

		content := CapturePaneContent(captureTarget, 25)
		reason, stuck := DetectAuthPrompt(content)
		if !stuck {
			continue
		}

		e.logger.Info("Detected logged-out executor session on processing task",
			"task", task.ID, "reason", reason)
		e.logLine(task.ID, "error", reason)

		// Move to blocked so it surfaces on the board and fires task.blocked.
		if err := e.updateStatus(task.ID, db.StatusBlocked); err != nil {
			e.logger.Error("Failed to block auth-stuck task", "task", task.ID, "error", err)
		}

		// Re-fetch so hooks/events see the updated status, then fire the
		// dedicated re-authentication event and hook.
		updated, gerr := e.db.GetTask(task.ID)
		if gerr != nil || updated == nil {
			updated = task
		}
		e.events.EmitTaskAuthRequired(updated, reason)
		e.hooks.Run(hooks.EventAuthRequired, updated, reason)
	}
}
