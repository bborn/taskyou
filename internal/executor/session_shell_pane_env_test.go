package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// recordingRunner is a Runner seam that records every command the code under test
// builds (so a test can assert on the argv the daemon path would have run for
// real) and returns canned stdout for the tmux subcommands whose output drives
// EnsureTaskWindow's control flow. Everything else exits 0.
//
// It exists because EnsureTaskWindow is otherwise impossible to exercising
// without a live tmux server: the function shells out to tmux for every step
// (list-windows, list-sessions, new-session, new-window, split-window,
// send-keys, select-pane, display-message). The mock lets a test drive the
// function end-to-end without tmux on PATH, so the regression this file pins
// runs wherever the suite does.
type recordingRunner struct {
	mu      sync.Mutex
	calls   [][]string
	outputs map[string][]byte
}

func (r *recordingRunner) Target() string { return "" }

func (r *recordingRunner) Command(ctx context.Context, _ string, _ string, args ...string) *exec.Cmd {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{"tmux"}, args...))
	r.mu.Unlock()

	// `tmuxCmd` prepends `-L <socket>` only when Socket() returns non-empty,
	// which under testing.Testing() + the package's tmuxtest.Main
	// (TASKYOU_TMUX_SOCKET=default) it never does, so args[0] is the user
	// subcommand ("list-windows", "send-keys", ...). Match outputs on that.
	if len(args) > 0 {
		if out, ok := r.outputs[args[0]]; ok {
			cmd := exec.CommandContext(ctx, "sh", "-c", `printf %s "$TY_REC_OUT"`)
			cmd.Env = append(os.Environ(), "TY_REC_OUT="+string(out))
			return cmd
		}
	}
	return exec.CommandContext(ctx, "true")
}

func (r *recordingRunner) snapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	for i, c := range r.calls {
		dup := make([]string, len(c))
		copy(dup, c)
		out[i] = dup
	}
	return out
}

// shellEnvProbeExecutor is a do-nothing TaskExecutor registered only for these
// tests. It avoids the real Claude CLI (so the test never shells out to a
// binary that isn't installed) and avoids setupClaudeHooks. Its BuildCommand
// stays alive long enough for the live-tmux test to read the shell pane back.
type shellEnvProbeExecutor struct{ name string }

func (s *shellEnvProbeExecutor) Name() string { return s.name }
func (s *shellEnvProbeExecutor) Execute(context.Context, *db.Task, string, string) ExecResult {
	return ExecResult{}
}
func (s *shellEnvProbeExecutor) Resume(context.Context, *db.Task, string, string, string) ExecResult {
	return ExecResult{}
}
func (s *shellEnvProbeExecutor) BuildCommand(*db.Task, string, string) string {
	// A bespoke marker keeps the executor pane alive in the live-tmux test so
	// the window does not collapse before capture-pane reads the shell pane.
	return "tail -f /dev/null"
}
func (s *shellEnvProbeExecutor) IsAvailable() bool                     { return true }
func (s *shellEnvProbeExecutor) GetProcessID(int64) int                { return 0 }
func (s *shellEnvProbeExecutor) Kill(int64) bool                       { return false }
func (s *shellEnvProbeExecutor) SupportsSessionResume() bool           { return false }
func (s *shellEnvProbeExecutor) SupportsDangerousMode() bool           { return false }
func (s *shellEnvProbeExecutor) FindSessionID(string) string           { return "" }
func (s *shellEnvProbeExecutor) ResumeDangerous(*db.Task, string) bool { return false }
func (s *shellEnvProbeExecutor) ResumeSafe(*db.Task, string) bool      { return false }

const shellEnvProbeExecutorName = "ty-shell-env-probe"

// setupShellEnvTest builds an Executor whose factory has the probe executor
// registered, an isolated database keyed at dbPath, and a worktree project
// whose task workdir is on disk. The single task created has the executor
// pinned to the probe so GetTaskExecutor resolves to it.
func setupShellEnvTest(t *testing.T) (*Executor, *db.DB, *recordingRunner, *db.Task, string) {
	t.Helper()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "tasks.db")
	// The spawn lock and the DB must both land in the test's temp dir, or
	// EnsureTaskWindow's executorlock.AcquireSpawn(executorSpawnLockDir()) —
	// which is filepath.Dir(db.DefaultPath()) — drops lock files next to the
	// user's real install.
	t.Setenv("WORKTREE_DB_PATH", dbPath)
	// The memory guard is inert by default (warn mode), but a CI runner that
	// happens to be tight makes this test flaky for no reason of its own.
	t.Setenv("TY_MEMORY_GUARD", "off")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	projectDir := t.TempDir()
	if err := database.CreateProject(&db.Project{Name: "p", Path: projectDir, UseWorktrees: true}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	e := New(database, config.New(database))
	e.executorFactory.Register(&shellEnvProbeExecutor{name: shellEnvProbeExecutorName})

	workDir := t.TempDir()
	task := &db.Task{
		Title:        "shell env probe",
		Status:       db.StatusBacklog,
		Project:      "p",
		Executor:     shellEnvProbeExecutorName,
		WorktreePath: workDir,
		Port:         31999,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	runner := &recordingRunner{outputs: map[string][]byte{
		// findExistingTaskWindow: no windows, so the spawn path runs.
		"list-windows": []byte(""),
		// findOrCreateDaemonSession: no sessions, so the new-session path runs.
		"list-sessions": []byte(""),
		// killOrphanHiddenShells: no parked shells to reap.
		"list-panes": []byte(""),
		// savePaneIDs reads back pane ids; the values are persisted but
		// otherwise unused by these tests.
		"display-message": []byte("%10\n"),
	}}

	return e, database, runner, task, workDir
}

// TestEnsureTaskWindowExportsShellPaneEnv pins the regression this change fixes:
// after the inline split-window, the shell pane must receive a
// `send-keys ... "export WORKTREE_TASK_ID=... WORKTREE_PORT=... WORKTREE_PATH=..." Enter`
// (and a `clear` Enter) so that the interactive shell the user types into has
// the task-context env vars the worktree .envrc promises. A non-direnv user
// running `ty complete` in such a pane otherwise gets "task id is required".
func TestEnsureTaskWindowExportsShellPaneEnv(t *testing.T) {
	e, _, runner, task, workDir := setupShellEnvTest(t)

	ctx, cancel := context.WithTimeout(WithRunner(context.Background(), runner), 10*time.Second)
	defer cancel()

	target, created, err := e.EnsureTaskWindow(ctx, task, "", "context for fresh start")
	cancel()
	if err != nil {
		t.Fatalf("EnsureTaskWindow: %v", err)
	}
	if !created {
		t.Fatalf("EnsureTaskWindow reported created=false; want true (window should be new)")
	}
	if !strings.Contains(target, TmuxWindowName(task.ID)) {
		t.Fatalf("window target %q does not name the task window %q", target, TmuxWindowName(task.ID))
	}

	sendKeysCalls := findSendKeys(t, runner.snapshot())
	if len(sendKeysCalls) == 0 {
		t.Fatal("EnsureTaskWindow never ran send-keys on the shell pane; the env-export fix is missing")
	}

	wantEnvCmd := "export WORKTREE_TASK_ID=" + strconv.FormatInt(task.ID, 10) +
		" WORKTREE_PORT=" + strconv.Itoa(task.Port) +
		" WORKTREE_PATH=\"" + workDir + "\""
	if !exportedShellPane(t, sendKeysCalls, wantEnvCmd) {
		t.Errorf("shell pane env-export send-keys missing; want a call targeting <window>.1 with %q", wantEnvCmd)
	}
	if !clearedShellPane(t, sendKeysCalls, target) {
		t.Errorf("shell pane clear send-keys missing; want `send-keys -t %s.1 clear Enter`", target)
	}
	for _, call := range sendKeysCalls {
		// The default config dir resolves to ~/.claude, which is the default —
		// CLAUDE_CONFIG_DIR must NOT be appended (it would break MCP discovery
		// by sending Claude to ~/.claude/.claude.json instead of ~/.claude.json).
		if strings.Contains(call, "CLAUDE_CONFIG_DIR=") {
			t.Errorf("default task should not export CLAUDE_CONFIG_DIR; saw %q", call)
		}
	}
}

// TestEnsureTaskWindowExportsClaudeConfigDirInShellEnv pins the second branch of
// the fix: a task with a non-default CLAUDE_CONFIG_DIR (e.g. an ollama-backed
// step routing through a different Claude config) must export it into the
// shell pane too, matching ensureShellPane and the worktree .envrc.
func TestEnsureTaskWindowExportsClaudeConfigDirInShellEnv(t *testing.T) {
	e, _, runner, task, workDir := setupShellEnvTest(t)

	customDir := filepath.Join(t.TempDir(), "claude-config")
	// claude_config_dir is column-set on insert, so set it before CreateTask.
	task.ClaudeConfigDir = customDir
	if err := e.db.UpdateTask(task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	ctx, cancel := context.WithTimeout(WithRunner(context.Background(), runner), 10*time.Second)
	defer cancel()

	target, _, err := e.EnsureTaskWindow(ctx, task, "", "")
	cancel()
	if err != nil {
		t.Fatalf("EnsureTaskWindow: %v", err)
	}

	sendKeysCalls := findSendKeys(t, runner.snapshot())
	if len(sendKeysCalls) == 0 {
		t.Fatal("EnsureTaskWindow never ran send-keys on the shell pane")
	}

	wantEnvCmd := "export WORKTREE_TASK_ID=" + strconv.FormatInt(task.ID, 10) +
		" WORKTREE_PORT=" + strconv.Itoa(task.Port) +
		" WORKTREE_PATH=\"" + workDir + "\"" +
		" CLAUDE_CONFIG_DIR=\"" + customDir + "\""
	if !exportedShellPane(t, sendKeysCalls, wantEnvCmd) {
		t.Errorf("shell pane env-export send-keys missing CLAUDE_CONFIG_DIR; want a call targeting <window>.1 with %q", wantEnvCmd)
	}
	_ = target
}

// TestEnsureTaskWindowExportsLaunchWorkdirNotWorktreePath pins a subtle
// requirement the live end-to-end smoke exposed: WORKTREE_PATH must be the
// directory the agent actually starts in (the value launchWorkdir returned,
// `workDir`), NOT `task.WorktreePath`. For a task in a non-worktree project
// that the daemon has never queued, `task.WorktreePath` is still empty —
// `setupSharedWorkDir` only populates it during the daemon's dispatch path
// (`executeTask`), and EnsureTaskWindow is the interactive path that runs
// BEFORE the daemon dispatches. Using `task.WorktreePath` here would export
// `WORKTREE_PATH=""`, leaving the shell without the worktree root the
// worktree `.envrc` and the agent both rely on; using `workDir` exports the
// project dir the shell actually cd'd into via `split-window -c <workDir>`.
func TestEnsureTaskWindowExportsLaunchWorkdirNotWorktreePath(t *testing.T) {
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "tasks.db")
	t.Setenv("WORKTREE_DB_PATH", dbPath)
	t.Setenv("TY_MEMORY_GUARD", "off")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// Non-worktree project: `--no-git` in CLI terms. ProjectUsesWorktrees
	// returns false, so launchWorkdir returns the project dir without
	// requiring a recorded WorktreePath on the task.
	projectDir := t.TempDir()
	if err := database.CreateProject(&db.Project{Name: "nw", Path: projectDir, UseWorktrees: false}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	e := New(database, config.New(database))
	e.executorFactory.Register(&shellEnvProbeExecutor{name: shellEnvProbeExecutorName})

	// Deliberately created with NO WorktreePath — this is the state of a
	// backlog task the daemon has never dispatched. The bug condition is
	// that the shell pane ends up with WORKTREE_PATH="".
	task := &db.Task{
		Title:    "non-worktree interactive open",
		Status:   db.StatusBacklog,
		Project:  "nw",
		Executor: shellEnvProbeExecutorName,
		Port:     31998,
		// WorktreePath intentionally unset
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	runner := &recordingRunner{outputs: map[string][]byte{
		"list-windows":    []byte(""),
		"list-sessions":   []byte(""),
		"list-panes":      []byte(""),
		"display-message": []byte("%10\n"),
	}}

	ctx, cancel := context.WithTimeout(WithRunner(context.Background(), runner), 10*time.Second)
	defer cancel()
	target, _, err := e.EnsureTaskWindow(ctx, task, "", "")
	cancel()
	if err != nil {
		t.Fatalf("EnsureTaskWindow: %v", err)
	}

	// The exported WORKTREE_PATH MUST be the project dir (what launchWorkdir
	// returned as `workDir`), not task.WorktreePath (which is still "").
	wantEnvCmd := "export WORKTREE_TASK_ID=" + strconv.FormatInt(task.ID, 10) +
		" WORKTREE_PORT=" + strconv.Itoa(task.Port) +
		" WORKTREE_PATH=\"" + projectDir + "\""
	if !exportedShellPane(t, findSendKeys(t, runner.snapshot()), wantEnvCmd) {
		t.Errorf("non-worktree project: WORKTREE_PATH should be the launchWorkdir result %q, not task.WorktreePath (\"\")", projectDir)
	}
	_ = target
}

// TestEnsureTaskWindow_ExportedEnvVarsReachLiveShellPane is the end-to-end
// proof: it starts a real isolated tmux server, runs EnsureTaskWindow against
// it, and reads the shell pane back to confirm the exported env vars actually
// land in the interactive shell — not just that we issued send-keys, but that
// the shell ended up with WORKTREE_TASK_ID/PORT/PATH in its environment.
func TestEnsureTaskWindow_ExportedEnvVarsReachLiveShellPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required for the live shell-pane env-var regression test")
	}
	// macOS's per-user TMPDIR + Go's descriptive test dir exceed tmux's ~104
	// byte socket path limit; keep the socket under /tmp.
	t.Setenv("TMPDIR", "/tmp")
	// Isolate per test rather than relying on the package's tmuxtest.Main so
	// the kill-server cleanup cannot race another tmux test in the same run.
	tmuxRoot := t.TempDir()
	t.Setenv("TMUX_TMPDIR", tmuxRoot)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TASKYOU_TMUX_SOCKET", "default")
	t.Setenv("WORKTREE_SESSION_ID", "shell-env-test")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tmuxCmd(ctx, "kill-server").Run()
	})

	runTmux := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := tmuxCmd(ctx, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	e, _, _, task, workDir := setupShellEnvTest(t)
	// Use the live tmux server, not the recording runner.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	target, created, err := e.EnsureTaskWindow(ctx, task, "", "live env probe")
	if err != nil {
		t.Fatalf("EnsureTaskWindow: %v", err)
	}
	if !created {
		t.Fatalf("Want created=true, got false")
	}

	// Ensure the shell pane (pane .1) is the active pane, then drop the env
	// exports + `env` into it and capture what the shell prints. The export
	// step is the fix; `env` echoes the resulting environment so capture-pane
	// can read it back.
	shellPane := target + ".1"
	runTmux("select-pane", "-t", shellPane)
	// Give the shell a moment to come up before we send keys.
	time.Sleep(300 * time.Millisecond)

	// Should be idempotent if a prior test left the env exported; either way
	// `env` after this prints WORKTREE_TASK_ID/PORT/PATH.
	runTmux("send-keys", "-t", shellPane, "env", "Enter")
	time.Sleep(500 * time.Millisecond)

	captured := runTmux("capture-pane", "-t", shellPane, "-p", "-S", "-200")
	for _, want := range []string{
		"WORKTREE_TASK_ID=" + strconv.FormatInt(task.ID, 10),
		"WORKTREE_PORT=" + strconv.Itoa(task.Port),
		"WORKTREE_PATH=" + workDir,
	} {
		if !strings.Contains(captured, want) {
			t.Errorf("shell pane is missing %q\n— capture:\n%s", want, captured)
		}
	}
}

// exportedShellPane reports whether send-keys calls show an env-export command
// delivered to <window>.1 (the shell pane) with `Enter`, containing wantEnvCmd.
func exportedShellPane(t *testing.T, sendKeysCalls []string, wantEnvCmd string) bool {
	t.Helper()
	for _, call := range sendKeysCalls {
		// EnsureTaskWindow targets the shell pane as `<target>.1`.
		if !strings.Contains(call, ".1 ") {
			continue
		}
		if strings.Contains(call, wantEnvCmd) && strings.Contains(call, "Enter") {
			return true
		}
	}
	return false
}

// clearedShellPane reports whether send-keys calls show `clear Enter` to pane
// .1, matching the daemon path's last clean-screen step.
func clearedShellPane(t *testing.T, sendKeysCalls []string, target string) bool {
	t.Helper()
	wantTarget := "-t " + target + ".1"
	for _, call := range sendKeysCalls {
		if strings.Contains(call, wantTarget) && strings.Contains(call, " clear Enter") {
			return true
		}
	}
	return false
}

// findSendKeys returns the argv of every send-keys call the runner observed,
// joined for substring matching.
func findSendKeys(t *testing.T, calls [][]string) []string {
	t.Helper()
	var out []string
	for _, c := range calls {
		// Each recorded call begins with "tmux" (the binary name the runner
		// was asked to run); the user subcommand is c[1].
		if len(c) >= 2 && c[1] == "send-keys" {
			out = append(out, strings.Join(c, " "))
		}
	}
	return out
}
