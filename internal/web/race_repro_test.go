package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// Concurrent-move duplicate reproduction. A move is a delete + create on the
// source task and the old task ID does not survive; the read → teardown →
// DeleteTask → CreateTask sequence in moveTask must be serialized per source
// task, or N concurrent movers each INSERT a distinct new row in the
// destination project, each return 200 with a different new task ID, and each
// fans out a "created" notification to every subscriber. The single pooled
// SQLite connection (SetMaxOpenConns(1)) only serializes individual statements,
// not the application's multi-statement critical section. These tests fire N
// concurrent requests at the same task through the existing
// setupServerWithMover / createProject / createTestTask / mockMover helpers
// and assert the no-duplicate invariant.

// runConcurrentMoveRace fires n concurrent move requests at the same source
// task and asserts the no-duplicate invariant. If moveViaPatch is true the
// requests are PATCH /api/tasks/{id} with `{"project":target}` (the
// project-change branch of handleUpdateTask); otherwise they are POST
// /api/tasks/{id}/move. The summary log line mirrors the bug report's so the
// race/no-race framing reads identically.
func runConcurrentMoveRace(t *testing.T, srv *Server, database *db.DB, mover *mockMover, task *db.Task, targetProject string, n int, moveViaPatch bool) {
	t.Helper()
	var wg sync.WaitGroup
	var mu sync.Mutex
	statusCodes := make(map[int]int)
	var newIDs []int64
	body := fmt.Sprintf(`{"project":%q}`, targetProject)
	// Release all movers simultaneously to maximize the read-side race so the
	// duplicate-row outcome is the bug's deterministic shape, not a
	// scheduler-dependent flake.
	begin := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-begin
			var w *httptest.ResponseRecorder
			if moveViaPatch {
				w = patchTask(t, srv, task.ID, body)
			} else {
				w = postMove(t, srv, task.ID, body)
			}
			mu.Lock()
			statusCodes[w.Code]++
			if w.Code == http.StatusOK {
				var got taskJSON
				if err := json.NewDecoder(w.Body).Decode(&got); err == nil {
					newIDs = append(newIDs, got.ID)
				}
			}
			mu.Unlock()
		}()
	}
	close(begin)
	wg.Wait()

	distinctNewIDs := make(map[int64]struct{})
	for _, id := range newIDs {
		distinctNewIDs[id] = struct{}{}
	}

	// Each duplicate of the move is a fresh row in the target project with the
	// same title as the source; count them.
	targetTasks, err := database.ListTasks(db.ListTasksOptions{Project: targetProject, IncludeClosed: true, Limit: -1})
	if err != nil {
		t.Fatalf("list target tasks: %v", err)
	}
	targetRows := 0
	for _, tt := range targetTasks {
		if tt.Title == task.Title {
			targetRows++
		}
	}

	notifs := mover.snapshot()
	successes := statusCodes[http.StatusOK]
	t.Logf("concurrent moves=%d, status_codes=%v, distinct_new_ids=%d, target_rows=%d, notifications=%d, successes=%d",
		n, statusCodes, len(distinctNewIDs), targetRows, len(notifs), successes)

	// The move contract is "a delete + create — the old task ID does not
	// survive", so N concurrent movers of the same task produce at most one new
	// row. With the per-task move lock, the first mover creates the row and any
	// racing mover 404s at requireTask or inside moveTask's reload guard.
	if targetRows > 1 {
		t.Errorf("RACE: %d duplicate 'race' tasks created in target project (expected 0 or 1)", targetRows)
	}
	if targetRows != successes {
		t.Errorf("target_rows=%d != successes=%d — every 200 must own exactly one new row", targetRows, successes)
	}
	if len(distinctNewIDs) != successes {
		t.Errorf("distinct_new_ids=%d != successes=%d — a duplicate row reused a new ID", len(distinctNewIDs), successes)
	}
	if successes == 0 {
		t.Errorf("no move succeeded — at least one mover must win (the source row existed before any mover started)")
	}
	if len(notifs) != 2*successes {
		t.Errorf("notifications=%d, want 2*successes=%d — a duplicate row fans out a 'created' event to every subscriber",
			len(notifs), 2*successes)
	}
}

// TestConcurrentMoveRacesDuplicate_NoWorktree: the move contract is "a delete
// + create, the old task ID does not survive" — N concurrent movers of the same
// source task must produce at most one new row in the destination project, with
// one 200 winner and the rest 404. The race is at its modal shape here: a draft
// task (no teardown) collapses the critical section to the three sequential
// single-statement DB calls requireTask → DeleteTask → CreateTask, with the
// pooled SQLite connection released between each — exactly the shape that the
// single-connection pool (SetMaxOpenConns(1)) cannot serialize on its own.
// Future-regression guard: any refactor that drops the per-task moveLock, or
// widens the critical section back across multiple connection releases, makes
// this test fail with N duplicate rows.
func TestConcurrentMoveRacesDuplicate_NoWorktree(t *testing.T) {
	mover := &mockMover{}
	srv, database := setupServerWithMover(t, mover)
	createProject(t, database, "target")

	task := createTestTask(t, database, &db.Task{Title: "race", Status: db.StatusBacklog, Project: "personal"})
	runConcurrentMoveRace(t, srv, database, mover, task, "target", 8, false)
}

// TestConcurrentPatchRacesDuplicate_NoWorktree: the project-change branch of
// PATCH /api/tasks/{id} routes through moveTask too, so it shares the same
// per-task critical section as POST /move. The PATCH handler runs JSON decode,
// optional GetTaskTypeByName, optional ValidateTaskModel, and GetProjectByName
// between requireTask and moveTask — each a connection-releasing DB call — so a
// fix that only serializes POST /move would leave the PATCH path racy.
// Future-regression guard: catches a refactor that splits the two routes'
// serialization or re-introduces a bare `task.Project = *req.Project` update.
func TestConcurrentPatchRacesDuplicate_NoWorktree(t *testing.T) {
	mover := &mockMover{}
	srv, database := setupServerWithMover(t, mover)
	createProject(t, database, "target")

	task := createTestTask(t, database, &db.Task{Title: "race", Status: db.StatusBacklog, Project: "personal"})
	runConcurrentMoveRace(t, srv, database, mover, task, "target", 8, true)
}

// blockingMover wraps a mockMover, blocking one task's CleanupWorktree on a
// release channel before delegating. Used to verify the per-task moveLock keeps
// unrelated moves concurrent: a global moveMu would make different-task moves
// wait on each other's teardown.
type blockingMover struct {
	inner       TaskMover
	blockTaskID int64
	blockCh     chan struct{}
}

func (b *blockingMover) KillClaudeProcess(taskID int64) bool {
	return b.inner.KillClaudeProcess(taskID)
}

func (b *blockingMover) NotifyTaskChange(eventType string, task *db.Task) {
	b.inner.NotifyTaskChange(eventType, task)
}

func (b *blockingMover) CleanupWorktree(task *db.Task) error {
	if task.ID == b.blockTaskID {
		<-b.blockCh
	}
	return b.inner.CleanupWorktree(task)
}

// TestMovePerTaskLockPreservesDifferentTaskConcurrency guards the lock-variant
// choice the bug report's recommended fix makes: per-task mutex, NOT a global
// one. A move of taskA with a slow teardown must not wait on a move of an
// unrelated taskB. The test's wall-clock assertion fails if a global moveMu is
// used instead — different tasks would serialize behind the same lock and B's
// move would hang waiting for A's release.
func TestMovePerTaskLockPreservesDifferentTaskConcurrency(t *testing.T) {
	mover := &mockMover{}
	srv, database := setupServerWithMover(t, mover)
	createProject(t, database, "target")

	aTask := createTestTask(t, database, &db.Task{Title: "race-a", Status: db.StatusBacklog, Project: "personal"})
	aTask.WorktreePath = "/tmp/ty-race-a-wt"
	if err := database.UpdateTask(aTask); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	bTask := createTestTask(t, database, &db.Task{Title: "race-b", Status: db.StatusBacklog, Project: "personal"})
	bTask.WorktreePath = "/tmp/ty-race-b-wt"
	if err := database.UpdateTask(bTask); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	srv.mover = &blockingMover{inner: mover, blockTaskID: aTask.ID, blockCh: release}

	// Each mover's recorder is handed back through a buffered channel so the
	// main goroutine owns it only after the goroutine has finished writing to
	// it — no shared-mutable access between the handler goroutine and the test
	// body, which -race would otherwise flag on the recorder's Code field.
	aCh := make(chan *httptest.ResponseRecorder, 1)
	bCh := make(chan *httptest.ResponseRecorder, 1)
	go func() { aCh <- postMove(t, srv, aTask.ID, `{"project":"target"}`) }()
	go func() { bCh <- postMove(t, srv, bTask.ID, `{"project":"target"}`) }()

	// B must complete within 2s even though A is still wedged in its teardown.
	// If the lock were global, B's moveTask would block on the same lock A
	// holds; the 2s deadline would fire and the test would fail.
	var wB *httptest.ResponseRecorder
	select {
	case wB = <-bCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("B's move did not complete in 2s — different-task moves are serialized (a global move lock, not per-task?)")
	}
	if wB.Code != http.StatusOK {
		t.Fatalf("B's move: want 200, got %d: %s", wB.Code, wB.Body.String())
	}

	// Now release A's teardown and ensure its move completes too. The
	// t.Cleanup close uses sync.Once so a leaked goroutine on test failure is
	// released rather than left wedged.
	once.Do(func() { close(release) })
	var wA *httptest.ResponseRecorder
	select {
	case wA = <-aCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("A's move did not complete within 2s after release")
	}
	if wA.Code != http.StatusOK {
		t.Errorf("A's move: want 200, got %d: %s", wA.Code, wA.Body.String())
	}

	// Each task should have exactly one row in the target project.
	targetTasks, err := database.ListTasks(db.ListTasksOptions{Project: "target", IncludeClosed: true, Limit: -1})
	if err != nil {
		t.Fatalf("list target tasks: %v", err)
	}
	rows := 0
	for _, tt := range targetTasks {
		if tt.Title == "race-a" || tt.Title == "race-b" {
			rows++
		}
	}
	if rows != 2 {
		t.Errorf("target rows = %d, want 2 (both A and B should have one row each after the move)", rows)
	}
}
