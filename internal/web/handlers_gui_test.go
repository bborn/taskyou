package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// mockSessions is a test double for SessionManager.
type mockSessions struct {
	target    string
	created   bool
	err       error
	ensured   []int64
	available []string
	all       []string
}

func (m *mockSessions) EnsureTaskWindow(ctx context.Context, task *db.Task, sessionID, handoff string) (string, bool, error) {
	m.ensured = append(m.ensured, task.ID)
	return m.target, m.created, m.err
}

func (m *mockSessions) AvailableExecutors() []string { return m.available }
func (m *mockSessions) AllExecutors() []string       { return m.all }

func setupServerWithSessions(t *testing.T, sessions SessionManager) (*Server, *db.DB, *mockRunner) {
	t.Helper()
	database := setupTestDB(t)
	runner := &mockRunner{}
	srv := New(Config{
		Addr:      ":0",
		DB:        database,
		CmdRunner: runner,
		Sessions:  sessions,
	})
	return srv, database, runner
}

func createTestTask(t *testing.T, database *db.DB, task *db.Task) *db.Task {
	t.Helper()
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

// --- Settings ---

func TestHandleGetSettings_FiltersSecrets(t *testing.T) {
	srv, database, _ := setupServer(t)
	if err := database.SetSetting("theme", "dark"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSetting("anthropic_api_key", "sk-secret"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/settings", nil)
	w := httptest.NewRecorder()
	srv.handleGetSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var settings map[string]string
	if err := json.NewDecoder(w.Body).Decode(&settings); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if settings["theme"] != "dark" {
		t.Errorf("expected theme=dark, got %q", settings["theme"])
	}
	if _, exists := settings["anthropic_api_key"]; exists {
		t.Error("secret setting leaked through GET /api/settings")
	}
}

func TestHandleUpdateSettings(t *testing.T) {
	srv, database, _ := setupServer(t)

	body := strings.NewReader(`{"theme":"light","projects_dir":"/tmp/projects"}`)
	req := httptest.NewRequest("PATCH", "/api/settings", body)
	w := httptest.NewRecorder()
	srv.handleUpdateSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if v, _ := database.GetSetting("theme"); v != "light" {
		t.Errorf("expected theme=light, got %q", v)
	}
	if v, _ := database.GetSetting("projects_dir"); v != "/tmp/projects" {
		t.Errorf("expected projects_dir set, got %q", v)
	}
}

func TestHandleUpdateSettings_RejectsSecrets(t *testing.T) {
	srv, database, _ := setupServer(t)

	body := strings.NewReader(`{"anthropic_api_key":"sk-evil"}`)
	req := httptest.NewRequest("PATCH", "/api/settings", body)
	w := httptest.NewRecorder()
	srv.handleUpdateSettings(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	if v, _ := database.GetSetting("anthropic_api_key"); v != "" {
		t.Errorf("secret setting should not have been written, got %q", v)
	}
}

func TestHandleUpdateSettings_InvalidBody(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("PATCH", "/api/settings", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	srv.handleUpdateSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Attachments ---

func addAttachmentRequestBody(filename, data string) *strings.Reader {
	encoded := base64.StdEncoding.EncodeToString([]byte(data))
	return strings.NewReader(fmt.Sprintf(`{"filename":%q,"data":%q}`, filename, encoded))
}

func TestAttachmentLifecycle(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "with attachment", Status: db.StatusBacklog})

	// Upload
	req := httptest.NewRequest("POST", "/api/tasks/1/attachments", addAttachmentRequestBody("notes.txt", "hello world"))
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleAddAttachment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var uploaded map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	attachmentID := int64(uploaded["id"].(float64))
	if uploaded["filename"] != "notes.txt" {
		t.Errorf("expected filename notes.txt, got %v", uploaded["filename"])
	}

	// List
	req = httptest.NewRequest("GET", "/api/tasks/1/attachments", nil)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w = httptest.NewRecorder()
	srv.handleListAttachments(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var list []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(list))
	}

	// Download
	req = httptest.NewRequest("GET", "/api/attachments/1", nil)
	req.SetPathValue("id", fmt.Sprint(attachmentID))
	w = httptest.NewRecorder()
	srv.handleGetAttachment(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("download: expected 200, got %d", w.Code)
	}
	if w.Body.String() != "hello world" {
		t.Errorf("expected body 'hello world', got %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("expected text/plain content type, got %q", ct)
	}

	// Delete
	req = httptest.NewRequest("DELETE", "/api/attachments/1", nil)
	req.SetPathValue("id", fmt.Sprint(attachmentID))
	w = httptest.NewRecorder()
	srv.handleDeleteAttachment(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d", w.Code)
	}

	// Download after delete
	req = httptest.NewRequest("GET", "/api/attachments/1", nil)
	req.SetPathValue("id", fmt.Sprint(attachmentID))
	w = httptest.NewRecorder()
	srv.handleGetAttachment(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w.Code)
	}
}

func TestHandleAddAttachment_Invalid(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "t", Status: db.StatusBacklog})

	cases := []struct {
		name string
		body string
		want int
	}{
		{"missing filename", `{"data":"aGk="}`, http.StatusBadRequest},
		{"missing data", `{"filename":"a.txt"}`, http.StatusBadRequest},
		{"bad base64", `{"filename":"a.txt","data":"!!!"}`, http.StatusBadRequest},
		{"empty payload", `{"filename":"a.txt","data":""}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/tasks/1/attachments", strings.NewReader(tc.body))
			req.SetPathValue("id", fmt.Sprint(task.ID))
			w := httptest.NewRecorder()
			srv.handleAddAttachment(w, req)
			if w.Code != tc.want {
				t.Errorf("expected %d, got %d", tc.want, w.Code)
			}
		})
	}
}

// --- Executors ---

func TestHandleListExecutors(t *testing.T) {
	sessions := &mockSessions{
		available: []string{"claude"},
		all:       []string{"claude", "codex", "gemini"},
	}
	srv, _, _ := setupServerWithSessions(t, sessions)

	req := httptest.NewRequest("GET", "/api/executors", nil)
	w := httptest.NewRecorder()
	srv.handleListExecutors(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var executors []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&executors); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(executors) != 3 {
		t.Fatalf("expected 3 executors, got %d", len(executors))
	}
	byName := map[string]map[string]interface{}{}
	for _, e := range executors {
		byName[e["name"].(string)] = e
	}
	if byName["claude"]["available"] != true {
		t.Error("claude should be available")
	}
	if byName["codex"]["available"] != false {
		t.Error("codex should be unavailable")
	}
}

func TestHandleListExecutors_NotConfigured(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/executors", nil)
	w := httptest.NewRecorder()
	srv.handleListExecutors(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// --- Autocomplete ---

func TestHandleAutocomplete_Unavailable(t *testing.T) {
	srv, _, _ := setupServer(t)
	t.Setenv("ANTHROPIC_API_KEY", "")

	body := strings.NewReader(`{"input":"fix the bug in"}`)
	req := httptest.NewRequest("POST", "/api/autocomplete", body)
	w := httptest.NewRecorder()
	srv.handleAutocomplete(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without API key, got %d", w.Code)
	}
}

func TestHandleAutocomplete_RequiresInput(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("POST", "/api/autocomplete", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.handleAutocomplete(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Latest logs ---

func TestHandleLatestLogs(t *testing.T) {
	srv, database, _ := setupServer(t)
	task1 := createTestTask(t, database, &db.Task{Title: "one", Status: db.StatusProcessing})
	task2 := createTestTask(t, database, &db.Task{Title: "two", Status: db.StatusProcessing})
	if err := database.AppendTaskLog(task1.ID, "text", "older"); err != nil {
		t.Fatal(err)
	}
	if err := database.AppendTaskLog(task1.ID, "tool", "newest for one"); err != nil {
		t.Fatal(err)
	}
	if err := database.AppendTaskLog(task2.ID, "output", "only for two"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/latest-logs?ids=%d,%d", task1.ID, task2.ID), nil)
	w := httptest.NewRecorder()
	srv.handleLatestLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result map[string]logJSON
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := result[fmt.Sprint(task1.ID)].Content; got != "newest for one" {
		t.Errorf("task1 latest = %q, want 'newest for one'", got)
	}
	if got := result[fmt.Sprint(task2.ID)].Content; got != "only for two" {
		t.Errorf("task2 latest = %q, want 'only for two'", got)
	}
}

func TestHandleLatestLogs_NoIDs(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/tasks/latest-logs", nil)
	w := httptest.NewRecorder()
	srv.handleLatestLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if body := strings.TrimSpace(w.Body.String()); body != "{}" {
		t.Errorf("expected empty object, got %q", body)
	}
}

// --- Terminal info & session ---

func TestHandleTerminalInfo(t *testing.T) {
	srv, database, runner := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "term", Status: db.StatusProcessing})
	if err := database.UpdateTaskDaemonSession(task.ID, "task-daemon-99"); err != nil {
		t.Fatal(err)
	}
	runner.outputVal = []byte(fmt.Sprintf("task-daemon-99:3:task-%d\nother-session:1:vim\n", task.ID))

	req := httptest.NewRequest("GET", "/api/tasks/1/terminal-info", nil)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleTerminalInfo(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var info terminalInfoJSON
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !info.WindowExists {
		t.Error("expected window_exists=true")
	}
	if info.WindowTarget != "task-daemon-99:3" {
		t.Errorf("window_target = %q, want task-daemon-99:3", info.WindowTarget)
	}
	if info.DaemonSession != "task-daemon-99" {
		t.Errorf("daemon_session = %q, want task-daemon-99", info.DaemonSession)
	}
}

func TestHandleTerminalInfo_NoWindow(t *testing.T) {
	srv, database, runner := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "term", Status: db.StatusBacklog})
	runner.outputVal = []byte("other-session:1:vim\n")

	req := httptest.NewRequest("GET", "/api/tasks/1/terminal-info", nil)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleTerminalInfo(w, req)

	var info terminalInfoJSON
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.WindowExists {
		t.Error("expected window_exists=false")
	}
}

func TestHandleEnsureSession(t *testing.T) {
	sessions := &mockSessions{target: "task-daemon-99:task-1", created: true}
	srv, database, runner := setupServerWithSessions(t, sessions)
	task := createTestTask(t, database, &db.Task{Title: "boot", Status: db.StatusBlocked})
	runner.outputVal = []byte(fmt.Sprintf("task-daemon-99:3:task-%d\n", task.ID))

	req := httptest.NewRequest("POST", "/api/tasks/1/session", nil)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleEnsureSession(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(sessions.ensured) != 1 || sessions.ensured[0] != task.ID {
		t.Errorf("EnsureTaskWindow not called for task %d: %v", task.ID, sessions.ensured)
	}
	var info terminalInfoJSON
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !info.WindowExists {
		t.Error("expected window_exists=true after session bootstrap")
	}
}

func TestHandleEnsureSession_NotConfigured(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "boot", Status: db.StatusBacklog})

	req := httptest.NewRequest("POST", "/api/tasks/1/session", nil)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleEnsureSession(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandleEnsureSession_Error(t *testing.T) {
	sessions := &mockSessions{err: fmt.Errorf("tmux unavailable")}
	srv, database, _ := setupServerWithSessions(t, sessions)
	task := createTestTask(t, database, &db.Task{Title: "boot", Status: db.StatusBacklog})

	req := httptest.NewRequest("POST", "/api/tasks/1/session", nil)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleEnsureSession(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// --- Extended task JSON ---

func TestTaskJSONIncludesTerminalFields(t *testing.T) {
	task := &db.Task{
		ID:            7,
		Title:         "json fields",
		Status:        db.StatusProcessing,
		Port:          4007,
		WorktreePath:  "/tmp/wt",
		EffortLevel:   "high",
		DaemonSession: "task-daemon-1",
		TmuxWindowID:  "@9",
		ClaudePaneID:  "%4",
		ShellPaneID:   "%5",
		PRNumber:      123,
	}
	tj := toTaskJSON(task)
	if tj.Port != 4007 || tj.WorktreePath != "/tmp/wt" || !tj.HasExecutor {
		t.Errorf("port/worktree/has_executor not mapped: %+v", tj)
	}
	if tj.EffortLevel != "high" || tj.DaemonSession != "task-daemon-1" || tj.TmuxWindowID != "@9" {
		t.Errorf("effort/daemon/window not mapped: %+v", tj)
	}
	if tj.ClaudePaneID != "%4" || tj.ShellPaneID != "%5" || tj.PRNumber != 123 {
		t.Errorf("pane ids/pr number not mapped: %+v", tj)
	}
}

func TestHandleUpdateTask_PermissionAndEffort(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "perm", Status: db.StatusBacklog})

	body := strings.NewReader(`{"permission_mode":"dangerous","effort_level":"high"}`)
	req := httptest.NewRequest("PATCH", "/api/tasks/1", body)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleUpdateTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	fresh, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.PermissionMode != "dangerous" {
		t.Errorf("permission_mode = %q, want dangerous", fresh.PermissionMode)
	}
	if fresh.EffortLevel != "high" {
		t.Errorf("effort_level = %q, want high", fresh.EffortLevel)
	}
}

func TestHandleTerminalInfo_PaneBorrowedByTUI(t *testing.T) {
	srv, database, runner := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "borrowed", Status: db.StatusProcessing})
	if err := database.UpdateTaskPaneIDs(task.ID, "%41", "%42"); err != nil {
		t.Fatal(err)
	}
	// Daemon window is gone (TUI joined the panes away), but the pane is
	// alive inside the TUI session.
	runner.outputByCmd = map[string][]byte{
		"list-windows": []byte("task-ui-123:0:detail\n"),
		"list-panes":   []byte("%40 some-other\n%41 task-ui-123\n"),
	}

	req := httptest.NewRequest("GET", "/api/tasks/1/terminal-info", nil)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleTerminalInfo(w, req)

	var info terminalInfoJSON
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.WindowExists {
		t.Error("expected window_exists=false while pane is borrowed")
	}
	if info.PaneBorrowedBy != "task-ui-123" {
		t.Errorf("pane_borrowed_by = %q, want task-ui-123", info.PaneBorrowedBy)
	}
}

func TestHandleEnsureSession_RefusesWhileBorrowed(t *testing.T) {
	sessions := &mockSessions{target: "x", created: true}
	srv, database, runner := setupServerWithSessions(t, sessions)
	task := createTestTask(t, database, &db.Task{Title: "borrowed", Status: db.StatusProcessing})
	if err := database.UpdateTaskPaneIDs(task.ID, "%41", "%42"); err != nil {
		t.Fatal(err)
	}
	runner.outputByCmd = map[string][]byte{
		"list-windows": []byte("task-ui-123:0:detail\n"),
		"list-panes":   []byte("%41 task-ui-123\n"),
	}

	req := httptest.NewRequest("POST", "/api/tasks/1/session", nil)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleEnsureSession(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 while pane is borrowed, got %d", w.Code)
	}
	if len(sessions.ensured) != 0 {
		t.Error("EnsureTaskWindow must not run while the pane is borrowed")
	}
}
