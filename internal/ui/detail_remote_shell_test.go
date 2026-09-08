package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

func TestRemoteAttachInstallsNavigationAndFocusesExecutor(t *testing.T) {
	app, _ := refreshTestModel(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "commands")
	t.Setenv("TY_TEST_TMUX_LOG", logPath)
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TY_TEST_TMUX_LOG"
case "$1" in
 display-message) case "$*" in *'#{session_name}'*) echo task-ui;; *'#{pane_id}'*) echo %0;; esac ;;
 list-panes) echo %0 ;;
 split-window) case "$*" in *'-h '*) echo %91;; *) echo %90;; esac ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "test")
	t.Setenv("TMUX_PANE", "%0")
	m := &DetailModel{database: app.db, task: &db.Task{ID: 42, DaemonSession: "task-daemon-7", PlacementTarget: "remote"}, shellPaneHidden: true, focusExecutorOnJoin: true}
	if id := m.attachRemotePane(executor.RemoteTaskLocation{Host: "remote"}); id != "%90" {
		t.Fatalf("attach returned %q", id)
	}
	if m.ClaudePaneID() != "%90" || m.claudePaneID != "" {
		t.Fatal("remote focus must target the SSH pane without treating it as a borrowed local agent")
	}
	commands, _ := os.ReadFile(logPath)
	if strings.Contains(string(commands), "sleep 0.3") {
		t.Fatal("task navigation still depends on a fixed attach delay")
	}
	for _, key := range []string{"S-Down", "S-Right", "S-Up", "S-Left", "M-S-Up", "M-S-Down"} {
		if !strings.Contains(string(commands), "bind-key -T root "+key) {
			t.Errorf("remote attach did not install %s", key)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(string(commands)), "select-pane -t %90") {
		t.Fatal("remote executor did not receive requested focus")
	}
	sshLog := filepath.Join(dir, "ssh-called")
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\nprintf called >> '"+sshLog+"'\necho %92\n"), 0700); err != nil {
		t.Fatal(err)
	}
	m.executor = executor.New(app.db, config.New(app.db))
	cmd := m.ToggleShellPane()
	if cmd == nil {
		t.Fatal("remote toggle did not schedule background work")
	}
	if _, err := os.Stat(sshLog); !os.IsNotExist(err) {
		t.Fatal("shell toggle ran SSH on the input loop")
	}
	for _, child := range cmd().(tea.BatchMsg) {
		result := child()
		if _, ok := result.(detailPaneResultMsg); ok {
			m.Update(result)
		}
	}
	if m.remoteShellPaneID != "%91" || m.workdirPaneID != "" || m.shellPaneHidden || m.paneLoading {
		t.Fatalf("remote shell was not shown independently: remote=%q local=%q hidden=%v loading=%v error=%q", m.remoteShellPaneID, m.workdirPaneID, m.shellPaneHidden, m.paneLoading, m.paneError)
	}
	m.remoteShellPaneID = "%91"
	m.closeRemotePane(false)
	commands, _ = os.ReadFile(logPath)
	if !strings.Contains(string(commands), "kill-pane -t %91") || m.remoteShellPaneID != "" {
		t.Fatal("closing the remote view leaked its shell SSH pane")
	}
	if strings.Contains(string(commands), "break-pane") {
		t.Fatal("remote views must never be returned to a local daemon")
	}
}

func TestPaneTaskNavigationDefersFocusUntilTaskIsLoaded(t *testing.T) {
	for _, tc := range []struct {
		key   tea.KeyType
		start int
		focus bool
	}{
		{tea.KeyCtrlDown, 0, true}, {tea.KeyCtrlUp, 1, true},
		{tea.KeyDown, 0, false}, {tea.KeyUp, 1, false},
	} {
		t.Run(tea.KeyMsg{Type: tc.key}.String(), func(t *testing.T) {
			app, _ := refreshTestModel(t)
			tasks := []*db.Task{{Title: "first", Status: db.StatusBacklog}, {Title: "second", Status: db.StatusBacklog}}
			for _, task := range tasks {
				if err := app.db.CreateTask(task); err != nil {
					t.Fatal(err)
				}
			}
			app.kanban.SetTasks(tasks)
			app.kanban.selectedRow = tc.start
			_, cmd := app.updateDetail(tea.KeyMsg{Type: tc.key})
			if cmd == nil {
				t.Fatal("navigation did not load another task")
			}
			loaded, ok := cmd().(taskLoadedMsg)
			if !ok || loaded.err != nil || loaded.focusExecutor != tc.focus {
				t.Fatalf("task load did not carry focus intent: %+v", loaded)
			}
		})
	}
}
