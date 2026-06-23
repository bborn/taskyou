package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

func TestToPRStatusJSON(t *testing.T) {
	if got := toPRStatusJSON(""); got != nil {
		t.Errorf("empty JSON should yield nil, got %+v", got)
	}
	if got := toPRStatusJSON("not json"); got != nil {
		t.Errorf("invalid JSON should yield nil, got %+v", got)
	}

	info := &github.PRInfo{
		Number:     42,
		URL:        "https://github.com/test/repo/pull/42",
		State:      github.PRStateOpen,
		CheckState: github.CheckStatePassing,
		Mergeable:  "MERGEABLE",
		Additions:  120,
		Deletions:  34,
	}
	got := toPRStatusJSON(github.MarshalPRInfo(info))
	if got == nil {
		t.Fatal("expected non-nil PR status")
	}
	if got.Number != 42 || got.URL != info.URL {
		t.Errorf("number/url mismatch: %+v", got)
	}
	if got.State != "open" {
		t.Errorf("state = %q, want open", got.State)
	}
	if got.CheckState != "passing" {
		t.Errorf("check_state = %q, want passing", got.CheckState)
	}
	if got.Mergeable != "MERGEABLE" {
		t.Errorf("mergeable = %q, want MERGEABLE", got.Mergeable)
	}
	if got.Additions != 120 || got.Deletions != 34 {
		t.Errorf("diff stats = +%d -%d, want +120 -34", got.Additions, got.Deletions)
	}
}

func TestPRStateAndCheckStateStrings(t *testing.T) {
	stateCases := map[github.PRState]string{
		github.PRStateOpen:   "open",
		github.PRStateDraft:  "draft",
		github.PRStateMerged: "merged",
		github.PRStateClosed: "closed",
		github.PRState(""):   "",
	}
	for in, want := range stateCases {
		if got := prStateString(in); got != want {
			t.Errorf("prStateString(%q) = %q, want %q", in, got, want)
		}
	}

	checkCases := map[github.CheckState]string{
		github.CheckStatePassing: "passing",
		github.CheckStateFailing: "failing",
		github.CheckStatePending: "pending",
		github.CheckStateNone:    "",
	}
	for in, want := range checkCases {
		if got := checkStateString(in); got != want {
			t.Errorf("checkStateString(%q) = %q, want %q", in, got, want)
		}
	}
}

// The /api/tasks payload must carry the live PR badge so the web board can render
// state + checks without a separate request.
func TestTaskJSON_IncludesPRStatus(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "PR task", Status: db.StatusBlocked, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	info := &github.PRInfo{
		Number:     7,
		URL:        "https://github.com/test/repo/pull/7",
		State:      github.PRStateDraft,
		CheckState: github.CheckStatePending,
		Mergeable:  "UNKNOWN",
		Additions:  5,
		Deletions:  2,
	}
	if err := database.UpdateTaskPRInfo(task.ID, info.URL, info.Number, github.MarshalPRInfo(info)); err != nil {
		t.Fatalf("update pr info: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	w := httptest.NewRecorder()
	srv.handleListTasks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var tasks []*taskJSON
	if err := json.NewDecoder(w.Body).Decode(&tasks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	pr := tasks[0].PR
	if pr == nil {
		t.Fatal("expected pr payload, got nil")
	}
	if pr.State != "draft" || pr.CheckState != "pending" || pr.Number != 7 {
		t.Errorf("unexpected pr payload: %+v", pr)
	}
}

// A task with no PR must omit the pr field entirely (omitempty).
func TestTaskJSON_OmitsPRWhenAbsent(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := &db.Task{Title: "No PR", Status: db.StatusBacklog, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	w := httptest.NewRecorder()
	srv.handleListTasks(w, req)

	var raw []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("expected 1 task, got %d", len(raw))
	}
	if _, present := raw[0]["pr"]; present {
		t.Error("pr field should be omitted when no PR is associated")
	}
}

// BuildBoardSnapshot entries should also carry the PR badge for the board API.
func TestBuildBoardSnapshot_IncludesPR(t *testing.T) {
	database := setupTestDB(t)
	task := &db.Task{Title: "Board PR", Status: db.StatusBlocked, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	info := &github.PRInfo{Number: 9, URL: "u", State: github.PRStateMerged}
	if err := database.UpdateTaskPRInfo(task.ID, info.URL, info.Number, github.MarshalPRInfo(info)); err != nil {
		t.Fatalf("update pr info: %v", err)
	}

	tasks, err := database.ListTasks(db.ListTasksOptions{IncludeClosed: true, Limit: 100})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	snap := BuildBoardSnapshot(tasks, 50)

	var found *prStatusJSON
	for _, col := range snap.Columns {
		for _, entry := range col.Tasks {
			if entry.ID == task.ID {
				found = entry.PR
			}
		}
	}
	if found == nil {
		t.Fatal("expected PR badge on board entry")
	}
	if found.State != "merged" {
		t.Errorf("state = %q, want merged", found.State)
	}
}
