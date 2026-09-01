package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

// placementRunner builds a hook Runner over a temp plugins dir, with a handler
// budget generous enough that forking a shell script cannot lose the race.
//
// The budget matters: a handler that misses it is (correctly) treated as a
// failure and falls back to local, so a tight budget here would turn every
// success-path assertion into a test of machine load rather than of behaviour.
// Under `go test ./...` the whole suite is competing for cores and a subprocess
// that starts in ~100ms alone takes far longer. Only the test that deliberately
// exercises the timeout shortens it, via placementRunnerWithTimeout.
func placementRunner(t *testing.T, pluginsDir string) *Runner {
	t.Helper()
	return placementRunnerWithTimeout(t, pluginsDir, 30*time.Second)
}

// placementRunnerWithTimeout is placementRunner with an explicit handler budget.
func placementRunnerWithTimeout(t *testing.T, pluginsDir string, budget time.Duration) *Runner {
	t.Helper()
	r := newRunner("", pluginsDir, log.NewWithOptions(nil, log.Options{Level: log.FatalLevel}))
	r.placementTimeoutOverride = budget
	return r
}

func placementTask() PlacementTaskInfo {
	return PlacementTaskInfo{
		ID: 5228, Title: "Add a consulted task.placement hook",
		Project: "taskyou", RepoPath: "/abs/path", Executor: "claude",
	}
}

// The common case: nobody has installed a placement plugin. Nothing is
// consulted, and the caller must see the zero placement so it can behave
// exactly as it did before this hook existed.
func TestResolvePlacement_NoHandlerIsNotConsultedAndIsLocal(t *testing.T) {
	r := placementRunner(t, t.TempDir())
	if r.HasPlacementHandler() {
		t.Fatal("HasPlacementHandler() = true with no plugins installed")
	}
	got := r.ResolvePlacement(context.Background(), placementTask())
	if got.Consulted() {
		t.Errorf("Consulted() = true, want false: nothing answered")
	}
	if !got.IsLocal() {
		t.Errorf("IsLocal() = false, want true (target %q)", got.Target)
	}
}

func TestResolvePlacement_EmptyTargetMeansLocal(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "ty-on", "name: ty-on\nhooks:\n  task.placement: resolve.sh\n",
		map[string]string{"resolve.sh": "#!/bin/sh\necho '{\"target\":\"\",\"reason\":\"no host serves this project\"}'\n"})

	got := placementRunner(t, root).ResolvePlacement(context.Background(), placementTask())
	if !got.IsLocal() {
		t.Errorf("IsLocal() = false, want true (target %q)", got.Target)
	}
	if !got.Consulted() || got.Handler != "ty-on" {
		t.Errorf("Handler = %q, want ty-on: a deliberate 'local' is still an answer", got.Handler)
	}
	if got.Reason != "no host serves this project" {
		t.Errorf("Reason = %q", got.Reason)
	}
}

func TestResolvePlacement_NamedTargetIsReturned(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "ty-on", "name: ty-on\nhooks:\n  task.placement: resolve.sh\n",
		map[string]string{"resolve.sh": "#!/bin/sh\necho '{\"target\":\"ol-agents\",\"workdir\":\"~/projects/engineering\",\"reason\":\"most free memory\"}'\n"})

	got := placementRunner(t, root).ResolvePlacement(context.Background(), placementTask())
	if got.Target != "ol-agents" {
		t.Errorf("Target = %q, want ol-agents", got.Target)
	}
	if got.WorkDir != "~/projects/engineering" {
		t.Errorf("WorkDir = %q: a remote path's ~ must be passed through untouched", got.WorkDir)
	}
	if got.Reason != "most free memory" {
		t.Errorf("Reason = %q", got.Reason)
	}
	if got.Handler != "ty-on" {
		t.Errorf("Handler = %q, want ty-on", got.Handler)
	}
}

// The handler is told which task it is deciding for, on stdin, in the shape
// ty-on is already built against.
func TestResolvePlacement_SendsTheRequestOnStdin(t *testing.T) {
	root := t.TempDir()
	// The handler echoes its own stdin back inside the "reason" field.
	writePlugin(t, root, "echoer", "name: echoer\nhooks:\n  task.placement: resolve.sh\n",
		map[string]string{"resolve.sh": "#!/bin/sh\nIN=$(cat)\nprintf '{\"target\":\"\",\"reason\":%s}\\n' \"$(printf '%s' \"$IN\" | sed 's/\"/\\\\\"/g; s/^/\"/; s/$/\"/')\"\n"})

	got := placementRunner(t, root).ResolvePlacement(context.Background(), placementTask())

	var req placementRequest
	if err := json.Unmarshal([]byte(got.Reason), &req); err != nil {
		t.Fatalf("handler stdin was not the request JSON: %v (got %q)", err, got.Reason)
	}
	if req.Event != EventTaskPlacement {
		t.Errorf("event = %q, want %q", req.Event, EventTaskPlacement)
	}
	if req.Task.ID != 5228 || req.Task.Project != "taskyou" ||
		req.Task.RepoPath != "/abs/path" || req.Task.Executor != "claude" {
		t.Errorf("task = %+v, does not match the documented contract", req.Task)
	}
}

// Every way of failing to answer means "local". A hook in the spawn path must
// never hang, crash, or confuse a task.
func TestResolvePlacement_FailuresFallBackToLocal(t *testing.T) {
	cases := []struct {
		name string
		// budget is the handler timeout for this case. Only the "slow" case is
		// about the timeout, so only it is given a short one; the others must not
		// be able to fail for the wrong reason under a loaded machine.
		budget time.Duration
		script string
	}{
		{"slow", 300 * time.Millisecond, "#!/bin/sh\nsleep 30\necho '{\"target\":\"ol-agents\"}'\n"},
		{"crash", 30 * time.Second, "#!/bin/sh\necho boom >&2\nexit 1\n"},
		{"malformed", 30 * time.Second, "#!/bin/sh\necho 'not json at all'\n"},
		{"silent", 30 * time.Second, "#!/bin/sh\n"},
		{"target-then-garbage", 30 * time.Second, "#!/bin/sh\necho '{\"target\":\"ol-agents\"'\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writePlugin(t, root, "ty-on", "name: ty-on\nhooks:\n  task.placement: resolve.sh\n",
				map[string]string{"resolve.sh": tc.script})

			start := time.Now()
			got := placementRunnerWithTimeout(t, root, tc.budget).
				ResolvePlacement(context.Background(), placementTask())
			if !got.IsLocal() {
				t.Errorf("IsLocal() = false, want true (target %q)", got.Target)
			}
			// The handler sleeps 30s; the point is that ty stops waiting long
			// before that, budget plus the grace period for closing its pipes.
			if elapsed := time.Since(start); elapsed > tc.budget+5*time.Second {
				t.Errorf("took %s with a %s budget: a placement handler must be bounded", elapsed, tc.budget)
			}
		})
	}
}

// A broken handler must not stop a working one from being heard.
func TestResolvePlacement_FirstDecisiveHandlerWins(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "a-broken", "name: a-broken\nhooks:\n  task.placement: resolve.sh\n",
		map[string]string{"resolve.sh": "#!/bin/sh\nexit 2\n"})
	writePlugin(t, root, "b-local", "name: b-local\nhooks:\n  task.placement: resolve.sh\n",
		map[string]string{"resolve.sh": "#!/bin/sh\necho '{\"target\":\"\"}'\n"})
	writePlugin(t, root, "c-decides", "name: c-decides\nhooks:\n  task.placement: resolve.sh\n",
		map[string]string{"resolve.sh": "#!/bin/sh\necho '{\"target\":\"mona\",\"workdir\":\"~/Projects/taskyou\"}'\n"})

	got := placementRunner(t, root).ResolvePlacement(context.Background(), placementTask())
	if got.Target != "mona" {
		t.Errorf("Target = %q, want mona", got.Target)
	}
	if got.Handler != "c-decides" {
		t.Errorf("Handler = %q, want c-decides", got.Handler)
	}
}

func TestHasPlacementHandler(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "notifier", "name: notifier\nhooks:\n  task.done: done.sh\n",
		map[string]string{"done.sh": "#!/bin/sh\n"})
	if placementRunner(t, root).HasPlacementHandler() {
		t.Error("a plugin that only handles task.done must not look like a placement handler")
	}

	writePlugin(t, root, "ty-on", "name: ty-on\nhooks:\n  task.placement: ty-on\n",
		map[string]string{"ty-on": "#!/bin/sh\necho '{}'\n"})
	if !placementRunner(t, root).HasPlacementHandler() {
		t.Error("HasPlacementHandler() = false with a task.placement plugin installed")
	}
}

// Whitespace around a target is a handler typo, not a hostname.
func TestResolvePlacement_TrimsTarget(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "ty-on", "name: ty-on\nhooks:\n  task.placement: resolve.sh\n",
		map[string]string{"resolve.sh": "#!/bin/sh\necho '{\"target\":\"  \",\"workdir\":\" ~/x \"}'\n"})

	got := placementRunner(t, root).ResolvePlacement(context.Background(), placementTask())
	if !got.IsLocal() {
		t.Errorf("a whitespace-only target must mean local, got %q", got.Target)
	}
	if strings.Contains(got.WorkDir, " ") {
		t.Errorf("WorkDir = %q, want trimmed", got.WorkDir)
	}
}

// Both consulted hooks now share one driver, so the rules they must agree on
// are asserted once, here, rather than drifting apart again.
func TestConsultedHooksShareTheirRules(t *testing.T) {
	place := func(script string) map[string]string {
		return map[string]string{"resolve.sh": script}
	}
	manifest := "hooks:\n  task.placement: resolve.sh\n"

	t.Run("first decisive answer wins and later handlers are not run", func(t *testing.T) {
		root := t.TempDir()
		marker := filepath.Join(root, "b-ran")
		// Plugin name decides order, so "a" is asked before "b".
		writePlugin(t, root, "a", "name: a\n"+manifest,
			place("#!/bin/sh\necho '{\"target\":\"host-a\",\"reason\":\"first\"}'\n"))
		writePlugin(t, root, "b", "name: b\n"+manifest,
			place("#!/bin/sh\ntouch "+marker+"\necho '{\"target\":\"host-b\"}'\n"))

		got := placementRunner(t, root).ResolvePlacement(context.Background(), placementTask())
		if got.Target != "host-a" {
			t.Errorf("target = %q, want the first handler's answer", got.Target)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Error("second handler ran after the question was already settled")
		}
	})

	t.Run("a failing handler is skipped, not fatal", func(t *testing.T) {
		root := t.TempDir()
		writePlugin(t, root, "a", "name: a\n"+manifest,
			place("#!/bin/sh\necho 'something broke' >&2\nexit 1\n"))
		writePlugin(t, root, "b", "name: b\n"+manifest,
			place("#!/bin/sh\necho '{\"target\":\"host-b\",\"reason\":\"survivor\"}'\n"))

		got := placementRunner(t, root).ResolvePlacement(context.Background(), placementTask())
		if got.Target != "host-b" {
			t.Errorf("target = %q; a broken handler must not stop the search", got.Target)
		}
	})

	t.Run("stderr is not parsed as the answer", func(t *testing.T) {
		root := t.TempDir()
		writePlugin(t, root, "a", "name: a\n"+manifest,
			place("#!/bin/sh\necho 'checking hosts...' >&2\necho '{\"target\":\"host-a\"}'\n"))

		got := placementRunner(t, root).ResolvePlacement(context.Background(), placementTask())
		if got.Target != "host-a" {
			t.Errorf("target = %q, want the stdout answer with stderr ignored", got.Target)
		}
	})
}
