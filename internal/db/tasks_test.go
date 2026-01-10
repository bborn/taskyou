package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetLastQuestion(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a task
	task := &Task{
		Title:    "Test Task",
		Body:     "Test body",
		Status:   StatusBacklog,
		Type:     TypeCode,
		Project:  "test",
			}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Test getting question when none exists
	question, err := db.GetLastQuestion(task.ID)
	if err != nil {
		t.Fatalf("failed to get last question: %v", err)
	}
	if question != "" {
		t.Errorf("expected empty question, got %q", question)
	}

	// Add some logs including a question
	db.AppendTaskLog(task.ID, "system", "Starting task")
	db.AppendTaskLog(task.ID, "output", "Some output")
	db.AppendTaskLog(task.ID, "question", "What database should I use?")
	db.AppendTaskLog(task.ID, "system", "Task needs input")

	// Test getting the question
	question, err = db.GetLastQuestion(task.ID)
	if err != nil {
		t.Fatalf("failed to get last question: %v", err)
	}
	if question != "What database should I use?" {
		t.Errorf("expected 'What database should I use?', got %q", question)
	}

	// Add another question to verify we get the latest
	db.AppendTaskLog(task.ID, "question", "Should I use PostgreSQL or MySQL?")

	question, err = db.GetLastQuestion(task.ID)
	if err != nil {
		t.Fatalf("failed to get last question: %v", err)
	}
	if question != "Should I use PostgreSQL or MySQL?" {
		t.Errorf("expected 'Should I use PostgreSQL or MySQL?', got %q", question)
	}
}

func TestTaskLogsWithQuestion(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a task
	task := &Task{
		Title:    "Test Task",
		Body:     "Test body",
		Status:   StatusBacklog,
		Type:     TypeCode,
		Project:  "test",
			}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Add logs with various types including question
	db.AppendTaskLog(task.ID, "system", "Starting")
	db.AppendTaskLog(task.ID, "question", "Need clarification")
	db.AppendTaskLog(task.ID, "output", "Done")

	// Get all logs
	logs, err := db.GetTaskLogs(task.ID, 100)
	if err != nil {
		t.Fatalf("failed to get task logs: %v", err)
	}

	if len(logs) != 3 {
		t.Fatalf("expected 3 logs, got %d", len(logs))
	}

	// Verify question log type is preserved
	found := false
	for _, log := range logs {
		if log.LineType == "question" {
			found = true
			if log.Content != "Need clarification" {
				t.Errorf("expected 'Need clarification', got %q", log.Content)
			}
		}
	}
	if !found {
		t.Error("question log not found")
	}
}

func TestGetTasksWithBranches(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create tasks with various states
	taskNoBranch := &Task{
		Title:    "Task without branch",
		Status:   StatusBacklog,
		Type:     TypeCode,
			}
	taskWithBranch := &Task{
		Title:      "Task with branch",
		Status:     StatusBlocked,
		Type:       TypeCode,
		BranchName: "task/1-task-with-branch",
	}
	taskDoneWithBranch := &Task{
		Title:      "Done task with branch",
		Status:     StatusDone,
		Type:       TypeCode,
		BranchName: "task/2-done-task",
	}
	taskProcessingWithBranch := &Task{
		Title:      "Processing task with branch",
		Status:     StatusProcessing,
		Type:       TypeCode,
		BranchName: "task/3-processing-task",
	}

	for _, task := range []*Task{taskNoBranch, taskWithBranch, taskDoneWithBranch, taskProcessingWithBranch} {
		if err := db.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}
		// Update branch name (CreateTask doesn't set it)
		if task.BranchName != "" {
			db.UpdateTask(task)
		}
	}

	// Get tasks with branches
	tasks, err := db.GetTasksWithBranches()
	if err != nil {
		t.Fatalf("failed to get tasks with branches: %v", err)
	}

	// Should return 2 tasks (with branch but not done)
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}

	// Verify we got the right tasks
	foundBlocked := false
	foundProcessing := false
	for _, task := range tasks {
		if task.BranchName == "task/1-task-with-branch" {
			foundBlocked = true
		}
		if task.BranchName == "task/3-processing-task" {
			foundProcessing = true
		}
		// Should never include done task
		if task.Status == StatusDone {
			t.Error("should not include done tasks")
		}
		// Should never include tasks without branches
		if task.BranchName == "" {
			t.Error("should not include tasks without branches")
		}
	}

	if !foundBlocked {
		t.Error("should include blocked task with branch")
	}
	if !foundProcessing {
		t.Error("should include processing task with branch")
	}
}

func TestGetProjectByPath(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create test projects
	proj1 := &Project{
		Name: "myproject",
		Path: "/Users/test/Projects/myproject",
	}
	proj2 := &Project{
		Name: "another",
		Path: "/Users/test/Work/another",
	}
	if err := db.CreateProject(proj1); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}
	if err := db.CreateProject(proj2); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	tests := []struct {
		name     string
		cwd      string
		wantProj string
		wantNil  bool
	}{
		{
			name:     "exact match",
			cwd:      "/Users/test/Projects/myproject",
			wantProj: "myproject",
		},
		{
			name:     "subdirectory match",
			cwd:      "/Users/test/Projects/myproject/src/internal",
			wantProj: "myproject",
		},
		{
			name:     "another project exact",
			cwd:      "/Users/test/Work/another",
			wantProj: "another",
		},
		{
			name:     "no match",
			cwd:      "/Users/test/Other/something",
			wantNil:  true,
		},
		{
			name:     "partial path no match",
			cwd:      "/Users/test/Projects/myproj", // not myproject
			wantNil:  true,
		},
		{
			name:    "empty cwd",
			cwd:     "",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proj, err := db.GetProjectByPath(tt.cwd)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if proj != nil {
					t.Errorf("expected nil, got project %q", proj.Name)
				}
			} else {
				if proj == nil {
					t.Fatalf("expected project %q, got nil", tt.wantProj)
				}
				if proj.Name != tt.wantProj {
					t.Errorf("expected project %q, got %q", tt.wantProj, proj.Name)
				}
			}
		})
	}
}

func TestDeletePersonalProject(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// The personal project should be created automatically by ensurePersonalProject
	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}

	// Find the personal project
	var personalProject *Project
	for _, p := range projects {
		if p.Name == "personal" {
			personalProject = p
			break
		}
	}

	if personalProject == nil {
		t.Fatal("personal project not found")
	}

	// Try to delete the personal project - should fail
	err = db.DeleteProject(personalProject.ID)
	if err == nil {
		t.Error("expected error when deleting personal project, got nil")
	}
	if err != nil && err.Error() != "cannot delete the personal project" {
		t.Errorf("expected 'cannot delete the personal project' error, got %q", err.Error())
	}

	// Verify the personal project still exists
	projects, err = db.ListProjects()
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}

	found := false
	for _, p := range projects {
		if p.Name == "personal" {
			found = true
			break
		}
	}
	if !found {
		t.Error("personal project was deleted despite protection")
	}

	// Create another project and verify it CAN be deleted
	otherProject := &Project{
		Name: "other-project",
		Path: filepath.Join(tmpDir, "other"),
	}
	if err := db.CreateProject(otherProject); err != nil {
		t.Fatalf("failed to create other project: %v", err)
	}

	// Delete the other project - should succeed
	err = db.DeleteProject(otherProject.ID)
	if err != nil {
		t.Errorf("expected no error when deleting other project, got %v", err)
	}

	// Verify it was deleted
	projects, err = db.ListProjects()
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}

	for _, p := range projects {
		if p.Name == "other-project" {
			t.Error("other project was not deleted")
		}
	}
}

func TestListProjectsPersonalFirst(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create additional projects (personal is created automatically)
	for _, name := range []string{"alpha", "beta", "zebra"} {
		proj := &Project{
			Name: name,
			Path: filepath.Join(tmpDir, name),
		}
		if err := db.CreateProject(proj); err != nil {
			t.Fatalf("failed to create project %s: %v", name, err)
		}
	}

	// List projects - personal should be first
	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}

	if len(projects) < 4 {
		t.Fatalf("expected at least 4 projects, got %d", len(projects))
	}

	// Personal should always be first
	if projects[0].Name != "personal" {
		t.Errorf("expected 'personal' to be first, got %q", projects[0].Name)
	}

	// Rest should be sorted alphabetically
	for i := 2; i < len(projects); i++ {
		if projects[i].Name < projects[i-1].Name {
			t.Errorf("projects not sorted: %q should come before %q", projects[i].Name, projects[i-1].Name)
		}
	}
}

func TestLastTaskTypeForProject(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Get last type for non-existent project - should return empty
	lastType, err := db.GetLastTaskTypeForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last task type: %v", err)
	}
	if lastType != "" {
		t.Errorf("expected empty string, got %q", lastType)
	}

	// Set last type for personal
	if err := db.SetLastTaskTypeForProject("personal", "code"); err != nil {
		t.Fatalf("failed to set last task type: %v", err)
	}

	// Get it back
	lastType, err = db.GetLastTaskTypeForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last task type: %v", err)
	}
	if lastType != "code" {
		t.Errorf("expected 'code', got %q", lastType)
	}

	// Set different type for another project
	if err := db.SetLastTaskTypeForProject("work", "writing"); err != nil {
		t.Fatalf("failed to set last task type: %v", err)
	}

	// Verify both are independent
	lastType, err = db.GetLastTaskTypeForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last task type: %v", err)
	}
	if lastType != "code" {
		t.Errorf("expected 'code' for personal, got %q", lastType)
	}

	lastType, err = db.GetLastTaskTypeForProject("work")
	if err != nil {
		t.Fatalf("failed to get last task type: %v", err)
	}
	if lastType != "writing" {
		t.Errorf("expected 'writing' for work, got %q", lastType)
	}

	// Update the type for personal
	if err := db.SetLastTaskTypeForProject("personal", "thinking"); err != nil {
		t.Fatalf("failed to update last task type: %v", err)
	}

	lastType, err = db.GetLastTaskTypeForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last task type: %v", err)
	}
	if lastType != "thinking" {
		t.Errorf("expected 'thinking' after update, got %q", lastType)
	}
}

func TestCreateTaskSavesLastType(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a task with a type
	task := &Task{
		Title:   "Test Task",
		Status:  StatusBacklog,
		Type:    "code",
		Project: "personal",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Verify last type was saved
	lastType, err := db.GetLastTaskTypeForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last task type: %v", err)
	}
	if lastType != "code" {
		t.Errorf("expected 'code', got %q", lastType)
	}

	// Create another task with a different type
	task2 := &Task{
		Title:   "Test Task 2",
		Status:  StatusBacklog,
		Type:    "writing",
		Project: "personal",
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Verify last type was updated
	lastType, err = db.GetLastTaskTypeForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last task type: %v", err)
	}
	if lastType != "writing" {
		t.Errorf("expected 'writing', got %q", lastType)
	}

	// Create task with empty type - should not update
	task3 := &Task{
		Title:   "Test Task 3",
		Status:  StatusBacklog,
		Type:    "",
		Project: "personal",
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Verify last type was NOT updated
	lastType, err = db.GetLastTaskTypeForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last task type: %v", err)
	}
	if lastType != "writing" {
		t.Errorf("expected 'writing' (unchanged), got %q", lastType)
	}
}
