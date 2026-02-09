package db

import (
	"os"
	"testing"
)

func setupOrchTestDB(t *testing.T) (*DB, func()) {
	tmpFile, err := os.CreateTemp("", "test-orch-*.db")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()

	db, err := Open(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to open database: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.Remove(tmpFile.Name())
	}

	return db, cleanup
}

func TestCreateTaskWithParent(t *testing.T) {
	db, cleanup := setupOrchTestDB(t)
	defer cleanup()

	// Create parent task
	parent := &Task{Title: "Parent Task", Status: StatusProcessing}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// Create subtask
	subtask := &Task{
		Title:    "Subtask 1",
		Status:   StatusBacklog,
		ParentID: &parent.ID,
	}
	if err := db.CreateTask(subtask); err != nil {
		t.Fatalf("Failed to create subtask: %v", err)
	}

	// Verify subtask has parent_id set
	fetched, err := db.GetTask(subtask.ID)
	if err != nil {
		t.Fatalf("Failed to get subtask: %v", err)
	}
	if fetched.ParentID == nil {
		t.Fatal("Expected subtask to have parent_id set")
	}
	if *fetched.ParentID != parent.ID {
		t.Errorf("Expected parent_id=%d, got %d", parent.ID, *fetched.ParentID)
	}
}

func TestGetSubtasks(t *testing.T) {
	db, cleanup := setupOrchTestDB(t)
	defer cleanup()

	// Create parent
	parent := &Task{Title: "Parent Task", Status: StatusProcessing}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// Create 3 subtasks
	for i := 1; i <= 3; i++ {
		st := &Task{
			Title:    "Subtask",
			Status:   StatusBacklog,
			ParentID: &parent.ID,
		}
		if err := db.CreateTask(st); err != nil {
			t.Fatalf("Failed to create subtask %d: %v", i, err)
		}
	}

	// Get subtasks
	subtasks, err := db.GetSubtasks(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get subtasks: %v", err)
	}
	if len(subtasks) != 3 {
		t.Errorf("Expected 3 subtasks, got %d", len(subtasks))
	}

	// Verify all have correct parent
	for _, st := range subtasks {
		if st.ParentID == nil || *st.ParentID != parent.ID {
			t.Errorf("Subtask %d has wrong parent_id", st.ID)
		}
	}
}

func TestGetSubtaskCount(t *testing.T) {
	db, cleanup := setupOrchTestDB(t)
	defer cleanup()

	parent := &Task{Title: "Parent", Status: StatusProcessing}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// No subtasks initially
	count, err := db.GetSubtaskCount(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get subtask count: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 subtasks, got %d", count)
	}

	// Add 2 subtasks
	for i := 0; i < 2; i++ {
		st := &Task{Title: "Sub", Status: StatusBacklog, ParentID: &parent.ID}
		if err := db.CreateTask(st); err != nil {
			t.Fatalf("Failed to create subtask: %v", err)
		}
	}

	count, err = db.GetSubtaskCount(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get subtask count: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 subtasks, got %d", count)
	}
}

func TestTaskOutput(t *testing.T) {
	db, cleanup := setupOrchTestDB(t)
	defer cleanup()

	task := &Task{Title: "Task with output", Status: StatusProcessing}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Initially no output
	output, err := db.GetTaskOutput(task.ID)
	if err != nil {
		t.Fatalf("Failed to get output: %v", err)
	}
	if output != "" {
		t.Errorf("Expected empty output, got %q", output)
	}

	// Set output
	expectedOutput := "Results: everything passed"
	if err := db.SetTaskOutput(task.ID, expectedOutput); err != nil {
		t.Fatalf("Failed to set output: %v", err)
	}

	// Get output
	output, err = db.GetTaskOutput(task.ID)
	if err != nil {
		t.Fatalf("Failed to get output: %v", err)
	}
	if output != expectedOutput {
		t.Errorf("Expected output %q, got %q", expectedOutput, output)
	}
}

func TestGetWorkflowStatus(t *testing.T) {
	db, cleanup := setupOrchTestDB(t)
	defer cleanup()

	parent := &Task{Title: "Workflow Parent", Status: StatusProcessing}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// Create subtasks in various states
	statuses := []string{StatusDone, StatusDone, StatusProcessing, StatusBacklog, StatusBlocked}
	for _, s := range statuses {
		st := &Task{Title: "Sub", Status: s, ParentID: &parent.ID}
		if err := db.CreateTask(st); err != nil {
			t.Fatalf("Failed to create subtask: %v", err)
		}
	}

	status, err := db.GetWorkflowStatus(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get workflow status: %v", err)
	}

	if status.Total != 5 {
		t.Errorf("Expected total=5, got %d", status.Total)
	}
	if status.Done != 2 {
		t.Errorf("Expected done=2, got %d", status.Done)
	}
	if status.Processing != 1 {
		t.Errorf("Expected processing=1, got %d", status.Processing)
	}
	if status.Pending != 1 {
		t.Errorf("Expected pending=1, got %d", status.Pending)
	}
	if status.Blocked != 1 {
		t.Errorf("Expected blocked=1, got %d", status.Blocked)
	}
	if status.IsComplete {
		t.Error("Expected workflow to not be complete")
	}
}

func TestGetWorkflowStatusAllDone(t *testing.T) {
	db, cleanup := setupOrchTestDB(t)
	defer cleanup()

	parent := &Task{Title: "Workflow Parent", Status: StatusProcessing}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// All subtasks done
	for i := 0; i < 3; i++ {
		st := &Task{Title: "Sub", Status: StatusDone, ParentID: &parent.ID}
		if err := db.CreateTask(st); err != nil {
			t.Fatalf("Failed to create subtask: %v", err)
		}
	}

	status, err := db.GetWorkflowStatus(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get workflow status: %v", err)
	}

	if !status.IsComplete {
		t.Error("Expected workflow to be complete")
	}
	if status.Total != 3 {
		t.Errorf("Expected total=3, got %d", status.Total)
	}
	if status.Done != 3 {
		t.Errorf("Expected done=3, got %d", status.Done)
	}
}

func TestCheckAndCompleteParent(t *testing.T) {
	db, cleanup := setupOrchTestDB(t)
	defer cleanup()

	parent := &Task{Title: "Workflow Parent", Status: StatusProcessing}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// Create two subtasks, both done
	for i := 0; i < 2; i++ {
		st := &Task{Title: "Sub", Status: StatusDone, ParentID: &parent.ID}
		if err := db.CreateTask(st); err != nil {
			t.Fatalf("Failed to create subtask: %v", err)
		}
		// Set output on subtask
		db.SetTaskOutput(st.ID, "output from subtask")
	}

	// Check and complete parent
	completed, err := db.CheckAndCompleteParent(parent.ID)
	if err != nil {
		t.Fatalf("Failed to check and complete parent: %v", err)
	}
	if !completed {
		t.Error("Expected parent to be completed")
	}

	// Verify parent is now done
	updatedParent, err := db.GetTask(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get parent: %v", err)
	}
	if updatedParent.Status != StatusDone {
		t.Errorf("Expected parent status=done, got %s", updatedParent.Status)
	}

	// Verify parent has aggregated output
	if updatedParent.Output == "" {
		t.Error("Expected parent to have aggregated output from subtasks")
	}
}

func TestCheckAndCompleteParentNotReady(t *testing.T) {
	db, cleanup := setupOrchTestDB(t)
	defer cleanup()

	parent := &Task{Title: "Workflow Parent", Status: StatusProcessing}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// One done, one still processing
	st1 := &Task{Title: "Sub 1", Status: StatusDone, ParentID: &parent.ID}
	st2 := &Task{Title: "Sub 2", Status: StatusProcessing, ParentID: &parent.ID}
	if err := db.CreateTask(st1); err != nil {
		t.Fatalf("Failed to create subtask 1: %v", err)
	}
	if err := db.CreateTask(st2); err != nil {
		t.Fatalf("Failed to create subtask 2: %v", err)
	}

	// Should not complete
	completed, err := db.CheckAndCompleteParent(parent.ID)
	if err != nil {
		t.Fatalf("Failed to check and complete parent: %v", err)
	}
	if completed {
		t.Error("Expected parent not to be completed (subtask still processing)")
	}

	// Parent should still be processing
	updatedParent, err := db.GetTask(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get parent: %v", err)
	}
	if updatedParent.Status != StatusProcessing {
		t.Errorf("Expected parent status=processing, got %s", updatedParent.Status)
	}
}

func TestSubtaskCompletionTriggersParentCompletion(t *testing.T) {
	db, cleanup := setupOrchTestDB(t)
	defer cleanup()

	parent := &Task{Title: "Workflow Parent", Status: StatusProcessing}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// Create two subtasks
	st1 := &Task{Title: "Sub 1", Status: StatusProcessing, ParentID: &parent.ID}
	st2 := &Task{Title: "Sub 2", Status: StatusDone, ParentID: &parent.ID}
	if err := db.CreateTask(st1); err != nil {
		t.Fatalf("Failed to create subtask 1: %v", err)
	}
	if err := db.CreateTask(st2); err != nil {
		t.Fatalf("Failed to create subtask 2: %v", err)
	}

	// Complete the last remaining subtask via UpdateTaskStatus
	// This should trigger auto-completion of the parent
	if err := db.UpdateTaskStatus(st1.ID, StatusDone); err != nil {
		t.Fatalf("Failed to update subtask status: %v", err)
	}

	// Verify parent is now done
	updatedParent, err := db.GetTask(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get parent: %v", err)
	}
	if updatedParent.Status != StatusDone {
		t.Errorf("Expected parent to be auto-completed, got status=%s", updatedParent.Status)
	}
}

func TestGetWorkflowStatusNoSubtasks(t *testing.T) {
	db, cleanup := setupOrchTestDB(t)
	defer cleanup()

	parent := &Task{Title: "No subtasks", Status: StatusProcessing}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	status, err := db.GetWorkflowStatus(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get workflow status: %v", err)
	}

	if status.Total != 0 {
		t.Errorf("Expected total=0, got %d", status.Total)
	}
	if status.IsComplete {
		t.Error("Expected workflow with no subtasks to not be marked complete")
	}
}
