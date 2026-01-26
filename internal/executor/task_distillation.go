// Package executor provides task distillation for extracting structured summaries from completed tasks.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// TaskSummary represents a distilled summary of a completed task.
// This structured format enables efficient search indexing and memory extraction.
type TaskSummary struct {
	WhatWasDone  string     `json:"what_was_done"`  // Brief description of what was accomplished
	FilesChanged []string   `json:"files_changed"`  // Key files that were modified
	Decisions    []Decision `json:"decisions"`      // Architectural/design decisions made
	Learnings    []Learning `json:"learnings"`      // Patterns, gotchas, and insights discovered
}

// Decision represents an architectural or design decision made during a task.
type Decision struct {
	Description string `json:"description"` // What was decided
	Rationale   string `json:"rationale"`   // Why this decision was made
}

// Learning represents a pattern, gotcha, or insight discovered during a task.
type Learning struct {
	Category string `json:"category"` // pattern, context, decision, gotcha, general
	Content  string `json:"content"`  // The actual learning
}

// ToSearchExcerpt converts the TaskSummary to a search-friendly string.
// This is used for FTS5 indexing and should be concise but comprehensive.
func (s *TaskSummary) ToSearchExcerpt() string {
	var parts []string

	// Add summary
	if s.WhatWasDone != "" {
		parts = append(parts, fmt.Sprintf("Summary: %s", s.WhatWasDone))
	}

	// Add files
	if len(s.FilesChanged) > 0 {
		parts = append(parts, fmt.Sprintf("Files: %s", strings.Join(s.FilesChanged, ", ")))
	}

	// Add decisions
	for _, d := range s.Decisions {
		if d.Rationale != "" {
			parts = append(parts, fmt.Sprintf("Decision: %s (Rationale: %s)", d.Description, d.Rationale))
		} else {
			parts = append(parts, fmt.Sprintf("Decision: %s", d.Description))
		}
	}

	// Add learnings
	for _, l := range s.Learnings {
		parts = append(parts, fmt.Sprintf("[%s] %s", l.Category, l.Content))
	}

	return strings.Join(parts, "\n")
}

// DistillTaskSummary uses an LLM to distill a compaction summary into a structured TaskSummary.
// Returns nil if the content is too short or empty.
func (e *Executor) DistillTaskSummary(ctx context.Context, task *db.Task, compactionContent string) (*TaskSummary, error) {
	// Require substantial content
	if len(compactionContent) < 100 {
		return nil, nil
	}

	// Use a timeout for distillation
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	prompt := buildDistillationPrompt(task, compactionContent)
	projectDir := e.getProjectDir(task.Project)

	// JSON schema for structured output
	jsonSchema := `{
		"type": "object",
		"properties": {
			"what_was_done": {"type": "string"},
			"files_changed": {"type": "array", "items": {"type": "string"}},
			"decisions": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"description": {"type": "string"},
						"rationale": {"type": "string"}
					},
					"required": ["description"]
				}
			},
			"learnings": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"category": {"type": "string"},
						"content": {"type": "string"}
					},
					"required": ["category", "content"]
				}
			}
		},
		"required": ["what_was_done"]
	}`

	args := []string{
		"-p",
		"--output-format", "json",
		"--json-schema", jsonSchema,
		prompt,
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	if projectDir != "" {
		cmd.Dir = projectDir
	}

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("claude execution: %w", err)
	}

	// Parse the JSON response
	var response struct {
		StructuredOutput TaskSummary `json:"structured_output"`
		IsError          bool        `json:"is_error"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("parse claude response: %w", err)
	}

	if response.IsError {
		return nil, fmt.Errorf("claude returned error")
	}

	return &response.StructuredOutput, nil
}

// buildDistillationPrompt creates the prompt for distilling a task summary.
func buildDistillationPrompt(task *db.Task, content string) string {
	var prompt strings.Builder

	prompt.WriteString(`Distill this completed task session into a structured summary. Extract the essential information that would help someone understand what was done without reading the full transcript.

## Task Information
`)
	prompt.WriteString(fmt.Sprintf("Title: %s\n", task.Title))
	if task.Project != "" {
		prompt.WriteString(fmt.Sprintf("Project: %s\n", task.Project))
	}
	if task.Body != "" {
		prompt.WriteString(fmt.Sprintf("Description: %s\n", task.Body))
	}

	prompt.WriteString(`
## Session Transcript
`)
	// Include the full content - the LLM will distill it
	prompt.WriteString(content)

	prompt.WriteString(`

## Instructions

Create a structured summary with:

1. **what_was_done**: A 1-2 sentence summary of the main accomplishment. Be specific.

2. **files_changed**: List the key files that were created or modified. Include paths. Limit to the most important 5-10 files.

3. **decisions**: List 0-3 significant architectural or design decisions made during this task. Include:
   - description: What was decided
   - rationale: Why this approach was chosen (if discussed)

4. **learnings**: List 0-5 useful learnings discovered. Each should have:
   - category: One of "pattern", "context", "decision", "gotcha", or "general"
   - content: A concise description of the learning (1-2 sentences)

Focus on information that would be valuable for future work on this project. Skip trivial details.
`)

	return prompt.String()
}

// SaveMemoriesFromSummary extracts and saves project memories from a TaskSummary.
func (e *Executor) SaveMemoriesFromSummary(task *db.Task, summary *TaskSummary) error {
	if task.Project == "" || summary == nil {
		return nil
	}

	// Save decisions as memories
	for _, d := range summary.Decisions {
		if d.Description == "" {
			continue
		}
		content := d.Description
		if d.Rationale != "" {
			content = fmt.Sprintf("%s (Rationale: %s)", d.Description, d.Rationale)
		}

		memory := &db.ProjectMemory{
			Project:      task.Project,
			Category:     db.MemoryCategoryDecision,
			Content:      content,
			SourceTaskID: &task.ID,
		}
		if err := e.db.CreateMemory(memory); err != nil {
			e.logger.Error("Failed to save decision memory", "error", err)
		}
	}

	// Save learnings as memories
	for _, l := range summary.Learnings {
		if l.Content == "" {
			continue
		}

		category := normalizeCategory(l.Category)
		memory := &db.ProjectMemory{
			Project:      task.Project,
			Category:     category,
			Content:      l.Content,
			SourceTaskID: &task.ID,
		}
		if err := e.db.CreateMemory(memory); err != nil {
			e.logger.Error("Failed to save learning memory", "error", err)
		}
	}

	return nil
}

// processCompletedTask orchestrates the post-completion processing:
// 1. Gets the compaction summary (full conversation transcript)
// 2. Distills it into a structured TaskSummary using an LLM
// 3. Saves the summary to the task for future reference
// 4. Extracts and saves project memories from the summary
// 5. Indexes the task for FTS5 search using the distilled summary
// 6. Updates .claude/memories.md with the latest memories
func (e *Executor) processCompletedTask(ctx context.Context, task *db.Task) error {
	// Get the compaction summary which contains the full conversation
	compaction, err := e.db.GetLatestCompactionSummary(task.ID)
	if err != nil {
		return fmt.Errorf("get compaction summary: %w", err)
	}

	var compactionContent string
	if compaction != nil {
		compactionContent = compaction.Summary
	}

	// Distill the summary using an LLM
	summary, err := e.DistillTaskSummary(ctx, task, compactionContent)
	if err != nil {
		e.logger.Error("Failed to distill task summary", "task", task.ID, "error", err)
		// Non-fatal - continue with empty summary
	}

	var summaryText string
	if summary != nil {
		summaryText = summary.ToSearchExcerpt()

		// Save the distilled summary to the task
		if err := e.db.SaveTaskSummary(task.ID, summaryText); err != nil {
			e.logger.Error("Failed to save task summary", "task", task.ID, "error", err)
		}

		// Extract and save project memories
		if err := e.SaveMemoriesFromSummary(task, summary); err != nil {
			e.logger.Error("Failed to save memories", "task", task.ID, "error", err)
		}

		// Log what we extracted
		e.logLine(task.ID, "system", fmt.Sprintf("Distilled: %s", truncateSummary(summary.WhatWasDone, 80)))
		for _, d := range summary.Decisions {
			e.logLine(task.ID, "system", fmt.Sprintf("Decision: %s", truncateSummary(d.Description, 60)))
		}
		for _, l := range summary.Learnings {
			e.logLine(task.ID, "system", fmt.Sprintf("Learned [%s]: %s", l.Category, truncateSummary(l.Content, 60)))
		}
	}

	// Index task for search using the distilled summary (or empty if distillation failed)
	if err := e.db.IndexTaskForSearch(
		task.ID,
		task.Project,
		task.Title,
		task.Body,
		task.Tags,
		summaryText,
	); err != nil {
		e.logger.Debug("Failed to index task for search", "task", task.ID, "error", err)
	} else {
		e.logger.Debug("Indexed task for search", "task", task.ID)
	}

	// Update .claude/memories.md with the latest memories
	if task.Project != "" {
		if err := e.GenerateMemoriesMD(task.Project); err != nil {
			e.logger.Error("Failed to generate memories.md", "error", err)
		}
	}

	return nil
}

// truncateSummary truncates a string with ellipsis if it exceeds maxLen.
func truncateSummary(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// DistillationInterval is the minimum time between distillations when no new content exists.
const DistillationInterval = 10 * time.Minute

// MaybeDistillTask checks if a task should be distilled and runs distillation if appropriate.
// This is called after every ExecResult to capture learnings continuously, not just on completion.
// Runs in background and doesn't block the main execution flow.
func (e *Executor) MaybeDistillTask(task *db.Task) {
	// Refresh task from DB to get latest state
	freshTask, err := e.db.GetTask(task.ID)
	if err != nil || freshTask == nil {
		e.logger.Debug("Failed to get fresh task for distillation check", "task", task.ID, "error", err)
		return
	}

	should, reason := e.shouldDistill(freshTask)
	if !should {
		e.logger.Debug("Skipping distillation", "task", task.ID, "reason", "no trigger")
		return
	}

	e.logger.Info("Triggering distillation", "task", task.ID, "reason", reason)

	// Run distillation in background
	go func() {
		ctx := context.Background()

		// Get the compaction summary
		compaction, err := e.db.GetLatestCompactionSummary(freshTask.ID)
		if err != nil {
			e.logger.Error("Failed to get compaction summary for distillation", "task", freshTask.ID, "error", err)
			return
		}

		var compactionContent string
		if compaction != nil {
			compactionContent = compaction.Summary
		}

		// Distill the summary using an LLM
		summary, err := e.DistillTaskSummary(ctx, freshTask, compactionContent)
		if err != nil {
			e.logger.Error("Failed to distill task summary", "task", freshTask.ID, "error", err)
			// Still update last_distilled_at to avoid retrying immediately
			e.db.UpdateTaskLastDistilledAt(freshTask.ID, time.Now())
			return
		}

		if summary != nil {
			// Save the distilled summary to the task
			summaryText := summary.ToSearchExcerpt()
			if err := e.db.SaveTaskSummary(freshTask.ID, summaryText); err != nil {
				e.logger.Error("Failed to save task summary", "task", freshTask.ID, "error", err)
			}

			// Extract and save project memories
			if err := e.SaveMemoriesFromSummary(freshTask, summary); err != nil {
				e.logger.Error("Failed to save memories", "task", freshTask.ID, "error", err)
			}

			// Log what we extracted
			e.logLine(freshTask.ID, "system", fmt.Sprintf("Distilled: %s", truncateSummary(summary.WhatWasDone, 80)))

			// Update .claude/memories.md with the latest memories
			if freshTask.Project != "" {
				if err := e.GenerateMemoriesMD(freshTask.Project); err != nil {
					e.logger.Error("Failed to generate memories.md", "error", err)
				}
			}
		}

		// Update last_distilled_at to track when we distilled
		if err := e.db.UpdateTaskLastDistilledAt(freshTask.ID, time.Now()); err != nil {
			e.logger.Error("Failed to update last_distilled_at", "task", freshTask.ID, "error", err)
		}
	}()
}

// shouldDistill determines if a task should be distilled based on:
// 1. New compaction content exists (compaction is newer than last_distilled_at)
// 2. Enough time has passed since last distillation (10+ min)
// Returns (should distill, reason) where reason is "new_compaction", "time_elapsed", or ""
func (e *Executor) shouldDistill(task *db.Task) (bool, string) {
	// Get the latest compaction summary
	compaction, err := e.db.GetLatestCompactionSummary(task.ID)
	if err != nil {
		e.logger.Debug("Failed to get compaction summary for distillation check", "task", task.ID, "error", err)
		return false, ""
	}

	now := time.Now()
	neverDistilled := task.LastDistilledAt == nil

	// Case 1: Check for new compaction content
	if compaction != nil {
		// If never distilled and has compaction, should distill
		if neverDistilled {
			return true, "new_compaction"
		}

		// If compaction is newer than last distillation, should distill
		if compaction.CreatedAt.Time.After(task.LastDistilledAt.Time) {
			return true, "new_compaction"
		}
	}

	// Case 2: Check time-based trigger (only if task has been started and we have some history)
	if !neverDistilled && task.StartedAt != nil {
		timeSinceDistillation := now.Sub(task.LastDistilledAt.Time)
		if timeSinceDistillation >= DistillationInterval {
			return true, "time_elapsed"
		}
	}

	// No distillation needed
	return false, ""
}
