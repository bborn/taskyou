package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/tmuxtest"
)

// mockRunner records commands instead of executing them. Guarded by a mutex
// because deferred work (the annotation nudge) runs on a timer goroutine.
//
// It also simulates tmux's per-pane wait-for lock, the one agentsend now holds
// around its paste / Enter sequence. wait-for -L <channel> blocks on a
// per-channel mutex; -U <channel> releases it. The annotation nudge tests, which
// race several senders at one pane, rely on this so the recorded calls don't
// interleave once the package's old in-process lock is gone.
type mockRunner struct {
	mu        sync.Mutex
	calls     [][]string
	err       error
	outputVal []byte
	outputErr error
	// outputByCmd, when set, selects output by the first argument
	// (e.g. "list-windows", "list-panes"); falls back to outputVal.
	outputByCmd map[string][]byte
	// delay simulates the real cost of shelling out, so tests can expose races
	// that a zero-cost mock would hide.
	delay time.Duration

	// locks holds the per-channel mutex standing in for tmux's wait-for
	// channel state.
	lockMu sync.Mutex
	locks  map[string]*sync.Mutex
}

// snapshot returns a copy of the calls recorded so far.
func (m *mockRunner) snapshot() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]string(nil), m.calls...)
}

func (m *mockRunner) Run(name string, args ...string) error {
	// wait-for is the per-pane lock agentsend holds around its paste + Enter. A
	// real -L blocks until the channel is free, then claims it; -U releases. A
	// -L is recorded AFTER the lock is held, and a -U AFTER it has been let go,
	// so the recorded order is what an outside observer of the tmux server
	// actually saw — same rule the agentsend fakeTmux uses.
	if len(args) > 0 && args[0] == "wait-for" {
		ch := args[len(args)-1]
		if len(args) >= 2 && args[1] == "-L" {
			if err := m.lockAcquire(ch); err != nil {
				return err
			}
			m.mu.Lock()
			m.calls = append(m.calls, append([]string{name}, args...))
			delay := m.delay
			m.mu.Unlock()
			if delay > 0 {
				time.Sleep(delay)
			}
			return m.err
		}
		if len(args) >= 2 && args[1] == "-U" {
			m.mu.Lock()
			m.calls = append(m.calls, append([]string{name}, args...))
			delay := m.delay
			m.mu.Unlock()
			if delay > 0 {
				time.Sleep(delay)
			}
			m.lockRelease(ch)
			return m.err
		}
	}
	m.mu.Lock()
	m.calls = append(m.calls, append([]string{name}, args...))
	delay, err := m.delay, m.err
	m.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	return err
}

// lockAcquire simulates `tmux wait-for -L <channel>` by blocking on a real
// per-channel mutex until it is held. A non-nil m.err makes the acquire fail,
// the way the real tmux server would refuse a wait-for when it cannot serve.
func (m *mockRunner) lockAcquire(ch string) error {
	if m.err != nil {
		return m.err
	}
	m.lockMu.Lock()
	mu, ok := m.locks[ch]
	if !ok {
		mu = &sync.Mutex{}
		if m.locks == nil {
			m.locks = map[string]*sync.Mutex{}
		}
		m.locks[ch] = mu
	}
	m.lockMu.Unlock()
	mu.Lock()
	return nil
}

// lockRelease simulates `tmux wait-for -U <channel>` and lets the next waiter
// through.
func (m *mockRunner) lockRelease(ch string) {
	m.lockMu.Lock()
	mu, ok := m.locks[ch]
	m.lockMu.Unlock()
	if ok {
		mu.Unlock()
	}
}

func (m *mockRunner) Output(name string, args ...string) ([]byte, error) {
	m.mu.Lock()
	m.calls = append(m.calls, append([]string{name}, args...))
	m.mu.Unlock()
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

// waitForPrompts polls until at least n delivery calls (everything but the pane
// lookups) have been recorded, and returns them.
func (m *mockRunner) waitForPrompts(t *testing.T, n int) [][]string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if got := prompts(m.snapshot()); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d delivery calls, got %v", n, prompts(m.snapshot()))
		}
		time.Sleep(5 * time.Millisecond)
	}
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

// The pane is resolved by tmux tag. A stale pane id on the row — tmux having
// handed that id to another task's pane — must not decide where the message
// goes.
func TestHandleTaskInput_SendsToTheTaggedPaneNotTheStoredOne(t *testing.T) {
	srv, database, runner := setupServer(t)

	task := &db.Task{Title: "Input task", Status: db.StatusBlocked, Project: "personal"}
	database.CreateTask(task)
	database.UpdateTaskPaneIDs(task.ID, "%42", "") // stale
	tagPaneInFakeTmux(runner, task.ID+1000, "%42") // %42 is someone else's now
	tagPaneInFakeTmux(runner, task.ID, "%7")

	w := postInput(t, srv, task.ID, `{"message":"hello"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	calls := runner.waitForPrompts(t, 3)
	if len(calls) != 3 {
		t.Fatalf("want set-buffer, paste-buffer, Enter; got %v", calls)
	}
	if calls[0][1] != "set-buffer" || calls[0][len(calls[0])-1] != "hello" {
		t.Errorf("text not staged as one buffer: %v", calls[0])
	}
	for _, call := range calls {
		for i, arg := range call {
			if arg == "-t" && i+1 < len(call) && call[i+1] != "%7" {
				t.Fatalf("input aimed at %q, want the tagged pane %%7: %v", call[i+1], call)
			}
		}
	}
}

// Multi-line input is one paste, not a line per send-keys — which would submit
// the first line and leave the rest typed at a prompt that had already moved on.
func TestHandleTaskInput_KeepsMultiLineTextWhole(t *testing.T) {
	srv, database, runner := setupServer(t)
	task := &db.Task{Title: "Multi-line", Status: db.StatusBlocked, Project: "personal"}
	database.CreateTask(task)
	tagPaneInFakeTmux(runner, task.ID, "%7")

	body, _ := json.Marshal(map[string]string{"message": "first line\nsecond line"})
	if w := postInput(t, srv, task.ID, string(body)); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	calls := runner.waitForPrompts(t, 3)
	if calls[0][len(calls[0])-1] != "first line\nsecond line" {
		t.Errorf("staged text = %q, want both lines in one buffer", calls[0][len(calls[0])-1])
	}
	if !hasFlag(calls[1], "-p") {
		t.Errorf("not pasted in bracketed-paste mode: %v", calls[1])
	}
}

// No tagged pane means no agent this message can be proved to belong to. It is
// refused rather than typed into whatever the stored id names now.
func TestHandleTaskInput_RefusesWhenNoPaneCarriesTheTag(t *testing.T) {
	srv, database, runner := setupServer(t)

	task := &db.Task{Title: "No pane", Status: db.StatusBacklog, Project: "personal"}
	database.CreateTask(task)
	database.UpdateTaskPaneIDs(task.ID, "%42", "")
	tagPaneInFakeTmux(runner, task.ID+1000, "%42")

	w := postInput(t, srv, task.ID, `{"message":"test"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if code := errCode(t, w); code != "no_agent_pane" {
		t.Errorf("code = %q, want no_agent_pane", code)
	}
	if got := prompts(runner.snapshot()); len(got) != 0 {
		t.Errorf("refused input still typed into tmux: %v", got)
	}
}

// A working agent is not typed over unless the caller says so.
func TestHandleTaskInput_RefusesABusyAgentUnlessForced(t *testing.T) {
	srv, database, runner := setupServer(t)

	task := &db.Task{Title: "Busy agent", Status: db.StatusProcessing, Project: "personal"}
	database.CreateTask(task)
	tagPaneInFakeTmux(runner, task.ID, "%7")

	w := postInput(t, srv, task.ID, `{"message":"are you there"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if code := errCode(t, w); code != "agent_busy" {
		t.Errorf("code = %q, want agent_busy", code)
	}
	if got := prompts(runner.snapshot()); len(got) != 0 {
		t.Errorf("refused input still typed into tmux: %v", got)
	}

	if w := postInput(t, srv, task.ID, `{"message":"are you there","force":true}`); w.Code != http.StatusOK {
		t.Fatalf("forced send: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := runner.waitForPrompts(t, 3); len(got) != 3 {
		t.Errorf("forced send delivered %v", got)
	}
}

// A keypress answers a menu the agent is showing, so it is not held back by the
// busy check — but it still goes only to the tagged pane.
func TestHandleTaskInput_KeyGoesToTheTaggedPane(t *testing.T) {
	srv, database, runner := setupServer(t)

	task := &db.Task{Title: "Key", Status: db.StatusProcessing, Project: "personal"}
	database.CreateTask(task)
	database.UpdateTaskPaneIDs(task.ID, "%42", "")
	tagPaneInFakeTmux(runner, task.ID, "%7")

	if w := postInput(t, srv, task.ID, `{"key":"Down","enter":true}`); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	calls := runner.waitForPrompts(t, 2)
	want := [][]string{{"tmux", "send-keys", "-t", "%7", "Down"}, {"tmux", "send-keys", "-t", "%7", "Enter"}}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

// wait=true holds the response until the agent answers THIS message. The Stop
// of the turn that was already running is not that answer.
func TestHandleTaskInput_WaitIgnoresThePreviousTurnsCompletion(t *testing.T) {
	srv, database, runner := setupServer(t)

	task := &db.Task{Title: "Wait for reply", Status: db.StatusBlocked, Project: "personal"}
	database.CreateTask(task)
	tagPaneInFakeTmux(runner, task.ID, "%7")

	// A turn from before this request, which ends while we are waiting.
	database.BeginAgentTurn(task.ID)
	go func() {
		time.Sleep(20 * time.Millisecond)
		database.CompleteAgentTurn(task.ID)
	}()

	w := postInput(t, srv, task.ID, `{"message":"status?","wait":true,"timeout_ms":300}`)
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504 (the old turn's Stop is not the answer), got %d: %s", w.Code, w.Body.String())
	}
	if code := errCode(t, w); code != "reply_timeout" {
		t.Errorf("code = %q, want reply_timeout", code)
	}

	// Now the agent takes the prompt and answers it.
	go func() {
		time.Sleep(20 * time.Millisecond)
		database.BeginAgentTurn(task.ID)
		time.Sleep(20 * time.Millisecond)
		database.CompleteAgentTurn(task.ID)
	}()
	w = postInput(t, srv, task.ID, `{"message":"status?","wait":true,"timeout_ms":5000}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Turn int64 `json:"turn"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Turn != 2 {
		t.Errorf("turn = %d, want 2", resp.Turn)
	}
}

func postInput(t *testing.T, srv *Server, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/input", id), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()
	srv.handleTaskInput(w, req)
	return w
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Code string `json:"code"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	return resp.Code
}

func hasFlag(call []string, flag string) bool {
	for _, a := range call {
		if a == flag {
			return true
		}
	}
	return false
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
	// The pane lookup comes first; the capture follows it. An untagged window
	// still falls back to the stored id, which is what this task has.
	if got := prompts(runner.snapshot()); len(got) == 0 || fmt.Sprint(got[0]) != fmt.Sprint(expected) {
		t.Errorf("call = %v, want %v", got, expected)
	}
}

// Output must be read from the pane tmux says is this task's, not from a pane id
// the row remembers — tmux hands ids out again.
func TestHandleTaskOutput_PrefersTheTaggedPane(t *testing.T) {
	srv, database, runner := setupServer(t)
	runner.outputVal = []byte("pane content")

	task := &db.Task{Title: "Out task", Status: db.StatusProcessing, Project: "personal"}
	database.CreateTask(task)
	database.UpdateTaskPaneIDs(task.ID, "%5", "") // stale
	tagPaneInFakeTmux(runner, task.ID+1000, "%5")
	tagPaneInFakeTmux(runner, task.ID, "%6")

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/%d/output", task.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleTaskOutput(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	got := prompts(runner.snapshot())
	if len(got) == 0 || got[0][3] != "%6" {
		t.Errorf("captured %v, want the tagged pane %%6", got)
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

// TestHandleUpdateProject_PersonalRenameRejected verifies the HTTP API can no
// longer be used to rename the seeded "personal" project. The DB-layer guard
// in db.UpdateProject is what blocks it for every other surface (CLI/HTTP),
// mirroring the personal-deletion guard; the response is non-2xx and the
// personal row is left intact for default task creation.
func TestHandleUpdateProject_PersonalRenameRejected(t *testing.T) {
	srv, database, _ := setupServer(t)

	personal, err := database.GetProjectByName("personal")
	if err != nil || personal == nil {
		t.Fatalf("get personal project: err=%v project=%v", err, personal)
	}
	personalID := personal.ID

	body := `{"name":"mywork"}`
	req := httptest.NewRequest("PATCH", "/api/projects/personal", strings.NewReader(body))
	req.SetPathValue("name", "personal")
	w := httptest.NewRecorder()
	srv.handleUpdateProject(w, req)

	if w.Code < 400 || w.Code >= 600 {
		t.Fatalf("expected non-2xx (4xx or 5xx) response for renaming personal, got %d: %s", w.Code, w.Body.String())
	}

	// The personal project must still exist with its original ID and name; no
	// "mywork" row may have been created.
	stillThere, err := database.GetProjectByName("personal")
	if err != nil {
		t.Fatalf("get personal after rejected HTTP rename: %v", err)
	}
	if stillThere == nil {
		t.Fatal("personal project disappeared after rejected HTTP rename")
	}
	if stillThere.ID != personalID {
		t.Errorf("personal ID = %d, want %d", stillThere.ID, personalID)
	}
	if stillThere.Name != "personal" {
		t.Errorf("personal Name = %q, want %q", stillThere.Name, "personal")
	}
	if other, _ := database.GetProjectByName("mywork"); other != nil {
		t.Errorf("a 'mywork' project exists after rejected HTTP rename: %+v", other)
	}

	// Non-rename edits of the personal project via PATCH (e.g., changing only
	// the color while keeping the name) must still succeed — the guard blocks
	// renames, not edits.
	body2 := `{"color":"#112233"}`
	req2 := httptest.NewRequest("PATCH", "/api/projects/personal", strings.NewReader(body2))
	req2.SetPathValue("name", "personal")
	w2 := httptest.NewRecorder()
	srv.handleUpdateProject(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-rename edit of personal, got %d: %s", w2.Code, w2.Body.String())
	}
	edited, err := database.GetProjectByName("personal")
	if err != nil || edited == nil {
		t.Fatalf("get personal after non-rename HTTP edit: err=%v project=%v", err, edited)
	}
	if edited.Color != "#112233" {
		t.Errorf("personal Color = %q, want %q", edited.Color, "#112233")
	}
	if edited.Name != "personal" {
		t.Errorf("personal Name = %q, want %q (renamed by non-rename edit?)", edited.Name, "personal")
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

// TestMain keeps every test in this package off the live tmux server; see
// internal/tmuxtest.
func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}
