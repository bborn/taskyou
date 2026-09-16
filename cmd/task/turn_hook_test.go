package main

import (
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// The turn counters exist so a surface that sends a prompt can tell the agent's
// reply from the end of whatever it was already doing. Both ends are moved by
// hooks, so this drives the hook handlers themselves. What the same hooks do to
// the task's STATUS is covered by claude_hook_test.go; this is only about the
// counters.
func TestTurnHooksCountAnExchange(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "hooks.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	if err := database.CreateProject(&db.Project{Name: "storefront", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &db.Task{Title: "Add the abandoned-cart email", Status: db.StatusProcessing, Project: "storefront"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := handleUserPromptSubmitHook(database, task.ID, &ClaudeHookInput{SessionID: "s1"}); err != nil {
		t.Fatalf("UserPromptSubmit hook: %v", err)
	}
	if got, _ := database.AgentTurnState(task.ID); got.Started != 1 || got.Completed != 0 {
		t.Fatalf("after the prompt = %+v, want {1 0}", got)
	}

	// A Stop that only means "a tool is about to run" is not the end of a turn.
	if err := handleStopHook(database, task.ID, &ClaudeHookInput{StopReason: "tool_use"}); err != nil {
		t.Fatalf("Stop(tool_use) hook: %v", err)
	}
	if got, _ := database.AgentTurnState(task.ID); got.Completed != 0 {
		t.Fatalf("a tool_use stop ended the turn: %+v", got)
	}

	if err := handleStopHook(database, task.ID, &ClaudeHookInput{StopReason: "end_turn"}); err != nil {
		t.Fatalf("Stop hook: %v", err)
	}
	if got, _ := database.AgentTurnState(task.ID); got.Started != 1 || got.Completed != 1 {
		t.Fatalf("after the stop = %+v, want {1 1}", got)
	}
}

// A task the Stop hook declines to touch (it never started) still has to move
// its counter, or a caller waiting on its reply waits forever.
func TestStopHookCountsTheTurnEvenForAnUnstartedTask(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "hooks.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	if err := database.CreateProject(&db.Project{Name: "storefront", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &db.Task{Title: "Trim the nightly report", Status: db.StatusBacklog, Project: "storefront"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	database.BeginAgentTurn(task.ID)
	if err := handleStopHook(database, task.ID, &ClaudeHookInput{StopReason: "end_turn"}); err != nil {
		t.Fatalf("Stop hook: %v", err)
	}
	if got, _ := database.AgentTurnState(task.ID); got.Completed != 1 {
		t.Errorf("turn = %+v, want the turn closed", got)
	}
}
