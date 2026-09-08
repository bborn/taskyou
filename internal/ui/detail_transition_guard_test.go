package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
)

// transitionTestModel builds an app sitting in the detail view of the first of
// three blocked tasks, with TMUX set so NewDetailModel takes the real async pane
// path. The pane command it returns is never run by the test, which is exactly
// what a slow join looks like to the update loop: the detail view exists, but
// panesJoinedMsg has not arrived yet.
func transitionTestModel(t *testing.T) (*AppModel, []*db.Task) {
	t.Helper()
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	app, _ := refreshTestModel(t)
	tasks := []*db.Task{
		{ID: 1, Title: "first", Status: db.StatusBlocked},
		{ID: 2, Title: "second", Status: db.StatusBlocked},
		{ID: 3, Title: "third", Status: db.StatusBlocked},
	}
	app.kanban.SetTasks(tasks)
	app.kanban.SelectTask(1)
	app.selectedTask = tasks[0]
	detail, _ := NewDetailModel(tasks[0], app.db, nil, 100, 40, false)
	app.detailView = detail
	app.currentView = ViewDetail
	return app, tasks
}

// A join that has not reported back must keep the transition guard closed. The
// guard exists to stop a second switch from starting while the first is still
// moving tmux panes around; clearing it when the *database* row loads reopens it
// within milliseconds, long before the ~0.5-2s join finishes, so queued keys each
// kick off another detach/join cycle and the executor pane visibly flickers.
func TestTaskTransitionGuardHeldUntilPanesJoined(t *testing.T) {
	app, tasks := transitionTestModel(t)

	if _, cmd := app.updateDetail(tea.KeyMsg{Type: tea.KeyCtrlDown}); cmd == nil {
		t.Fatal("first switch did not start")
	}
	if !app.transitionInProgress() {
		t.Fatal("guard not closed by the first switch")
	}

	// The row load finishes fast; the pane join has not.
	app.detailCleanupInFlight = false
	app.Update(taskLoadedMsg{task: tasks[1], revision: app.taskLoadRevision, focusExecutor: true})
	if app.detailView == nil || !app.detailView.paneLoading {
		t.Fatal("detail view is not waiting on an async pane join")
	}
	if !app.transitionInProgress() {
		t.Fatal("guard reopened once the row loaded, while the pane join was still running")
	}

	// A key arriving during the join must be dropped, not start a second cycle.
	before := app.taskLoadRevision
	if _, cmd := app.updateDetail(tea.KeyMsg{Type: tea.KeyCtrlDown}); cmd != nil {
		t.Fatal("second switch started while panes were still joining")
	}
	if app.taskLoadRevision != before {
		t.Fatal("second switch loaded another task mid-join")
	}

	// Once the join reports, navigation is available again.
	app.Update(panesJoinedMsg{claudePaneID: "%1"})
	if app.transitionInProgress() {
		t.Fatal("guard still closed after panes joined")
	}
	if _, cmd := app.updateDetail(tea.KeyMsg{Type: tea.KeyCtrlDown}); cmd == nil {
		t.Fatal("navigation not restored after the join completed")
	}
}

// Waiting on the daemon to build a window is an open-ended wait, so it must
// release the guard rather than wedge navigation behind an executor that may
// never appear.
func TestTaskTransitionGuardReleasedWhenWaitingForExecutor(t *testing.T) {
	app, tasks := transitionTestModel(t)
	app.updateDetail(tea.KeyMsg{Type: tea.KeyCtrlDown})
	app.detailCleanupInFlight = false
	app.Update(taskLoadedMsg{task: tasks[1], revision: app.taskLoadRevision})
	if !app.transitionInProgress() {
		t.Fatal("guard not held across the row load")
	}
	app.Update(paneWaitForExecutorMsg{})
	if app.transitionInProgress() {
		t.Fatal("guard held while merely waiting for the daemon's executor")
	}
}

// A detail view with no pane work to do (not under tmux) must not hold the guard
// at all, or every switch would wait for a message that never comes.
func TestTaskTransitionGuardReleasedWithoutPaneWork(t *testing.T) {
	t.Setenv("TMUX", "")
	app, _ := refreshTestModel(t)
	tasks := []*db.Task{
		{ID: 1, Title: "first", Status: db.StatusBlocked},
		{ID: 2, Title: "second", Status: db.StatusBlocked},
	}
	app.kanban.SetTasks(tasks)
	app.kanban.SelectTask(1)
	app.currentView = ViewDetail
	app.beginTaskTransition()
	app.Update(taskLoadedMsg{task: tasks[1], revision: app.taskLoadRevision})
	if app.detailView == nil || app.detailView.paneLoading {
		t.Fatal("expected a detail view with no pending pane work")
	}
	if app.transitionInProgress() {
		t.Fatal("guard held even though there was no pane work to wait for")
	}
}

// The guard must never be able to wedge navigation permanently: if the pane
// result is lost, it expires on its own.
func TestTaskTransitionGuardExpires(t *testing.T) {
	app, _ := transitionTestModel(t)
	app.updateDetail(tea.KeyMsg{Type: tea.KeyCtrlDown})
	if !app.transitionInProgress() {
		t.Fatal("guard not closed")
	}
	app.taskTransitionDeadline = app.taskTransitionDeadline.Add(-2 * taskTransitionTimeout)
	if app.transitionInProgress() {
		t.Fatal("guard outlived its deadline and wedged navigation")
	}
}

// detachDetail clears detailView and then waits on paneWork, so the pane result
// of the task being *left* arrives while the switch to the next task is still in
// flight. It must not be mistaken for the incoming task's panes.
func TestStalePaneResultDoesNotOpenGuardMidSwitch(t *testing.T) {
	app, _ := transitionTestModel(t)
	app.updateDetail(tea.KeyMsg{Type: tea.KeyCtrlDown})
	if app.detailView != nil {
		t.Fatal("detach did not release the detail view")
	}
	if !app.transitionInProgress() {
		t.Fatal("guard not closed by the switch")
	}
	// The outgoing task's join finally reports, with no detail view owning it.
	app.Update(panesJoinedMsg{claudePaneID: "%old"})
	if !app.transitionInProgress() {
		t.Fatal("outgoing task's pane result opened the guard mid-switch")
	}
}
