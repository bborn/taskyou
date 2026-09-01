package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// EventTaskPlacement is the one hook ty *consults* rather than merely notifies.
//
// Every other event in this package is fire-and-forget: the script is launched in
// the background and nothing waits for it or reads what it says. Placement cannot
// work that way — where a task runs has to be decided BEFORE the executor spawns,
// and the answer has to come back. So this event is synchronous, bounded by a
// short timeout, and its stdout is parsed.
//
// A handler receives the request as JSON on stdin:
//
//	{"event":"task.placement","task":{"id":5228,"title":"...","project":"taskyou",
//	 "repo_path":"/abs/path","executor":"claude"}}
//
// and answers with JSON on stdout:
//
//	{"target":"ol-agents","workdir":"~/projects/engineering","reason":"most free memory"}
//
// An empty target means "run locally", which is also what ty assumes for every
// way a handler can fail. Core never learns what a host is: it asks a question
// and uses the answer.
const EventTaskPlacement = "task.placement"

// DefaultPlacementTimeout bounds a placement handler. This runs in the task spawn
// path, so a slow resolver must never hang a task: past this budget ty stops
// waiting, logs loudly, and runs the task locally.
const DefaultPlacementTimeout = 5 * time.Second

// placementWaitDelay is how long ty waits, after killing a handler that blew its
// deadline, for the pipes it inherited to close before force-closing them.
//
// Without it "bounded" is a lie: exec.Cmd.Wait blocks until every writer of the
// captured stdout is gone, and a handler that spawned a child (an `on ls` probe,
// say) leaves that child holding the pipe open long after the handler itself is
// dead. The spawn path would hang for exactly as long as the runaway child ran.
const placementWaitDelay = 500 * time.Millisecond

// PlacementTaskInfo is the task half of the placement request — the facts a
// resolver needs to pick a host, and nothing more.
type PlacementTaskInfo struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Project  string `json:"project"`
	RepoPath string `json:"repo_path"`
	Executor string `json:"executor"`
}

// placementRequest is the JSON written to a handler's stdin.
type placementRequest struct {
	Event string            `json:"event"`
	Task  PlacementTaskInfo `json:"task"`
}

// Placement is a handler's answer: where the task should run.
type Placement struct {
	// Target names the host to run on. Empty means local.
	Target string `json:"target"`
	// WorkDir is the task's directory ON THAT HOST. It is a remote path, so a
	// leading "~" is left alone for the remote shell to expand.
	WorkDir string `json:"workdir"`
	// Reason explains the choice in the user's words. Always surfaced, so that a
	// surprising placement can be understood without digging.
	Reason string `json:"reason"`

	// Handler is the plugin that answered, or "" when no handler is installed.
	// Not part of the wire contract — ty fills it in.
	Handler string `json:"-"`
}

// IsLocal reports whether this placement means "run here", which is both the
// explicit empty-target answer and the answer to every failure.
func (p Placement) IsLocal() bool { return strings.TrimSpace(p.Target) == "" }

// Consulted reports whether any handler actually answered. False is the common
// case — no placement plugin installed — and must leave behaviour untouched.
func (p Placement) Consulted() bool { return p.Handler != "" }

// HasPlacementHandler reports whether any loaded plugin handles task.placement.
// The spawn path checks this first so that a user with no placement plugin pays
// nothing at all: no JSON built, no process spawned, no log line.
func (r *Runner) HasPlacementHandler() bool {
	for _, p := range r.plugins {
		if _, ok := p.ScriptFor(EventTaskPlacement); ok {
			return true
		}
	}
	return false
}

// ResolvePlacement asks the installed placement handlers where a task should run.
//
// Handlers are consulted in plugin-name order and the first one to name a target
// wins; a handler that answers "local" (empty target) lets the next one try. Any
// failure — timeout, crash, non-zero exit, unparseable stdout — is logged loudly
// and treated as "local", because failing to DECIDE where to run must never fail
// the task. (Failing to EXECUTE where a handler told us to is a different matter,
// and is not silently downgraded; see the executor's placement handling.)
//
// With no handler installed the zero Placement comes back, Consulted() is false,
// and the caller must behave exactly as it did before this existed.
func (r *Runner) ResolvePlacement(ctx context.Context, info PlacementTaskInfo) Placement {
	req, err := json.Marshal(placementRequest{Event: EventTaskPlacement, Task: info})
	if err != nil {
		// Can't happen for these field types, but a placement request we cannot
		// even build is emphatically not a reason to fail the task.
		r.logger.Error("placement: could not build request", "task", info.ID, "error", err)
		return Placement{}
	}

	var answered Placement
	r.consult(ctx, EventTaskPlacement, r.placementTimeout(), req,
		func(p Plugin) []string {
			return append(os.Environ(),
				"TASK_EVENT="+EventTaskPlacement,
				"TASK_PLUGIN_NAME="+p.Name,
				"TASK_PLUGIN_DIR="+p.Dir,
			)
		},
		func(a handlerAnswer) bool {
			got, err := parsePlacementOutput(a.Stdout)
			if err != nil {
				r.logger.Error("placement handler answered unusably; running locally",
					"task", info.ID, "plugin", a.Plugin, "error", err)
				return false
			}
			got.Handler = a.Plugin

			if !got.IsLocal() {
				r.logger.Info("placement decided", "task", info.ID, "plugin", a.Plugin,
					"target", got.Target, "workdir", got.WorkDir, "reason", got.Reason)
				answered = got
				return true
			}

			// A deliberate "local" from a handler still counts as an answer: it is
			// what ty reports. But it does not stop the search, so a later handler
			// with an opinion about a host still gets asked.
			if !answered.Consulted() {
				answered = got
			}
			return false
		})
	return answered
}

// parsePlacementOutput reads a placement handler's stdout.
//
// Silence is a valid answer — "no opinion", an empty target by another
// spelling — so it is not an error. Malformed JSON is, because a handler that
// meant to answer and got the format wrong should be visible to whoever wrote
// it rather than silently treated as indifference.
func parsePlacementOutput(stdout string) (Placement, error) {
	out := bytes.TrimSpace([]byte(stdout))
	if len(out) == 0 {
		return Placement{}, nil
	}
	var placement Placement
	if err := json.Unmarshal(out, &placement); err != nil {
		return Placement{}, fmt.Errorf("handler wrote malformed JSON: %w (output: %s)",
			err, truncateForLog(string(out)))
	}
	placement.Target = strings.TrimSpace(placement.Target)
	placement.WorkDir = strings.TrimSpace(placement.WorkDir)
	return placement, nil
}

// placementTimeout returns the handler budget, defaulting when unset (tests
// shorten it; nothing user-facing configures it).
func (r *Runner) placementTimeout() time.Duration {
	if r.placementTimeoutOverride > 0 {
		return r.placementTimeoutOverride
	}
	return DefaultPlacementTimeout
}

// truncateForLog keeps a malformed-output log line readable.
func truncateForLog(s string) string {
	const max = 200
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
