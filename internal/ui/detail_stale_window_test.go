package ui

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/tmuxtest"
)

// A second ty opening a task in the moment after the first launched its agent
// must join that window, not kill it. Before the fix the agent pane, still
// `sh -c` with nothing exec'd yet, read as a dead shell, and two TUIs killed and
// relaunched task 5436 between them until it had no agent at all.
func TestStartingAgentWindowIsNotKilled(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	tmuxtest.Isolate(t)
	t.Setenv("WORKTREE_DB_PATH", filepath.Join(t.TempDir(), "tasks.db"))
	wt := t.TempDir()

	// The agent pane as EnsureTaskWindow launches it: argv sh -c <script>. `read`
	// is a builtin, so the pane's current command stays `sh`, as it does while a
	// real launch script runs. The shell pane beside it is a plain shell.
	agent := viewTmux(t, "new-session", "-d", "-s", fixtureDaemon, "-n", "task-9", "-c", wt,
		"-P", "-F", "#{pane_id}", "sh", "-c", "read x")
	window := viewTmux(t, "display-message", "-p", "-t", agent, "#{window_id}")
	viewTmux(t, "split-window", "-d", "-h", "-t", agent, "-c", wt, "sh")
	target := fixtureDaemon + ":" + window
	// macOS's /bin/sh reports itself as bash; either way it reads as a shell.
	waitForTmux(t, func() bool {
		return isShellCommand(viewTmux(t, "display-message", "-p", "-t", agent, "#{pane_current_command}"))
	}, "agent pane never reported a shell")

	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	m := &DetailModel{task: &db.Task{ID: 9, WorktreePath: wt}, database: database}

	if !m.windowHasLiveExecutor(target) {
		dumpTmux(t)
		t.Fatal("a window whose agent is still starting was judged dead")
	}
	if m.killStaleWindow(target) {
		t.Fatal("killStaleWindow killed a window with a starting agent")
	}
	if !viewTmuxOK("display-message", "-p", "-t", agent, "#{pane_id}") {
		t.Fatal("the agent pane is gone")
	}

	// Once the agent is gone and only the shell remains, the window is dead and
	// may be rebuilt.
	viewTmux(t, "kill-pane", "-t", agent)
	if m.windowHasLiveExecutor(target) {
		dumpTmux(t)
		t.Fatal("a window holding only a shell was judged live")
	}
	if !m.killStaleWindow(target) {
		t.Fatal("killStaleWindow refused a dead window")
	}
	if viewTmuxOK("list-panes", "-t", target) {
		t.Fatal("dead window still exists")
	}
}
