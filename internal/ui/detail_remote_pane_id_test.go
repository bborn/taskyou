package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// recordingTmux installs a tmux that appends every invocation to a log and
// answers queries with values belonging to a DIFFERENT ty instance, which is
// what tmux returns when another client is the "current" one.
func recordingTmux(t *testing.T) (logPath string) {
	t.Helper()
	root := t.TempDir()
	logPath = filepath.Join(root, "calls.log")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> '" + logPath + "'\n" +
		"case \"$*\" in\n" +
		"  *split-window*) echo '%777';; \n" + // a split reports its own new pane
		"  *list-panes*) echo '%999'; echo '%888';; \n" +
		"  *'#{pane_id}'*) echo '%999';; \n" + // the OTHER instance's pane
		"  *'#{session_name}'*) echo 'task-ui-OTHER';; \n" +
		"esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(root, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// The pane this process draws into is $TMUX_PANE, by definition. Asking tmux for
// the "current" pane returns whichever client tmux considers active — with a
// second ty running, that is the other instance's pane. attachRemotePane then
// kills "leftover" panes around it and splits into it, so a racy answer here
// destroys another instance's panes. detail.go already documents this rule for
// updateTmuxPaneTitle; the remote path must follow it too.
func TestAttachRemotePaneUsesOwnPaneNotTmuxCurrent(t *testing.T) {
	t.Setenv("TMUX", "/tmp/fake,1,0")
	t.Setenv("TMUX_PANE", "%42")
	app, _ := refreshTestModel(t)
	calls := recordingTmux(t)

	m := &DetailModel{database: app.db, task: &db.Task{ID: 5350, PlacementTarget: "ol-agents"}, shellPaneHidden: true}
	m.attachRemotePane(executor.RemoteTaskLocation{Host: "ol-agents"})

	if m.tuiPaneID == "%999" {
		t.Fatal("adopted another instance's pane as its own TUI pane")
	}
	if m.tuiPaneID != "%42" {
		t.Fatalf("tuiPaneID = %q, want $TMUX_PANE (%%42)", m.tuiPaneID)
	}

	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "#{pane_id}") && !strings.Contains(line, "-t ") {
			t.Fatalf("unscoped pane-id query still issued: %q", line)
		}
		if strings.Contains(line, "#{session_name}") && !strings.Contains(line, "-t ") {
			t.Fatalf("unscoped session query still issued: %q", line)
		}
	}
}

// Killing "leftover" panes must never target a pane outside this instance's own
// session, or one ty wipes out another's executor panes.
func TestAttachRemotePaneNeverKillsOwnTuiPane(t *testing.T) {
	t.Setenv("TMUX", "/tmp/fake,1,0")
	t.Setenv("TMUX_PANE", "%42")
	app, _ := refreshTestModel(t)
	calls := recordingTmux(t)

	m := &DetailModel{database: app.db, task: &db.Task{ID: 5350, PlacementTarget: "ol-agents"}, shellPaneHidden: true}
	m.attachRemotePane(executor.RemoteTaskLocation{Host: "ol-agents"})

	data, _ := os.ReadFile(calls)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "kill-pane") && strings.Contains(line, "%42") {
			t.Fatalf("killed its own TUI pane: %q", line)
		}
	}
}
