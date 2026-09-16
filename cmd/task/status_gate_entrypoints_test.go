package main

import (
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

// Only a human finishes a task, and the rule lives in exactly one place
// (db.SetTaskStatus), so every way into the system inherits it. This file
// checks that claim from each entry point in turn — the real `ty` binary, the
// HTTP handler, and the agent-facing completion path — on a task whose stored
// pull request state still says OPEN. (The TUI's command path is checked the
// same way in internal/ui/status_gate_test.go.)
//
// Two incidents shaped it. `ty close`, `ty status done`, `ty bulk close` and the
// web API once buried tasks with live PRs and left nothing behind; so every
// write now lands in the log with its actor. And later, the open-PR gate
// refused a person's close seconds after they merged, because the cached PR
// badge lagged the merge. So a human close always goes through, and the agent
// — automation — is the one that is refused.

var (
	tyBinOnce sync.Once
	tyBinPath string
	errTyBin  error
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
			errTyBin = err
			return
		}
		tyBinPath = filepath.Join(dir, "ty")
		out, err := exec.Command("go", "build", "-o", tyBinPath, ".").CombinedOutput()
		if err != nil {
			errTyBin = fmt.Errorf("build ty: %v\n%s", err, out)
		}
	})
	if errTyBin != nil {
		t.Fatalf("%v", errTyBin)
	}
	return tyBinPath
}

// gatedTaskDB builds a database holding one task that genuinely ran and whose
// stored pull request state is OPEN — the stale-badge shape a human close used
// to bounce off.
func gatedTaskDB(t *testing.T) (*db.DB, string, *db.Task) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database, path, openPRTask(t, database, 512)
}

// openPRTask adds a started, blocked task with an OPEN PR to database.
func openPRTask(t *testing.T, database *db.DB, prNumber int) *db.Task {
	t.Helper()
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
		`UPDATE tasks SET pr_number = ?, pr_url = ?, pr_info_json = '{"state":"OPEN"}' WHERE id = ?`,
		prNumber, fmt.Sprintf("https://github.com/acme/payments/pull/%d", prNumber), task.ID); err != nil {
		t.Fatalf("attach open PR: %v", err)
	}
	reloaded, _ := database.GetTask(task.ID)
	return reloaded
}

// assertClosedBy is the claim shared by every human entry point: the task is
// done, and the log says who closed it and that a person asked.
func assertClosedBy(t *testing.T, database *db.DB, taskID int64, wantActor db.Actor) {
	t.Helper()
	task, err := database.GetTask(taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != db.StatusDone {
		t.Fatalf("the %s entry point did not close task #%d: status %q", wantActor, taskID, task.Status)
	}
	events, err := database.GetStatusEvents(taskID)
	if err != nil {
		t.Fatalf("status events: %v", err)
	}
	last := events[len(events)-1]
	if last.Outcome != db.OutcomeApplied || last.To != db.StatusDone || last.Actor != wantActor {
		t.Fatalf("the close is not in the log as %s: %+v", wantActor, last)
	}
	if strings.TrimSpace(last.Evidence.Human) == "" {
		t.Errorf("the %s close does not record that a person asked: %+v", wantActor, last.Evidence)
	}
}

// assertRefusedHumanOnly: the task did not move, and the attempt is in the log
// with the human-only gate and the actor that tried.
func assertRefusedHumanOnly(t *testing.T, database *db.DB, taskID int64, wantActor db.Actor) {
	t.Helper()
	task, err := database.GetTask(taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status == db.StatusDone {
		t.Fatalf("automation (%s) moved the task to done", wantActor)
	}
	events, err := database.GetStatusEvents(taskID)
	if err != nil {
		t.Fatalf("status events: %v", err)
	}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Outcome == db.OutcomeRefused && e.Gate == db.GateHumanOnly && e.Actor == wantActor {
			return
		}
	}
	t.Fatalf("no human-only refusal by actor %q in the log: %+v", wantActor, events)
}

// TestHumanClose_CLIEntryPoint drives the real binary. Each subcommand closes a
// task whose cached PR state still says OPEN.
func TestHumanClose_CLIEntryPoint(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the ty binary")
	}
	database, path, first := gatedTaskDB(t)
	bin := buildTyBinary(t)

	for i, sub := range [][]string{{"close"}, {"status", "", "done"}, {"bulk", "close"}} {
		task := first
		if i > 0 {
			task = openPRTask(t, database, 512+i)
		}
		args := append([]string{}, sub...)
		if len(args) == 3 {
			args[1] = fmt.Sprint(task.ID)
		} else {
			args = append(args, fmt.Sprint(task.ID))
		}
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "WORKTREE_DB_PATH="+path, "TY_SKIP_VERSION_CHECK=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("`ty %s` failed on a task with a stale OPEN PR: %v\n%s",
				strings.Join(args, " "), err, out)
			continue
		}
		assertClosedBy(t, database, task.ID, db.ActorCLI)
	}
}

// TestHumanClose_WebEntryPoint drives the HTTP handlers the web UI and the
// desktop app call: a close is a person's decision and succeeds.
func TestHumanClose_WebEntryPoint(t *testing.T) {
	database, _, first := gatedTaskDB(t)
	srv := web.New(web.Config{Addr: ":0", DB: database})

	for i, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"close", "POST", "/api/tasks/%d/close", ""},
		{"set-status", "POST", "/api/tasks/%d/status", `{"status":"done"}`},
	} {
		task := first
		if i > 0 {
			task = openPRTask(t, database, 512+i)
		}
		req := httptest.NewRequest(tc.method, fmt.Sprintf(tc.path, task.ID), strings.NewReader(tc.body))
		req.SetPathValue("id", fmt.Sprint(task.ID))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("%s: want 200, got %d (%s)", tc.name, w.Code, w.Body.String())
			continue
		}
		assertClosedBy(t, database, task.ID, db.ActorWeb)
	}
}

// TestAgentCannotClose_MCPEntryPoint covers the agent-facing path twice over.
//
// taskyou_complete routes a PR-bearing task to review rather than done — that
// is the designed behaviour, and the first assertion holds it. The second: an
// agent that skips the MCP tool and writes the status itself (the historic
// "agents without the taskyou MCP tools write status straight into the DB")
// hits the human-only gate, because the gate is not in the tool.
func TestAgentCannotClose_MCPEntryPoint(t *testing.T) {
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
		t.Fatal("taskyou_complete moved a task to done")
	}

	// The direct write an agent without the MCP tools would attempt.
	err = database.SetTaskStatus(task.ID, db.StatusDone, db.ActorMCP,
		"agent asserts the work is finished",
		db.Observedf("agent reports it opened PR #512 and is done"))
	if !db.IsRefused(err) || db.RefusalGate(err) != db.GateHumanOnly {
		t.Fatalf("a direct agent done-write was not refused as human-only: %v", err)
	}
	assertRefusedHumanOnly(t, database, task.ID, db.ActorMCP)
}

// TestStatusConsistencyAfterRefusals: refusals are recorded but change
// nothing, so the cached row must still equal the fold over the log.
func TestStatusConsistencyAfterRefusals(t *testing.T) {
	database, _, task := gatedTaskDB(t)

	for _, actor := range []db.Actor{db.ActorMCP, db.ActorSweep, db.ActorDaemon, db.ActorHook, db.ActorSystem} {
		_ = database.SetTaskStatus(task.ID, db.StatusDone, actor,
			"attempted close", db.Observedf("bypass attempt as %s", actor))
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
