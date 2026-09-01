package hooks

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// EventTaskRoute is fired immediately before a task is spawned, and is the one
// event a plugin can *answer* rather than merely observe.
//
// Every other hook is a notification: it fires after the fact, runs detached,
// and nothing waits for it. A router has to be the opposite — the decision it
// makes (which Claude profile this task runs under) is only useful before the
// command is built, so this hook runs synchronously, in the foreground of the
// spawn, and its stdout is read back.
//
// That inversion is deliberate, and bounded: RouteTimeout caps the wait, a
// failing or silent script yields no decision and the task spawns exactly as it
// would have, and the first plugin to answer wins so a slow one can't be made to
// re-litigate a settled choice.
const EventTaskRoute = "task.route"

// RouteTimeout bounds a routing hook. A task spawn blocks on this, so it is
// tight: a router that needs longer than this to pick a profile is a router that
// should be caching, and the safe answer while it does is "spawn as configured".
const RouteTimeout = 15 * time.Second

// RouteDecision is what a routing hook answers with. The zero value means "no
// opinion" — the caller proceeds with the task's existing configuration.
type RouteDecision struct {
	// Plugin is the name of the plugin that answered, for logging.
	Plugin string
	// ClaudeConfigDir routes the task to a particular Claude profile. Empty
	// leaves the task's existing (project or per-task) config dir alone.
	ClaudeConfigDir string
	// Hold asks the caller not to start this task yet — every candidate profile
	// is out of headroom, and running now would only burn a session on a 429.
	// The task stays queued and is reconsidered on the next tick.
	Hold bool
	// Reason explains a Hold (or annotates a routing choice) for the task log.
	Reason string
}

// Empty reports whether the decision carries no instruction at all.
func (d RouteDecision) Empty() bool {
	return !d.Hold && strings.TrimSpace(d.ClaudeConfigDir) == ""
}

// HandlesRoute reports whether any loaded plugin declares a task.route hook.
// Spawn checks this first so the overwhelmingly common case — nobody has
// installed a router — costs a slice scan instead of a subprocess.
func (r *Runner) HandlesRoute() bool {
	for _, p := range r.plugins {
		if _, ok := p.ScriptFor(EventTaskRoute); ok {
			return true
		}
	}
	return false
}

// Route asks every plugin that handles task.route what to do with this task,
// in plugin-name order, and returns the first non-empty decision.
//
// Plugins are consulted in order rather than in parallel and the first answer
// stands, which keeps the outcome deterministic when more than one router is
// installed — the alternative (merging or last-write-wins) makes the effective
// policy depend on which script happened to finish first.
//
// A hook that errors, times out, or prints nothing usable is skipped: routing
// is an optimization, and failing to optimize must never be the reason a task
// doesn't run.
func (r *Runner) Route(ctx context.Context, task *db.Task) RouteDecision {
	if task == nil {
		return RouteDecision{}
	}
	for _, p := range r.plugins {
		script, ok := p.ScriptFor(EventTaskRoute)
		if !ok {
			continue
		}
		env := append(taskEnv(EventTaskRoute, task, ""),
			"TASK_PLUGIN_NAME="+p.Name,
			"TASK_PLUGIN_DIR="+p.Dir,
			"TASK_EXECUTOR="+task.Executor,
			"TASK_CLAUDE_CONFIG_DIR="+task.ClaudeConfigDir,
		)

		decision, err := runRouteScript(ctx, script, p.Dir, env)
		if err != nil {
			r.logger.Warn("route hook failed", "plugin", p.Name, "task", task.ID, "error", err)
			continue
		}
		if decision.Empty() {
			continue
		}
		decision.Plugin = p.Name
		return decision
	}
	return RouteDecision{}
}

// runRouteScript executes one routing script and parses its verdict.
func runRouteScript(ctx context.Context, script, workDir string, env []string) (RouteDecision, error) {
	ctx, cancel := context.WithTimeout(ctx, RouteTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, script)
	cmd.Dir = workDir
	cmd.Env = env

	// stdout is the decision channel and stderr is free for the script to log
	// on, so they are captured separately — otherwise an `echo "checking..." >&2`
	// in a router would be parsed as part of its answer.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return RouteDecision{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return ParseRouteOutput(stdout.String()), nil
}

// ParseRouteOutput reads a routing script's stdout.
//
// The format is deliberately the dullest thing that works — KEY=VALUE, one per
// line, unknown keys ignored — because the scripts writing it are shell. A
// router shouldn't need a JSON encoder to say "use this directory".
//
//	CLAUDE_CONFIG_DIR=/Users/me/.claude-work
//	HOLD=1
//	REASON=both profiles above 90%, next reset 14:00
//
// Values are taken literally after the first '='; surrounding quotes are
// stripped so `CLAUDE_CONFIG_DIR="$dir"` from a script that quoted its output
// still parses.
func ParseRouteOutput(out string) RouteDecision {
	var d RouteDecision
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		value = unquote(strings.TrimSpace(value))
		switch strings.ToUpper(strings.TrimSpace(key)) {
		case "CLAUDE_CONFIG_DIR":
			d.ClaudeConfigDir = value
		case "HOLD", "DEFER":
			d.Hold = isTruthy(value)
		case "REASON":
			d.Reason = value
		}
	}
	return d
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y", "on":
		return true
	}
	return false
}
