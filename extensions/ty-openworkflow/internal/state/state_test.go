package state

import (
	"testing"
	"time"
)

func TestDB_OpenAndClose(t *testing.T) {
	tmpDir := t.TempDir()

	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("failed to close database: %v", err)
	}
}

func TestDB_SaveAndGetWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	workflow := &Workflow{
		ID:          "test-workflow",
		Name:        "Test Workflow",
		Description: "A test workflow",
		Version:     "1.0.0",
		Runtime:     "node",
		Code:        "function workflow() {}",
		Adapter:     "exec",
	}

	// Save workflow
	if err := db.SaveWorkflow(workflow); err != nil {
		t.Fatalf("failed to save workflow: %v", err)
	}

	// Get workflow
	retrieved, err := db.GetWorkflow("test-workflow")
	if err != nil {
		t.Fatalf("failed to get workflow: %v", err)
	}

	if retrieved == nil {
		t.Fatal("retrieved workflow is nil")
	}
	if retrieved.ID != workflow.ID {
		t.Errorf("expected ID '%s', got '%s'", workflow.ID, retrieved.ID)
	}
	if retrieved.Name != workflow.Name {
		t.Errorf("expected Name '%s', got '%s'", workflow.Name, retrieved.Name)
	}
	if retrieved.Version != workflow.Version {
		t.Errorf("expected Version '%s', got '%s'", workflow.Version, retrieved.Version)
	}
}

func TestDB_ListWorkflows(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Add some workflows
	for i := 1; i <= 3; i++ {
		workflow := &Workflow{
			ID:      "workflow-" + string(rune('0'+i)),
			Name:    "Workflow " + string(rune('0'+i)),
			Version: "1.0.0",
			Runtime: "node",
			Code:    "function workflow() {}",
			Adapter: "exec",
		}
		if err := db.SaveWorkflow(workflow); err != nil {
			t.Fatalf("failed to save workflow: %v", err)
		}
	}

	// List workflows
	workflows, err := db.ListWorkflows()
	if err != nil {
		t.Fatalf("failed to list workflows: %v", err)
	}

	if len(workflows) != 3 {
		t.Errorf("expected 3 workflows, got %d", len(workflows))
	}
}

func TestDB_DeleteWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	workflow := &Workflow{
		ID:      "to-delete",
		Name:    "To Delete",
		Version: "1.0.0",
		Runtime: "node",
		Code:    "function workflow() {}",
		Adapter: "exec",
	}

	if err := db.SaveWorkflow(workflow); err != nil {
		t.Fatalf("failed to save workflow: %v", err)
	}

	if err := db.DeleteWorkflow("to-delete"); err != nil {
		t.Fatalf("failed to delete workflow: %v", err)
	}

	// Should not exist anymore
	retrieved, err := db.GetWorkflow("to-delete")
	if err != nil {
		t.Fatalf("failed to get workflow: %v", err)
	}
	if retrieved != nil {
		t.Error("workflow should have been deleted")
	}
}

func TestDB_SaveAndGetRun(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// First save a workflow
	workflow := &Workflow{
		ID:      "workflow-1",
		Name:    "Workflow 1",
		Version: "1.0.0",
		Runtime: "node",
		Code:    "function workflow() {}",
		Adapter: "exec",
	}
	if err := db.SaveWorkflow(workflow); err != nil {
		t.Fatalf("failed to save workflow: %v", err)
	}

	// Save a run
	run := &WorkflowRun{
		ID:         "run-123",
		WorkflowID: "workflow-1",
		TaskID:     42,
		Adapter:    "exec",
		Status:     "running",
		Input:      `{"key":"value"}`,
		StartedAt:  time.Now(),
	}

	if err := db.SaveRun(run); err != nil {
		t.Fatalf("failed to save run: %v", err)
	}

	// Get run
	retrieved, err := db.GetRun("run-123")
	if err != nil {
		t.Fatalf("failed to get run: %v", err)
	}

	if retrieved == nil {
		t.Fatal("retrieved run is nil")
	}
	if retrieved.ID != run.ID {
		t.Errorf("expected ID '%s', got '%s'", run.ID, retrieved.ID)
	}
	if retrieved.TaskID != run.TaskID {
		t.Errorf("expected TaskID %d, got %d", run.TaskID, retrieved.TaskID)
	}
	if retrieved.Status != run.Status {
		t.Errorf("expected Status '%s', got '%s'", run.Status, retrieved.Status)
	}
}

func TestDB_ListRuns(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Save a workflow
	workflow := &Workflow{
		ID:      "workflow-1",
		Name:    "Workflow 1",
		Version: "1.0.0",
		Runtime: "node",
		Code:    "function workflow() {}",
		Adapter: "exec",
	}
	if err := db.SaveWorkflow(workflow); err != nil {
		t.Fatalf("failed to save workflow: %v", err)
	}

	// Save multiple runs
	statuses := []string{"running", "completed", "running"}
	for i, status := range statuses {
		run := &WorkflowRun{
			ID:         "run-" + string(rune('0'+i+1)),
			WorkflowID: "workflow-1",
			Adapter:    "exec",
			Status:     status,
			StartedAt:  time.Now(),
		}
		if err := db.SaveRun(run); err != nil {
			t.Fatalf("failed to save run: %v", err)
		}
	}

	// List all runs
	runs, err := db.ListRuns("", "", 0)
	if err != nil {
		t.Fatalf("failed to list runs: %v", err)
	}
	if len(runs) != 3 {
		t.Errorf("expected 3 runs, got %d", len(runs))
	}

	// List by status
	runs, err = db.ListRuns("", "running", 0)
	if err != nil {
		t.Fatalf("failed to list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Errorf("expected 2 running runs, got %d", len(runs))
	}

	// List with limit
	runs, err = db.ListRuns("", "", 1)
	if err != nil {
		t.Fatalf("failed to list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("expected 1 run with limit, got %d", len(runs))
	}
}

func TestDB_UpdateRunStatus(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Save a workflow
	workflow := &Workflow{
		ID:      "workflow-1",
		Name:    "Workflow 1",
		Version: "1.0.0",
		Runtime: "node",
		Code:    "function workflow() {}",
		Adapter: "exec",
	}
	if err := db.SaveWorkflow(workflow); err != nil {
		t.Fatalf("failed to save workflow: %v", err)
	}

	// Save a run
	run := &WorkflowRun{
		ID:         "run-to-update",
		WorkflowID: "workflow-1",
		Adapter:    "exec",
		Status:     "running",
		StartedAt:  time.Now(),
	}
	if err := db.SaveRun(run); err != nil {
		t.Fatalf("failed to save run: %v", err)
	}

	// Update status
	output := `{"result":"success"}`
	if err := db.UpdateRunStatus("run-to-update", "completed", output, ""); err != nil {
		t.Fatalf("failed to update run status: %v", err)
	}

	// Verify update
	retrieved, err := db.GetRun("run-to-update")
	if err != nil {
		t.Fatalf("failed to get run: %v", err)
	}

	if retrieved.Status != "completed" {
		t.Errorf("expected status 'completed', got '%s'", retrieved.Status)
	}
	if retrieved.Output != output {
		t.Errorf("expected output '%s', got '%s'", output, retrieved.Output)
	}
	if retrieved.CompletedAt == nil {
		t.Error("completed_at should be set")
	}
}

func TestDB_LinkTaskToWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Save a workflow
	workflow := &Workflow{
		ID:      "workflow-1",
		Name:    "Workflow 1",
		Version: "1.0.0",
		Runtime: "node",
		Code:    "function workflow() {}",
		Adapter: "exec",
	}
	if err := db.SaveWorkflow(workflow); err != nil {
		t.Fatalf("failed to save workflow: %v", err)
	}

	// Link task to workflow
	if err := db.LinkTaskToWorkflow(123, "workflow-1", "run-abc"); err != nil {
		t.Fatalf("failed to link task: %v", err)
	}

	// Get task workflow
	workflowID, runID, err := db.GetTaskWorkflow(123)
	if err != nil {
		t.Fatalf("failed to get task workflow: %v", err)
	}

	if workflowID != "workflow-1" {
		t.Errorf("expected workflow ID 'workflow-1', got '%s'", workflowID)
	}
	if runID != "run-abc" {
		t.Errorf("expected run ID 'run-abc', got '%s'", runID)
	}
}

func TestDB_GetRunByTaskID(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Save a workflow
	workflow := &Workflow{
		ID:      "workflow-1",
		Name:    "Workflow 1",
		Version: "1.0.0",
		Runtime: "node",
		Code:    "function workflow() {}",
		Adapter: "exec",
	}
	if err := db.SaveWorkflow(workflow); err != nil {
		t.Fatalf("failed to save workflow: %v", err)
	}

	// Save a run with task ID
	run := &WorkflowRun{
		ID:         "run-with-task",
		WorkflowID: "workflow-1",
		TaskID:     456,
		Adapter:    "exec",
		Status:     "running",
		StartedAt:  time.Now(),
	}
	if err := db.SaveRun(run); err != nil {
		t.Fatalf("failed to save run: %v", err)
	}

	// Get run by task ID
	retrieved, err := db.GetRunByTaskID(456)
	if err != nil {
		t.Fatalf("failed to get run: %v", err)
	}

	if retrieved == nil {
		t.Fatal("retrieved run is nil")
	}
	if retrieved.ID != "run-with-task" {
		t.Errorf("expected run ID 'run-with-task', got '%s'", retrieved.ID)
	}
}

func TestDB_GetPendingRuns(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Save a workflow
	workflow := &Workflow{
		ID:      "workflow-1",
		Name:    "Workflow 1",
		Version: "1.0.0",
		Runtime: "node",
		Code:    "function workflow() {}",
		Adapter: "exec",
	}
	if err := db.SaveWorkflow(workflow); err != nil {
		t.Fatalf("failed to save workflow: %v", err)
	}

	// Save runs with different statuses
	statuses := []string{"running", "completed", "running", "failed"}
	for i, status := range statuses {
		run := &WorkflowRun{
			ID:         "run-" + string(rune('a'+i)),
			WorkflowID: "workflow-1",
			Adapter:    "exec",
			Status:     status,
			StartedAt:  time.Now(),
		}
		if err := db.SaveRun(run); err != nil {
			t.Fatalf("failed to save run: %v", err)
		}
	}

	// Get pending (running) runs
	pending, err := db.GetPendingRuns()
	if err != nil {
		t.Fatalf("failed to get pending runs: %v", err)
	}

	if len(pending) != 2 {
		t.Errorf("expected 2 pending runs, got %d", len(pending))
	}
}
