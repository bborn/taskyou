package ui

import (
	"github.com/bborn/workflow/internal/db"
	"os"
	"testing"
	"time"
)

func TestDetailRefreshReadsOutsideInputLoop(t *testing.T) {
	app, marker := refreshTestModel(t)
	task := &db.Task{Title: "Refresh detail", Status: db.StatusProcessing}
	if err := app.db.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := app.db.AppendTaskLog(task.ID, "output", "new output"); err != nil {
		t.Fatal(err)
	}
	detail := &DetailModel{task: task, database: app.db, claudePaneID: "%qa-only", workdirPaneID: "%qa-shell", lastPaneCheck: time.Now()}
	cmd := detail.Refresh()
	if cmd == nil {
		t.Fatal("no refresh scheduled")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("process ran on input loop")
	}
	if detail.Refresh() != nil {
		t.Fatal("overlapping refresh scheduled")
	}
	msg := cmd().(detailRefreshMsg)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("background process checks missing", err)
	}
	// Applying data must not read the DB or execute the pane-title command.
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	detail.database = nil
	titleCmd := detail.handleRefreshSnapshot(msg)
	if len(detail.logs) != 1 || detail.logs[0].Content != "new output" {
		t.Fatal("logs not delivered")
	}
	if titleCmd == nil {
		t.Fatal("pane title update not scheduled")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("result handler ran subprocess")
	}
	if detail.refreshInFlight {
		t.Fatal("refresh slot not released")
	}
}

func TestDetailRefreshPreservesNewerTaskAndLogState(t *testing.T) {
	old := &db.Task{ID: 1, Title: "old"}
	current := &db.Task{ID: 1, Title: "new"}
	detail := &DetailModel{task: current, lastLogCount: 10, refreshInFlight: true}
	detail.handleRefreshSnapshot(detailRefreshMsg{owner: detail, previousTask: old, task: old, previousLogCount: 0, logCount: 1, logs: []*db.TaskLog{{Content: "old"}}})
	if detail.task != current || detail.lastLogCount != 10 {
		t.Fatal("late refresh replaced newer state")
	}
	other := &DetailModel{task: old, refreshInFlight: true}
	other.handleRefreshSnapshot(detailRefreshMsg{owner: detail, previousTask: old, task: old})
	if !other.refreshInFlight {
		t.Fatal("old view result changed another view")
	}
}

func BenchmarkDetailRefreshScheduling(b *testing.B) {
	detail := &DetailModel{task: &db.Task{ID: 1}, database: &db.DB{}, lastPaneCheck: time.Now().Add(time.Hour)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		detail.refreshInFlight = false
		_ = detail.Refresh()
	}
}
