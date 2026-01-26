package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/log"
)

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

// TestProcessCompletedTaskSkipsEmptyCompaction verifies that task processing
// handles edge cases gracefully.
func TestProcessCompletedTaskSkipsEmptyCompaction(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	projectName := "empty-compaction-project"
	err = database.CreateProject(&db.Project{
		Name: projectName,
		Path: tmpDir,
	})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Task with no compaction summary
	task := &db.Task{
		Title:   "Empty task",
		Status:  db.StatusDone,
		Project: projectName,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	cfg := config.New(database)
	executor := &Executor{
		db:     database,
		config: cfg,
		logger: log.NewWithOptions(nil, log.Options{Level: log.DebugLevel}),
	}

	ctx := context.Background()
	err = executor.processCompletedTask(ctx, task)
	if err != nil {
		t.Errorf("processing should not fail on empty compaction: %v", err)
	}

	// Verify no memories were created
	memories, err := database.GetProjectMemories(projectName, 10)
	if err != nil {
		t.Fatalf("failed to get memories: %v", err)
	}
	if len(memories) != 0 {
		t.Errorf("expected 0 memories for empty compaction, got %d", len(memories))
	}
}

// TestProcessCompletedTaskHandlesNoProject verifies that tasks without
// a project process gracefully.
func TestProcessCompletedTaskHandlesNoProject(t *testing.T) {
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

	cfg := config.New(database)
	executor := &Executor{
		db:     database,
		config: cfg,
		logger: log.NewWithOptions(nil, log.Options{Level: log.DebugLevel}),
	}

	ctx := context.Background()
	err = executor.processCompletedTask(ctx, task)
	if err != nil {
		t.Errorf("processing should not fail for tasks without project: %v", err)
	}
}

// TestGenerateMemoriesMD verifies that .claude/memories.md is generated correctly
// from project memories.
func TestGenerateMemoriesMD(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	projectName := "memories-md-test-project"
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
		{Project: projectName, Category: db.MemoryCategoryGotcha, Content: "The cache has a 5 minute TTL"},
	}

	for _, m := range testMemories {
		if err := database.CreateMemory(m); err != nil {
			t.Fatalf("failed to create memory: %v", err)
		}
	}

	cfg := config.New(database)
	executor := &Executor{
		db:     database,
		config: cfg,
		logger: log.NewWithOptions(nil, log.Options{Level: log.DebugLevel}),
	}

	err = executor.GenerateMemoriesMD(projectName)
	if err != nil {
		t.Fatalf("failed to generate memories.md: %v", err)
	}

	// Read the generated file - should be in .claude/memories.md
	memoriesPath := filepath.Join(tmpDir, ".claude", "memories.md")
	content, err := os.ReadFile(memoriesPath)
	if err != nil {
		t.Fatalf("failed to read memories.md: %v", err)
	}

	contentStr := string(content)

	// Verify header
	if !strings.Contains(contentStr, "# Project Memories") {
		t.Error("Missing main header")
	}

	// Verify category sections exist
	expectedSections := []string{
		"## Patterns & Conventions",
		"## Project Context",
		"## Key Decisions",
		"## Known Gotchas",
	}

	for _, section := range expectedSections {
		if !strings.Contains(contentStr, section) {
			t.Errorf("Missing section: %s", section)
		}
	}

	// Verify memory content is present
	for _, m := range testMemories {
		if !strings.Contains(contentStr, m.Content) {
			t.Errorf("Missing memory content: %s", m.Content)
		}
	}

	t.Logf("Generated memories.md content:\n%s", contentStr)
}

// TestGenerateMemoriesMDNoMemories verifies that .claude/memories.md is generated
// even when there are no memories.
func TestGenerateMemoriesMDNoMemories(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	projectName := "no-memories-test-project"
	err = database.CreateProject(&db.Project{
		Name: projectName,
		Path: tmpDir,
	})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	cfg := config.New(database)
	executor := &Executor{
		db:     database,
		config: cfg,
		logger: log.NewWithOptions(nil, log.Options{Level: log.DebugLevel}),
	}

	err = executor.GenerateMemoriesMD(projectName)
	if err != nil {
		t.Fatalf("failed to generate memories.md: %v", err)
	}

	// Read the generated file
	memoriesPath := filepath.Join(tmpDir, ".claude", "memories.md")
	content, err := os.ReadFile(memoriesPath)
	if err != nil {
		t.Fatalf("failed to read memories.md: %v", err)
	}

	contentStr := string(content)

	// Verify placeholder text for no memories
	if !strings.Contains(contentStr, "No project memories have been captured yet") {
		t.Error("Missing placeholder text for no memories")
	}

	t.Logf("Generated memories.md content:\n%s", contentStr)
}

// TestGenerateMemoriesMDDoesNotClobberClaudeMD verifies that generating memories.md
// does not affect any existing CLAUDE.md in the project root.
func TestGenerateMemoriesMDDoesNotClobberClaudeMD(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	projectName := "clobber-test-project"
	err = database.CreateProject(&db.Project{
		Name: projectName,
		Path: tmpDir,
	})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create an existing CLAUDE.md file that should not be touched
	existingClaudeMD := "# My Custom CLAUDE.md\n\nThis should not be modified."
	claudeMDPath := filepath.Join(tmpDir, "CLAUDE.md")
	if err := os.WriteFile(claudeMDPath, []byte(existingClaudeMD), 0644); err != nil {
		t.Fatalf("failed to create CLAUDE.md: %v", err)
	}

	// Create a memory
	if err := database.CreateMemory(&db.ProjectMemory{
		Project:  projectName,
		Category: db.MemoryCategoryContext,
		Content:  "Test memory",
	}); err != nil {
		t.Fatalf("failed to create memory: %v", err)
	}

	cfg := config.New(database)
	executor := &Executor{
		db:     database,
		config: cfg,
		logger: log.NewWithOptions(nil, log.Options{Level: log.DebugLevel}),
	}

	err = executor.GenerateMemoriesMD(projectName)
	if err != nil {
		t.Fatalf("failed to generate memories.md: %v", err)
	}

	// Verify CLAUDE.md was not modified
	content, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("failed to read CLAUDE.md: %v", err)
	}

	if string(content) != existingClaudeMD {
		t.Error("CLAUDE.md was modified when it should not have been")
	}

	// Verify memories.md was created separately
	memoriesPath := filepath.Join(tmpDir, ".claude", "memories.md")
	if _, err := os.Stat(memoriesPath); os.IsNotExist(err) {
		t.Error("memories.md was not created")
	}
}
