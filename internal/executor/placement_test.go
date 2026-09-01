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

// placementExecutor builds an Executor whose plugins come from a temp dir, so a
// test can install (or not install) a task.placement handler.
func placementExecutor(t *testing.T, pluginsDir string) (*Executor, *db.DB) {
	t.Helper()
	t.Setenv("TY_PLUGINS_DIR", pluginsDir)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "test", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return New(database, config.New(database)), database
}

// installPlacementPlugin writes a plugin whose task.placement handler is the
// given shell script body.
func installPlacementPlugin(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: " + name + "\nhooks:\n  task.placement: resolve.sh\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resolve.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// stubSSH points every remote command at a script instead of a real ssh client.
func stubSSH(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := sshBinary
	sshBinary = path
	t.Cleanup(func() { sshBinary = prev })
	return path
}

func placementTestTask(t *testing.T, database *db.DB) *db.Task {
	t.Helper()
	task := &db.Task{Title: "a task", Type: "task", Project: "test"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	return task
}

// The common case, and the one that must not change: nobody has installed a
// placement plugin, so nothing is asked and the local runner is used.
func TestResolvePlacementNoHandlerMeansLocalAndRecordsNothing(t *testing.T) {
	e, database := placementExecutor(t, t.TempDir())
	task := placementTestTask(t, database)

	runner, placement, err := e.resolvePlacement(context.Background(), task)
	if err != nil {
		t.Fatalf("resolvePlacement: %v", err)
	}
	if _, ok := runner.(LocalRunner); !ok {
		t.Errorf("runner = %T, want LocalRunner", runner)
	}
	if placement.Consulted() {
		t.Error("placement was consulted with no handler installed")
	}

	target, reason, err := database.GetTaskPlacement(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target != "" || reason != "" {
		t.Errorf("placement recorded (%q, %q) for a task nobody was asked about", target, reason)
	}
}

func TestResolvePlacementEmptyTargetMeansLocal(t *testing.T) {
	plugins := t.TempDir()
	installPlacementPlugin(t, plugins, "ty-on",
		"#!/bin/sh\necho '{\"target\":\"\",\"reason\":\"no host serves test\"}'\n")
	e, database := placementExecutor(t, plugins)
	task := placementTestTask(t, database)

	runner, placement, err := e.resolvePlacement(context.Background(), task)
	if err != nil {
		t.Fatalf("resolvePlacement: %v", err)
	}
	if _, ok := runner.(LocalRunner); !ok {
		t.Errorf("runner = %T, want LocalRunner", runner)
	}
	if !placement.Consulted() {
		t.Error("Consulted() = false after a handler answered")
	}

	// A deliberate "local" is still worth recording: it is how a user finds out
	// why their fleet was not used.
	target, reason, _ := database.GetTaskPlacement(task.ID)
	if target != "" {
		t.Errorf("PlacementTarget = %q, want empty", target)
	}
	if reason != "no host serves test" {
		t.Errorf("PlacementReason = %q", reason)
	}
}

func TestResolvePlacementNamedTargetSelectsARemoteRunner(t *testing.T) {
	plugins := t.TempDir()
	installPlacementPlugin(t, plugins, "ty-on",
		"#!/bin/sh\necho '{\"target\":\"ol-agents\",\"workdir\":\"~/projects/engineering\",\"reason\":\"most free memory\"}'\n")
	// The stub stands in for a reachable host: it answers the preflight with the
	// resolved absolute workdir, exactly as `pwd` on the far end would.
	stubSSH(t, "#!/bin/sh\necho /home/agent/projects/engineering\n")

	e, database := placementExecutor(t, plugins)
	task := placementTestTask(t, database)

	runner, placement, err := e.resolvePlacement(context.Background(), task)
	if err != nil {
		t.Fatalf("resolvePlacement: %v", err)
	}
	remote, ok := placedRemotely(runner)
	if !ok {
		t.Fatalf("runner = %T, want RemoteRunner", runner)
	}
	if remote.Host != "ol-agents" {
		t.Errorf("Host = %q, want ol-agents", remote.Host)
	}
	// The tilde the inventory wrote must have been expanded by the remote shell,
	// not left for tmux -c (which does no expansion at all).
	if remote.WorkDir != "/home/agent/projects/engineering" {
		t.Errorf("WorkDir = %q, want the remote-resolved absolute path", remote.WorkDir)
	}
	if placement.Reason != "most free memory" {
		t.Errorf("Reason = %q", placement.Reason)
	}

	target, reason, _ := database.GetTaskPlacement(task.ID)
	if target != "ol-agents" || reason != "most free memory" {
		t.Errorf("recorded placement = (%q, %q), want (ol-agents, most free memory)", target, reason)
	}
	if task.PlacementTarget != "ol-agents" {
		t.Errorf("in-memory task not updated: PlacementTarget = %q", task.PlacementTarget)
	}
}

// A host we cannot reach is an error, never a quiet local run.
func TestResolvePlacementUnreachableHostIsAnError(t *testing.T) {
	plugins := t.TempDir()
	installPlacementPlugin(t, plugins, "ty-on",
		"#!/bin/sh\necho '{\"target\":\"ol-agents\",\"workdir\":\"/srv/x\",\"reason\":\"only host\"}'\n")
	stubSSH(t, "#!/bin/sh\necho 'ssh: connect to host ol-agents port 22: No route to host' >&2\nexit 255\n")

	e, database := placementExecutor(t, plugins)
	task := placementTestTask(t, database)

	runner, _, err := e.resolvePlacement(context.Background(), task)
	if err == nil {
		t.Fatalf("resolvePlacement returned runner %T and no error for an unreachable host", runner)
	}
	if runner != nil {
		t.Errorf("runner = %T, want nil so no caller can mistake this for a local run", runner)
	}
	// The placement is still recorded: knowing which host it tried is the whole
	// point of the failure being visible.
	if target, _, _ := database.GetTaskPlacement(task.ID); target != "ol-agents" {
		t.Errorf("recorded target = %q, want ol-agents", target)
	}
}

// Every way a handler can fail means local. Nothing here may fail the task.
func TestResolvePlacementHandlerFailuresFallBackToLocal(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"crash", "#!/bin/sh\nexit 3\n"},
		{"malformed", "#!/bin/sh\necho 'definitely not json'\n"},
		{"silent", "#!/bin/sh\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plugins := t.TempDir()
			installPlacementPlugin(t, plugins, "ty-on", tc.body)
			e, database := placementExecutor(t, plugins)
			task := placementTestTask(t, database)

			runner, _, err := e.resolvePlacement(context.Background(), task)
			if err != nil {
				t.Fatalf("a failing handler must not fail the task: %v", err)
			}
			if _, ok := runner.(LocalRunner); !ok {
				t.Errorf("runner = %T, want LocalRunner", runner)
			}
		})
	}
}

func TestRemoteLaunchScriptStagesThePromptRemotely(t *testing.T) {
	task := &db.Task{ID: 42, Port: 3042}
	script, err := remoteLaunchScript(task, "claude", "/home/agent/projects/x", "do the thing")
	if err != nil {
		t.Fatalf("remoteLaunchScript: %v", err)
	}
	for _, want := range []string{
		"WORKTREE_TASK_ID=42",
		"WORKTREE_PORT=3042",
		"claude ",
		`"$(cat '/tmp/ty-task-42-prompt.txt')"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script %q is missing %q", script, want)
		}
	}
	// Nothing that only exists on this machine may leak into the remote command.
	for _, forbidden := range []string{"--mcp-config", "CLAUDE_CONFIG_DIR", os.TempDir() + "/task-prompt"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("script %q carries a local-only path: %q", script, forbidden)
		}
	}
}

// An executor ty cannot launch remotely fails visibly instead of quietly
// becoming a local run.
func TestRemoteLaunchScriptRefusesAnUnsupportedExecutor(t *testing.T) {
	_, err := remoteLaunchScript(&db.Task{ID: 1}, "codex", "/srv/x", "hi")
	if err == nil {
		t.Fatal("remoteLaunchScript accepted a non-claude executor")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Errorf("error %q does not name the executor it cannot launch", err)
	}
}
