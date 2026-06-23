package db

import (
	"testing"
)

func TestGetTaskTimelineChronological(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := &Task{
		Title:   "Timeline Task",
		Body:    "body",
		Status:  StatusBacklog,
		Project: "personal",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Drive a realistic lifecycle: queued -> processing -> blocked -> done.
	transitions := []string{StatusQueued, StatusProcessing, StatusBlocked, StatusDone}
	for _, s := range transitions {
		if err := database.UpdateTaskStatus(task.ID, s); err != nil {
			t.Fatalf("update status %s: %v", s, err)
		}
	}

	entries, err := database.GetTaskTimeline(task.ID, 0)
	if err != nil {
		t.Fatalf("get timeline: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("expected timeline entries, got none")
	}

	// First entry must be the creation event.
	if entries[0].EventType != "task.created" {
		t.Errorf("expected first entry task.created, got %s", entries[0].EventType)
	}
	if entries[0].Label != "Created" {
		t.Errorf("expected first label 'Created', got %q", entries[0].Label)
	}

	// Entries must be chronologically non-decreasing.
	for i := 1; i < len(entries); i++ {
		if entries[i].CreatedAt.Time.Before(entries[i-1].CreatedAt.Time) {
			t.Errorf("entries out of order at %d: %v before %v",
				i, entries[i].CreatedAt.Time, entries[i-1].CreatedAt.Time)
		}
		if entries[i].CreatedAt.Time.IsZero() {
			t.Errorf("entry %d has zero timestamp", i)
		}
	}

	// A status transition entry must carry an arrow label.
	var sawTransition bool
	for _, e := range entries {
		if e.Label == "queued → processing" {
			sawTransition = true
		}
	}
	if !sawTransition {
		t.Errorf("expected a 'queued → processing' transition entry; got %+v", labels(entries))
	}

	// The terminal completion must be present.
	var sawCompleted bool
	for _, e := range entries {
		if e.EventType == "task.completed" {
			sawCompleted = true
		}
	}
	if !sawCompleted {
		t.Errorf("expected a task.completed entry; got %+v", labels(entries))
	}
}

func TestGetTaskTimelineRetry(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := &Task{Title: "Retry Task", Status: StatusBlocked, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := database.RetryTask(task.ID, "try again differently"); err != nil {
		t.Fatalf("retry task: %v", err)
	}

	entries, err := database.GetTaskTimeline(task.ID, 0)
	if err != nil {
		t.Fatalf("get timeline: %v", err)
	}

	var retry *TaskTimelineEntry
	for i := range entries {
		if entries[i].EventType == "task.retry" {
			retry = &entries[i]
		}
	}
	if retry == nil {
		t.Fatalf("expected a task.retry entry; got %+v", labels(entries))
	}
	if retry.Label != "Retried" {
		t.Errorf("expected label 'Retried', got %q", retry.Label)
	}
	if retry.Detail != "try again differently" {
		t.Errorf("expected retry feedback in detail, got %q", retry.Detail)
	}
}

func TestGetTaskTimelineLimit(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := &Task{Title: "Limit Task", Status: StatusBacklog, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Generate several status flips to produce many events.
	flips := []string{StatusQueued, StatusBacklog, StatusQueued, StatusBacklog, StatusQueued}
	for _, s := range flips {
		if err := database.UpdateTaskStatus(task.ID, s); err != nil {
			t.Fatalf("update status: %v", err)
		}
	}

	entries, err := database.GetTaskTimeline(task.ID, 2)
	if err != nil {
		t.Fatalf("get timeline: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with limit=2, got %d", len(entries))
	}
	// With a limit, we keep the newest events but still return them oldest-first.
	if entries[0].CreatedAt.Time.After(entries[1].CreatedAt.Time) {
		t.Errorf("limited entries not in chronological order")
	}
}

func TestGetTaskTimelineEmpty(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	entries, err := database.GetTaskTimeline(99999, 0)
	if err != nil {
		t.Fatalf("get timeline for missing task: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no entries for unknown task, got %d", len(entries))
	}
}

func TestTimelineLabel(t *testing.T) {
	cases := []struct {
		name       string
		eventType  string
		message    string
		metadata   string
		wantLabel  string
		wantDetail string
	}{
		{"created", "task.created", "My Task", "{}", "Created", "My Task"},
		{"completed", "task.completed", "My Task", "{}", "Completed", ""},
		{"blocked generic", "task.blocked", "status change", "{}", "Blocked", ""},
		{"blocked reason", "task.blocked", "needs input", "{}", "Blocked", "needs input"},
		{"retry", "task.retry", "do over", "{}", "Retried", "do over"},
		{
			"status transition",
			"task.updated", "My Task",
			`{"status":{"old":"queued","new":"processing"}}`,
			"queued → processing", "",
		},
		{
			"field update",
			"task.updated", "My Task",
			`{"title":{"old":"a","new":"b"}}`,
			"Updated", "title",
		},
		{"bare update", "task.updated", "My Task", "{}", "Updated", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			label, detail := timelineLabel(tc.eventType, tc.message, tc.metadata)
			if label != tc.wantLabel {
				t.Errorf("label: got %q want %q", label, tc.wantLabel)
			}
			if detail != tc.wantDetail {
				t.Errorf("detail: got %q want %q", detail, tc.wantDetail)
			}
		})
	}
}

func labels(entries []TaskTimelineEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Label
	}
	return out
}
