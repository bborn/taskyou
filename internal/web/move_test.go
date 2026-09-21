package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// mockMover is the TaskMover test double: it records what the move path asked
// for so assertions can prove teardown happened, without needing a real
// *executor.Executor (which would shell out to tmux and git).
//
// CleanupWorktree mirrors the real executor's in-memory side effect — it
// clears WorktreePath/BranchName on the task it was handed — so tests that
// inspect the task after the move see the same state the executor would
// leave behind.
type mockMover struct {
	mu                 sync.Mutex
	killedIDs          []int64
	cleanupWorktreeFor []*db.Task
	notifications      []mockMoverEvent
	cleanupErr         error
}

type mockMoverEvent struct {
	eventType string
	task      *db.Task
}

func (m *mockMover) KillClaudeProcess(taskID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.killedIDs = append(m.killedIDs, taskID)
	return false
}

func (m *mockMover) CleanupWorktree(task *db.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *task
	m.cleanupWorktreeFor = append(m.cleanupWorktreeFor, &cp)
	// Mirror the real CleanupWorktree which clears these on the in-memory
	// task; tests inspect the new row (created by delete + create), so
	// the cleared state matches what production leaves behind.
	task.WorktreePath = ""
	task.BranchName = ""
	return m.cleanupErr
}

func (m *mockMover) NotifyTaskChange(eventType string, task *db.Task) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *task
	m.notifications = append(m.notifications, mockMoverEvent{eventType, &cp})
}

func (m *mockMover) snapshot() []mockMoverEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mockMoverEvent, len(m.notifications))
	copy(out, m.notifications)
	return out
}

func (m *mockMover) cleanupTargets() []*db.Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*db.Task, len(m.cleanupWorktreeFor))
	copy(out, m.cleanupWorktreeFor)
	return out
}

func (m *mockMover) killedTaskIDs() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int64, len(m.killedIDs))
	copy(out, m.killedIDs)
	return out
}

// setupServerWithMover builds a server whose TaskMover is the supplied mock.
// The server has no SessionManager (so handleEnsureSession is unavailable),
// matching the production shape where Mover and Sessions are both the same
// executor instance — but tests want to spy on Mover without dragging the
// rest of the executor in.
func setupServerWithMover(t *testing.T, mover TaskMover) (*Server, *db.DB) {
	t.Helper()
	database := setupTestDB(t)
	srv := New(Config{
		Addr:      ":0",
		DB:        database,
		CmdRunner: &mockRunner{},
		Mover:     mover,
	})
	return srv, database
}

// createProject inserts a project row so CreateTask (and moveTask via
// CreateTask) will accept tasks that point at it. The personal project is
// always seeded by Open.
func createProject(t *testing.T, database *db.DB, name string) *db.Project {
	t.Helper()
	p := &db.Project{Name: name, Path: t.TempDir()}
	if err := database.CreateProject(p); err != nil {
		t.Fatalf("create project %q: %v", name, err)
	}
	return p
}

// postMove issues POST /api/tasks/{id}/move and returns the recorder.
func postMove(t *testing.T, srv *Server, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/move", id), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()
	srv.handleMoveTask(w, req)
	return w
}

// ---------- POST /api/tasks/{id}/move ----------

// TestHandleMoveTask_OldTaskWithWorktreeTearsDown: the bug report's
// reproduction. A task with an existing worktree (i.e., one that has run) is
// moved: the move handler must call KillClaudeProcess, CleanupWorktree, and
// notify "deleted"+"created"; the new row has no WorktreePath/branch/port,
// and the old row is gone. Without the fix, WorktreePath is re-persisted
// verbatim and CleanupWorktree is never called — the leak the bug describes.
// Future-regression guard: catches any refactor that drops teardown, drops
// the delete+create, or forgets to reset execution state.
func TestHandleMoveTask_OldTaskWithWorktreeTearsDown(t *testing.T) {
	mover := &mockMover{}
	srv, database := setupServerWithMover(t, mover)
	createProject(t, database, "target")

	task := createTestTask(t, database, &db.Task{
		Title: "ran", Status: db.StatusDone, Project: "personal", Executor: "claude",
	})
	task.WorktreePath = "/tmp/ty-move-test-wt"
	task.BranchName = "task-42"
	task.Port = 4242
	task.ClaudeSessionID = "claude-session-42"
	task.DaemonSession = "task-daemon-42"
	task.TmuxWindowID = "@42"
	task.ClaudePaneID = "%42"
	task.ShellPaneID = "%43"
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("seed execution state: %v", err)
	}
	oldID := task.ID

	w := postMove(t, srv, oldID, `{"project":"target"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got taskJSON
	json.NewDecoder(w.Body).Decode(&got)

	// Teardown ran for the worktree task.
	if ids := mover.killedTaskIDs(); len(ids) != 1 || ids[0] != oldID {
		t.Errorf("KillClaudeProcess calls = %v, want [%d]", ids, oldID)
	}
	if targets := mover.cleanupTargets(); len(targets) != 1 || targets[0].ID != oldID {
		t.Errorf("CleanupWorktree calls = %v, want task %d", targets, oldID)
	}
	// Notification happens twice: deleted + created.
	notifs := mover.snapshot()
	if len(notifs) != 2 {
		t.Fatalf("NotifyTaskChange calls = %d, want 2 (deleted, created)", len(notifs))
	}
	if notifs[0].eventType != "deleted" || notifs[0].task.ID != oldID {
		t.Errorf("notif[0] = (%q, id %d), want (deleted, %d)", notifs[0].eventType, notifs[0].task.ID, oldID)
	}
	if notifs[1].eventType != "created" || notifs[1].task.ID != got.ID {
		t.Errorf("notif[1] = (%q, id %d), want (created, %d)", notifs[1].eventType, notifs[1].task.ID, got.ID)
	}

	// Old row gone.
	if still, _ := database.GetTask(oldID); still != nil {
		t.Errorf("old row %d still present", oldID)
	}

	// New row: fresh, with no pointers at the old project.
	stored, _ := database.GetTask(got.ID)
	if stored == nil {
		t.Fatalf("new row %d not in db", got.ID)
	}
	if stored.Project != "target" {
		t.Errorf("stored project = %q, want target", stored.Project)
	}
	if stored.WorktreePath != "" {
		t.Errorf("stored WorktreePath = %q — worktree leak: this is the bug", stored.WorktreePath)
	}
	if stored.BranchName != "" {
		t.Errorf("stored BranchName = %q, want empty", stored.BranchName)
	}
	if stored.Port != 0 {
		t.Errorf("stored Port = %d, want 0", stored.Port)
	}
	if stored.ClaudeSessionID != "" {
		t.Errorf("stored ClaudeSessionID = %q, want empty", stored.ClaudeSessionID)
	}
	if stored.DaemonSession != "" {
		t.Errorf("stored DaemonSession = %q, want empty", stored.DaemonSession)
	}
	if stored.TmuxWindowID != "" {
		t.Errorf("stored TmuxWindowID = %q, want empty", stored.TmuxWindowID)
	}
	if stored.Status != db.StatusDone {
		t.Errorf("stored Status = %q, want done (done is preserved)", stored.Status)
	}
}

// TestHandleMoveTask_ProcessingResetsToBacklog: a task in "processing" loses
// its work when moved (the running work is torn down). The new row is backlog,
// matching the CLI help and the TUI doc comment. Future-regression guard:
// pins the documented status-reset rule against a fix that always or never
// resets.
func TestHandleMoveTask_ProcessingResetsToBacklog(t *testing.T) {
	mover := &mockMover{}
	srv, database := setupServerWithMover(t, mover)
	createProject(t, database, "target")

	task := createTestTask(t, database, &db.Task{Title: "running", Status: db.StatusProcessing, Project: "personal"})
	task.WorktreePath = "/tmp/wt"
	database.UpdateTask(task)

	w := postMove(t, srv, task.ID, `{"project":"target"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got taskJSON
	json.NewDecoder(w.Body).Decode(&got)
	if got.Status != db.StatusBacklog {
		t.Errorf("status = %q, want backlog — processing work is lost on a move", got.Status)
	}
}

// TestHandleMoveTask_BlockedResetsToBacklog mirrors the above for blocked.
func TestHandleMoveTask_BlockedResetsToBacklog(t *testing.T) {
	mover := &mockMover{}
	srv, database := setupServerWithMover(t, mover)
	createProject(t, database, "target")

	task := createTestTask(t, database, &db.Task{Title: "parked", Status: db.StatusBlocked, Project: "personal"})
	task.WorktreePath = "/tmp/wt"
	database.UpdateTask(task)

	w := postMove(t, srv, task.ID, `{"project":"target"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got taskJSON
	json.NewDecoder(w.Body).Decode(&got)
	if got.Status != db.StatusBacklog {
		t.Errorf("status = %q, want backlog", got.Status)
	}
}

// TestHandleMoveTask_NoMoverWithWorktreeReturns503: a server without a
// configured Mover cannot tear down a task that has run. Refusing the move
// (503) keeps the leak from happening — the old row, worktree, and agent
// stay in their original project, where they belong. Future-regression
// guard: pins the no-Mover branch; a refactor that drops the guard would
// silently leak the worktree again.
func TestHandleMoveTask_NoMoverWithWorktreeReturns503(t *testing.T) {
	database := setupTestDB(t)
	createProject(t, database, "target")
	server := New(Config{Addr: ":0", DB: database, CmdRunner: &mockRunner{}}) // no Mover

	task := createTestTask(t, database, &db.Task{Title: "ran", Status: db.StatusDone, Project: "personal"})
	task.WorktreePath = "/tmp/wt"
	database.UpdateTask(task)

	w := postMove(t, server, task.ID, `{"project":"target"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (no mover to tear down), got %d: %s", w.Code, w.Body.String())
	}

	// The original row is intact — no half-applied move.
	stored, _ := database.GetTask(task.ID)
	if stored == nil {
		t.Fatal("task was deleted on a refused move")
	}
	if stored.Project != "personal" {
		t.Errorf("project mutated on refused move: %q", stored.Project)
	}
	if stored.WorktreePath != "/tmp/wt" {
		t.Errorf("WorktreePath mutated on refused move: %q", stored.WorktreePath)
	}
}

// TestHandleMoveTask_NoMoverDraftStillMoves: a draft task has nothing to tear
// down, so a server without a Mover can still move it. This keeps the
// cheap-relabel case working even when the executor isn't attached (the
// asker can still reorganize the backlog without an executor running).
// Future-regression guard: a refactor that demands a Mover for the draft
// case would break every `ty serve` that hasn't wired an executor in.
func TestHandleMoveTask_NoMoverDraftStillMoves(t *testing.T) {
	database := setupTestDB(t)
	createProject(t, database, "target")
	server := New(Config{Addr: ":0", DB: database, CmdRunner: &mockRunner{}})

	task := createTestTask(t, database, &db.Task{Title: "draft", Status: db.StatusBacklog, Project: "personal"})

	w := postMove(t, server, task.ID, `{"project":"target"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a draft move with no mover, got %d: %s", w.Code, w.Body.String())
	}
	if still, _ := database.GetTask(task.ID); still != nil {
		t.Errorf("old row survived a draft move (no+relabel wing): should be deleted and replaced")
	}
}

// TestHandleMoveTask_PreservesContentFields: the new row inherits the
// content fields from the old one and resets every execution column.
// Future-regression guard: keeps the preserve-list honest. The CLI's
// narrower preserve-list (cmd/task/main.go) does not include Model,
// ClaudeConfigDir, etc.; a future author might copy it and silently drop
// content fields. This test catches that.
func TestHandleMoveTask_PreservesContentFields(t *testing.T) {
	mover := &mockMover{}
	srv, database := setupServerWithMover(t, mover)
	createProject(t, database, "target")

	task := createTestTask(t, database, &db.Task{
		Title:   "the title",
		Body:    "the body",
		Type:    "code",
		Tags:    "a,b",
		Project: "personal",
		Pinned:  true,
	})
	task.WorktreePath = "/tmp/wt"
	task.BranchName = "task-7"
	task.Port = 7777
	task.ClaudeSessionID = "sess"
	task.DaemonSession = "daemon"
	task.PRNumber = 99
	task.PRURL = "https://example.com/pr/99"
	task.PRInfoJSON = "{}"
	task.ArchiveRef = "refs/task-archive/99"
	task.ArchiveCommit = "deadbeef"
	task.ArchiveWorktreePath = "/tmp/old-wt"
	task.ArchiveBranchName = "task-99"
	task.PlacementTarget = "remote-host"
	task.PlacementReason = "pinned to remote"
	task.PermissionMode = "dangerous"
	task.EffortLevel = "high"
	task.Model = "opus"
	database.UpdateTask(task)

	w := postMove(t, srv, task.ID, `{"project":"target"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got taskJSON
	json.NewDecoder(w.Body).Decode(&got)
	stored, _ := database.GetTask(got.ID)

	checks := map[string]struct {
		got, want any
	}{
		"Title":          {stored.Title, "the title"},
		"Body":           {stored.Body, "the body"},
		"Type":           {stored.Type, "code"},
		"Tags":           {stored.Tags, "a,b"},
		"Pinned":         {stored.Pinned, true},
		"PermissionMode": {stored.PermissionMode, "dangerous"},
		"EffortLevel":    {stored.EffortLevel, "high"},
		"Model":          {stored.Model, "opus"},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", name, c.got, c.want)
		}
	}
	// Execution state is reset: this is the part the bug got wrong.
	if stored.WorktreePath != "" || stored.BranchName != "" || stored.Port != 0 ||
		stored.ClaudeSessionID != "" || stored.DaemonSession != "" ||
		stored.PRNumber != 0 || stored.PRURL != "" || stored.PRInfoJSON != "" ||
		stored.ArchiveRef != "" || stored.ArchiveCommit != "" ||
		stored.ArchiveWorktreePath != "" || stored.ArchiveBranchName != "" ||
		stored.PlacementTarget != "" || stored.PlacementReason != "" {
		t.Errorf("execution state not reset on moved task: %+v", stored)
	}
}

// ---------- PATCH /api/tasks/{id} with `project` ----------

// patchTaskWithReader mirrors patchTask but lets the caller pass a body
// string instead of a reader.
func patchTaskWithReader(t *testing.T, srv *Server, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	return patchTask(t, srv, id, body)
}

// TestPatchTask_ProjectChangeMovesWithoutOtherFieldUpdates: a bare
// `{"project":"X"}` PATCH does what /move does — delete + recreate in X.
// This is the smallest PATCH-with-project case the bug report calls out.
// Future-regression guard: pins the PATCH path routes through moveTask; a
// refactor that re-introduces the bare `task.Project = *req.Project` line
// would silently relabel and re-persist execution state again.
func TestPatchTask_ProjectChangeMovesWithoutOtherFieldUpdates(t *testing.T) {
	mover := &mockMover{}
	srv, database := setupServerWithMover(t, mover)
	createProject(t, database, "target")

	task := createTestTask(t, database, &db.Task{
		Title:   "title",
		Status:  db.StatusBacklog,
		Project: "personal",
	})
	task.WorktreePath = "/tmp/wt"
	task.BranchName = "task"
	task.Port = 99
	task.ClaudeSessionID = "sess"
	database.UpdateTask(task)
	oldID := task.ID

	w := patchTaskWithReader(t, srv, oldID, `{"project":"target"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got taskJSON
	json.NewDecoder(w.Body).Decode(&got)
	if got.Project != "target" {
		t.Errorf("response project = %q, want target", got.Project)
	}
	if got.ID == oldID {
		t.Errorf("response id = %d, want new id (a project change is a delete + create)", got.ID)
	}
	// The mover was used — this is the part the buggy branch skipped entirely.
	if len(mover.killedTaskIDs()) != 1 || mover.killedTaskIDs()[0] != oldID {
		t.Errorf("KillClaudeProcess was not called for the PATCH move: %v", mover.killedTaskIDs())
	}
	if len(mover.cleanupTargets()) != 1 {
		t.Errorf("CleanupWorktree was not called for the PATCH move: %v", mover.cleanupTargets())
	}
	notifs := mover.snapshot()
	if len(notifs) != 2 {
		t.Errorf("expected 2 notifications (deleted, created), got %d", len(notifs))
	}

	stored, _ := database.GetTask(got.ID)
	if stored.WorktreePath != "" || stored.Port != 0 || stored.ClaudeSessionID != "" {
		t.Errorf("execution state re-persisted by the PATCH move: %+v", stored)
	}
}

// TestPatchTask_ProjectChangeInheritsOtherEdits: a multi-field PATCH that
// includes a project change routes the whole edit through move; the new
// row carries the other field updates. Future-regression guard: catches a
// refactor that runs PATCH field updates through UpdateTask before the
// move, dropping them on the floor when the row is replaced.
func TestPatchTask_ProjectChangeInheritsOtherEdits(t *testing.T) {
	mover := &mockMover{}
	srv, database := setupServerWithMover(t, mover)
	createProject(t, database, "target")

	task := createTestTask(t, database, &db.Task{Title: "old", Body: "old body", Status: db.StatusBacklog, Project: "personal"})

	w := patchTaskWithReader(t, srv, task.ID, `{"project":"target","title":"new","body":"new body","pinned":true,"tags":"x,y"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got taskJSON
	json.NewDecoder(w.Body).Decode(&got)

	stored, _ := database.GetTask(got.ID)
	if stored.Project != "target" {
		t.Errorf("project = %q, want target", stored.Project)
	}
	if stored.Title != "new" {
		t.Errorf("title = %q, want new", stored.Title)
	}
	if stored.Body != "new body" {
		t.Errorf("body = %q, want 'new body'", stored.Body)
	}
	if !stored.Pinned {
		t.Errorf("pinned = false, want true")
	}
	if stored.Tags != "x,y" {
		t.Errorf("tags = %q, want 'x,y'", stored.Tags)
	}
}

// TestPatchTask_SameProjectRequestDoesNotMove: a PATCH that re-asserts the
// current project is a no-op on project, and the row keeps its id. This is
// the regression guard against always-move-on-project-present — a refactor
// that treats any present `project` field as a move would break re-asserts
// (the desktop client's updateTask type accepts `project` as a field).
func TestPatchTask_SameProjectRequestDoesNotMove(t *testing.T) {
	mover := &mockMover{}
	srv, database := setupServerWithMover(t, mover)

	task := createTestTask(t, database, &db.Task{Title: "x", Project: "personal"})
	oldID := task.ID

	w := patchTaskWithReader(t, srv, task.ID, `{"project":"personal","title":"renamed"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got taskJSON
	json.NewDecoder(w.Body).Decode(&got)
	if got.ID != oldID {
		t.Errorf("id = %d, want %d (no move when the project is unchanged)", got.ID, oldID)
	}
	if got.Title != "renamed" {
		t.Errorf("title = %q, want renamed", got.Title)
	}
	if len(mover.killedTaskIDs()) != 0 || len(mover.cleanupTargets()) != 0 {
		t.Errorf("mover was invoked on a non-move PATCH")
	}
}

// TestPatchTask_StatusRefusalWinsOverProjectMove: a PATCH that combines
// `project` (which would move) with `status` (which is refused) returns
// 400 from the status guard, before the move runs. The whole PATCH is
// rejected — including the project change. Future-regression guard: the
// status refusal is a documented contract (status changes go through
// POST /api/tasks/{id}/status). A refactor that runs the move first and
// the status-refusal second would let a status-with-project PATCH move
// the task and *then* fail, leaving the row in the new project.
func TestPatchTask_StatusRefusalWinsOverProjectMove(t *testing.T) {
	mover := &mockMover{}
	srv, database := setupServerWithMover(t, mover)
	createProject(t, database, "target")

	task := createTestTask(t, database, &db.Task{Title: "x", Status: db.StatusBacklog, Project: "personal"})

	w := patchTaskWithReader(t, srv, task.ID, `{"project":"target","status":"done"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (status refused before move), got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "/status") {
		t.Errorf("refusal must point at /status, got %s", w.Body.String())
	}
	// Nothing moved.
	stored, _ := database.GetTask(task.ID)
	if stored == nil || stored.Project != "personal" {
		t.Errorf("project changed despite the status refusal: %+v", stored)
	}
	if len(mover.killedTaskIDs()) != 0 || len(mover.cleanupTargets()) != 0 {
		t.Errorf("mover ran despite the status refusal")
	}
}

// TestPatchTask_EmptyProjectTreatedAsUnknown: a PATCH setting project to ""
// would today persist an empty project; with the move routing, the empty
// string fails the existence check (no project named ""), producing a 404
// before any row mutation. Future-regression guard: the old bug path
// silently accepted `project:""` and persisted empty. A refactor that
// re-introduces the bare assignment would lose this guard.
func TestPatchTask_EmptyProjectTreatedAsUnknown(t *testing.T) {
	srv, database := setupServerWithMover(t, &mockMover{})

	task := createTestTask(t, database, &db.Task{Title: "x", Project: "personal"})

	w := patchTaskWithReader(t, srv, task.ID, `{"project":""}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for empty project (no such project), got %d: %s", w.Code, w.Body.String())
	}
	stored, _ := database.GetTask(task.ID)
	if stored == nil || stored.Project != "personal" {
		t.Errorf("original row mutated on refused empty-project move: %+v", stored)
	}
}

// _ keeps the interface assertion inert.
var _ TaskMover = (*mockMover)(nil)
