package executor

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/hooks"
)

// Profile routing gives a plugin the last word on which Claude account a task
// runs under, at the only moment where that word is still worth anything: after
// the task is cleared to run, before its command is built.
//
// Everything downstream already supports this — Task.ClaudeConfigDir has always
// been the per-task profile lever, honored identically by the daemon's command
// builder and the TUI's. What was missing was anyone to set it automatically.
// Routing fills that in: it stamps the column and lets the existing machinery
// carry the decision the rest of the way, so there is no second code path for a
// routed task and no chance of the two builders disagreeing about which profile
// is in play.
//
// Two rules keep it from getting in the way:
//
//   - An explicit choice always wins. A task that already names a config dir —
//     set by hand, by a workflow step, or by an earlier routing pass — is left
//     alone. Routing fills a vacuum; it does not overrule a person.
//   - Silence means "carry on". No router installed, a script that fails, times
//     out, or prints nothing: the task spawns exactly as it would have before
//     any of this existed.

// routeHoldLog remembers the last hold reason logged per task, so a task parked
// behind exhausted profiles writes one log line rather than one per daemon tick.
var routeHoldLog sync.Map // taskID -> last reason written

// routeTask consults the task.route plugin hook and applies its decision.
//
// It returns false only when a router asked to hold the task — every other
// outcome, including every kind of failure, returns true and lets the spawn
// proceed. allowHold is false on the manual "run this now" path: a person who
// explicitly started a task has already made the call, and silently refusing
// would look like the button was broken.
func (e *Executor) routeTask(ctx context.Context, task *db.Task, allowHold bool) bool {
	if task == nil || e.hooks == nil {
		return true
	}
	// CLAUDE_CONFIG_DIR is a Claude concept; a codex or gemini task has no
	// profile to route between.
	if task.Executor != "" && task.Executor != db.ExecutorClaude {
		return true
	}
	if strings.TrimSpace(task.ClaudeConfigDir) != "" {
		return true
	}
	// A project that names its own config dir has already chosen a profile, and
	// that choice is load-bearing: a config dir carries the account's MCP
	// connectors and their OAuth logins, which are per-profile and cannot be
	// shared. Routing an influencekit task onto a personal profile doesn't error
	// — it silently runs without the Linear/InfluenceKit servers it needs, and
	// the agent works around the gap. So pinning a project is also how you opt it
	// out of routing.
	if project := e.projectConfigDir(task.Project); project != "" {
		return true
	}
	if !e.hooks.HandlesRoute() {
		return true
	}

	decision := e.hooks.Route(ctx, task)
	if decision.Empty() {
		routeHoldLog.Delete(task.ID)
		return true
	}

	if decision.Hold && allowHold {
		e.noteRouteHold(task, decision)
		return false
	}
	routeHoldLog.Delete(task.ID)

	dir := strings.TrimSpace(decision.ClaudeConfigDir)
	if dir == "" {
		return true
	}
	resolved := ResolveClaudeConfigDir(dir)
	if err := e.db.UpdateTaskClaudeConfigDir(task.ID, resolved); err != nil {
		// The write is what makes the decision visible to the TUI and to a
		// later resume. If it fails, don't apply the route in memory either —
		// a task whose spawned profile disagrees with its recorded one is the
		// exact confusion this feature is supposed to remove.
		e.logger.Error("Failed to record routed Claude profile", "id", task.ID, "dir", resolved, "error", err)
		return true
	}
	task.ClaudeConfigDir = resolved

	msg := fmt.Sprintf("Routed to Claude profile %s (by plugin %q)", resolved, decision.Plugin)
	if decision.Reason != "" {
		msg += ": " + decision.Reason
	}
	e.logger.Info("Routed task to Claude profile", "id", task.ID, "dir", resolved, "plugin", decision.Plugin)
	e.logLine(task.ID, "system", msg)
	return true
}

// noteRouteHold records a hold, writing to the task log only when the reason
// changes. The daemon reconsiders a queued task every tick, so an unconditional
// log line would bury the task's real history under thousands of repeats of
// "waiting for headroom".
func (e *Executor) noteRouteHold(task *db.Task, decision hooks.RouteDecision) {
	reason := decision.Reason
	if reason == "" {
		reason = "no Claude profile has headroom right now"
	}
	e.logger.Info("Holding task: no Claude profile available", "id", task.ID, "plugin", decision.Plugin, "reason", reason)

	if prev, ok := routeHoldLog.Load(task.ID); ok && prev == reason {
		return
	}
	routeHoldLog.Store(task.ID, reason)
	e.logLine(task.ID, "system", fmt.Sprintf("Waiting to start — %s (plugin %q). Will retry automatically.", reason, decision.Plugin))
}

// projectConfigDir returns the config dir a project pins, or "" when it uses the
// default. Errors read as "not pinned": the caller's next step is to route, and
// a DB hiccup shouldn't be the thing that decides a profile.
func (e *Executor) projectConfigDir(project string) string {
	if project == "" {
		return ""
	}
	p, err := e.db.GetProjectByName(project)
	if err != nil || p == nil {
		return ""
	}
	return strings.TrimSpace(p.ClaudeConfigDir)
}
