package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// startedTask creates a task in the given status with started_at set, which is
// what every hook handler requires before it will touch a status.
func startedTask(t *testing.T, database *db.DB, title, sessionID, status string) *db.Task {
	t.Helper()
	task := &db.Task{Title: title, Status: db.StatusProcessing, Type: db.TypeCode}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if sessionID != "" {
		if err := database.UpdateTaskClaudeSessionID(task.ID, sessionID); err != nil {
			t.Fatalf("set session id: %v", err)
		}
	}
	if err := database.MarkTaskStarted(task.ID); err != nil {
		t.Fatalf("mark started: %v", err)
	}
	if status != db.StatusProcessing {
		if err := database.SetTaskStatus(task.ID, status, db.ActorSystem, "test fixture", db.NoEvidence); err != nil {
			t.Fatalf("set status %s: %v", status, err)
		}
	}
	fetched, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if fetched.Status != status {
		t.Fatalf("fixture status = %q, want %q", fetched.Status, status)
	}
	return fetched
}

func taskStatus(t *testing.T, database *db.DB, taskID int64) string {
	t.Helper()
	task, err := database.GetTask(taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	return task.Status
}

func logContains(t *testing.T, database *db.DB, taskID int64, substr string) bool {
	t.Helper()
	found, err := database.HasLogLineContaining(taskID, substr)
	if err != nil {
		t.Fatalf("search logs: %v", err)
	}
	return found
}

func testDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// TestNestedClaudeSessionCannotDriveTask is the regression test for the hole the
// hook path shipped with: ANY process holding WORKTREE_TASK_ID in its
// environment could move a task.
//
// A `claude -p ...` the agent runs from Bash inherits that variable and loads
// the worktree's hooks, so its own Stop hook used to mark the parent task
// blocked (and, for a workflow step, advance the DAG) the moment the nested
// session finished its unrelated turn.
func TestNestedClaudeSessionCannotDriveTask(t *testing.T) {
	database := testDB(t)

	t.Run("nested session's Stop hook is ignored", func(t *testing.T) {
		task := startedTask(t, database, "owned by the real session", "owner-session", db.StatusProcessing)

		nested := &ClaudeHookInput{SessionID: "nested-session", StopReason: "end_turn"}
		if err := dispatchClaudeHook(database, task.ID, "Stop", nested); err != nil {
			t.Fatalf("dispatchClaudeHook: %v", err)
		}

		if got := taskStatus(t, database, task.ID); got != db.StatusProcessing {
			t.Errorf("status = %q, want %q — a nested Claude must not park the task it was launched from", got, db.StatusProcessing)
		}
		if !logContains(t, database, task.ID, "nested-session") || !logContains(t, database, task.ID, "owner-session") {
			t.Error("the rejection should be recorded on the task log with both session IDs")
		}
	})

	t.Run("nested session's Notification hook is ignored", func(t *testing.T) {
		task := startedTask(t, database, "owned, nested notification", "owner-session", db.StatusProcessing)

		nested := &ClaudeHookInput{SessionID: "nested-session", NotificationType: "idle_prompt"}
		if err := dispatchClaudeHook(database, task.ID, "Notification", nested); err != nil {
			t.Fatalf("dispatchClaudeHook: %v", err)
		}
		if got := taskStatus(t, database, task.ID); got != db.StatusProcessing {
			t.Errorf("status = %q, want %q", got, db.StatusProcessing)
		}
	})

	t.Run("nested session's SessionStart does not steal ownership", func(t *testing.T) {
		task := startedTask(t, database, "ownership stays put", "owner-session", db.StatusProcessing)

		nested := &ClaudeHookInput{SessionID: "nested-session", Source: "startup"}
		if err := dispatchClaudeHook(database, task.ID, "SessionStart", nested); err != nil {
			t.Fatalf("dispatchClaudeHook: %v", err)
		}
		fetched, _ := database.GetTask(task.ID)
		if fetched.ClaudeSessionID != "owner-session" {
			t.Errorf("claude_session_id = %q, want it left as %q", fetched.ClaudeSessionID, "owner-session")
		}
	})

	t.Run("the owning session still drives the task", func(t *testing.T) {
		task := startedTask(t, database, "owner acts", "owner-session", db.StatusProcessing)

		owner := &ClaudeHookInput{SessionID: "owner-session", StopReason: "end_turn"}
		if err := dispatchClaudeHook(database, task.ID, "Stop", owner); err != nil {
			t.Fatalf("dispatchClaudeHook: %v", err)
		}
		if got := taskStatus(t, database, task.ID); got != db.StatusBlocked {
			t.Errorf("status = %q, want %q", got, db.StatusBlocked)
		}
	})

	t.Run("a foreign session is still held to the worktree write-guard", func(t *testing.T) {
		// Ownership decides who moves the board. Where bytes land is a separate
		// question, and a nested agent is no more entitled to write outside the
		// worktree than the owning one.
		worktree := filepath.Join(t.TempDir(), ".task-worktrees", "wt-1")
		if err := os.MkdirAll(worktree, 0o755); err != nil {
			t.Fatal(err)
		}
		task := startedTask(t, database, "guarded", "owner-session", db.StatusProcessing)
		task.WorktreePath = worktree
		if err := database.UpdateTask(task); err != nil {
			t.Fatalf("set worktree: %v", err)
		}

		// Not a temp path: the guard always allows scratch writes under /tmp.
		escape := "/opt/some-other-project/outside.txt"
		nested := &ClaudeHookInput{
			SessionID: "nested-session",
			ToolName:  "Write",
			ToolInput: json.RawMessage(fmt.Sprintf(`{"file_path":%q,"content":"x"}`, escape)),
			Cwd:       worktree,
		}
		stdout := captureStdout(t, func() {
			if err := dispatchClaudeHook(database, task.ID, "PreToolUse", nested); err != nil {
				t.Fatalf("dispatchClaudeHook: %v", err)
			}
		})
		if !strings.Contains(stdout, "permissionDecision") {
			t.Errorf("no guard decision emitted for a nested session's out-of-worktree write; got %q", stdout)
		}
	})

	t.Run("rejection is logged once per foreign session", func(t *testing.T) {
		task := startedTask(t, database, "no log spam", "owner-session", db.StatusProcessing)

		for i := 0; i < 5; i++ {
			nested := &ClaudeHookInput{SessionID: "chatty-nested", ToolName: "Bash"}
			if err := dispatchClaudeHook(database, task.ID, "PreToolUse", nested); err != nil {
				t.Fatalf("dispatchClaudeHook: %v", err)
			}
		}
		logs, err := database.GetTaskLogs(task.ID, 100)
		if err != nil {
			t.Fatalf("get logs: %v", err)
		}
		n := 0
		for _, l := range logs {
			if strings.Contains(l.Content, "chatty-nested") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("logged the same foreign session %d times, want 1", n)
		}
	})
}

// TestHookSessionOwnershipChanges covers the legitimate ways the owning session
// ID changes: ty clearing it before a fresh launch, and the same conversation
// being re-keyed in place by /clear, a fork or a compaction.
func TestHookSessionOwnershipChanges(t *testing.T) {
	database := testDB(t)

	t.Run("an empty slot is claimed by the first session it sees", func(t *testing.T) {
		// This is the state ty leaves behind whenever it starts a fresh session:
		// UpdateTaskClaudeSessionID(id, "") then relaunch.
		task := startedTask(t, database, "fresh launch", "", db.StatusProcessing)

		in := &ClaudeHookInput{SessionID: "brand-new", Source: "startup"}
		if err := dispatchClaudeHook(database, task.ID, "SessionStart", in); err != nil {
			t.Fatalf("dispatchClaudeHook: %v", err)
		}
		fetched, _ := database.GetTask(task.ID)
		if fetched.ClaudeSessionID != "brand-new" {
			t.Errorf("claude_session_id = %q, want %q", fetched.ClaudeSessionID, "brand-new")
		}
		if !logContains(t, database, task.ID, "Claude session: brand-new") {
			t.Error("the session ID should be logged so a resume can find it")
		}
	})

	for _, source := range []string{"resume", "clear", "compact", "fork"} {
		t.Run("SessionStart source="+source+" re-keys the owning session", func(t *testing.T) {
			task := startedTask(t, database, "re-keyed by "+source, "old-session", db.StatusProcessing)

			in := &ClaudeHookInput{SessionID: "new-" + source, Source: source}
			if err := dispatchClaudeHook(database, task.ID, "SessionStart", in); err != nil {
				t.Fatalf("dispatchClaudeHook: %v", err)
			}
			fetched, _ := database.GetTask(task.ID)
			if fetched.ClaudeSessionID != "new-"+source {
				t.Errorf("claude_session_id = %q, want %q", fetched.ClaudeSessionID, "new-"+source)
			}

			// And the re-keyed session can now drive the task.
			if err := dispatchClaudeHook(database, task.ID, "Stop",
				&ClaudeHookInput{SessionID: "new-" + source, StopReason: "end_turn"}); err != nil {
				t.Fatalf("dispatchClaudeHook: %v", err)
			}
			if got := taskStatus(t, database, task.ID); got != db.StatusBlocked {
				t.Errorf("status = %q, want %q", got, db.StatusBlocked)
			}
		})
	}

	t.Run("a payload with no session ID is still handled", func(t *testing.T) {
		// Older CLIs (and our own synthetic calls) send no session_id. Ownership
		// cannot be checked, and dropping the hook would be worse than honouring it.
		task := startedTask(t, database, "no session id", "owner-session", db.StatusProcessing)

		if err := dispatchClaudeHook(database, task.ID, "Stop", &ClaudeHookInput{StopReason: "end_turn"}); err != nil {
			t.Fatalf("dispatchClaudeHook: %v", err)
		}
		if got := taskStatus(t, database, task.ID); got != db.StatusBlocked {
			t.Errorf("status = %q, want %q", got, db.StatusBlocked)
		}
	})
}

// TestUserPromptSubmitHook: a blocked task goes back to processing as soon as a
// prompt is submitted. Before this hook existed the task only recovered on the
// next PreToolUse, so a follow-up the agent answered from its own head — no tool
// call at all — spent the whole turn showing as "waiting for input".
func TestUserPromptSubmitHook(t *testing.T) {
	database := testDB(t)

	t.Run("blocked task resumes", func(t *testing.T) {
		task := startedTask(t, database, "blocked, then answered", "owner", db.StatusBlocked)

		in := &ClaudeHookInput{SessionID: "owner"}
		if err := dispatchClaudeHook(database, task.ID, "UserPromptSubmit", in); err != nil {
			t.Fatalf("dispatchClaudeHook: %v", err)
		}
		if got := taskStatus(t, database, task.ID); got != db.StatusProcessing {
			t.Errorf("status = %q, want %q", got, db.StatusProcessing)
		}
	})

	t.Run("unstarted task is left alone", func(t *testing.T) {
		task := &db.Task{Title: "never started", Status: db.StatusQueued, Type: db.TypeCode}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("create task: %v", err)
		}
		if err := dispatchClaudeHook(database, task.ID, "UserPromptSubmit", &ClaudeHookInput{}); err != nil {
			t.Fatalf("dispatchClaudeHook: %v", err)
		}
		if got := taskStatus(t, database, task.ID); got != db.StatusQueued {
			t.Errorf("status = %q, want %q", got, db.StatusQueued)
		}
	})
}

// TestStopFailureHook: a turn that ERRORED is not a turn that finished. The task
// parks for a human and the error is recorded where the board can show it.
func TestStopFailureHook(t *testing.T) {
	database := testDB(t)
	task := startedTask(t, database, "turn failed", "owner", db.StatusProcessing)

	in := &ClaudeHookInput{SessionID: "owner", Error: "API Error: 429 rate_limit_error"}
	if err := dispatchClaudeHook(database, task.ID, "StopFailure", in); err != nil {
		t.Fatalf("dispatchClaudeHook: %v", err)
	}
	if got := taskStatus(t, database, task.ID); got != db.StatusBlocked {
		t.Errorf("status = %q, want %q", got, db.StatusBlocked)
	}
	if !logContains(t, database, task.ID, "429 rate_limit_error") {
		t.Error("the provider error should be on the task log, not only in the transcript")
	}
}

// TestSessionEndHook: the agent's exit is recorded as a signal the daemon can
// read, and records nothing else. An exit says nothing about whether the work
// was finished, so it must never move a task on its own.
func TestSessionEndHook(t *testing.T) {
	database := testDB(t)

	t.Run("a real exit is recorded, the status is not touched", func(t *testing.T) {
		task := startedTask(t, database, "agent exited", "owner", db.StatusProcessing)
		database.AppendTaskLog(task.ID, "system", "Starting new claude session")

		in := &ClaudeHookInput{SessionID: "owner", Reason: "prompt_input_exit"}
		if err := dispatchClaudeHook(database, task.ID, "SessionEnd", in); err != nil {
			t.Fatalf("dispatchClaudeHook: %v", err)
		}
		if got := taskStatus(t, database, task.ID); got != db.StatusProcessing {
			t.Errorf("status = %q, want it untouched at %q", got, db.StatusProcessing)
		}
		ended, err := database.HasSessionEnded(task.ID)
		if err != nil {
			t.Fatalf("HasSessionEnded: %v", err)
		}
		if !ended {
			t.Error("the daemon should be able to see that the agent exited")
		}
	})

	t.Run("a /clear is not an exit", func(t *testing.T) {
		// /clear (and a compaction) end one conversation inside a still-running
		// agent. Reading those as an exit would have the daemon stop polling a
		// task whose agent is working.
		task := startedTask(t, database, "cleared, not exited", "owner", db.StatusProcessing)
		database.AppendTaskLog(task.ID, "system", "Starting new claude session")

		in := &ClaudeHookInput{SessionID: "owner", Reason: "clear"}
		if err := dispatchClaudeHook(database, task.ID, "SessionEnd", in); err != nil {
			t.Fatalf("dispatchClaudeHook: %v", err)
		}
		ended, err := database.HasSessionEnded(task.ID)
		if err != nil {
			t.Fatalf("HasSessionEnded: %v", err)
		}
		if ended {
			t.Error("a cleared conversation must not look like an exited agent")
		}
	})

	t.Run("a relaunch clears the signal", func(t *testing.T) {
		task := startedTask(t, database, "exited then relaunched", "owner", db.StatusProcessing)
		database.AppendTaskLog(task.ID, "system", "Starting new claude session")
		if err := dispatchClaudeHook(database, task.ID, "SessionEnd",
			&ClaudeHookInput{SessionID: "owner", Reason: "other"}); err != nil {
			t.Fatalf("dispatchClaudeHook: %v", err)
		}
		database.AppendTaskLog(task.ID, "system", "Resuming existing session owner")

		ended, err := database.HasSessionEnded(task.ID)
		if err != nil {
			t.Fatalf("HasSessionEnded: %v", err)
		}
		if ended {
			t.Error("an exit from before the relaunch must not count against the new session")
		}
	})
}

// TestReadClaudeHookInputOversized: UserPromptSubmit carries the whole prompt and
// PostToolUse the whole tool response, so a payload can be tens of megabytes. The
// hook reads a bounded prefix, drains the rest so the agent's write completes,
// and still acts on the scalar fields — which Claude puts first.
func TestReadClaudeHookInputOversized(t *testing.T) {
	huge := strings.Repeat("x", hookStdinLimit*2)
	payload := fmt.Sprintf(`{"session_id":"owner","hook_event_name":"UserPromptSubmit","cwd":"/tmp","prompt":%q}`, huge)

	r, w := io.Pipe()
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(w, payload)
		w.Close()
		written <- err
	}()

	done := make(chan *ClaudeHookInput, 1)
	go func() { done <- readClaudeHookInput(r) }()

	var input *ClaudeHookInput
	select {
	case input = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("readClaudeHookInput did not return on an oversized payload")
	}

	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("the agent's write to the hook failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the writer was left blocked — an undrained pipe stalls the agent")
	}

	if !input.truncated {
		t.Error("an oversized payload should be marked truncated")
	}
	if input.SessionID != "owner" {
		t.Errorf("session_id = %q, want %q recovered from the truncated payload", input.SessionID, "owner")
	}
	if input.HookEventName != "UserPromptSubmit" {
		t.Errorf("hook_event_name = %q, want it recovered from the truncated payload", input.HookEventName)
	}

	// And the transition it carries still happens.
	database := testDB(t)
	task := startedTask(t, database, "huge prompt", "owner", db.StatusBlocked)
	if err := dispatchClaudeHook(database, task.ID, "UserPromptSubmit", input); err != nil {
		t.Fatalf("dispatchClaudeHook: %v", err)
	}
	if got := taskStatus(t, database, task.ID); got != db.StatusProcessing {
		t.Errorf("status = %q, want %q", got, db.StatusProcessing)
	}
}

// TestParseClaudeHookInputTruncated checks the salvage path directly: a payload
// cut mid-field still yields the scalars that decide the transition.
func TestParseClaudeHookInputTruncated(t *testing.T) {
	full := `{"session_id":"s-1","hook_event_name":"Stop","stop_reason":"end_turn","transcript_path":"/tmp/t.jsonl","tool_input":{"command":"ls`
	in := parseClaudeHookInput([]byte(full))
	if in.SessionID != "s-1" || in.StopReason != "end_turn" || in.TranscriptPath != "/tmp/t.jsonl" {
		t.Errorf("salvaged %+v, want session s-1 / stop end_turn / transcript /tmp/t.jsonl", in)
	}
	if got := parseClaudeHookInput([]byte("not json at all")).SessionID; got != "" {
		t.Errorf("session_id = %q, want empty for an unparseable payload", got)
	}
}

// TestClaudeHookOnLockedDatabase: a database another process holds locked costs a
// status update, never a stalled agent. The hook gives up on its short busy
// timeout and reports success to Claude regardless.
func TestClaudeHookOnLockedDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	task := startedTask(t, database, "locked out", "owner", db.StatusProcessing)
	database.Close()

	// Hold a write transaction open from another connection, as a busy daemon does.
	locker, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open locker: %v", err)
	}
	defer locker.Close()
	tx, err := locker.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO task_logs (task_id, line_type, content) VALUES (?, 'system', 'holding the write lock')`, task.ID); err != nil {
		t.Fatalf("take write lock: %v", err)
	}
	defer tx.Rollback()

	t.Setenv("WORKTREE_DB_PATH", dbPath)
	t.Setenv("WORKTREE_TASK_ID", fmt.Sprint(task.ID))
	stdin, err := os.CreateTemp(t.TempDir(), "hook-*.json")
	if err != nil {
		t.Fatalf("temp stdin: %v", err)
	}
	if _, err := stdin.WriteString(`{"session_id":"owner","stop_reason":"end_turn"}`); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	stdin.Seek(0, io.SeekStart)
	realStdin := os.Stdin
	os.Stdin = stdin
	defer func() { os.Stdin = realStdin; stdin.Close() }()

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		// The command wrapper is what Claude runs: it must return (exit 0)
		// whatever happened underneath.
		runClaudeHookCommand("Stop")
		done <- time.Since(start)
	}()

	select {
	case elapsed := <-done:
		if elapsed > 4*time.Second {
			t.Errorf("hook took %s against a locked database; the agent waits for this", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("hook never returned against a locked database")
	}
}
