package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bborn/workflow/internal/completion"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/web"
)

// The open-PR gate lives in exactly one place, so every way into the system
// inherits it. This file attacks that claim from each entry point in turn —
// the real `ty` binary, the HTTP handler, and the agent-facing completion path
// — on a task whose pull request is still open. (The TUI's command path is
// attacked the same way in internal/ui/status_gate_test.go, where the model's
// own commands are reachable.)
//
// The historic incident: `ty close`, `ty status done`, `ty bulk close` and the
// web API each performed a plain status write, so each one buried tasks with
// live PRs and left nothing behind explaining it. Every entry point below must
// refuse, and the refusal must be a fact in the log.

var (
	tyBinOnce sync.Once
	tyBinPath string
	tyBinErr  error
)

// buildTyBinary compiles the real CLI once per test run. The `ty close` path
// exits the process on refusal, so it cannot be exercised in-process — and a
// test that called SetTaskStatus directly instead would be asserting about the
// gate rather than about the command that reaches it.
func buildTyBinary(t *testing.T) string {
	t.Helper()
	tyBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ty-bin-*")
		if err != nil {
			tyBinErr = err
			return
		}
		tyBinPath = filepath.Join(dir, "ty")
		out, err := exec.Command("go", "build", "-o", tyBinPath, ".").CombinedOutput()
		if err != nil {
			tyBinErr = fmt.Errorf("build ty: %v\n%s", err, out)
		}
	})
	if tyBinErr != nil {
		t.Fatalf("%v", tyBinErr)
	}
	return tyBinPath
}

// gatedTaskDB builds a database holding one task that genuinely ran and whose
// pull request is still open — the exact shape `ty close` used to bury.
func gatedTaskDB(t *testing.T) (*db.DB, string, *db.Task) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	task := &db.Task{
		Title:   "Add retry-with-backoff to the Stripe webhook consumer",
		Body:    "The consumer drops events when Stripe redelivers during a deploy.",
		Status:  db.StatusBlocked,
		Type:    db.TypeCode,
		Project: "personal",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := database.MarkTaskStarted(task.ID); err != nil {
		t.Fatalf("mark started: %v", err)
	}
	if _, err := database.Exec(
		`UPDATE tasks SET pr_number = 512, pr_url = ?, pr_info_json = '{"state":"OPEN"}' WHERE id = ?`,
		"https://github.com/acme/payments/pull/512", task.ID); err != nil {
		t.Fatalf("attach open PR: %v", err)
	}
	reloaded, _ := database.GetTask(task.ID)
	return database, path, reloaded
}

// assertStillParked is the claim shared by every entry point: the task did not
// move, and the attempt is in the log with the gate that stopped it.
func assertStillParked(t *testing.T, database *db.DB, taskID int64, wantActor db.Actor) {
	t.Helper()
	task, err := database.GetTask(taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status == db.StatusDone {
		t.Fatalf("the task with an open PR was buried by the %s entry point", wantActor)
	}
	events, err := database.GetStatusEvents(taskID)
	if err != nil {
		t.Fatalf("status events: %v", err)
	}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Outcome == db.OutcomeRefused && e.Gate == db.GateOpenPR && e.Actor == wantActor {
			return
		}
	}
	t.Fatalf("no open-pr refusal by actor %q in the log: %+v", wantActor, events)
}

// TestOpenPRGate_CLIEntryPoint drives the real binary: `ty close <id>`.
func TestOpenPRGate_CLIEntryPoint(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the ty binary")
	}
	database, path, task := gatedTaskDB(t)
	bin := buildTyBinary(t)

	// Each subcommand that used to bury a task with a live PR.
	for _, args := range [][]string{
		{"close", fmt.Sprint(task.ID)},
		{"status", fmt.Sprint(task.ID), "done"},
		{"bulk", "close", fmt.Sprint(task.ID)},
	} {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "WORKTREE_DB_PATH="+path, "TY_SKIP_VERSION_CHECK=1")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("`ty %s` succeeded on a task with an open PR:\n%s",
				strings.Join(args, " "), out)
		}
		if !strings.Contains(string(out), "512") {
			t.Errorf("`ty %s` did not tell the user which PR blocked it:\n%s",
				strings.Join(args, " "), out)
		}
	}
	assertStillParked(t, database, task.ID, db.ActorCLI)
}

// TestOpenPRGate_WebEntryPoint drives the HTTP handlers the web UI and the
// desktop app call. A refusal is a 409 carrying the gate's own words, not a
// 500 and not a silent success.
func TestOpenPRGate_WebEntryPoint(t *testing.T) {
	database, _, task := gatedTaskDB(t)
	srv := web.New(web.Config{Addr: ":0", DB: database})

	for _, tc := range []struct {
		name    string
		method  string
		target  string
		body    string
		handler string
	}{
		{"close", "POST", fmt.Sprintf("/api/tasks/%d/close", task.ID), "", "close"},
		{"set-status", "POST", fmt.Sprintf("/api/tasks/%d/status", task.ID), `{"status":"done"}`, "status"},
	} {
		req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
		req.SetPathValue("id", fmt.Sprint(task.ID))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusConflict {
			t.Errorf("%s: want 409, got %d (%s)", tc.name, w.Code, w.Body.String())
			continue
		}
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if !strings.Contains(body.Error, "512") {
			t.Errorf("%s: the 409 does not name the PR that blocked it: %q", tc.name, body.Error)
		}
	}
	assertStillParked(t, database, task.ID, db.ActorWeb)
}

// TestOpenPRGate_MCPEntryPoint covers the agent-facing path twice over.
//
// taskyou_complete routes a PR-bearing task to review rather than done — that
// is the designed behaviour, and the first assertion holds it. The second is
// the one that matters for this change: an agent that skips the MCP tool and
// writes the status itself (the historic "agents without the taskyou MCP tools
// write status straight into the DB") now hits the same gate, because the gate
// is no longer in the tool.
func TestOpenPRGate_MCPEntryPoint(t *testing.T) {
	database, _, task := gatedTaskDB(t)

	outcome, err := completion.Complete(database, task.ID,
		"Added exponential backoff and a redelivery dedupe key.",
		completion.Options{AsyncSummary: false, Actor: db.ActorMCP})
	if err != nil {
		t.Fatalf("completion.Complete: %v", err)
	}
	if outcome.Kind != completion.KindPRReview {
		t.Fatalf("taskyou_complete returned %v for a task with an open PR, want PR-review parking", outcome.Kind)
	}
	if reloaded, _ := database.GetTask(task.ID); reloaded.Status == db.StatusDone {
		t.Fatal("taskyou_complete buried a task with an open PR")
	}

	// The direct write an agent without the MCP tools would attempt.
	err = database.SetTaskStatus(task.ID, db.StatusDone, db.ActorMCP,
		"agent asserts the work is finished",
		db.Observedf("agent reports it opened PR #512 and is done"))
	if !db.IsRefused(err) || db.RefusalGate(err) != db.GateOpenPR {
		t.Fatalf("a direct agent done-write was not refused: %v", err)
	}
	assertStillParked(t, database, task.ID, db.ActorMCP)
}

// TestStatusConsistencyAfterRefusals: refusals are recorded but change
// nothing, so the cached row must still equal the fold over the log.
func TestStatusConsistencyAfterRefusals(t *testing.T) {
	database, _, task := gatedTaskDB(t)

	for _, actor := range []db.Actor{db.ActorCLI, db.ActorWeb, db.ActorMCP, db.ActorTUI, db.ActorSweep} {
		_ = database.SetTaskStatus(task.ID, db.StatusDone, actor,
			"attempted close", db.ByHuman("bypass attempt as %s", actor))
	}
	mismatches, err := database.CheckStatusConsistency()
	if err != nil {
		t.Fatalf("CheckStatusConsistency: %v", err)
	}
	if len(mismatches) != 0 {
		t.Fatalf("refusals broke the projection: %v", mismatches)
	}
	events, _ := database.GetStatusEvents(task.ID)
	refusals := 0
	for _, e := range events {
		if e.Outcome == db.OutcomeRefused {
			refusals++
		}
	}
	if refusals != 5 {
		t.Fatalf("want 5 logged refusals, got %d", refusals)
	}
}
