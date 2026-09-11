package ui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// assertNoUnscopedQueries fails when tmux was asked about "the current" pane,
// session or window without naming a target. Unscoped, tmux answers for the
// foremost client — another ty instance when more than one is attached.
func assertNoUnscopedQueries(t *testing.T, logPath string) {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		return // no tmux calls recorded
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "display-message") {
			continue
		}
		if !strings.Contains(line, "#{pane_id}") &&
			!strings.Contains(line, "#{session_name}") &&
			!strings.Contains(line, "#{window_height}") {
			continue
		}
		if !strings.Contains(line, "-t ") {
			t.Fatalf("unscoped tmux query: %q", line)
		}
	}
}

// viewTaskWindow splits its viewer from the TUI pane it names, and clears view
// panes around it. Taking another instance's pane would put the view, and that
// cleanup, in the other instance's window.
func TestViewTaskWindowUsesOwnPane(t *testing.T) {
	app, _ := refreshTestModel(t)
	t.Setenv("TMUX", "/tmp/fake,1,0")
	t.Setenv("TMUX_PANE", "%42")
	calls := recordingTmux(t)

	m := &DetailModel{database: app.db, task: &db.Task{ID: 900, WorktreePath: t.TempDir()}, shellPaneHidden: true}
	m.cachedWindowTarget = "task-daemon-1:@5"
	m.viewTaskWindow()

	if m.tuiPaneID == "%999" {
		t.Fatal("viewTaskWindow adopted another instance's pane")
	}
	if m.tuiPaneID != "%42" {
		t.Fatalf("tuiPaneID = %q, want %%42 from $TMUX_PANE", m.tuiPaneID)
	}
	assertNoUnscopedQueries(t, calls)
}

// A pane created by split-window must be identified by split-window's own
// output, not by asking which pane is active afterwards.
func TestShellPaneIdComesFromSplitNotActivePane(t *testing.T) {
	app, _ := refreshTestModel(t)
	t.Setenv("TMUX", "/tmp/fake,1,0")
	t.Setenv("TMUX_PANE", "%42")
	calls := recordingTmux(t)

	m := &DetailModel{database: app.db, task: &db.Task{ID: 901, WorktreePath: t.TempDir()}, tuiPaneID: "%42", claudePaneID: "%50"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.showShellPane(ctx)

	if m.workdirPaneID == "%999" {
		t.Fatal("adopted the globally-active pane as the new shell pane")
	}
	assertNoUnscopedQueries(t, calls)
}

// Focus means "is my own pane the active one", which must be asked about this
// pane specifically, not about whichever client tmux considers foremost.
func TestFocusStateAsksAboutOwnPane(t *testing.T) {
	t.Setenv("TMUX", "/tmp/fake,1,0")
	t.Setenv("TMUX_PANE", "%42")
	calls := recordingTmux(t)

	m := &DetailModel{tuiPaneID: "%42"}
	if cmd := m.focusStateCmd(); cmd != nil {
		cmd()
	}
	assertNoUnscopedQueries(t, calls)
}
