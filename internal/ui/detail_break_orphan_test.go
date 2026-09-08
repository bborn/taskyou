package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
)

// stubTmux puts a tmux on PATH that fails the named subcommands and succeeds
// (printing canned output) for everything else, so breakTmuxPanes can be driven
// down a specific failure branch without a tmux server.
func stubTmux(t *testing.T, failing ...string) {
	t.Helper()
	root := t.TempDir()
	script := "#!/bin/sh\n"
	for _, sub := range failing {
		script += "case \"$*\" in *" + sub + "*) exit 1;; esac\n"
	}
	// display-message drives window/pane lookups; a non-empty answer keeps
	// findOrCreateTaskWindow on its "found a window" path.
	script += "case \"$*\" in *display-message*) echo '@99';; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(root, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func breakTestModel() *DetailModel {
	return &DetailModel{
		task:            &db.Task{ID: 7, TmuxWindowID: "@99", WorktreePath: "/tmp/wt-7"},
		claudePaneID:    "%700",
		daemonSessionID: "task-daemon-1",
		tuiPaneID:       "%1",
	}
}

// When tmux cannot move the executor pane out of task-ui, the pane is still in
// the TUI window and a live Claude is still attached to it. Reporting a clean
// handoff there loses the pane: the board releases the executor lock, and the
// next join is free to kill it as a "leftover" pane.
func TestBreakKeepsPaneIDWhenPaneCouldNotLeaveTaskUI(t *testing.T) {
	stubTmux(t, "join-pane", "break-pane")
	m := breakTestModel()
	m.breakTmuxPanes(false, false)
	if m.claudePaneID == "" {
		t.Fatal("reported a clean handoff for a pane still sitting in task-ui")
	}
}

// The same branch when break-pane rescues the pane into a new daemon window: it
// really did leave task-ui, so the model must forget it.
func TestBreakClearsPaneIDWhenBreakPaneRescuesIt(t *testing.T) {
	stubTmux(t, "join-pane")
	m := breakTestModel()
	m.breakTmuxPanes(false, false)
	if m.claudePaneID != "" {
		t.Fatalf("pane %q left task-ui but is still tracked", m.claudePaneID)
	}
}

// A stuck pane must reach the user as the "could not return panes" state rather
// than a silent return to the board.
func TestDetachReportsFailureWhenPaneIsStuck(t *testing.T) {
	app, _ := refreshTestModel(t)
	detail := breakTestModel()
	app.detailView, app.currentView = detail, ViewDetail
	detail.claudePaneID = "%700"

	cleanup := app.detachDetail(false)
	if cleanup == nil {
		t.Fatal("no cleanup scheduled")
	}
	msg, ok := cleanup().(detailCleanupMsg)
	if !ok {
		t.Fatalf("unexpected cleanup message %T", msg)
	}
	if msg.failed == nil {
		t.Fatal("stuck executor pane reported as a clean handoff")
	}
	app.Update(msg)
	if app.currentView != ViewDetail || app.notification == "" {
		t.Fatal("user was not told the panes could not be returned")
	}
}

var _ tea.Msg = detailCleanupMsg{}
