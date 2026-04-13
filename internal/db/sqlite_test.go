package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTimestampLocalization(t *testing.T) {
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

	// Retrieve the task
	retrieved, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	// Check that CreatedAt is in local timezone
	localZone := time.Now().Location()
	if retrieved.CreatedAt.Location() != localZone {
		t.Errorf("expected CreatedAt timezone %v, got %v", localZone, retrieved.CreatedAt.Location())
	}

	// Verify the time is reasonable (within last minute)
	now := time.Now()
	diff := now.Sub(retrieved.CreatedAt.Time)
	if diff < 0 || diff > time.Minute {
		t.Errorf("CreatedAt %v is not within expected range of now %v", retrieved.CreatedAt.Time, now)
	}
}

func TestBusyTimeoutIsSet(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	var timeout int
	if err := database.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("failed to query busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("expected busy_timeout=5000, got %d", timeout)
	}
}

func TestBusyTimeoutSurvivesConnectionRecycling(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Force connection recycling by waiting longer than ConnMaxLifetime (2s)
	time.Sleep(3 * time.Second)

	// After recycling, the new connection should still have busy_timeout
	var timeout int
	if err := database.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("failed to query busy_timeout after recycling: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("expected busy_timeout=5000 after connection recycling, got %d", timeout)
	}

	// Also verify WAL mode persists
	var journalMode string
	if err := database.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("failed to query journal_mode after recycling: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal_mode=wal after connection recycling, got %s", journalMode)
	}
}

func TestPersonalProjectCreation(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Verify 'personal' project was created
	personal, err := db.GetProjectByName("personal")
	if err != nil {
		t.Fatalf("failed to get personal project: %v", err)
	}
	if personal == nil {
		t.Fatal("personal project was not created")
	}
	if personal.Name != "personal" {
		t.Errorf("expected project name 'personal', got '%s'", personal.Name)
	}

	// Verify that creating a task with empty project defaults to 'personal'
	task := &Task{
		Title:   "Test Task",
		Body:    "Test body",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "", // Empty project
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Retrieve the task and verify it has 'personal' project
	retrieved, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.Project != "personal" {
		t.Errorf("expected task project 'personal', got '%s'", retrieved.Project)
	}
}

func TestProjectContext(t *testing.T) {
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
	projectName := "test-context-project"
	if err := db.CreateProject(&Project{Name: projectName, Path: tmpDir}); err != nil {
		t.Fatalf("failed to create test project: %v", err)
	}

	// Initially context should be empty
	context, err := db.GetProjectContext(projectName)
	if err != nil {
		t.Fatalf("failed to get project context: %v", err)
	}
	if context != "" {
		t.Errorf("expected empty context initially, got '%s'", context)
	}

	// Set project context
	testContext := "This is a Go project with a cmd/ and internal/ structure. Key packages: db, executor, mcp."
	if err := db.SetProjectContext(projectName, testContext); err != nil {
		t.Fatalf("failed to set project context: %v", err)
	}

	// Verify context was saved
	context, err = db.GetProjectContext(projectName)
	if err != nil {
		t.Fatalf("failed to get project context: %v", err)
	}
	if context != testContext {
		t.Errorf("expected context '%s', got '%s'", testContext, context)
	}

	// Update context
	newContext := "Updated context with more details."
	if err := db.SetProjectContext(projectName, newContext); err != nil {
		t.Fatalf("failed to update project context: %v", err)
	}

	// Verify updated context
	context, err = db.GetProjectContext(projectName)
	if err != nil {
		t.Fatalf("failed to get updated project context: %v", err)
	}
	if context != newContext {
		t.Errorf("expected updated context '%s', got '%s'", newContext, context)
	}

	// Test getting context for non-existent project
	context, err = db.GetProjectContext("nonexistent-project")
	if err != nil {
		t.Fatalf("unexpected error for non-existent project: %v", err)
	}
	if context != "" {
		t.Errorf("expected empty context for non-existent project, got '%s'", context)
	}

	// Test setting context for non-existent project (should fail)
	err = db.SetProjectContext("nonexistent-project", "some context")
	if err == nil {
		t.Error("expected error when setting context for non-existent project")
	}
}

func TestProjectUseWorktrees(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Default project (personal) should use worktrees
	personal, err := db.GetProjectByName("personal")
	if err != nil {
		t.Fatalf("failed to get personal project: %v", err)
	}
	if !personal.UsesWorktrees() {
		t.Error("personal project should default to using worktrees")
	}

	// Create a project with worktrees enabled (default)
	gitProject := &Project{Name: "git-project", Path: filepath.Join(tmpDir, "git"), UseWorktrees: true}
	if err := db.CreateProject(gitProject); err != nil {
		t.Fatalf("failed to create git project: %v", err)
	}

	// Create a project with worktrees disabled
	noGitProject := &Project{Name: "no-git-project", Path: filepath.Join(tmpDir, "nogit"), UseWorktrees: false}
	if err := db.CreateProject(noGitProject); err != nil {
		t.Fatalf("failed to create no-git project: %v", err)
	}

	// Verify via GetProjectByName
	p, err := db.GetProjectByName("git-project")
	if err != nil {
		t.Fatalf("failed to get git-project: %v", err)
	}
	if !p.UsesWorktrees() {
		t.Error("git-project should use worktrees")
	}

	p, err = db.GetProjectByName("no-git-project")
	if err != nil {
		t.Fatalf("failed to get no-git-project: %v", err)
	}
	if p.UsesWorktrees() {
		t.Error("no-git-project should NOT use worktrees")
	}

	// Verify via ListProjects
	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}
	for _, proj := range projects {
		switch proj.Name {
		case "git-project":
			if !proj.UsesWorktrees() {
				t.Error("git-project should use worktrees in list")
			}
		case "no-git-project":
			if proj.UsesWorktrees() {
				t.Error("no-git-project should NOT use worktrees in list")
			}
		}
	}

	// Test updating worktrees setting
	noGitProject.UseWorktrees = true
	if err := db.UpdateProject(noGitProject); err != nil {
		t.Fatalf("failed to update project: %v", err)
	}
	p, err = db.GetProjectByName("no-git-project")
	if err != nil {
		t.Fatalf("failed to get updated project: %v", err)
	}
	if !p.UsesWorktrees() {
		t.Error("no-git-project should use worktrees after update")
	}

	// Test alias-based lookup preserves UseWorktrees
	aliasProject := &Project{Name: "alias-proj", Path: filepath.Join(tmpDir, "alias"), Aliases: "ap,aliased", UseWorktrees: false}
	if err := db.CreateProject(aliasProject); err != nil {
		t.Fatalf("failed to create alias project: %v", err)
	}
	p, err = db.GetProjectByName("ap")
	if err != nil {
		t.Fatalf("failed to get project by alias: %v", err)
	}
	if p == nil {
		t.Fatal("expected project from alias lookup, got nil")
	}
	if p.UsesWorktrees() {
		t.Error("alias-proj looked up by alias should NOT use worktrees")
	}
}
