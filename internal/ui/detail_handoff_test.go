package ui

import (
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
	detail := &DetailModel{task: &db.Task{ID: 1}, viewerPaneID: "%qa-viewer"}
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
	case <-done:
		t.Fatal("cleanup overtook registered pane work")
	case <-time.After(50 * time.Millisecond):
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
	if detail.viewerPaneID != "" {
		t.Fatal("view not closed")
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

// Back must return to the board at once even when tmux is slow and failing,
// and the cleanup cannot fail in a way that keeps the view: nothing was
// borrowed, so there is nothing left to give back.
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
	detail := &DetailModel{task: task, database: app.db, claudePaneID: "%qa", viewerPaneID: "%qa-viewer", tuiPaneID: "%qa-ui"}
	app.detailView, app.currentView = detail, ViewDetail
	start := time.Now()
	_, cmd := app.updateDetail(tea.KeyMsg{Type: tea.KeyEsc})
	t.Logf("Back scheduling: %.3f ms", float64(time.Since(start).Microseconds())/1000)
	if app.detailView != nil || app.currentView != ViewDashboard {
		t.Fatal("Back did not return to the board")
	}
	start = time.Now()
	app.Update(cmd())
	t.Logf("Background cleanup: %.3f ms", float64(time.Since(start).Microseconds())/1000)
	if app.detailView != nil || app.currentView != ViewDashboard || app.detailCleanupInFlight {
		t.Fatal("cleanup against a failing tmux did not finish on the board")
	}
	if detail.viewerPaneID != "" || detail.claudePaneID != "" {
		t.Fatal("view state survived cleanup")
	}
}
