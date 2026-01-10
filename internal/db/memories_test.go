package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectMemories(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a memory
	mem := &ProjectMemory{
		Project:  "testproject",
		Category: MemoryCategoryPattern,
		Content:  "Use functional options pattern for constructors",
	}
	if err := db.CreateMemory(mem); err != nil {
		t.Fatalf("failed to create memory: %v", err)
	}
	if mem.ID == 0 {
		t.Error("expected memory ID to be set")
	}

	// Retrieve the memory
	retrieved, err := db.GetMemory(mem.ID)
	if err != nil {
		t.Fatalf("failed to get memory: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected memory to be found")
	}
	if retrieved.Project != "testproject" {
		t.Errorf("expected project 'testproject', got '%s'", retrieved.Project)
	}
	if retrieved.Category != MemoryCategoryPattern {
		t.Errorf("expected category '%s', got '%s'", MemoryCategoryPattern, retrieved.Category)
	}
	if retrieved.Content != "Use functional options pattern for constructors" {
		t.Errorf("unexpected content: %s", retrieved.Content)
	}
}

func TestListMemoriesFiltering(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create memories for different projects and categories
	memories := []*ProjectMemory{
		{Project: "projectA", Category: MemoryCategoryPattern, Content: "A pattern"},
		{Project: "projectA", Category: MemoryCategoryGotcha, Content: "A gotcha"},
		{Project: "projectB", Category: MemoryCategoryPattern, Content: "B pattern"},
		{Project: "projectB", Category: MemoryCategoryContext, Content: "B context"},
	}

	for _, m := range memories {
		if err := db.CreateMemory(m); err != nil {
			t.Fatalf("failed to create memory: %v", err)
		}
	}

	// Test filter by project
	projectAMems, err := db.ListMemories(ListMemoriesOptions{Project: "projectA"})
	if err != nil {
		t.Fatalf("failed to list memories: %v", err)
	}
	if len(projectAMems) != 2 {
		t.Errorf("expected 2 memories for projectA, got %d", len(projectAMems))
	}

	// Test filter by category
	patternMems, err := db.ListMemories(ListMemoriesOptions{Category: MemoryCategoryPattern})
	if err != nil {
		t.Fatalf("failed to list memories: %v", err)
	}
	if len(patternMems) != 2 {
		t.Errorf("expected 2 pattern memories, got %d", len(patternMems))
	}

	// Test filter by project and category
	projectBPatterns, err := db.ListMemories(ListMemoriesOptions{
		Project:  "projectB",
		Category: MemoryCategoryPattern,
	})
	if err != nil {
		t.Fatalf("failed to list memories: %v", err)
	}
	if len(projectBPatterns) != 1 {
		t.Errorf("expected 1 pattern memory for projectB, got %d", len(projectBPatterns))
	}
}

func TestUpdateMemory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	mem := &ProjectMemory{
		Project:  "testproject",
		Category: MemoryCategoryGeneral,
		Content:  "Original content",
	}
	if err := db.CreateMemory(mem); err != nil {
		t.Fatalf("failed to create memory: %v", err)
	}

	// Update the memory
	mem.Content = "Updated content"
	mem.Category = MemoryCategoryDecision
	if err := db.UpdateMemory(mem); err != nil {
		t.Fatalf("failed to update memory: %v", err)
	}

	// Verify update
	retrieved, err := db.GetMemory(mem.ID)
	if err != nil {
		t.Fatalf("failed to get memory: %v", err)
	}
	if retrieved.Content != "Updated content" {
		t.Errorf("expected 'Updated content', got '%s'", retrieved.Content)
	}
	if retrieved.Category != MemoryCategoryDecision {
		t.Errorf("expected category '%s', got '%s'", MemoryCategoryDecision, retrieved.Category)
	}
}

func TestDeleteMemory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	mem := &ProjectMemory{
		Project:  "testproject",
		Category: MemoryCategoryGeneral,
		Content:  "To be deleted",
	}
	if err := db.CreateMemory(mem); err != nil {
		t.Fatalf("failed to create memory: %v", err)
	}

	// Delete the memory
	if err := db.DeleteMemory(mem.ID); err != nil {
		t.Fatalf("failed to delete memory: %v", err)
	}

	// Verify deletion
	retrieved, err := db.GetMemory(mem.ID)
	if err != nil {
		t.Fatalf("failed to get memory: %v", err)
	}
	if retrieved != nil {
		t.Error("expected memory to be deleted")
	}
}

func TestGetProjectMemories(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create several memories for a project
	for i := 0; i < 5; i++ {
		mem := &ProjectMemory{
			Project:  "myproject",
			Category: MemoryCategoryGeneral,
			Content:  "Memory content",
		}
		if err := db.CreateMemory(mem); err != nil {
			t.Fatalf("failed to create memory: %v", err)
		}
	}

	// GetProjectMemories with limit
	memories, err := db.GetProjectMemories("myproject", 3)
	if err != nil {
		t.Fatalf("failed to get project memories: %v", err)
	}
	if len(memories) != 3 {
		t.Errorf("expected 3 memories with limit, got %d", len(memories))
	}

	// GetProjectMemories with default limit
	allMemories, err := db.GetProjectMemories("myproject", 0)
	if err != nil {
		t.Fatalf("failed to get project memories: %v", err)
	}
	if len(allMemories) != 5 {
		t.Errorf("expected 5 memories, got %d", len(allMemories))
	}
}

func TestMemoryWithSourceTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a task first
	task := &Task{
		Title:    "Source Task",
		Body:    "Task body",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "testproject",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Create memory with source task reference
	taskID := task.ID
	mem := &ProjectMemory{
		Project:      "testproject",
		Category:     MemoryCategoryContext,
		Content:      "Learned from task execution",
		SourceTaskID: &taskID,
	}
	if err := db.CreateMemory(mem); err != nil {
		t.Fatalf("failed to create memory: %v", err)
	}

	// Retrieve and verify
	retrieved, err := db.GetMemory(mem.ID)
	if err != nil {
		t.Fatalf("failed to get memory: %v", err)
	}
	if retrieved.SourceTaskID == nil {
		t.Error("expected SourceTaskID to be set")
	} else if *retrieved.SourceTaskID != task.ID {
		t.Errorf("expected SourceTaskID %d, got %d", task.ID, *retrieved.SourceTaskID)
	}
}
