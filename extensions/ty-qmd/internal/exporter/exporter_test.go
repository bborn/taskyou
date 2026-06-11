package exporter

import (
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

func TestExportBasicTask(t *testing.T) {
	exp := New(false, 0)

	created := time.Date(2025, 3, 10, 12, 0, 0, 0, time.UTC)
	task := &db.Task{
		ID:        42,
		Title:     "Fix authentication bug",
		Body:      "Users are getting logged out unexpectedly.",
		Status:    "done",
		Type:      "code",
		Project:   "myapp",
		Tags:      "auth,bugfix",
		CreatedAt: db.LocalTime{Time: created},
	}

	md := exp.Export(task, nil)

	// Check frontmatter
	if !strings.Contains(md, "task_id: 42") {
		t.Error("missing task_id in frontmatter")
	}
	if !strings.Contains(md, "project: myapp") {
		t.Error("missing project in frontmatter")
	}
	if !strings.Contains(md, "status: done") {
		t.Error("missing status in frontmatter")
	}
	if !strings.Contains(md, "type: code") {
		t.Error("missing type in frontmatter")
	}
	if !strings.Contains(md, "created: 2025-03-10") {
		t.Error("missing created date in frontmatter")
	}
	if !strings.Contains(md, "  - auth") {
		t.Error("missing auth tag")
	}
	if !strings.Contains(md, "  - bugfix") {
		t.Error("missing bugfix tag")
	}

	// Check title and body
	if !strings.Contains(md, "# Fix authentication bug") {
		t.Error("missing title")
	}
	if !strings.Contains(md, "## Description") {
		t.Error("missing description section")
	}
	if !strings.Contains(md, "Users are getting logged out unexpectedly.") {
		t.Error("missing body content")
	}
}

func TestExportWithCompletedAt(t *testing.T) {
	exp := New(false, 0)

	created := time.Date(2025, 3, 10, 12, 0, 0, 0, time.UTC)
	completed := db.LocalTime{Time: time.Date(2025, 3, 11, 14, 0, 0, 0, time.UTC)}
	task := &db.Task{
		ID:          1,
		Title:       "Done task",
		Status:      "done",
		CreatedAt:   db.LocalTime{Time: created},
		CompletedAt: &completed,
	}

	md := exp.Export(task, nil)

	if !strings.Contains(md, "completed: 2025-03-11") {
		t.Error("missing completed date in frontmatter")
	}
}

func TestExportWithSummary(t *testing.T) {
	exp := New(false, 0)

	task := &db.Task{
		ID:        1,
		Title:     "Task with summary",
		Status:    "done",
		Summary:   "Implemented the feature successfully.",
		CreatedAt: db.LocalTime{Time: time.Now()},
	}

	md := exp.Export(task, nil)

	if !strings.Contains(md, "## Summary") {
		t.Error("missing summary section")
	}
	if !strings.Contains(md, "Implemented the feature successfully.") {
		t.Error("missing summary content")
	}
}

func TestExportWithLogs(t *testing.T) {
	exp := New(true, 100)

	task := &db.Task{
		ID:        1,
		Title:     "Task with logs",
		Status:    "done",
		CreatedAt: db.LocalTime{Time: time.Now()},
	}

	logTime := time.Date(2025, 3, 10, 15, 30, 0, 0, time.UTC)
	logs := []*db.TaskLog{
		{ID: 1, TaskID: 1, LineType: "output", Content: "Starting build...", CreatedAt: db.LocalTime{Time: logTime}},
		{ID: 2, TaskID: 1, LineType: "error", Content: "Build failed", CreatedAt: db.LocalTime{Time: logTime.Add(time.Minute)}},
	}

	md := exp.Export(task, logs)

	if !strings.Contains(md, "## Activity Log") {
		t.Error("missing activity log section")
	}
	if !strings.Contains(md, "[output]: Starting build...") {
		t.Error("missing first log entry")
	}
	if !strings.Contains(md, "[error]: Build failed") {
		t.Error("missing second log entry")
	}
}

func TestExportLogsDisabled(t *testing.T) {
	exp := New(false, 0)

	task := &db.Task{
		ID:        1,
		Title:     "Task",
		Status:    "done",
		CreatedAt: db.LocalTime{Time: time.Now()},
	}

	logs := []*db.TaskLog{
		{ID: 1, TaskID: 1, LineType: "output", Content: "Should not appear", CreatedAt: db.LocalTime{Time: time.Now()}},
	}

	md := exp.Export(task, logs)

	if strings.Contains(md, "Activity Log") {
		t.Error("activity log should not appear when includeLogs=false")
	}
}

func TestExportLogsTruncation(t *testing.T) {
	exp := New(true, 1)

	task := &db.Task{
		ID:        1,
		Title:     "Task",
		Status:    "done",
		CreatedAt: db.LocalTime{Time: time.Now()},
	}

	logs := []*db.TaskLog{
		{ID: 1, TaskID: 1, LineType: "output", Content: "First", CreatedAt: db.LocalTime{Time: time.Now()}},
		{ID: 2, TaskID: 1, LineType: "output", Content: "Second", CreatedAt: db.LocalTime{Time: time.Now()}},
	}

	md := exp.Export(task, logs)

	if !strings.Contains(md, "First") {
		t.Error("expected first log entry")
	}
	if strings.Contains(md, "Second") {
		t.Error("second log entry should be truncated by maxLogLines=1")
	}
}

func TestExportLongLogMessage(t *testing.T) {
	exp := New(true, 100)

	task := &db.Task{
		ID:        1,
		Title:     "Task",
		Status:    "done",
		CreatedAt: db.LocalTime{Time: time.Now()},
	}

	longMsg := strings.Repeat("x", 600)
	logs := []*db.TaskLog{
		{ID: 1, TaskID: 1, LineType: "output", Content: longMsg, CreatedAt: db.LocalTime{Time: time.Now()}},
	}

	md := exp.Export(task, logs)

	if !strings.Contains(md, "...") {
		t.Error("expected long message to be truncated with '...'")
	}
	// Should not contain the full 600-char message
	if strings.Contains(md, longMsg) {
		t.Error("expected message to be truncated, but full message found")
	}
}
