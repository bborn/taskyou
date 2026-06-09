package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// BenchmarkDockRefresh_Closed proves that navigating the board with the dock
// closed does zero work: refreshDockForSelection must short-circuit before any
// capture or allocation. This is the regression guard for "zero cost when closed".
func BenchmarkDockRefresh_Closed(b *testing.B) {
	f := &fakePaneController{snapshot: "● working"}
	kanban := NewKanbanBoard(120, 40)
	task := &db.Task{ID: 1, Title: "t", Status: db.StatusProcessing}
	kanban.SetTasks([]*db.Task{task})
	kanban.SelectTask(1)

	m := &AppModel{
		width:  120,
		height: 40,
		kanban: kanban,
		dock:   NewDockModel(f), // closed
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.refreshDockForSelection()
	}
	b.StopTimer()
	if len(f.captures) != 0 {
		b.Fatalf("closed dock captured %d times; must be 0", len(f.captures))
	}
}

// BenchmarkDockRefresh_OpenSnapshot measures the Go-side overhead of a snapshot
// refresh (the capture itself is faked). It exists to catch accidental
// per-refresh allocations in the state machine, not to measure tmux latency
// (that is measured against a real server in QA).
func BenchmarkDockRefresh_OpenSnapshot(b *testing.B) {
	f := &fakePaneController{snapshot: "● working\nline2\nline3"}
	kanban := NewKanbanBoard(120, 40)
	task := &db.Task{ID: 1, Title: "t", Status: db.StatusProcessing}
	kanban.SetTasks([]*db.Task{task})
	kanban.SelectTask(1)

	m := &AppModel{
		width:  120,
		height: 40,
		kanban: kanban,
		dock:   NewDockModel(f),
	}
	m.dock.Toggle() // open

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.refreshDockForSelection()
	}
}
