package executor

import (
	"context"
	"fmt"
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
	{"please run grok login", "Grok session expired — run grok login to re-authenticate"},
	{"run `grok login`", "Grok session expired — run grok login to re-authenticate"},
	{"sign in to grok", "Grok is showing the login screen — re-authentication required"},
	{"please run agent login", "Cursor session expired — run agent login to re-authenticate"},
	{"run `agent login`", "Cursor session expired — run agent login to re-authenticate"},
	{"run cursor-agent login", "Cursor session expired — run agent login to re-authenticate"},
	{"sign in to cursor", "Cursor is showing the login screen — re-authentication required"},
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
		content, ok := e.captureExecutorPane(task)
		if !ok {
			continue
		}
		reason, stuck := DetectAuthPrompt(content)
		if stuck {
			if task.PlacementTarget != "" {
				reason = fmt.Sprintf("%s (on %s)", reason, task.PlacementTarget)
			}
			e.reportAuthRequired(task, reason)
			continue
		}

		// A dialog waiting on a keystroke stalls a task exactly as a login
		// prompt does, and this sweep is the only thing that looks at a LOCAL
		// task's screen — the idle poll that would eventually park it is
		// remote-only by design. Without this, a local task sitting at an
		// onboarding or trust prompt stays "processing" until somebody thinks
		// to attach to the pane and look.
		if reason, blocked := DetectBlockingPrompt(content); blocked {
			if task.PlacementTarget != "" {
				reason = fmt.Sprintf("%s (on %s)", reason, task.PlacementTarget)
			}
			e.reportBlockingPrompt(task, reason)
		}
	}
}

// reportBlockingPrompt parks a task whose executor is waiting on a question and
// puts the question itself where a human will see it.
//
// This deliberately does not fire task.auth_required: nothing is wrong with the
// credentials, and an operator with a re-authentication routine wired to that
// hook should not have it woken by an onboarding dialog. It is ordinary blocked
// work that needs an answer, so it fires task.blocked and says what to answer.
func (e *Executor) reportBlockingPrompt(task *db.Task, reason string) {
	e.logger.Info("Detected executor waiting on a dialog",
		"task", task.ID, "host", task.PlacementTarget, "reason", reason)
	e.logLine(task.ID, "error", reason)

	if err := e.updateStatus(task.ID, db.StatusBlocked); err != nil {
		e.logger.Error("Failed to block prompt-stuck task", "task", task.ID, "error", err)
	}

	updated, gerr := e.db.GetTask(task.ID)
	if gerr != nil || updated == nil {
		updated = task
	}
	e.events.EmitTaskBlocked(updated, reason)
	e.hooks.Run(hooks.EventTaskBlocked, updated, reason)
}

// captureExecutorPane reads what a task's executor is currently showing,
// wherever that executor is running.
//
// A remotely placed task's window lives in a tmux server on ANOTHER machine, so
// the local capture this used to do could only ever come back empty — which
// reads as "no login prompt here" and is why a logged-out fleet host produced a
// task that hung and then parked with nothing saying why. Which executor it is
// does not enter into it: the capture is the same, and the patterns applied to
// it are per-executor already.
func (e *Executor) captureExecutorPane(task *db.Task) (string, bool) {
	if task.PlacementTarget != "" {
		if task.DaemonSession == "" {
			return "", false
		}
		target := task.DaemonSession + ":" + TmuxWindowName(task.ID)
		return capturePaneRemote(context.Background(), target, task.PlacementTarget)
	}

	// Prefer the stable pane ID, which survives tmux join-pane moving the pane
	// between the daemon and task-ui sessions; fall back to the daemon window
	// target.
	captureTarget := task.ClaudePaneID
	if captureTarget == "" {
		captureTarget = TmuxSessionName(task.ID)
	}
	content := CapturePaneContent(captureTarget, 25)
	return content, content != ""
}

// reportAuthRequired parks a task whose executor is sitting at a login prompt
// and says so everywhere a human might be looking.
//
// Both detectors end here — the periodic sweep over processing tasks, and the
// remote poll reading the screen it already captured — so a logged-out session
// is announced identically however it was noticed.
func (e *Executor) reportAuthRequired(task *db.Task, reason string) {
	e.logger.Info("Detected logged-out executor session on processing task",
		"task", task.ID, "host", task.PlacementTarget, "reason", reason)
	e.logLine(task.ID, "error", reason)

	// Move to blocked so it surfaces on the board and fires task.blocked.
	if err := e.updateStatus(task.ID, db.StatusBlocked); err != nil {
		e.logger.Error("Failed to block auth-stuck task", "task", task.ID, "error", err)
	}

	// Re-fetch so hooks/events see the updated status, then fire the dedicated
	// re-authentication event and hook.
	updated, gerr := e.db.GetTask(task.ID)
	if gerr != nil || updated == nil {
		updated = task
	}
	e.events.EmitTaskAuthRequired(updated, reason)
	e.hooks.Run(hooks.EventAuthRequired, updated, reason)
}
