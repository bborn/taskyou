package executor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/log"
)

// TestTaskSummaryStructure verifies the TaskSummary struct has all required fields.
func TestTaskSummaryStructure(t *testing.T) {
	summary := &TaskSummary{
		WhatWasDone:  "Implemented user authentication with JWT tokens",
		FilesChanged: []string{"internal/auth/jwt.go", "internal/auth/middleware.go"},
		Decisions: []Decision{
			{Description: "Use RS256 for JWT signing", Rationale: "More secure than HS256 for distributed systems"},
		},
		Learnings: []Learning{
			{Category: "pattern", Content: "Auth middleware should be applied at router level"},
			{Category: "gotcha", Content: "Token refresh requires separate endpoint"},
		},
	}

	if summary.WhatWasDone == "" {
		t.Error("WhatWasDone should not be empty")
	}
	if len(summary.FilesChanged) != 2 {
		t.Errorf("Expected 2 files changed, got %d", len(summary.FilesChanged))
	}
	if len(summary.Decisions) != 1 {
		t.Errorf("Expected 1 decision, got %d", len(summary.Decisions))
	}
	if len(summary.Learnings) != 2 {
		t.Errorf("Expected 2 learnings, got %d", len(summary.Learnings))
	}
}

// TestTaskSummaryToSearchExcerpt verifies that a TaskSummary can be converted to a search-friendly string.
func TestTaskSummaryToSearchExcerpt(t *testing.T) {
	summary := &TaskSummary{
		WhatWasDone:  "Implemented user authentication",
		FilesChanged: []string{"auth.go", "middleware.go"},
		Decisions: []Decision{
			{Description: "Use JWT tokens", Rationale: "Industry standard"},
		},
		Learnings: []Learning{
			{Category: "pattern", Content: "Apply middleware at router level"},
		},
	}

	excerpt := summary.ToSearchExcerpt()

	// Should contain the summary
	if !strings.Contains(excerpt, "Implemented user authentication") {
		t.Error("Excerpt should contain WhatWasDone")
	}

	// Should contain files
	if !strings.Contains(excerpt, "auth.go") {
		t.Error("Excerpt should contain files changed")
	}

	// Should contain decisions
	if !strings.Contains(excerpt, "Use JWT tokens") {
		t.Error("Excerpt should contain decisions")
	}

	// Should contain learnings
	if !strings.Contains(excerpt, "Apply middleware at router level") {
		t.Error("Excerpt should contain learnings")
	}

	// Should be reasonably sized (not bloated)
	if len(excerpt) > 2000 {
		t.Errorf("Excerpt should be under 2000 chars, got %d", len(excerpt))
	}
}

// TestDistillTaskSummaryRequiresContent verifies that distillation requires substantial content.
func TestDistillTaskSummaryRequiresContent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	cfg := config.New(database)
	executor := &Executor{
		db:     database,
		config: cfg,
		logger: log.NewWithOptions(nil, log.Options{Level: log.DebugLevel}),
	}

	task := &db.Task{
		ID:      1,
		Title:   "Test task",
		Project: "test-project",
	}

	// Should return nil summary for empty content
	summary, err := executor.DistillTaskSummary(context.Background(), task, "")
	if err != nil {
		t.Errorf("Expected no error for empty content, got: %v", err)
	}
	if summary != nil {
		t.Error("Expected nil summary for empty content")
	}

	// Should return nil summary for very short content
	summary, err = executor.DistillTaskSummary(context.Background(), task, "short")
	if err != nil {
		t.Errorf("Expected no error for short content, got: %v", err)
	}
	if summary != nil {
		t.Error("Expected nil summary for short content")
	}
}

// TestSaveTaskSummary verifies that task summaries are saved to the database.
func TestSaveTaskSummary(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Create a project
	err = database.CreateProject(&db.Project{
		Name: "test-project",
		Path: tmpDir,
	})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create a task
	task := &db.Task{
		Title:   "Test task",
		Status:  db.StatusDone,
		Project: "test-project",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	summary := &TaskSummary{
		WhatWasDone:  "Implemented feature X",
		FilesChanged: []string{"file1.go", "file2.go"},
		Decisions: []Decision{
			{Description: "Used pattern Y", Rationale: "Because Z"},
		},
		Learnings: []Learning{
			{Category: "pattern", Content: "Pattern A works well"},
		},
	}

	// Save the summary
	err = database.SaveTaskSummary(task.ID, summary.ToSearchExcerpt())
	if err != nil {
		t.Fatalf("failed to save task summary: %v", err)
	}

	// Retrieve the task and verify summary is saved
	retrieved, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	if retrieved.Summary == "" {
		t.Error("Summary should be saved on task")
	}

	if !strings.Contains(retrieved.Summary, "Implemented feature X") {
		t.Error("Summary should contain WhatWasDone")
	}
}

// TestSearchIndexUsesDistilledSummary verifies that search indexing uses the distilled summary.
func TestSearchIndexUsesDistilledSummary(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Create a project
	err = database.CreateProject(&db.Project{
		Name: "test-project",
		Path: tmpDir,
	})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create a task with a summary
	task := &db.Task{
		Title:   "Implement authentication",
		Status:  db.StatusDone,
		Project: "test-project",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Save a summary
	summaryText := "Implemented JWT authentication. Files: auth.go, middleware.go. Decision: Use RS256. Learning: Apply middleware at router level."
	err = database.SaveTaskSummary(task.ID, summaryText)
	if err != nil {
		t.Fatalf("failed to save summary: %v", err)
	}

	// Get the task to get the summary
	task, _ = database.GetTask(task.ID)

	// Index the task for search using the summary
	err = database.IndexTaskForSearch(
		task.ID,
		task.Project,
		task.Title,
		task.Body,
		task.Tags,
		task.Summary, // Use the saved summary instead of raw transcript
	)
	if err != nil {
		t.Fatalf("failed to index task: %v", err)
	}

	// Search for the task
	results, err := database.FindSimilarTasks(&db.Task{Title: "authentication JWT"}, 10)
	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}

	// Should find the task
	found := false
	for _, r := range results {
		if r.TaskID == task.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("Should find task by searching for terms in the summary")
	}
}

// TestShouldDistillWithNewCompaction verifies distillation triggers when compaction is newer than last distillation.
func TestShouldDistillWithNewCompaction(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Create a project and task
	err = database.CreateProject(&db.Project{Name: "test-project", Path: tmpDir})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	task := &db.Task{
		Title:   "Test task",
		Status:  db.StatusProcessing,
		Project: "test-project",
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

	// Set last_distilled_at to 1 hour ago
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	if err := database.UpdateTaskLastDistilledAt(task.ID, oneHourAgo); err != nil {
		t.Fatalf("failed to update last_distilled_at: %v", err)
	}

	// Save a compaction summary (created now, after last_distilled_at)
	summary := &db.CompactionSummary{
		TaskID:    task.ID,
		SessionID: "test-session",
		Trigger:   "auto",
		Summary:   "This is a substantial compaction summary with enough content to distill.",
	}
	if err := database.SaveCompactionSummary(summary); err != nil {
		t.Fatalf("failed to save compaction summary: %v", err)
	}

	// Reload task
	task, _ = database.GetTask(task.ID)

	// Should return true - compaction is newer than last distillation
	should, reason := executor.shouldDistill(task)
	if !should {
		t.Errorf("Expected shouldDistill=true when compaction is newer, got false. Reason: %s", reason)
	}
	if reason != "new_compaction" {
		t.Errorf("Expected reason='new_compaction', got %q", reason)
	}
}

// TestShouldDistillWithTimeElapsed verifies distillation triggers after enough time has passed.
func TestShouldDistillWithTimeElapsed(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Create a project and task
	err = database.CreateProject(&db.Project{Name: "test-project", Path: tmpDir})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	task := &db.Task{
		Title:   "Test task",
		Status:  db.StatusProcessing,
		Project: "test-project",
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

	// Set last_distilled_at to 15 minutes ago (beyond the 10 min threshold)
	fifteenMinAgo := time.Now().Add(-15 * time.Minute)
	if err := database.UpdateTaskLastDistilledAt(task.ID, fifteenMinAgo); err != nil {
		t.Fatalf("failed to update last_distilled_at: %v", err)
	}

	// No compaction summary, but task has been started
	startedAt := time.Now().Add(-20 * time.Minute)
	if err := database.UpdateTaskStartedAt(task.ID, startedAt); err != nil {
		t.Fatalf("failed to update started_at: %v", err)
	}

	// Reload task
	task, _ = database.GetTask(task.ID)

	// Should return true - enough time has passed
	should, reason := executor.shouldDistill(task)
	if !should {
		t.Errorf("Expected shouldDistill=true when enough time elapsed, got false. Reason: %s", reason)
	}
	if reason != "time_elapsed" {
		t.Errorf("Expected reason='time_elapsed', got %q", reason)
	}
}

// TestShouldNotDistillWhenRecentlyDistilled verifies no distillation when recently done and no new content.
func TestShouldNotDistillWhenRecentlyDistilled(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Create a project and task
	err = database.CreateProject(&db.Project{Name: "test-project", Path: tmpDir})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	task := &db.Task{
		Title:   "Test task",
		Status:  db.StatusProcessing,
		Project: "test-project",
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

	// Set last_distilled_at to 2 minutes ago (within the 10 min threshold)
	twoMinAgo := time.Now().Add(-2 * time.Minute)
	if err := database.UpdateTaskLastDistilledAt(task.ID, twoMinAgo); err != nil {
		t.Fatalf("failed to update last_distilled_at: %v", err)
	}

	// Reload task
	task, _ = database.GetTask(task.ID)

	// Should return false - recently distilled, no new compaction
	should, reason := executor.shouldDistill(task)
	if should {
		t.Errorf("Expected shouldDistill=false when recently distilled, got true. Reason: %s", reason)
	}
}

// TestShouldDistillNeverDistilledBefore verifies distillation triggers for tasks never distilled.
func TestShouldDistillNeverDistilledBefore(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Create a project and task
	err = database.CreateProject(&db.Project{Name: "test-project", Path: tmpDir})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	task := &db.Task{
		Title:   "Test task",
		Status:  db.StatusProcessing,
		Project: "test-project",
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

	// Save a compaction summary
	summary := &db.CompactionSummary{
		TaskID:    task.ID,
		SessionID: "test-session",
		Trigger:   "auto",
		Summary:   "This is a substantial compaction summary with enough content to distill.",
	}
	if err := database.SaveCompactionSummary(summary); err != nil {
		t.Fatalf("failed to save compaction summary: %v", err)
	}

	// Reload task (last_distilled_at is nil)
	task, _ = database.GetTask(task.ID)

	// Should return true - never distilled and has compaction
	should, reason := executor.shouldDistill(task)
	if !should {
		t.Errorf("Expected shouldDistill=true for never-distilled task with compaction, got false. Reason: %s", reason)
	}
}

// TestShouldNotDistillNoContent verifies no distillation when there's no content to distill.
func TestShouldNotDistillNoContent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Create a project and task
	err = database.CreateProject(&db.Project{Name: "test-project", Path: tmpDir})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	task := &db.Task{
		Title:   "Test task",
		Status:  db.StatusProcessing,
		Project: "test-project",
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

	// No compaction summary, never distilled, task just created
	task, _ = database.GetTask(task.ID)

	// Should return false - no content to distill
	should, reason := executor.shouldDistill(task)
	if should {
		t.Errorf("Expected shouldDistill=false when no content, got true. Reason: %s", reason)
	}
}
