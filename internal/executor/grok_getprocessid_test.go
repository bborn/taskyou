package executor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// realPATH is captured before any test prepends a fake-bin directory to PATH,
// so db.Open's migration can still reach the real `git` binary it shells out to
// (db.Open -> migrate -> ensurePersonalProject -> initGitRepo runs `git init`).
var realPATH = os.Getenv("PATH")

// writeFakeBin writes an executable shell script named `name` into `dir`.
func writeFakeBin(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeTmuxListPanes installs a `tmux` stub in `dir` that prints `panes` when
// invoked as `tmux list-panes ...` (the only invocation GetProcessID makes via
// tmuxctl.Agent; tmuxctl.Socket() normalizes "default" to "", so no "-L" is
// prepended during tests) and exits 1 for any other command.
func fakeTmuxListPanes(t *testing.T, dir, panes string) {
	t.Helper()
	panesFile := filepath.Join(dir, "panes.out")
	if err := os.WriteFile(panesFile, []byte(panes), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFakeBin(t, dir, "tmux", "if [ \"$1\" = \"list-panes\" ]; then\ncat "+panesFile+"\nexit 0\nfi\nexit 1\n")
}

// newGrokForGetProcessIDTest wires up a GrokExecutor backed by a temp DB, with
// fake tmux/ps/pgrep binaries on PATH. The real PATH is preserved after the
// fakes so db.Open's `git`-driven migration keeps working.
func newGrokForGetProcessIDTest(t *testing.T, panes, psComm, pgrepOut string) *GrokExecutor {
	t.Helper()
	bin := t.TempDir()
	fakeTmuxListPanes(t, bin, panes)
	writeFakeBin(t, bin, "ps", psComm)
	writeFakeBin(t, bin, "pgrep", pgrepOut)
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+realPATH)

	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	ex := New(database, &config.Config{})
	grok, ok := ex.GetExecutor(db.ExecutorGrok).(*GrokExecutor)
	if !ok || grok == nil {
		t.Fatalf("grok executor not registered: %T", ex.GetExecutor(db.ExecutorGrok))
	}
	return grok
}

// TestGrokGetProcessID pins GrokExecutor.GetProcessID after the fix that routes
// pane lookup through the shared findPanesForWindow helper (exact window-name
// field match) instead of the old `strings.Contains(target, windowName)`
// substring match over the whole "session:window:pane" triple. The exact-match
// semantics of the helper itself are covered by TestFindPanesForWindow; these
// cases guard GetProcessID's own behavior: that it does not re-inline a
// substring match (the prefix/superset cases), and that the ps/pgrep comm filter
// is preserved.
func TestGrokGetProcessID(t *testing.T) {
	tests := []struct {
		name   string
		panes  string
		psComm string // output of `ps -p <pid> -o comm=`
		pgrep  string // output of `pgrep -P <pid> grok`
		taskID int64
		want   int
	}{
		{
			// The reported bug: task-123's pane precedes task-12's pane in
			// tmux iteration order. The old substring match (`task-12` is a
			// substring of `task-123`) returned 12345 (task-123's pane); exact
			// window matching must return 67890 (task-12's own pane).
			name:   "prefix_collision_wrong_first_returns_correct_pid",
			panes:  "task-daemon-5000:task-123:0 12345\ntask-daemon-5000:task-12:0 67890",
			psComm: "echo grok",
			pgrep:  "exit 1",
			taskID: 12,
			want:   67890,
		},
		{
			// Only the superset-id task-123 is alive. The old substring match
			// matched task-123's pane (returned 12345); exact matching returns 0.
			name:   "only_superset_present_returns_zero",
			panes:  "task-daemon-5000:task-123:0 12345",
			psComm: "echo grok",
			pgrep:  "exit 1",
			taskID: 12,
			want:   0,
		},
		{
			// The session name itself contains the sought window name; the
			// window name differs. The old substring match over the whole
			// session:window:pane triple matched anyway; exact window-field
			// matching must NOT.
			name:   "session_name_contains_window_name_but_window_differs",
			panes:  "task-daemon-task-12:other-window:0 12345",
			psComm: "echo grok",
			pgrep:  "exit 1",
			taskID: 12,
			want:   0,
		},
		{
			// A pane whose matching window is found but whose process is not
			// grok, and has no grok child, must be skipped -> returns 0. This
			// preserves the cross-executor filter (e.g. a codex pane reusing the
			// window name) that the comm/pgrep filter is designed to reject.
			name:   "matching_window_non_grok_comm_no_child_returns_zero",
			panes:  "task-daemon-5000:task-12:0 67890",
			psComm: "echo codex",
			pgrep:  "exit 1",
			taskID: 12,
			want:   0,
		},
		{
			// When the matching pane's own comm is not grok but it has a grok
			// child process (pgrep prints a pid), GetProcessID returns the
			// child pid. Exercises the pgrep branch.
			name:   "matching_window_grok_child_pid_returned",
			panes:  "task-daemon-5000:task-12:0 67890",
			psComm: "echo sh",
			pgrep:  "echo 55555",
			taskID: 12,
			want:   55555,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grok := newGrokForGetProcessIDTest(t, tt.panes, tt.psComm, tt.pgrep)
			if got := grok.GetProcessID(tt.taskID); got != tt.want {
				t.Fatalf("GetProcessID(%d) = %d, want %d\npanes=%q",
					tt.taskID, got, tt.want, tt.panes)
			}
		})
	}
}

// TestGrokGetProcessID_TmuxFailureIsolated asserts that when the tmux stub
// itself exits non-zero on list-panes, GetProcessID returns 0 and never reaches
// the ps/pgrep filter.
func TestGrokGetProcessID_TmuxFailureIsolated(t *testing.T) {
	bin := t.TempDir()
	// tmux stub that always errors, regardless of subcommand.
	writeFakeBin(t, bin, "tmux", "exit 1\n")
	psMarker := filepath.Join(bin, "ps-called")
	writeFakeBin(t, bin, "ps", "printf 'called\\n' >> "+psMarker+"\ntrue\n")
	writeFakeBin(t, bin, "pgrep", "exit 1\n")
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+realPATH)

	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	ex := New(database, &config.Config{})
	grok, ok := ex.GetExecutor(db.ExecutorGrok).(*GrokExecutor)
	if !ok || grok == nil {
		t.Fatalf("grok executor not registered: %T", ex.GetExecutor(db.ExecutorGrok))
	}

	if got := grok.GetProcessID(12); got != 0 {
		t.Fatalf("GetProcessID(12) = %d, want 0 when tmux fails", got)
	}
	if _, err := os.Stat(psMarker); err == nil {
		t.Fatalf("ps was invoked even though tmux list-panes failed")
	}
}

// TestGrokGetProcessID_KillRoutesExactPID confirms the resolution Kill relies
// on: for two concurrent grok tasks with prefix-superset IDs sharing a daemon
// session, GetProcessID returns each task its own pane pid (so Kill signals the
// right process). Kill of a task with no live pane returns false without
// signalling.
func TestGrokGetProcessID_KillRoutesExactPID(t *testing.T) {
	// task-123's pane is iterated before task-12's.
	panes := "task-daemon-5000:task-123:0 12345\ntask-daemon-5000:task-12:0 67890"
	grok := newGrokForGetProcessIDTest(t, panes, "echo grok", "exit 1")

	if got := grok.GetProcessID(12); got != 67890 {
		t.Fatalf("GetProcessID(12) = %d, want 67890 (task-12's pane); "+
			"Kill(12) would otherwise SIGTERM task-123 (pid 12345)", got)
	}
	if got := grok.GetProcessID(123); got != 12345 {
		t.Fatalf("GetProcessID(123) = %d, want 12345 (task-123's own pane)", got)
	}

	// Kill of a task with no live pane returns false without signalling.
	grokNoPane := newGrokForGetProcessIDTest(t,
		"task-daemon-5000:task-123:0 12345", "echo grok", "exit 1")
	if killed := grokNoPane.Kill(12); killed {
		t.Fatalf("Kill(12) = true, want false when no pane matches task-12")
	}
}
