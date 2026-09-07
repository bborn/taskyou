package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
)

func TestDetailExitWaitsForPaneWorkerWithoutBlockingBoard(t *testing.T) {
	app, _ := refreshTestModel(t)
	released := make(chan struct{})
	detail := &DetailModel{task: &db.Task{ID: 1}, executorLockRelease: func() { close(released) }}
	app.detailView, app.currentView = detail, ViewDetail
	gate := make(chan struct{})
	worker := detail.paneCommand(func() tea.Msg { <-gate; return panesJoinedMsg{} })
	_, cleanup := app.updateDetail(tea.KeyMsg{Type: tea.KeyEsc})
	if cleanup == nil || app.detailView != nil || app.currentView != ViewDashboard {
		t.Fatal("exit did not immediately return to board")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cleanup() }()
	// Cleanup must wait even when a registered worker has not started yet.
	select {
	case <-released:
		t.Fatal("cleanup overtook registered pane work")
	default:
	}
	oldResult := make(chan tea.Msg, 1)
	go func() { oldResult <- worker() }()
	close(gate)
	select {
	case result := <-done:
		app.Update(result)
	case <-time.After(time.Second):
		t.Fatal("cleanup did not finish")
	}
	select {
	case <-released:
	default:
		t.Fatal("executor ownership not released")
	}
	app.Update(<-oldResult)
	if app.detailView != nil || app.currentView != ViewDashboard {
		t.Fatal("late pane result reopened detail")
	}
}

func TestDetailLoadWaitsForCleanupAndCanBeCancelled(t *testing.T) {
	app, _ := refreshTestModel(t)
	app.detailCleanupInFlight = true
	expected := errors.New("loaded after cleanup")
	app.Update(taskLoadedMsg{err: expected})
	if app.err != nil || app.pendingDetailLoad == nil {
		t.Fatal("task load overtook cleanup")
	}
	app.Update(detailCleanupMsg{})
	if app.err != expected || app.pendingDetailLoad != nil {
		t.Fatal("deferred load was not delivered")
	}

	app.err = nil
	app.currentView = ViewDetail
	app.detailCleanupInFlight = true
	app.Update(taskLoadedMsg{err: expected})
	app.updateDetail(tea.KeyMsg{Type: tea.KeyEsc})
	app.Update(detailCleanupMsg{})
	app.Update(taskLoadedMsg{err: expected}) // older request can also finish after Back
	if app.err != nil || app.pendingDetailLoad != nil {
		t.Fatal("cancelled load reopened detail")
	}
}

func TestPaneHealthRunsOutsideInputLoopAndRejectsStalePane(t *testing.T) {
	app, marker := refreshTestModel(t)
	t.Setenv("TMUX", "qa-test")
	detail := &DetailModel{task: &db.Task{ID: 1}, database: app.db, claudePaneID: "%qa-old", workdirPaneID: "%qa-shell"}
	cmd := detail.paneHealthCmd()
	if cmd == nil {
		t.Fatal("probe not scheduled")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("probe ran on input loop")
	}
	if detail.paneHealthCmd() != nil {
		t.Fatal("overlapping probe scheduled")
	}
	result := cmd().(detailPaneResultMsg)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("background probe missing")
	}
	detail.claudePaneID = "%qa-new"
	detail.Update(result)
	if detail.claudePaneID != "%qa-new" || detail.paneHealthInFlight {
		t.Fatal("stale probe modified pane or blocked future probes")
	}
}

func TestPaneReturnRecreatesVanishedDaemonSession(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "created")
	stub := "#!/bin/sh\ncase \"$1\" in\nnew-window) exit 1;;\nnew-session) touch '" + state + "';;\nlist-windows) if [ -f '" + state + "' ]; then echo '@qa:task-1'; else exit 1; fi;;\n*) exit 1;;\nesac\n"
	if err := os.WriteFile(filepath.Join(root, "tmux"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	detail := &DetailModel{task: &db.Task{ID: 1}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if got := detail.findOrCreateTaskWindow(ctx, "task-daemon-qa", "task-1"); got != "@qa" {
		t.Fatalf("destination not recovered: %q", got)
	}
}

func TestDetailExitWithSlowTmux(t *testing.T) {
	app, _ := refreshTestModel(t)
	root := t.TempDir()
	stub := []byte("#!/bin/sh\n/bin/sleep 0.03\nexit 1\n")
	if err := os.WriteFile(filepath.Join(root, "tmux"), stub, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "qa-stub")
	task := &db.Task{Title: "Slow pane fixture", Status: db.StatusBacklog}
	if err := app.db.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	detail := &DetailModel{task: task, database: app.db, claudePaneID: "%qa", tuiPaneID: "%qa-ui"}
	app.detailView, app.currentView = detail, ViewDetail
	start := time.Now()
	_, cmd := app.updateDetail(tea.KeyMsg{Type: tea.KeyEsc})
	t.Logf("Back scheduling: %.3f ms", float64(time.Since(start).Microseconds())/1000)
	start = time.Now()
	app.Update(cmd())
	t.Logf("Background cleanup: %.3f ms", float64(time.Since(start).Microseconds())/1000)
	if app.detailView != detail || app.currentView != ViewDetail {
		t.Fatal("failed handoff allowed another detail to replace the preserved pane")
	}
	if detail.claudePaneID != "%qa" {
		t.Fatal("failed destination destroyed executor pane state")
	}
}
