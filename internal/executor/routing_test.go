package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// newRoutingExecutor builds an Executor whose plugins come from a temp dir, so a
// routing test exercises the real hook path (subprocess, stdout parsing, DB
// write) without depending on what is installed on the machine.
func newRoutingExecutor(t *testing.T, routeScript string) (*Executor, *db.DB) {
	t.Helper()

	pluginsDir := t.TempDir()
	if routeScript != "" {
		dir := filepath.Join(pluginsDir, "router")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := "name: router\nhooks:\n  task.route: route.sh\n"
		if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "route.sh"), []byte(routeScript), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TY_PLUGINS_DIR", pluginsDir)

	tmpFile, err := os.CreateTemp("", "test-routing-*.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })
	tmpFile.Close()

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "test", Path: "/tmp/test"}); err != nil {
		t.Fatal(err)
	}

	// The hold-log memo is package state; keep tests from leaking into each other.
	t.Cleanup(func() { routeHoldLog.Range(func(k, _ any) bool { routeHoldLog.Delete(k); return true }) })

	return New(database, &config.Config{}), database
}

func newRoutingTask(t *testing.T, database *db.DB, executorName string) *db.Task {
	t.Helper()
	task := &db.Task{Title: "route me", Type: "task", Project: "test", Executor: executorName}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestRouteTask_AppliesAndPersistsConfigDir(t *testing.T) {
	e, database := newRoutingExecutor(t, "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/tmp/claude-work\necho REASON=12% used\n")
	task := newRoutingTask(t, database, db.ExecutorClaude)

	if ok := e.routeTask(context.Background(), task, true); !ok {
		t.Fatal("routeTask returned false, want the task cleared to run")
	}
	if task.ClaudeConfigDir != "/tmp/claude-work" {
		t.Errorf("in-memory ClaudeConfigDir = %q", task.ClaudeConfigDir)
	}

	// The write matters as much as the in-memory value: the TUI and any later
	// resume read the column, and a disagreement there is exactly the confusion
	// routing is meant to remove.
	reloaded, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ClaudeConfigDir != "/tmp/claude-work" {
		t.Errorf("persisted ClaudeConfigDir = %q", reloaded.ClaudeConfigDir)
	}

	logs, err := database.GetTaskLogs(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range logs {
		if strings.Contains(l.Content, "/tmp/claude-work") && strings.Contains(l.Content, "12% used") {
			found = true
		}
	}
	if !found {
		t.Errorf("routing decision was not written to the task log: %+v", logs)
	}
}

func TestRouteTask_ExplicitConfigDirIsNotOverridden(t *testing.T) {
	// A dir chosen by a person or a workflow step is a decision, not a default.
	e, database := newRoutingExecutor(t, "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/tmp/router-choice\n")
	task := newRoutingTask(t, database, db.ExecutorClaude)
	task.ClaudeConfigDir = "/tmp/chosen-by-hand"

	if ok := e.routeTask(context.Background(), task, true); !ok {
		t.Fatal("routeTask returned false")
	}
	if task.ClaudeConfigDir != "/tmp/chosen-by-hand" {
		t.Errorf("router overrode an explicit config dir: %q", task.ClaudeConfigDir)
	}
}

func TestRouteTask_ProjectPinnedProfileIsNotOverridden(t *testing.T) {
	// A project's config dir is a real choice, not a default: it carries that
	// account's MCP connectors and their per-profile OAuth logins. Routing an
	// influencekit task onto a personal profile wouldn't error, it would just
	// silently run without the servers it needs.
	e, database := newRoutingExecutor(t, "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/tmp/router-choice\n")
	if err := database.CreateProject(&db.Project{Name: "pinned", Path: "/tmp/pinned", ClaudeConfigDir: "~/.claude-work"}); err != nil {
		t.Fatal(err)
	}
	task := &db.Task{Title: "t", Type: "task", Project: "pinned", Executor: db.ExecutorClaude}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	if ok := e.routeTask(context.Background(), task, true); !ok {
		t.Fatal("routeTask returned false")
	}
	if task.ClaudeConfigDir != "" {
		t.Errorf("router overrode a project-pinned profile: %q", task.ClaudeConfigDir)
	}
}

func TestRouteTask_UnpinnedProjectStillRoutes(t *testing.T) {
	// The flip side: pinning is opt-out, so a project that hasn't chosen still
	// gets routed. Otherwise the fix above would quietly disable the feature.
	e, database := newRoutingExecutor(t, "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/tmp/router-choice\n")
	task := newRoutingTask(t, database, db.ExecutorClaude) // project "test", no config dir

	if ok := e.routeTask(context.Background(), task, true); !ok {
		t.Fatal("routeTask returned false")
	}
	if task.ClaudeConfigDir != "/tmp/router-choice" {
		t.Errorf("unpinned project was not routed: %q", task.ClaudeConfigDir)
	}
}

func TestRouteTask_ResumedTaskStaysOnItsProfile(t *testing.T) {
	// Session affinity, and it is not optional: a Claude session lives inside
	// one config dir, so a task resumed under a different profile would find no
	// session to resume and silently start a fresh conversation. Once a task has
	// been routed, the stamped dir must pin it for the rest of its life — even
	// when the router would now prefer somewhere else.
	e, database := newRoutingExecutor(t, "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/tmp/now-emptier\n")
	task := newRoutingTask(t, database, db.ExecutorClaude)

	// First spawn: the router picks a profile and it is recorded.
	if ok := e.routeTask(context.Background(), task, true); !ok {
		t.Fatal("routeTask returned false")
	}
	first := task.ClaudeConfigDir
	if first == "" {
		t.Fatal("first spawn was not routed")
	}
	task.ClaudeSessionID = "sess-abc"

	// Second pass (a resume after the task was blocked, say) must not move it.
	if ok := e.routeTask(context.Background(), task, true); !ok {
		t.Fatal("routeTask returned false on resume")
	}
	if task.ClaudeConfigDir != first {
		t.Errorf("resumed task moved profiles: %q -> %q", first, task.ClaudeConfigDir)
	}
}

func TestRouteTask_NonClaudeExecutorIsUntouched(t *testing.T) {
	// CLAUDE_CONFIG_DIR means nothing to codex; setting it would be noise at
	// best and a misleading task log at worst.
	e, database := newRoutingExecutor(t, "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/tmp/nope\n")
	task := newRoutingTask(t, database, db.ExecutorCodex)

	if ok := e.routeTask(context.Background(), task, true); !ok {
		t.Fatal("routeTask returned false")
	}
	if task.ClaudeConfigDir != "" {
		t.Errorf("codex task was routed: %q", task.ClaudeConfigDir)
	}
}

func TestRouteTask_EveryNonClaudeExecutorIsUntouched(t *testing.T) {
	// The guard is an allowlist (claude only), not a denylist, so an executor
	// added later is skipped without anyone remembering to update routing. This
	// walks the full set so that stays true rather than being merely intended —
	// grok and cursor both landed after this guard was written.
	for _, name := range []string{
		db.ExecutorCodex, db.ExecutorGemini, db.ExecutorGrok, db.ExecutorCursor,
		db.ExecutorOpenClaw, db.ExecutorOpenCode, db.ExecutorPi,
	} {
		t.Run(name, func(t *testing.T) {
			e, database := newRoutingExecutor(t, "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/tmp/nope\n")
			task := newRoutingTask(t, database, name)

			if ok := e.routeTask(context.Background(), task, true); !ok {
				t.Fatal("routeTask returned false")
			}
			if task.ClaudeConfigDir != "" {
				t.Errorf("%s task was routed: %q", name, task.ClaudeConfigDir)
			}
		})
	}
}

func TestRouteTask_HoldKeepsTaskQueued(t *testing.T) {
	e, database := newRoutingExecutor(t, "#!/bin/sh\necho HOLD=1\necho 'REASON=every profile above 90%'\n")
	task := newRoutingTask(t, database, db.ExecutorClaude)
	if err := database.SetTaskStatus(task.ID, db.StatusQueued, db.ActorCLI, "test fixture", db.ByHuman("test fixture")); err != nil {
		t.Fatal(err)
	}

	if ok := e.routeTask(context.Background(), task, true); ok {
		t.Fatal("routeTask returned true, want the spawn held")
	}

	// A held task must stay queued: parking it as blocked would take a human to
	// undo, when the whole point is that it starts by itself once limits reset.
	reloaded, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != db.StatusQueued {
		t.Errorf("status = %q, want it left queued", reloaded.Status)
	}
}

func TestRouteTask_RepeatedHoldLogsOnce(t *testing.T) {
	// The daemon reconsiders a queued task every tick. Logging each refusal
	// would bury the task's real history under thousands of identical lines.
	e, database := newRoutingExecutor(t, "#!/bin/sh\necho HOLD=1\necho 'REASON=every profile above 90%'\n")
	task := newRoutingTask(t, database, db.ExecutorClaude)

	for i := 0; i < 3; i++ {
		if ok := e.routeTask(context.Background(), task, true); ok {
			t.Fatal("routeTask returned true, want held")
		}
	}

	logs, err := database.GetTaskLogs(task.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	holds := 0
	for _, l := range logs {
		if strings.Contains(l.Content, "Waiting to start") {
			holds++
		}
	}
	if holds != 1 {
		t.Errorf("wrote %d hold log lines across 3 ticks, want 1", holds)
	}
}

func TestRouteTask_HoldIgnoredOnManualRun(t *testing.T) {
	// `ty run` / "start now" is an explicit instruction. Silently refusing it
	// would read as a broken button.
	e, database := newRoutingExecutor(t, "#!/bin/sh\necho HOLD=1\n")
	task := newRoutingTask(t, database, db.ExecutorClaude)

	if ok := e.routeTask(context.Background(), task, false); !ok {
		t.Error("a manual run should not be held")
	}
}

func TestRouteTask_NoRouterIsANoOp(t *testing.T) {
	e, database := newRoutingExecutor(t, "")
	task := newRoutingTask(t, database, db.ExecutorClaude)

	if ok := e.routeTask(context.Background(), task, true); !ok {
		t.Fatal("routeTask returned false with no router installed")
	}
	if task.ClaudeConfigDir != "" {
		t.Errorf("ClaudeConfigDir = %q, want untouched", task.ClaudeConfigDir)
	}
}

func TestRouteTask_FailingRouterStillSpawns(t *testing.T) {
	// Routing is an optimization. Failing to optimize must never be why a task
	// doesn't run.
	e, database := newRoutingExecutor(t, "#!/bin/sh\necho boom >&2\nexit 1\n")
	task := newRoutingTask(t, database, db.ExecutorClaude)

	if ok := e.routeTask(context.Background(), task, true); !ok {
		t.Error("a failing router should not block the spawn")
	}
	if task.ClaudeConfigDir != "" {
		t.Errorf("ClaudeConfigDir = %q, want untouched", task.ClaudeConfigDir)
	}
}

func TestRouteTask_ExpandsTildeInRoutedDir(t *testing.T) {
	// A router written in shell may well emit a literal ~; the stored value has
	// to be the resolved path, since it is spliced straight into the spawn
	// command as CLAUDE_CONFIG_DIR="…".
	e, database := newRoutingExecutor(t, "#!/bin/sh\necho 'CLAUDE_CONFIG_DIR=~/.claude-work'\n")
	task := newRoutingTask(t, database, db.ExecutorClaude)

	if ok := e.routeTask(context.Background(), task, true); !ok {
		t.Fatal("routeTask returned false")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	want := filepath.Join(home, ".claude-work")
	if task.ClaudeConfigDir != want {
		t.Errorf("ClaudeConfigDir = %q, want %q", task.ClaudeConfigDir, want)
	}
}

func TestRouteTask_EmptyExecutorIsTreatedAsClaude(t *testing.T) {
	// Older tasks carry no executor; claude is the default, so they should route.
	e, database := newRoutingExecutor(t, "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/tmp/x\n")
	task := newRoutingTask(t, database, "")

	if ok := e.routeTask(context.Background(), task, true); !ok {
		t.Fatal("routeTask returned false")
	}
	if task.ClaudeConfigDir != "/tmp/x" {
		t.Errorf("ClaudeConfigDir = %q, want /tmp/x", task.ClaudeConfigDir)
	}
}
