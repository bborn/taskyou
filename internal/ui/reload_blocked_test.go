package ui

import (
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// A task switch holds the navigation guard until its panes report back. If that
// report is lost the guard expires by deadline — but only through
// transitionInProgress(). beginReload read the bare field, so a stuck guard
// silently blocked every future reload: a TUI left in a detail view never picked
// up a new binary, and no amount of re-requesting helped.
func TestReloadIsNotBlockedByAnExpiredTransition(t *testing.T) {
	app, _ := refreshTestModel(t)
	app.reloadEnabled = true
	app.reloadPending = true
	app.currentView = ViewDashboard

	app.beginTaskTransition()
	app.taskTransitionDeadline = time.Now().Add(-2 * taskTransitionTimeout) // report never arrived

	if app.transitionInProgress() {
		t.Fatal("guard should have expired")
	}
	if cmd := app.beginReload(); cmd == nil {
		t.Fatal("reload still blocked by a transition that has already expired")
	}
}

// While a switch really is in flight, the reload must wait rather than tear the
// panes out from under it.
func TestReloadWaitsForAnActiveTransition(t *testing.T) {
	app, _ := refreshTestModel(t)
	app.reloadEnabled = true
	app.reloadPending = true
	app.currentView = ViewDashboard
	app.beginTaskTransition()

	if cmd := app.beginReload(); cmd != nil {
		t.Fatal("reload ran while a task switch was genuinely in flight")
	}
}

// A TUI sitting in a detail view must still reload: beginReload hands the panes
// back first, and the cleanup result drives the second pass.
func TestReloadProceedsFromDetailView(t *testing.T) {
	app, _ := refreshTestModel(t)
	app.reloadEnabled = true
	app.reloadPending = true
	app.currentView = ViewDetail
	app.detailView = &DetailModel{task: &db.Task{ID: 7}}
	app.selectedTask = &db.Task{ID: 7}

	cmd := app.beginReload()
	if cmd == nil {
		t.Fatal("reload refused while the detail view was open")
	}
	if app.currentView != ViewDashboard || app.detailView != nil {
		t.Fatal("reload did not release the detail view before quitting")
	}
	if !app.reloadPrepared || app.reloadSnapshot.TaskID != 7 || !app.reloadSnapshot.Detail {
		t.Fatalf("reload snapshot did not capture the open task: %+v", app.reloadSnapshot)
	}
}
