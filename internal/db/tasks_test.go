package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	// Create the test project first
	if err := db.CreateProject(&Project{Name: "test", Path: tmpDir}); err != nil {
		t.Fatalf("failed to create test project: %v", err)
	}

	// Create a task
	task := &Task{
		Title:   "Test Task",
		Body:    "Test body",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "test",
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

	// Create the test project first
	if err := db.CreateProject(&Project{Name: "test", Path: tmpDir}); err != nil {
		t.Fatalf("failed to create test project: %v", err)
	}

	// Create a task
	task := &Task{
		Title:   "Test Task",
		Body:    "Test body",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "test",
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
		Title:  "Task without branch",
		Status: StatusBacklog,
		Type:   TypeCode,
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
			name:    "no match",
			cwd:     "/Users/test/Other/something",
			wantNil: true,
		},
		{
			name:    "partial path no match",
			cwd:     "/Users/test/Projects/myproj", // not myproject
			wantNil: true,
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

func TestLastExecutorForProject(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Get last executor for non-existent project - should return empty
	lastExecutor, err := db.GetLastExecutorForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last executor: %v", err)
	}
	if lastExecutor != "" {
		t.Errorf("expected empty string for non-existent project, got %q", lastExecutor)
	}

	// Set last executor for personal
	if err := db.SetLastExecutorForProject("personal", "claude"); err != nil {
		t.Fatalf("failed to set last executor: %v", err)
	}

	// Get it back
	lastExecutor, err = db.GetLastExecutorForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last executor: %v", err)
	}
	if lastExecutor != "claude" {
		t.Errorf("expected 'claude', got %q", lastExecutor)
	}

	// Set different executor for another project
	if err := db.SetLastExecutorForProject("work", "codex"); err != nil {
		t.Fatalf("failed to set last executor: %v", err)
	}

	// Verify both are independent
	lastExecutor, err = db.GetLastExecutorForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last executor: %v", err)
	}
	if lastExecutor != "claude" {
		t.Errorf("expected 'claude' for personal, got %q", lastExecutor)
	}

	lastExecutor, err = db.GetLastExecutorForProject("work")
	if err != nil {
		t.Fatalf("failed to get last executor: %v", err)
	}
	if lastExecutor != "codex" {
		t.Errorf("expected 'codex' for work, got %q", lastExecutor)
	}

	// Update the executor for personal
	if err := db.SetLastExecutorForProject("personal", "codex"); err != nil {
		t.Fatalf("failed to update last executor: %v", err)
	}

	lastExecutor, err = db.GetLastExecutorForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last executor: %v", err)
	}
	if lastExecutor != "codex" {
		t.Errorf("expected 'codex' after update, got %q", lastExecutor)
	}
}

func TestUpdateTaskStatus(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a task starting in done status
	task := &Task{
		Title:   "Test Task",
		Status:  StatusDone,
		Type:    TypeCode,
		Project: "personal",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Verify initial status is done
	retrieved, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.Status != StatusDone {
		t.Errorf("expected status %q, got %q", StatusDone, retrieved.Status)
	}

	// Change status to backlog (the feature we're implementing)
	if err := db.UpdateTaskStatus(task.ID, StatusBacklog); err != nil {
		t.Fatalf("failed to update task status: %v", err)
	}

	// Verify status was changed
	retrieved, err = db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.Status != StatusBacklog {
		t.Errorf("expected status %q, got %q", StatusBacklog, retrieved.Status)
	}

	// Change status to queued
	if err := db.UpdateTaskStatus(task.ID, StatusQueued); err != nil {
		t.Fatalf("failed to update task status: %v", err)
	}

	retrieved, err = db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.Status != StatusQueued {
		t.Errorf("expected status %q, got %q", StatusQueued, retrieved.Status)
	}

	// Change status to blocked
	if err := db.UpdateTaskStatus(task.ID, StatusBlocked); err != nil {
		t.Fatalf("failed to update task status: %v", err)
	}

	retrieved, err = db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.Status != StatusBlocked {
		t.Errorf("expected status %q, got %q", StatusBlocked, retrieved.Status)
	}
	// Blocked sets completed_at
	if retrieved.CompletedAt == nil {
		t.Error("expected completed_at to be set for blocked status")
	}

	// Change back to backlog
	if err := db.UpdateTaskStatus(task.ID, StatusBacklog); err != nil {
		t.Fatalf("failed to update task status: %v", err)
	}

	retrieved, err = db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.Status != StatusBacklog {
		t.Errorf("expected status %q, got %q", StatusBacklog, retrieved.Status)
	}
}

func TestListTasksClosedSortedByCompletedAt(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	// Create the test project first
	if err := database.CreateProject(&Project{Name: "test", Path: tmpDir}); err != nil {
		t.Fatalf("failed to create test project: %v", err)
	}

	// Create tasks in specific order - older tasks first
	task1 := &Task{
		Title:   "First task - closed early",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "test",
	}
	task2 := &Task{
		Title:   "Second task - closed late",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "test",
	}
	task3 := &Task{
		Title:   "Third task - not closed",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "test",
	}

	// Create tasks
	if err := database.CreateTask(task1); err != nil {
		t.Fatalf("failed to create task1: %v", err)
	}
	if err := database.CreateTask(task2); err != nil {
		t.Fatalf("failed to create task2: %v", err)
	}
	if err := database.CreateTask(task3); err != nil {
		t.Fatalf("failed to create task3: %v", err)
	}

	// Close task1 first
	if err := database.UpdateTaskStatus(task1.ID, StatusDone); err != nil {
		t.Fatalf("failed to close task1: %v", err)
	}

	// Then close task2 (so task2 has later completed_at)
	if err := database.UpdateTaskStatus(task2.ID, StatusDone); err != nil {
		t.Fatalf("failed to close task2: %v", err)
	}

	// List all tasks including closed
	tasks, err := database.ListTasks(ListTasksOptions{IncludeClosed: true})
	if err != nil {
		t.Fatalf("failed to list tasks: %v", err)
	}

	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}

	// Find done tasks - the one closed most recently (task2) should appear before task1
	var doneTasks []*Task
	for _, task := range tasks {
		if task.Status == StatusDone {
			doneTasks = append(doneTasks, task)
		}
	}

	if len(doneTasks) != 2 {
		t.Fatalf("expected 2 done tasks, got %d", len(doneTasks))
	}

	// task2 (closed later) should be first
	if doneTasks[0].ID != task2.ID {
		t.Errorf("expected task2 (most recently closed) to be first, got task%d", doneTasks[0].ID)
	}
	if doneTasks[1].ID != task1.ID {
		t.Errorf("expected task1 (closed earlier) to be second, got task%d", doneTasks[1].ID)
	}
}

func TestListTasksPinnedFirst(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	if err := database.CreateProject(&Project{Name: "test", Path: tmpDir}); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	makeTask := func(title string) *Task {
		return &Task{Title: title, Status: StatusBacklog, Type: TypeCode, Project: "test"}
	}

	pinned := makeTask("Pinned older task")
	recent := makeTask("Recent task")
	third := makeTask("Another task")

	for _, task := range []*Task{pinned, recent, third} {
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task %q: %v", task.Title, err)
		}
	}

	// Complete tasks in order so pinned has the oldest completed_at
	if err := database.UpdateTaskStatus(pinned.ID, StatusDone); err != nil {
		t.Fatalf("failed to complete pinned task: %v", err)
	}
	if err := database.UpdateTaskStatus(recent.ID, StatusDone); err != nil {
		t.Fatalf("failed to complete recent task: %v", err)
	}
	if err := database.UpdateTaskStatus(third.ID, StatusDone); err != nil {
		t.Fatalf("failed to complete third task: %v", err)
	}

	// Ensure pinned task looks older by pushing its completed_at further back
	if _, err := database.Exec(`UPDATE tasks SET completed_at = datetime('now', '-10 minutes') WHERE id = ?`, pinned.ID); err != nil {
		t.Fatalf("failed to adjust completed_at: %v", err)
	}

	if err := database.UpdateTaskPinned(pinned.ID, true); err != nil {
		t.Fatalf("failed to pin task: %v", err)
	}

	// Limit results to 2 to verify pinned tasks take precedence even when older
	tasks, err := database.ListTasks(ListTasksOptions{Status: StatusDone, Limit: 2})
	if err != nil {
		t.Fatalf("failed to list tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	if tasks[0].ID != pinned.ID {
		t.Fatalf("expected pinned task #%d to appear first, got #%d", pinned.ID, tasks[0].ID)
	}
	if tasks[1].ID == pinned.ID {
		t.Fatalf("expected pinned task to appear only once in results")
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

func TestCreateTaskSavesLastExecutor(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a task with an executor
	task := &Task{
		Title:    "Test Task",
		Status:   StatusBacklog,
		Type:     "code",
		Project:  "personal",
		Executor: ExecutorClaude,
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Verify last executor was saved
	lastExecutor, err := db.GetLastExecutorForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last executor: %v", err)
	}
	if lastExecutor != ExecutorClaude {
		t.Errorf("expected %q, got %q", ExecutorClaude, lastExecutor)
	}

	// Create another task with a different executor
	task2 := &Task{
		Title:    "Test Task 2",
		Status:   StatusBacklog,
		Type:     "code",
		Project:  "personal",
		Executor: ExecutorCodex,
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Verify last executor was updated
	lastExecutor, err = db.GetLastExecutorForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last executor: %v", err)
	}
	if lastExecutor != ExecutorCodex {
		t.Errorf("expected %q, got %q", ExecutorCodex, lastExecutor)
	}

	// Create task with gemini executor
	taskGemini := &Task{
		Title:    "Test Task Gemini",
		Status:   StatusBacklog,
		Type:     "code",
		Project:  "personal",
		Executor: ExecutorGemini,
	}
	if err := db.CreateTask(taskGemini); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Verify last executor was updated to gemini
	lastExecutor, err = db.GetLastExecutorForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last executor: %v", err)
	}
	if lastExecutor != ExecutorGemini {
		t.Errorf("expected %q, got %q", ExecutorGemini, lastExecutor)
	}

	// Create task with empty executor - should still save the default executor
	task3 := &Task{
		Title:    "Test Task 3",
		Status:   StatusBacklog,
		Type:     "code",
		Project:  "personal",
		Executor: "", // Will be set to default by CreateTask
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Verify last executor was updated to the default (claude)
	lastExecutor, err = db.GetLastExecutorForProject("personal")
	if err != nil {
		t.Fatalf("failed to get last executor: %v", err)
	}
	if lastExecutor != ExecutorClaude {
		t.Errorf("expected %q (default), got %q", ExecutorClaude, lastExecutor)
	}
}

func TestPortAllocation(t *testing.T) {
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
	task1 := &Task{
		Title:   "Task 1",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "personal",
	}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Allocate port for task1
	port1, err := db.AllocatePort(task1.ID)
	if err != nil {
		t.Fatalf("failed to allocate port: %v", err)
	}

	// Verify port is in valid range
	if port1 < PortRangeStart || port1 > PortRangeEnd {
		t.Errorf("allocated port %d outside valid range %d-%d", port1, PortRangeStart, PortRangeEnd)
	}

	// First port should be the start of range
	if port1 != PortRangeStart {
		t.Errorf("expected first port to be %d, got %d", PortRangeStart, port1)
	}

	// Verify task was updated with port
	task1, err = db.GetTask(task1.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if task1.Port != port1 {
		t.Errorf("task port not updated: expected %d, got %d", port1, task1.Port)
	}

	// Create and allocate port for another task
	task2 := &Task{
		Title:   "Task 2",
		Status:  StatusProcessing,
		Type:    TypeCode,
		Project: "personal",
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	port2, err := db.AllocatePort(task2.ID)
	if err != nil {
		t.Fatalf("failed to allocate port: %v", err)
	}

	// Second port should be different from first
	if port2 == port1 {
		t.Errorf("second port should be different from first: both are %d", port1)
	}

	// Second port should be next in sequence
	if port2 != PortRangeStart+1 {
		t.Errorf("expected second port to be %d, got %d", PortRangeStart+1, port2)
	}
}

func TestPortAllocationReusesFreedPorts(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create and allocate port for task1
	task1 := &Task{
		Title:   "Task 1",
		Status:  StatusProcessing,
		Type:    TypeCode,
		Project: "personal",
	}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}
	port1, err := db.AllocatePort(task1.ID)
	if err != nil {
		t.Fatalf("failed to allocate port: %v", err)
	}

	// Create and allocate port for task2
	task2 := &Task{
		Title:   "Task 2",
		Status:  StatusProcessing,
		Type:    TypeCode,
		Project: "personal",
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}
	port2, err := db.AllocatePort(task2.ID)
	if err != nil {
		t.Fatalf("failed to allocate port: %v", err)
	}

	// Mark task1 as done - this frees its port
	if err := db.UpdateTaskStatus(task1.ID, StatusDone); err != nil {
		t.Fatalf("failed to update task status: %v", err)
	}

	// Create task3 and allocate port
	task3 := &Task{
		Title:   "Task 3",
		Status:  StatusProcessing,
		Type:    TypeCode,
		Project: "personal",
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}
	port3, err := db.AllocatePort(task3.ID)
	if err != nil {
		t.Fatalf("failed to allocate port: %v", err)
	}

	// Task3 should get task1's freed port
	if port3 != port1 {
		t.Errorf("expected task3 to reuse freed port %d, got %d", port1, port3)
	}

	// Verify port2 is still in use
	ports, err := db.GetActiveTaskPorts()
	if err != nil {
		t.Fatalf("failed to get active ports: %v", err)
	}
	if !ports[port2] {
		t.Errorf("port %d should still be marked as in use", port2)
	}
}

func TestGetActiveTaskPorts(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Initially no ports in use
	ports, err := db.GetActiveTaskPorts()
	if err != nil {
		t.Fatalf("failed to get active ports: %v", err)
	}
	if len(ports) != 0 {
		t.Errorf("expected no active ports, got %d", len(ports))
	}

	// Create tasks with different statuses
	statuses := []struct {
		status   string
		expected bool // whether port should be considered "in use"
	}{
		{StatusBacklog, true},
		{StatusQueued, true},
		{StatusProcessing, true},
		{StatusBlocked, true},
		{StatusDone, false},
	}

	for i, tc := range statuses {
		task := &Task{
			Title:   "Task",
			Status:  tc.status,
			Type:    TypeCode,
			Project: "personal",
		}
		if err := db.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}
		if _, err := db.AllocatePort(task.ID); err != nil {
			t.Fatalf("failed to allocate port: %v", err)
		}

		// Count active ports
		ports, err := db.GetActiveTaskPorts()
		if err != nil {
			t.Fatalf("failed to get active ports: %v", err)
		}

		expectedCount := 0
		for j := 0; j <= i; j++ {
			if statuses[j].expected {
				expectedCount++
			}
		}

		if len(ports) != expectedCount {
			t.Errorf("after creating task with status %q: expected %d active ports, got %d",
				tc.status, expectedCount, len(ports))
		}
	}
}

func TestCreateTaskRejectsNonExistentProject(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Try to create a task with a non-existent project
	task := &Task{
		Title:   "Test Task",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "nonexistent-project",
	}
	err = db.CreateTask(task)

	// Should fail with project not found error
	if err == nil {
		t.Fatal("expected error when creating task with non-existent project, got nil")
	}
	if !strings.Contains(err.Error(), "project not found") {
		t.Errorf("expected 'project not found' error, got: %v", err)
	}

	// Verify task was not created
	tasks, err := db.ListTasks(ListTasksOptions{IncludeClosed: true})
	if err != nil {
		t.Fatalf("failed to list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks after failed creation, got %d", len(tasks))
	}
}

func TestCreateTaskAllowsExistingProject(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a project first
	project := &Project{
		Name: "my-project",
		Path: tmpDir,
	}
	if err := db.CreateProject(project); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Now create a task with that project
	task := &Task{
		Title:   "Test Task",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "my-project",
	}
	err = db.CreateTask(task)

	// Should succeed
	if err != nil {
		t.Fatalf("expected task creation to succeed, got error: %v", err)
	}

	// Verify task was created with correct project
	retrieved, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.Project != "my-project" {
		t.Errorf("expected project 'my-project', got '%s'", retrieved.Project)
	}
}

func TestCreateTaskAllowsProjectAlias(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a project with an alias
	project := &Project{
		Name:    "my-long-project-name",
		Path:    tmpDir,
		Aliases: "myproj, proj",
	}
	if err := db.CreateProject(project); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create a task using the alias
	task := &Task{
		Title:   "Test Task",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "myproj",
	}
	err = db.CreateTask(task)

	// Should succeed
	if err != nil {
		t.Fatalf("expected task creation with alias to succeed, got error: %v", err)
	}

	// Verify task was created with the alias (not the full name)
	retrieved, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.Project != "myproj" {
		t.Errorf("expected project 'myproj', got '%s'", retrieved.Project)
	}
}

func TestCreateTaskDefaultsToPersonal(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a task with empty project (should default to 'personal')
	task := &Task{
		Title:   "Test Task",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "",
	}
	err = db.CreateTask(task)

	// Should succeed because 'personal' project is auto-created
	if err != nil {
		t.Fatalf("expected task creation to succeed with default project, got error: %v", err)
	}

	// Verify task was created with 'personal' project
	retrieved, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.Project != "personal" {
		t.Errorf("expected project 'personal', got '%s'", retrieved.Project)
	}
}

func TestCountTasksByProject(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a test project
	project := &Project{
		Name: "test-project",
		Path: tmpDir,
	}
	if err := db.CreateProject(project); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Initially no tasks for project
	count, err := db.CountTasksByProject("test-project")
	if err != nil {
		t.Fatalf("failed to count tasks: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tasks, got %d", count)
	}

	// Create tasks for the project
	for i := 0; i < 3; i++ {
		task := &Task{
			Title:   "Test Task",
			Status:  StatusBacklog,
			Type:    TypeCode,
			Project: "test-project",
		}
		if err := db.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}
	}

	// Count should now be 3
	count, err = db.CountTasksByProject("test-project")
	if err != nil {
		t.Fatalf("failed to count tasks: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 tasks, got %d", count)
	}

	// Count for non-existent project should be 0
	count, err = db.CountTasksByProject("nonexistent-project")
	if err != nil {
		t.Fatalf("failed to count tasks: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tasks for nonexistent project, got %d", count)
	}
}

func TestUpdateTaskDangerousMode(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a task (dangerous_mode defaults to false)
	task := &Task{
		Title:   "Test Task",
		Status:  StatusProcessing,
		Type:    TypeCode,
		Project: "personal",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Verify initial dangerous mode is false
	retrieved, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.DangerousMode {
		t.Error("expected DangerousMode to be false initially")
	}

	// Enable dangerous mode
	if err := db.UpdateTaskDangerousMode(task.ID, true); err != nil {
		t.Fatalf("failed to update dangerous mode: %v", err)
	}

	// Verify dangerous mode is now true
	retrieved, err = db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if !retrieved.DangerousMode {
		t.Error("expected DangerousMode to be true after enabling")
	}

	// Disable dangerous mode
	if err := db.UpdateTaskDangerousMode(task.ID, false); err != nil {
		t.Fatalf("failed to update dangerous mode: %v", err)
	}

	// Verify dangerous mode is now false
	retrieved, err = db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.DangerousMode {
		t.Error("expected DangerousMode to be false after disabling")
	}
}

func TestTaskDangerousModeInListTasks(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create tasks with different dangerous mode states
	task1 := &Task{
		Title:   "Safe Task",
		Status:  StatusProcessing,
		Type:    TypeCode,
		Project: "personal",
	}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	task2 := &Task{
		Title:   "Dangerous Task",
		Status:  StatusProcessing,
		Type:    TypeCode,
		Project: "personal",
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Enable dangerous mode for task2
	if err := db.UpdateTaskDangerousMode(task2.ID, true); err != nil {
		t.Fatalf("failed to update dangerous mode: %v", err)
	}

	// List tasks and verify dangerous mode is correctly returned
	tasks, err := db.ListTasks(ListTasksOptions{})
	if err != nil {
		t.Fatalf("failed to list tasks: %v", err)
	}

	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	// Find each task and verify dangerous mode
	var foundSafe, foundDangerous bool
	for _, task := range tasks {
		if task.ID == task1.ID {
			foundSafe = true
			if task.DangerousMode {
				t.Error("expected task1 to have DangerousMode=false")
			}
		}
		if task.ID == task2.ID {
			foundDangerous = true
			if !task.DangerousMode {
				t.Error("expected task2 to have DangerousMode=true")
			}
		}
	}

	if !foundSafe {
		t.Error("safe task not found in list")
	}
	if !foundDangerous {
		t.Error("dangerous task not found in list")
	}
}

func TestGetMostRecentlyCreatedTask(t *testing.T) {
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
	if err := db.CreateProject(&Project{Name: "project1", Path: tmpDir}); err != nil {
		t.Fatalf("failed to create project1: %v", err)
	}
	if err := db.CreateProject(&Project{Name: "project2", Path: tmpDir + "/sub"}); err != nil {
		t.Fatalf("failed to create project2: %v", err)
	}

	// Test when no tasks exist
	task, err := db.GetMostRecentlyCreatedTask()
	if err != nil {
		t.Fatalf("failed to get most recently created task: %v", err)
	}
	if task != nil {
		t.Errorf("expected nil task when no tasks exist, got %v", task)
	}

	// Create first task
	task1 := &Task{
		Title:   "First Task",
		Body:    "First task body",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "project1",
	}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("failed to create first task: %v", err)
	}

	// Most recent should be task1
	mostRecent, err := db.GetMostRecentlyCreatedTask()
	if err != nil {
		t.Fatalf("failed to get most recently created task: %v", err)
	}
	if mostRecent == nil {
		t.Fatal("expected non-nil task")
	}
	if mostRecent.Title != "First Task" {
		t.Errorf("expected 'First Task', got %q", mostRecent.Title)
	}
	if mostRecent.Project != "project1" {
		t.Errorf("expected 'project1', got %q", mostRecent.Project)
	}

	// Wait a bit to ensure different timestamps
	time.Sleep(10 * time.Millisecond)

	// Create second task with different project
	task2 := &Task{
		Title:   "Second Task",
		Body:    "Second task body",
		Status:  StatusQueued,
		Type:    TypeCode,
		Project: "project2",
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("failed to create second task: %v", err)
	}

	// Most recent should now be task2
	mostRecent, err = db.GetMostRecentlyCreatedTask()
	if err != nil {
		t.Fatalf("failed to get most recently created task: %v", err)
	}
	if mostRecent == nil {
		t.Fatal("expected non-nil task")
	}
	if mostRecent.Title != "Second Task" {
		t.Errorf("expected 'Second Task', got %q", mostRecent.Title)
	}
	if mostRecent.Project != "project2" {
		t.Errorf("expected 'project2', got %q", mostRecent.Project)
	}

	// Mark second task as done - should still be most recently created
	task2.Status = StatusDone
	if err := db.UpdateTaskStatus(task2.ID, StatusDone); err != nil {
		t.Fatalf("failed to update task status: %v", err)
	}

	mostRecent, err = db.GetMostRecentlyCreatedTask()
	if err != nil {
		t.Fatalf("failed to get most recently created task: %v", err)
	}
	if mostRecent.Title != "Second Task" {
		t.Errorf("expected 'Second Task' (completed task should still be returned), got %q", mostRecent.Title)
	}
}

func TestLastUsedProject(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Get last used project before setting any - should return empty
	lastProject, err := db.GetLastUsedProject()
	if err != nil {
		t.Fatalf("failed to get last used project: %v", err)
	}
	if lastProject != "" {
		t.Errorf("expected empty string, got %q", lastProject)
	}

	// Set last used project
	if err := db.SetLastUsedProject("personal"); err != nil {
		t.Fatalf("failed to set last used project: %v", err)
	}

	// Get it back
	lastProject, err = db.GetLastUsedProject()
	if err != nil {
		t.Fatalf("failed to get last used project: %v", err)
	}
	if lastProject != "personal" {
		t.Errorf("expected 'personal', got %q", lastProject)
	}

	// Update to different project
	if err := db.SetLastUsedProject("work"); err != nil {
		t.Fatalf("failed to set last used project: %v", err)
	}

	lastProject, err = db.GetLastUsedProject()
	if err != nil {
		t.Fatalf("failed to get last used project: %v", err)
	}
	if lastProject != "work" {
		t.Errorf("expected 'work' after update, got %q", lastProject)
	}
}

func TestCreateTaskSavesLastProject(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a second project for testing
	if err := db.CreateProject(&Project{Name: "work", Path: tmpDir + "/work"}); err != nil {
		t.Fatalf("failed to create work project: %v", err)
	}

	// Create a task with a project
	task := &Task{
		Title:   "Test Task",
		Status:  StatusBacklog,
		Type:    "code",
		Project: "personal",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Verify last used project was saved
	lastProject, err := db.GetLastUsedProject()
	if err != nil {
		t.Fatalf("failed to get last used project: %v", err)
	}
	if lastProject != "personal" {
		t.Errorf("expected 'personal', got %q", lastProject)
	}

	// Create another task with a different project
	task2 := &Task{
		Title:   "Test Task 2",
		Status:  StatusBacklog,
		Type:    "writing",
		Project: "work",
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Verify last used project was updated
	lastProject, err = db.GetLastUsedProject()
	if err != nil {
		t.Fatalf("failed to get last used project: %v", err)
	}
	if lastProject != "work" {
		t.Errorf("expected 'work', got %q", lastProject)
	}
}

func TestGetExecutorUsageByProject(t *testing.T) {
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
	if err := db.CreateProject(&Project{Name: "project1", Path: tmpDir}); err != nil {
		t.Fatalf("failed to create project1: %v", err)
	}
	if err := db.CreateProject(&Project{Name: "project2", Path: tmpDir + "/p2"}); err != nil {
		t.Fatalf("failed to create project2: %v", err)
	}

	// Test empty project - should return empty map
	usage, err := db.GetExecutorUsageByProject("project1")
	if err != nil {
		t.Fatalf("failed to get executor usage: %v", err)
	}
	if len(usage) != 0 {
		t.Errorf("expected empty usage map, got %v", usage)
	}

	// Create tasks with different executors for project1
	tasks := []struct {
		title    string
		project  string
		executor string
	}{
		{"Task 1", "project1", "claude"},
		{"Task 2", "project1", "claude"},
		{"Task 3", "project1", "claude"},
		{"Task 4", "project1", "codex"},
		{"Task 5", "project1", "gemini"},
		{"Task 6", "project2", "codex"}, // Different project
		{"Task 7", "project2", "codex"},
	}

	for _, tt := range tasks {
		task := &Task{
			Title:    tt.title,
			Status:   StatusBacklog,
			Project:  tt.project,
			Executor: tt.executor,
		}
		if err := db.CreateTask(task); err != nil {
			t.Fatalf("failed to create task %s: %v", tt.title, err)
		}
	}

	// Test project1 usage
	usage, err = db.GetExecutorUsageByProject("project1")
	if err != nil {
		t.Fatalf("failed to get executor usage: %v", err)
	}

	// project1 should have: claude=3, codex=1, gemini=1
	if usage["claude"] != 3 {
		t.Errorf("expected claude=3, got %d", usage["claude"])
	}
	if usage["codex"] != 1 {
		t.Errorf("expected codex=1, got %d", usage["codex"])
	}
	if usage["gemini"] != 1 {
		t.Errorf("expected gemini=1, got %d", usage["gemini"])
	}

	// Test project2 usage - should only count project2 tasks
	usage, err = db.GetExecutorUsageByProject("project2")
	if err != nil {
		t.Fatalf("failed to get executor usage: %v", err)
	}

	// project2 should have: codex=2 only
	if usage["codex"] != 2 {
		t.Errorf("expected codex=2, got %d", usage["codex"])
	}
	if usage["claude"] != 0 {
		t.Errorf("expected claude=0, got %d", usage["claude"])
	}

	// Test non-existent project
	usage, err = db.GetExecutorUsageByProject("nonexistent")
	if err != nil {
		t.Fatalf("failed to get executor usage for nonexistent project: %v", err)
	}
	if len(usage) != 0 {
		t.Errorf("expected empty usage map for nonexistent project, got %v", usage)
	}
}
