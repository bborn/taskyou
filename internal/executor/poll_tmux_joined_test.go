package executor

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// Opening task detail moves both executor panes into the instance-specific UI
// session. Moving the final pane destroys the original daemon window, but the
// agent pane keeps its stable ID. pollTmuxSession must follow that ID or it will
// false-block a visibly working agent after its three missing-window checks.
func TestPollTmuxSessionFollowsAgentPaneMovedIntoInstanceUI(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required for the joined-pane regression test")
	}

	// macOS's default per-user temp path plus Go's descriptive test directory
	// exceeds tmux's Unix-socket path limit. Keep the fixture in t.TempDir, but
	// ask Go for that directory under the short system /tmp path.
	t.Setenv("TMPDIR", "/tmp")
	tmuxRoot := t.TempDir()
	t.Setenv("TMUX_TMPDIR", tmuxRoot)
	t.Setenv("TMUX", "")
	t.Setenv("WORKTREE_SESSION_ID", "poll-joined-test")

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
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tmuxCmd(ctx, "kill-server").Run()
	})

	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	workDir := t.TempDir()
	task := &db.Task{Title: "live joined agent", Status: db.StatusProcessing, WorktreePath: workDir}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	daemonSession := getDaemonSessionName()
	windowTarget := daemonSession + ":" + TmuxWindowName(task.ID)
	runTmux("new-session", "-d", "-s", daemonSession, "-n", TmuxWindowName(task.ID), "-c", workDir, "tail", "-f", "/dev/null")
	runTmux("split-window", "-d", "-t", windowTarget, "-c", workDir, "tail", "-f", "/dev/null")
	panes := strings.Fields(runTmux("list-panes", "-t", windowTarget, "-F", "#{pane_id}"))
	if len(panes) != 2 {
		t.Fatalf("executor window has %d panes, want 2: %v", len(panes), panes)
	}
	agentPane, shellPane := panes[0], panes[1]
	if err := database.UpdateTaskPaneIDs(task.ID, agentPane, shellPane); err != nil {
		t.Fatal(err)
	}

	uiSession := "task-ui-poll-joined-test"
	runTmux("new-session", "-d", "-s", uiSession, "-n", "tui", "tail", "-f", "/dev/null")
	runTmux("join-pane", "-d", "-s", agentPane, "-t", uiSession+":tui")
	runTmux("join-pane", "-d", "-s", shellPane, "-t", agentPane)

	probeCtx, probeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	if probeWindow(probeCtx, windowTarget, false) != windowGone {
		probeCancel()
		t.Fatal("moving both panes should destroy the source daemon window")
	}
	probeCancel()
	probeCtx, probeCancel = context.WithTimeout(context.Background(), 3*time.Second)
	if probeWindow(probeCtx, agentPane, false) != windowLive {
		probeCancel()
		t.Fatal("agent pane ID should remain live after moving into the UI session")
	}
	probeCancel()

	executor := New(database, &config.Config{})
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()
	resultCh := make(chan execResult, 1)
	go func() {
		resultCh <- executor.pollTmuxSession(pollCtx, task.ID, windowTarget)
	}()

	// The old hard-coded task-ui probe returned NeedsInput after three one-second
	// misses here. Staying active past that boundary proves the moved pane is used.
	select {
	case result := <-resultCh:
		t.Fatalf("live agent pane was treated as finished: %+v", result)
	case <-time.After(3500 * time.Millisecond):
	}

	// A real agent exit must retain the existing behavior. Executor panes launch
	// the agent as their command (there is no interactive shell to return to), so
	// stopping that command closes the pane. Once its stable ID is gone for three
	// checks, the task is parked for review.
	runTmux("send-keys", "-t", agentPane, "C-c")
	select {
	case result := <-resultCh:
		if !result.NeedsInput || result.Message != "Task needs review" {
			t.Fatalf("dead agent result = %+v, want needs-input review", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dead agent pane was not detected after the missing-pane threshold")
	}
}
