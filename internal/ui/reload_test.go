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

func TestReloadClosesViewBeforeQuitting(t *testing.T) {
	m, _ := refreshTestModel(t)
	task := &db.Task{ID: 42, Status: db.StatusBacklog}
	m.selectedTask = task
	m.currentView = ViewDetail
	detail := &DetailModel{task: task, viewerPaneID: "%qa-viewer"}
	m.detailView = detail
	m.reloadPending = true
	cleanup := m.beginReload()
	if cleanup == nil || m.reloadReady {
		t.Fatal("reload skipped asynchronous cleanup")
	}
	m.Update(cleanup())
	state, ready := m.ReloadState()
	if !ready || detail.viewerPaneID != "" || !state.Detail || state.TaskID != 42 {
		t.Fatal("reload lost the view cleanup ordering or the task selection")
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
