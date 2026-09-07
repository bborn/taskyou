package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestReloadWaitsForUnfinishedForms(t *testing.T) {
	m, _ := refreshTestModel(t)
	m.EnableReload("old")
	m.currentView = ViewNewTask
	m.Update(reloadTokenMsg{token: "new"})
	if !m.reloadPending || m.reloadReady {
		t.Fatal("reload interrupted form")
	}
	if m.beginReload() != nil {
		t.Fatal("form allowed reload")
	}
	m.currentView = ViewDashboard
	if m.beginReload() == nil || !m.reloadReady {
		t.Fatal("reload did not resume after form closed")
	}
}

func TestReloadReturnsBorrowedPanesBeforeQuitting(t *testing.T) {
	m, _ := refreshTestModel(t)
	task := &db.Task{ID: 42, Status: db.StatusBacklog}
	released := false
	m.selectedTask = task
	m.currentView = ViewDetail
	m.detailView = &DetailModel{task: task, executorLockRelease: func() { released = true }}
	m.reloadPending = true
	cleanup := m.beginReload()
	if cleanup == nil || m.reloadReady || released {
		t.Fatal("reload skipped asynchronous cleanup")
	}
	m.Update(cleanup())
	state, ready := m.ReloadState()
	if !ready || !released || !state.Detail || state.TaskID != 42 {
		t.Fatal("reload lost ownership ordering or task selection")
	}
}

func TestFailedPaneHandoffCancelsReload(t *testing.T) {
	m, _ := refreshTestModel(t)
	m.reloadPending, m.reloadPrepared = true, true
	detail := &DetailModel{task: &db.Task{ID: 1}}
	m.Update(detailCleanupMsg{failed: detail})
	if m.reloadPending || m.reloadReady || m.detailView != detail {
		t.Fatal("reload continued after failed handoff")
	}
}

func TestReloadTokenIsAcknowledged(t *testing.T) {
	m, _ := refreshTestModel(t)
	m.EnableReload("current")
	m.Update(reloadTokenMsg{token: "current"})
	if m.reloadPending {
		t.Fatal("new process replayed old request")
	}
}

func TestReloadWaitsForPendingSave(t *testing.T) {
	m, _ := refreshTestModel(t)
	m.reloadPending = true
	m.reloadWrites.Add(1)
	if m.beginReload() != nil || m.reloadReady {
		t.Fatal("reload interrupted save")
	}
	m.reloadWrites.Add(-1)
	if m.beginReload() == nil || !m.reloadReady {
		t.Fatal("reload did not resume after save")
	}
}
