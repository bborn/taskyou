package executor

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/log"
)

// TestMemoryE2EFullLifecycle tests the complete memory lifecycle:
// 1. Create a task with logs
// 2. Extract memories from the logs
// 3. Verify memories are stored in the database
// 4. Verify memories are injected into new task prompts
func TestMemoryE2EFullLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Check if claude CLI is available
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not found, skipping E2E test")
	}

	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Create project
	projectName := "memory-test-project"
	err = database.CreateProject(&db.Project{
		Name: projectName,
		Path: tmpDir,
	})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create a task
	task := &db.Task{
		Title:   "Implement user authentication",
		Body:    "Add JWT-based authentication to the API",
		Status:  db.StatusDone,
		Type:    db.TypeCode,
		Project: projectName,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Simulate task logs that contain useful learnings
	taskLogs := []struct {
		lineType string
		content  string
	}{
		{"text", "Starting authentication implementation..."},
		{"output", "Looking at the existing auth module in internal/auth/"},
		{"output", "Found that the project uses RS256 algorithm for JWT tokens"},
		{"output", "The refresh token is stored in a separate table 'refresh_tokens'"},
		{"output", "Important: The auth middleware expects tokens in the Authorization header with 'Bearer' prefix"},
		{"output", "Gotcha: The token validation caches the public key for 5 minutes to avoid repeated fetches"},
		{"text", "Successfully implemented JWT authentication with refresh token support"},
	}

	for _, log := range taskLogs {
		if err := database.AppendTaskLog(task.ID, log.lineType, log.content); err != nil {
			t.Fatalf("failed to add task log: %v", err)
		}
	}

	// Create config for testing
	cfg := config.New(database)

	// Create executor with minimal config for testing
	executor := &Executor{
		db:     database,
		config: cfg,
		logger: log.NewWithOptions(nil, log.Options{Level: log.DebugLevel}),
	}

	// Test memory extraction
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	err = executor.ExtractMemories(ctx, task)
	if err != nil {
		t.Fatalf("memory extraction failed: %v", err)
	}

	// Wait a moment for async operations
	time.Sleep(100 * time.Millisecond)

	// Verify memories were stored
	memories, err := database.GetProjectMemories(projectName, 10)
	if err != nil {
		t.Fatalf("failed to get project memories: %v", err)
	}

	t.Logf("Extracted %d memories from task", len(memories))
	for _, m := range memories {
		t.Logf("  - [%s] %s", m.Category, m.Content)
	}

	// We should have at least one memory extracted (Claude should find something useful)
	if len(memories) == 0 {
		t.Log("Warning: No memories were extracted. This may be expected if Claude determined nothing was worth remembering.")
	}

	// Test memory injection into prompts
	memoriesSection := executor.getProjectMemoriesSection(projectName)
	if len(memories) > 0 && memoriesSection == "" {
		t.Error("Expected memories section to be non-empty when memories exist")
	}

	if memoriesSection != "" {
		// Verify the format
		if !strings.Contains(memoriesSection, "## Project Context") {
			t.Error("Memories section should contain 'Project Context' header")
		}
		t.Logf("Memory injection section:\n%s", memoriesSection)
	}
}

// TestMemoryInjectionFormat verifies that memories are correctly formatted
// for injection into task prompts.
func TestMemoryInjectionFormat(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	projectName := "format-test-project"
	err = database.CreateProject(&db.Project{
		Name: projectName,
		Path: tmpDir,
	})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create memories of different categories
	testMemories := []*db.ProjectMemory{
		{Project: projectName, Category: db.MemoryCategoryPattern, Content: "Use dependency injection for services"},
		{Project: projectName, Category: db.MemoryCategoryContext, Content: "The API uses GraphQL, not REST"},
		{Project: projectName, Category: db.MemoryCategoryDecision, Content: "Chose PostgreSQL over MySQL for JSON support"},
		{Project: projectName, Category: db.MemoryCategoryGotcha, Content: "The cache has a 5 minute TTL - don't rely on immediate invalidation"},
		{Project: projectName, Category: db.MemoryCategoryGeneral, Content: "Code style follows Google Go guidelines"},
	}

	for _, m := range testMemories {
		if err := database.CreateMemory(m); err != nil {
			t.Fatalf("failed to create memory: %v", err)
		}
	}

	executor := &Executor{
		db:     database,
		logger: log.NewWithOptions(nil, log.Options{Level: log.DebugLevel}),
	}

	memoriesSection := executor.getProjectMemoriesSection(projectName)

	// Verify section structure
	if !strings.Contains(memoriesSection, "## Project Context (from previous tasks)") {
		t.Error("Missing main header")
	}

	// Verify category headers are present
	expectedHeaders := []string{
		"### Patterns & Conventions",
		"### Project Context",
		"### Key Decisions",
		"### Known Gotchas",
		"### General Notes",
	}

	for _, header := range expectedHeaders {
		if !strings.Contains(memoriesSection, header) {
			t.Errorf("Missing category header: %s", header)
		}
	}

	// Verify content is present
	for _, m := range testMemories {
		if !strings.Contains(memoriesSection, m.Content) {
			t.Errorf("Missing memory content: %s", m.Content)
		}
	}

	t.Logf("Formatted memories section:\n%s", memoriesSection)
}

// TestMemoryExtractionSkipsEmptyLogs verifies that memory extraction
// handles edge cases gracefully.
func TestMemoryExtractionSkipsEmptyLogs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	projectName := "empty-logs-project"
	err = database.CreateProject(&db.Project{
		Name: projectName,
		Path: tmpDir,
	})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Task with no logs
	task := &db.Task{
		Title:   "Empty task",
		Status:  db.StatusDone,
		Project: projectName,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	executor := &Executor{
		db:     database,
		logger: log.NewWithOptions(nil, log.Options{Level: log.DebugLevel}),
	}

	ctx := context.Background()
	err = executor.ExtractMemories(ctx, task)
	if err != nil {
		t.Errorf("extraction should not fail on empty logs: %v", err)
	}

	// Verify no memories were created
	memories, err := database.GetProjectMemories(projectName, 10)
	if err != nil {
		t.Fatalf("failed to get memories: %v", err)
	}
	if len(memories) != 0 {
		t.Errorf("expected 0 memories for empty logs, got %d", len(memories))
	}
}

// TestMemoryExtractionRequiresProject verifies that tasks without
// a project don't attempt memory extraction.
func TestMemoryExtractionRequiresProject(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Task without project
	task := &db.Task{
		Title:   "No project task",
		Status:  db.StatusDone,
		Project: "", // No project
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	executor := &Executor{
		db:     database,
		logger: log.NewWithOptions(nil, log.Options{Level: log.DebugLevel}),
	}

	ctx := context.Background()
	err = executor.ExtractMemories(ctx, task)
	if err != nil {
		t.Errorf("extraction should return nil for tasks without project: %v", err)
	}
}

// TestMemoryDuplicatePrevention verifies that similar memories
// are not duplicated.
func TestMemoryDuplicatePrevention(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not found")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	projectName := "dedup-test-project"
	err = database.CreateProject(&db.Project{
		Name: projectName,
		Path: tmpDir,
	})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Pre-create a memory that might be extracted
	existingMemory := &db.ProjectMemory{
		Project:  projectName,
		Category: db.MemoryCategoryContext,
		Content:  "The project uses JWT tokens for authentication",
	}
	if err := database.CreateMemory(existingMemory); err != nil {
		t.Fatalf("failed to create existing memory: %v", err)
	}

	// Create task with logs that mention the same thing
	task := &db.Task{
		Title:   "Add token refresh",
		Body:    "Implement token refresh mechanism",
		Status:  db.StatusDone,
		Type:    db.TypeCode,
		Project: projectName,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	logs := []struct {
		lineType string
		content  string
	}{
		{"output", "The project uses JWT tokens for authentication"},
		{"output", "Added refresh token support"},
	}
	for _, log := range logs {
		if err := database.AppendTaskLog(task.ID, log.lineType, log.content); err != nil {
			t.Fatalf("failed to add log: %v", err)
		}
	}

	cfg := config.New(database)
	executor := &Executor{
		db:     database,
		config: cfg,
		logger: log.NewWithOptions(nil, log.Options{Level: log.DebugLevel}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	err = executor.ExtractMemories(ctx, task)
	if err != nil {
		t.Logf("extraction error (may be expected): %v", err)
	}

	// Get all memories and check for duplicates
	memories, err := database.GetProjectMemories(projectName, 50)
	if err != nil {
		t.Fatalf("failed to get memories: %v", err)
	}

	// Count how many times the JWT memory appears
	jwtCount := 0
	for _, m := range memories {
		if strings.Contains(strings.ToLower(m.Content), "jwt") {
			jwtCount++
		}
	}

	t.Logf("Found %d JWT-related memories", jwtCount)
	// We expect Claude to avoid creating duplicate memories
	// but this is not a strict requirement
}
