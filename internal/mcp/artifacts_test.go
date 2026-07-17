package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// resultText runs a single tools/call and returns the first content block's text.
func callArtifactTool(t *testing.T, database *db.DB, taskID int64, name string, args map[string]interface{}) string {
	t.Helper()
	request := map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": name, "arguments": args},
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
		t.Fatalf("tool %s errored: %s", name, resp.Error.Message)
	}
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %v", resp.Result)
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("no content blocks: %v", result)
	}
	block, _ := content[0].(map[string]interface{})
	text, _ := block["text"].(string)
	return text
}

// newWorkflowStep creates a pipeline-tagged task on the given shared branch.
func newWorkflowStep(t *testing.T, database *db.DB, title, branch string, root bool) *db.Task {
	t.Helper()
	task := &db.Task{Title: title, Status: db.StatusProcessing, Project: "test-project", Tags: "pipeline"}
	if !root {
		task.SourceBranch = branch
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create step: %v", err)
	}
	if root {
		task.BranchName = branch // CreateTask doesn't persist branch_name.
		if err := database.UpdateTask(task); err != nil {
			t.Fatalf("set branch: %v", err)
		}
	}
	return task
}

// TestArtifactRoundTripScopedToBranch proves an artifact set by one workflow step
// is readable by another step on the same branch, and that a step on a different
// branch cannot see it.
func TestArtifactRoundTripScopedToBranch(t *testing.T) {
	database := testDB(t)
	if err := database.CreateProject(&db.Project{Name: "test-project", Path: "/tmp/test-project"}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	branch := "pipeline/1-do-thing"
	research := newWorkflowStep(t, database, "[Research] goal", branch, true)
	implement := newWorkflowStep(t, database, "[Implement] goal", branch, false)

	// research writes the artifact.
	callArtifactTool(t, database, research.ID, "taskyou_set_artifact", map[string]interface{}{
		"name": "research", "content": "the findings",
	})

	// implement (same branch) reads it back by name.
	got := callArtifactTool(t, database, implement.ID, "taskyou_get_artifact", map[string]interface{}{
		"name": "research",
	})
	if !strings.Contains(got, "the findings") {
		t.Errorf("implement did not read the research artifact; got: %q", got)
	}

	// A step on a different branch must not see it.
	otherBranch := "pipeline/2-other"
	other := newWorkflowStep(t, database, "[Research] other", otherBranch, true)
	otherGot := callArtifactTool(t, database, other.ID, "taskyou_get_artifact", map[string]interface{}{
		"name": "research",
	})
	if strings.Contains(otherGot, "the findings") {
		t.Errorf("artifact leaked across branches; other-branch get returned: %q", otherGot)
	}
}

// TestArtifactGetWithoutNameListsAll confirms omitting name returns every artifact
// for the workflow branch.
func TestArtifactGetWithoutNameListsAll(t *testing.T) {
	database := testDB(t)
	if err := database.CreateProject(&db.Project{Name: "test-project", Path: "/tmp/test-project"}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	branch := "pipeline/1-do-thing"
	step := newWorkflowStep(t, database, "[Research] goal", branch, true)

	callArtifactTool(t, database, step.ID, "taskyou_set_artifact", map[string]interface{}{
		"name": "research-questions", "content": "the questions",
	})
	callArtifactTool(t, database, step.ID, "taskyou_set_artifact", map[string]interface{}{
		"name": "research", "content": "the findings",
	})

	all := callArtifactTool(t, database, step.ID, "taskyou_get_artifact", map[string]interface{}{})
	if !strings.Contains(all, "the questions") || !strings.Contains(all, "the findings") {
		t.Errorf("list-all did not include both artifacts; got: %q", all)
	}
}
