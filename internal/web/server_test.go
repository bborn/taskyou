package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// mockRunner records commands instead of executing them.
type mockRunner struct {
	calls     [][]string
	err       error
	outputVal []byte
	outputErr error
	// outputByCmd, when set, selects output by the first argument
	// (e.g. "list-windows", "list-panes"); falls back to outputVal.
	outputByCmd map[string][]byte
}

func (m *mockRunner) Run(name string, args ...string) error {
	m.calls = append(m.calls, append([]string{name}, args...))
	return m.err
}

func (m *mockRunner) Output(name string, args ...string) ([]byte, error) {
	m.calls = append(m.calls, append([]string{name}, args...))
	if m.outputErr != nil {
		return nil, m.outputErr
	}
	if m.outputByCmd != nil && len(args) > 0 {
		if out, ok := m.outputByCmd[args[0]]; ok {
			return out, nil
		}
	}
	return m.outputVal, nil
}

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func setupServer(t *testing.T) (*Server, *db.DB, *mockRunner) {
	t.Helper()
	database := setupTestDB(t)
	runner := &mockRunner{}
	srv := New(Config{
		Addr:      ":0",
		DB:        database,
		CmdRunner: runner,
	})
	return srv, database, runner
}

// --- Board ---

func TestHandleBoard_Empty(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/board", nil)
	w := httptest.NewRecorder()
	srv.handleBoard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var snap BoardSnapshot
	if err := json.NewDecoder(w.Body).Decode(&snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snap.Columns) != 4 {
		t.Fatalf("expected 4 columns, got %d", len(snap.Columns))
	}
}

func TestHandleBoard_WithTasks(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Test task", Status: db.StatusBacklog, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/board?limit=10", nil)
	w := httptest.NewRecorder()
	srv.handleBoard(w, req)

	var snap BoardSnapshot
	json.NewDecoder(w.Body).Decode(&snap)

	var backlog *BoardColumn
	for i := range snap.Columns {
		if snap.Columns[i].Status == db.StatusBacklog {
			backlog = &snap.Columns[i]
		}
	}
	if backlog == nil || backlog.Count != 1 {
		t.Fatal("expected 1 task in backlog")
	}
}

// --- Tasks CRUD ---

func TestHandleListTasks(t *testing.T) {
	srv, database, _ := setupServer(t)

	database.CreateTask(&db.Task{Title: "T1", Project: "personal"})
	database.CreateTask(&db.Task{Title: "T2", Project: "personal"})

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	w := httptest.NewRecorder()
	srv.handleListTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var tasks []*taskJSON
	json.NewDecoder(w.Body).Decode(&tasks)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestHandleCreateTask(t *testing.T) {
	srv, _, _ := setupServer(t)

	body := `{"title":"New task","type":"code","project":"personal"}`
	req := httptest.NewRequest("POST", "/api/tasks", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleCreateTask(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var task taskJSON
	json.NewDecoder(w.Body).Decode(&task)
	if task.Title != "New task" {
		t.Errorf("title = %q, want 'New task'", task.Title)
	}
}

func TestHandleCreateTask_Empty(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("POST", "/api/tasks", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.handleCreateTask(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleTaskDetail_OK(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Detail task", Body: "Some body", Status: db.StatusProcessing, Project: "personal"}
	database.CreateTask(task)
	database.AppendTaskLog(task.ID, "output", "hello world")

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/%d", task.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleTaskDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]json.RawMessage
	json.NewDecoder(w.Body).Decode(&resp)
	if _, ok := resp["task"]; !ok {
		t.Error("response missing 'task' key")
	}
	if _, ok := resp["logs"]; !ok {
		t.Error("response missing 'logs' key")
	}
}

func TestHandleTaskDetail_NotFound(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/tasks/9999", nil)
	req.SetPathValue("id", "9999")
	w := httptest.NewRecorder()
	srv.handleTaskDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleUpdateTask(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Old title", Project: "personal"}
	database.CreateTask(task)

	body := `{"title":"New title"}`
	req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/tasks/%d", task.ID), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleUpdateTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated taskJSON
	json.NewDecoder(w.Body).Decode(&updated)
	if updated.Title != "New title" {
		t.Errorf("title = %q, want 'New title'", updated.Title)
	}
}

func TestHandleDeleteTask(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Delete me", Project: "personal"}
	database.CreateTask(task)

	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/tasks/%d", task.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleDeleteTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Soft-delete: the row survives (recoverable) but is trashed and hidden from
	// the default listing.
	got, _ := database.GetTask(task.ID)
	if got == nil {
		t.Fatal("task row should survive a soft delete")
	}
	active, _ := database.ListTasks(db.ListTasksOptions{})
	for _, tk := range active {
		if tk.ID == task.ID {
			t.Error("trashed task should not appear in default listing")
		}
	}
	trashed, _ := database.ListTrashedTasks()
	if len(trashed) != 1 || trashed[0].ID != task.ID {
		t.Errorf("expected task in trash, got %+v", trashed)
	}
}

func TestTaskJSON_IncludesPortWorktreeExecutor(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Rich task", Status: db.StatusProcessing, Project: "personal"}
	database.CreateTask(task)
	task.Port = 3142
	task.WorktreePath = "/tmp/wt"
	database.UpdateTask(task)
	database.UpdateTaskPaneIDs(task.ID, "%7", "")

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	w := httptest.NewRecorder()
	srv.handleListTasks(w, req)

	var tasks []*taskJSON
	json.NewDecoder(w.Body).Decode(&tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	got := tasks[0]
	if got.Port != 3142 {
		t.Errorf("port = %d, want 3142", got.Port)
	}
	if got.WorktreePath != "/tmp/wt" {
		t.Errorf("worktree_path = %q, want /tmp/wt", got.WorktreePath)
	}
	if !got.HasExecutor {
		t.Error("has_executor = false, want true")
	}
}

// --- Task actions ---

func TestHandleExecuteTask(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Exec me", Status: db.StatusBacklog, Project: "personal"}
	database.CreateTask(task)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/execute", task.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleExecuteTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	updated, _ := database.GetTask(task.ID)
	if updated.Status != db.StatusQueued {
		t.Errorf("status = %q, want 'queued'", updated.Status)
	}
}

func TestHandleExecuteTask_AlreadyQueued(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Already queued", Status: db.StatusQueued, Project: "personal"}
	database.CreateTask(task)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/execute", task.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleExecuteTask(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestHandleCloseTask(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Close me", Status: db.StatusProcessing, Project: "personal"}
	database.CreateTask(task)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/close", task.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleCloseTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	updated, _ := database.GetTask(task.ID)
	if updated.Status != db.StatusDone {
		t.Errorf("status = %q, want 'done'", updated.Status)
	}
}

func TestHandleRetryTask(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Retry me", Status: db.StatusDone, Project: "personal"}
	database.CreateTask(task)

	body := `{"feedback":"try again please"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/retry", task.ID), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleRetryTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	updated, _ := database.GetTask(task.ID)
	if updated.Status != db.StatusQueued {
		t.Errorf("status = %q, want 'queued'", updated.Status)
	}
}

func TestHandlePinTask_Toggle(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Pin me", Project: "personal"}
	database.CreateTask(task)

	body := `{"toggle":true}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/pin", task.ID), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handlePinTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]bool
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp["pinned"] {
		t.Error("expected pinned=true after toggle from false")
	}
}

func TestHandleSetStatus(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Status change", Status: db.StatusBacklog, Project: "personal"}
	database.CreateTask(task)

	body := `{"status":"queued"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/status", task.ID), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleSetStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	updated, _ := database.GetTask(task.ID)
	if updated.Status != db.StatusQueued {
		t.Errorf("status = %q, want 'queued'", updated.Status)
	}
}

func TestHandleSetStatus_Invalid(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Bad status", Project: "personal"}
	database.CreateTask(task)

	body := `{"status":"invalid"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/status", task.ID), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleSetStatus(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleTaskInput_SendMessage(t *testing.T) {
	srv, database, runner := setupServer(t)

	task := &db.Task{Title: "Input task", Status: db.StatusProcessing, Project: "personal"}
	database.CreateTask(task)
	database.UpdateTaskPaneIDs(task.ID, "%42", "")

	body := `{"message":"hello"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/input", task.ID), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleTaskInput(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	expected := []string{"tmux", "send-keys", "-t", "%42", "hello", "Enter"}
	if fmt.Sprint(runner.calls[0]) != fmt.Sprint(expected) {
		t.Errorf("call = %v, want %v", runner.calls[0], expected)
	}
}

func TestHandleTaskInput_NoPaneID(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "No pane", Status: db.StatusBacklog, Project: "personal"}
	database.CreateTask(task)

	body := `{"message":"test"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/input", task.ID), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleTaskInput(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleTaskOutput_JoinsWrappedLines(t *testing.T) {
	srv, database, runner := setupServer(t)
	runner.outputVal = []byte("pane content")

	task := &db.Task{Title: "Out task", Status: db.StatusProcessing, Project: "personal"}
	database.CreateTask(task)
	database.UpdateTaskPaneIDs(task.ID, "%5", "")

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/%d/output", task.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleTaskOutput(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	expected := []string{"tmux", "capture-pane", "-t", "%5", "-p", "-J", "-S", "-200"}
	if fmt.Sprint(runner.calls[0]) != fmt.Sprint(expected) {
		t.Errorf("call = %v, want %v", runner.calls[0], expected)
	}
}

// --- Projects ---

func TestHandleListProjects(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/projects", nil)
	w := httptest.NewRecorder()
	srv.handleListProjects(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var projects []map[string]interface{}
	json.NewDecoder(w.Body).Decode(&projects)
	// Should have at least the default "personal" project
	if len(projects) < 1 {
		t.Fatal("expected at least 1 project")
	}
}

func TestHandleCreateProject(t *testing.T) {
	srv, _, _ := setupServer(t)

	body := `{"name":"testproj","path":"/tmp"}`
	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleCreateProject(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateProject_Duplicate(t *testing.T) {
	srv, _, _ := setupServer(t)

	body := `{"name":"personal","path":"/tmp"}`
	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleCreateProject(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestHandleDeleteProject_Personal(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("DELETE", "/api/projects/personal", nil)
	req.SetPathValue("name", "personal")
	w := httptest.NewRecorder()
	srv.handleDeleteProject(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

// --- Types ---

func TestHandleListTypes(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/types", nil)
	w := httptest.NewRecorder()
	srv.handleListTypes(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var types []map[string]interface{}
	json.NewDecoder(w.Body).Decode(&types)
	if len(types) < 1 {
		t.Fatal("expected at least 1 type")
	}
}

func TestHandleDeleteType_Builtin(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("DELETE", "/api/types/code", nil)
	req.SetPathValue("name", "code")
	w := httptest.NewRecorder()
	srv.handleDeleteType(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

// --- Events & Status ---

func TestHandleListEvents(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/events?limit=10", nil)
	w := httptest.NewRecorder()
	srv.handleListEvents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleStatus(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	srv.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want 'ok'", resp["status"])
	}
}

// --- Dependencies ---

func TestHandleBlockUnblock(t *testing.T) {
	srv, database, _ := setupServer(t)

	t1 := &db.Task{Title: "Blocker", Project: "personal"}
	t2 := &db.Task{Title: "Blocked", Project: "personal"}
	database.CreateTask(t1)
	database.CreateTask(t2)

	// Block
	body := fmt.Sprintf(`{"blocker_id":%d}`, t1.ID)
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/block", t2.ID), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", t2.ID))
	w := httptest.NewRecorder()
	srv.handleBlock(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("block: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Get deps
	req = httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/%d/deps", t2.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", t2.ID))
	w = httptest.NewRecorder()
	srv.handleGetDeps(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("deps: expected 200, got %d", w.Code)
	}

	// Unblock
	body = fmt.Sprintf(`{"blocker_id":%d}`, t1.ID)
	req = httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/unblock", t2.ID), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", t2.ID))
	w = httptest.NewRecorder()
	srv.handleUnblock(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unblock: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// --- Board snapshot unit test ---

func TestBuildBoardSnapshot(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "T1", Status: db.StatusBacklog},
		{ID: 2, Title: "T2", Status: db.StatusProcessing},
		{ID: 3, Title: "T3", Status: db.StatusDone},
		{ID: 4, Title: "T4", Status: db.StatusArchived},
	}
	snap := BuildBoardSnapshot(tasks, 50)
	if len(snap.Columns) != 4 {
		t.Fatalf("expected 4 columns, got %d", len(snap.Columns))
	}

	total := 0
	for _, col := range snap.Columns {
		total += col.Count
	}
	if total != 3 {
		t.Errorf("expected 3 total tasks (excluding archived), got %d", total)
	}
}

// --- CORS ---

func TestCORS(t *testing.T) {
	srv, _, _ := setupServer(t)

	// Test preflight
	req := httptest.NewRequest("OPTIONS", "/api/board", nil)
	w := httptest.NewRecorder()
	cors(http.HandlerFunc(srv.handleBoard)).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS header")
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
