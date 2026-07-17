package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// completeTask runs one taskyou_complete call for the given task and returns the
// first content block's text.
func completeTask(t *testing.T, database *db.DB, taskID int64) string {
	t.Helper()
	request := map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{
			"name":      "taskyou_complete",
			"arguments": map[string]interface{}{"summary": "done"},
		},
	}
	reqBytes, _ := json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	server, output := testServer(database, taskID, string(reqBytes))
	server.Run()

	var resp jsonRPCResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("taskyou_complete errored: %s", resp.Error.Message)
	}
	result, _ := resp.Result.(map[string]interface{})
	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("no content blocks: %v", result)
	}
	block, _ := content[0].(map[string]interface{})
	text, _ := block["text"].(string)
	return text
}

// verifyGateStep creates a processing task with a real worktree dir and an
// evidence-gate command registered.
func verifyGateStep(t *testing.T, database *db.DB, command string) *db.Task {
	t.Helper()
	if err := database.CreateProject(&db.Project{Name: "test-project", Path: "/tmp/test-project"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &db.Task{Title: "gated step", Status: db.StatusProcessing, Project: "test-project"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	task.WorktreePath = t.TempDir()
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	if err := database.SetStepVerify(task.ID, command); err != nil {
		t.Fatalf("set verify: %v", err)
	}
	return task
}

// TestVerifyGateRejectsOnFailure proves a failing verify command blocks completion:
// the task stays 'processing' (NOT done) and the command output comes back to the
// agent so it can fix and retry.
func TestVerifyGateRejectsOnFailure(t *testing.T) {
	database := testDB(t)
	task := verifyGateStep(t, database, "echo BUILD_IS_RED >&2; exit 1")

	text := completeTask(t, database, task.ID)

	reloaded, _ := database.GetTask(task.ID)
	if reloaded.Status == db.StatusDone {
		t.Fatal("task was marked done despite a failing verify command")
	}
	if reloaded.Status != db.StatusProcessing {
		t.Errorf("task status = %q, want still processing", reloaded.Status)
	}
	if !strings.Contains(text, "BUILD_IS_RED") {
		t.Errorf("response did not include the verify output; got: %q", text)
	}
}

// TestVerifyGatePassesOnSuccess proves a passing verify command lets completion
// through: the task reaches 'done'.
func TestVerifyGatePassesOnSuccess(t *testing.T) {
	database := testDB(t)
	task := verifyGateStep(t, database, "exit 0")

	completeTask(t, database, task.ID)

	reloaded, _ := database.GetTask(task.ID)
	if reloaded.Status != db.StatusDone {
		t.Errorf("task status = %q, want done after passing verify", reloaded.Status)
	}
}
