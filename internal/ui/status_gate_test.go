package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// The TUI's half of the bypass test. The other three entry points — the real
// `ty` binary, the HTTP handlers, and the agent-facing completion path — are
// attacked in cmd/task/status_gate_entrypoints_test.go; the board's own
// commands are only reachable from inside this package.
//
// Two claims here, and the second is the one that used to fail: the gate
// refuses the close, AND the board tells the user why. A refusal that only
// reloads the board reads as a dropped keypress, which is how people learned
// to press close twice.

func gatedBoardTask(t *testing.T) (*db.DB, *AppModel, *db.Task) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "tasks.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	task := &db.Task{
		Title:   "Add retry-with-backoff to the Stripe webhook consumer",
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

	model := NewAppModel(database, executor.New(database, config.New(database)), dir, "test")
	return database, model, reloaded
}

// TestOpenPRGate_TUIEntryPoint: the board's close command is refused for a
// task whose PR is still open, and the refusal is a fact in the log.
func TestOpenPRGate_TUIEntryPoint(t *testing.T) {
	database, model, task := gatedBoardTask(t)

	msg := model.closeTask(task.ID)()
	closed, ok := msg.(taskClosedMsg)
	if !ok {
		t.Fatalf("closeTask returned %T, want taskClosedMsg", msg)
	}
	if closed.err == nil {
		t.Fatal("the board closed a task with an open PR")
	}
	if !db.IsRefused(closed.err) || db.RefusalGate(closed.err) != db.GateOpenPR {
		t.Fatalf("want an open-pr refusal, got %v", closed.err)
	}

	reloaded, _ := database.GetTask(task.ID)
	if reloaded.Status == db.StatusDone {
		t.Fatalf("the task moved to %q anyway", reloaded.Status)
	}

	events, err := database.GetStatusEvents(task.ID)
	if err != nil {
		t.Fatalf("status events: %v", err)
	}
	last := events[len(events)-1]
	if last.Outcome != db.OutcomeRefused || last.Actor != db.ActorTUI || last.Gate != db.GateOpenPR {
		t.Fatalf("the TUI's refused close is not in the log: %+v", last)
	}
}

// TestRefusedCloseTellsTheUserWhy: the board must render the gate's own
// explanation, not swallow it and reload.
func TestRefusedCloseTellsTheUserWhy(t *testing.T) {
	_, model, task := gatedBoardTask(t)

	msg := model.closeTask(task.ID)()
	model.Update(msg)

	if model.notification == "" {
		t.Fatal("a refused close showed the user nothing")
	}
	if !strings.Contains(model.notification, "512") {
		t.Errorf("the notice does not name the PR that blocked the close: %q", model.notification)
	}
	if !strings.Contains(strings.ToLower(model.notification), "refused") {
		t.Errorf("the notice does not say the close was refused: %q", model.notification)
	}
	if !model.notifyUntil.After(time.Now()) {
		t.Error("the notice expired immediately")
	}
}

// TestTUIMoveToDoneIsGatedToo: dragging a card into Done is the same write as
// pressing close, and inherits the same gate.
func TestTUIMoveToDoneIsGatedToo(t *testing.T) {
	database, model, task := gatedBoardTask(t)

	msg := model.changeTaskStatus(task.ID, db.StatusDone)()
	changed, ok := msg.(taskStatusChangedMsg)
	if !ok {
		t.Fatalf("changeTaskStatus returned %T", msg)
	}
	if db.RefusalGate(changed.err) != db.GateOpenPR {
		t.Fatalf("moving a card to Done skipped the open-PR gate: %v", changed.err)
	}
	reloaded, _ := database.GetTask(task.ID)
	if reloaded.Status == db.StatusDone {
		t.Fatal("the card landed in Done anyway")
	}

	model.Update(msg)
	if !strings.Contains(model.notification, "512") {
		t.Errorf("a refused board move showed no reason: %q", model.notification)
	}
}

// TestRefusalNoticeFallsBackToPlainErrors: not every failure is a gate
// refusal, and an ordinary error must still reach the user unmangled.
func TestRefusalNoticeFallsBackToPlainErrors(t *testing.T) {
	plain := fmt.Errorf("database is locked")
	if got := refusalNotice(plain); got != "database is locked" {
		t.Errorf("refusalNotice mangled a plain error: %q", got)
	}
	refused := &db.RefusedError{TaskID: 7, From: "blocked", To: "done",
		Gate: db.GateOpenPR, Detail: "PR #512 is still open"}
	if got := refusalNotice(refused); !strings.HasPrefix(got, "Refused: ") {
		t.Errorf("refusalNotice did not mark a refusal as one: %q", got)
	}
}

// statusActionError must not lose an error just because the message type
// travels in a shared update case.
func TestStatusActionErrorCoversEveryStatusMessage(t *testing.T) {
	boom := fmt.Errorf("boom")
	for _, msg := range []tea.Msg{
		taskClosedMsg{err: boom},
		taskArchivedMsg{err: boom},
		taskUnarchivedMsg{err: boom},
		taskRetriedMsg{err: boom},
		taskStatusChangedMsg{err: boom},
	} {
		if got := statusActionError(msg); got != boom {
			t.Errorf("%T: statusActionError returned %v, want the error", msg, got)
		}
	}
	if got := statusActionError(taskDeletedMsg{}); got != nil {
		t.Errorf("statusActionError invented an error for a message without one: %v", got)
	}
}
