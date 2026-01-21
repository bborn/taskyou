// Package executor provides memory extraction from completed tasks.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// ExtractedMemory represents a memory extracted from task execution.
type ExtractedMemory struct {
	Category string `json:"category"` // pattern, context, decision, gotcha, general
	Content  string `json:"content"`
}

// MemoryExtractionResult holds the result of memory extraction.
type MemoryExtractionResult struct {
	Memories []ExtractedMemory `json:"memories"`
}

// ExtractMemories analyzes completed task logs and extracts useful memories.
func (e *Executor) ExtractMemories(ctx context.Context, task *db.Task) error {
	if task.Project == "" {
		return nil // Can't store memories without a project
	}

	// Get the compaction summary which contains the actual Claude conversation
	summary, err := e.db.GetLatestCompactionSummary(task.ID)
	if err != nil {
		return fmt.Errorf("get compaction summary: %w", err)
	}

	var logContent strings.Builder

	// Use compaction summary if available (contains the actual conversation)
	if summary != nil && len(summary.Summary) > 100 {
		logContent.WriteString(summary.Summary)
	} else {
		// Fallback to task logs if no compaction summary
		logs, err := e.db.GetTaskLogs(task.ID, 500)
		if err != nil {
			return fmt.Errorf("get task logs: %w", err)
		}

		if len(logs) == 0 {
			return nil
		}

		// Build context from logs - include system logs which contain Claude's output
		for _, log := range logs {
			if log.LineType == "output" || log.LineType == "text" || log.LineType == "system" {
				logContent.WriteString(log.Content)
				logContent.WriteString("\n")
			}
		}
	}

	// Skip if not enough content
	if logContent.Len() < 100 {
		return nil
	}

	// Get existing memories to avoid duplicates
	existingMemories, err := e.db.GetProjectMemories(task.Project, 50)
	if err != nil {
		return fmt.Errorf("get existing memories: %w", err)
	}

	var existingContent strings.Builder
	for _, m := range existingMemories {
		existingContent.WriteString(fmt.Sprintf("- [%s] %s\n", m.Category, m.Content))
	}

	// Build extraction prompt
	prompt := buildExtractionPrompt(task, logContent.String(), existingContent.String())

	// Run Claude to extract memories
	memories, err := e.runMemoryExtraction(ctx, task, prompt)
	if err != nil {
		e.logger.Error("Memory extraction failed", "task", task.ID, "error", err)
		return err
	}

	// Save extracted memories
	for _, mem := range memories {
		// Validate category
		category := normalizeCategory(mem.Category)
		if category == "" {
			continue
		}

		// Skip if content is too short or empty
		content := strings.TrimSpace(mem.Content)
		if len(content) < 10 {
			continue
		}

		memory := &db.ProjectMemory{
			Project:      task.Project,
			Category:     category,
			Content:      content,
			SourceTaskID: &task.ID,
		}

		if err := e.db.CreateMemory(memory); err != nil {
			e.logger.Error("Failed to save memory", "error", err)
			continue
		}

		e.logLine(task.ID, "system", fmt.Sprintf("Learned: [%s] %s", category, truncate(content, 60)))
	}

	// Update .claude/memories.md with the latest memories so Claude doesn't have to re-explore the codebase
	if err := e.GenerateMemoriesMD(task.Project); err != nil {
		e.logger.Error("Failed to generate memories.md", "error", err)
		// Non-fatal - continue even if memories.md generation fails
	}

	return nil
}

func buildExtractionPrompt(task *db.Task, logContent, existingMemories string) string {
	var prompt strings.Builder

	prompt.WriteString(`Analyze this completed task and extract any useful learnings that should be remembered for future tasks on this project.

## Task Information
`)
	prompt.WriteString(fmt.Sprintf("Title: %s\n", task.Title))
	prompt.WriteString(fmt.Sprintf("Project: %s\n", task.Project))
	prompt.WriteString(fmt.Sprintf("Type: %s\n", task.Type))
	if task.Body != "" {
		prompt.WriteString(fmt.Sprintf("Description: %s\n", task.Body))
	}

	prompt.WriteString(`
## Task Execution Log (truncated)
`)
	// Truncate log content to avoid token limits
	maxLogLen := 8000
	if len(logContent) > maxLogLen {
		logContent = logContent[:maxLogLen] + "\n... (truncated)"
	}
	prompt.WriteString(logContent)

	if existingMemories != "" {
		prompt.WriteString(`

## Existing Memories (avoid duplicates)
`)
		prompt.WriteString(existingMemories)
	}

	prompt.WriteString(`

## Instructions

Extract 0-3 key learnings from this task that would be useful for future work on this project. Focus on:

- **pattern**: Code patterns, naming conventions, file organization discovered
- **context**: Important project context (architecture, key dependencies, how things work)
- **decision**: Architectural or design decisions made and why
- **gotcha**: Pitfalls, workarounds, things that didn't work as expected
- **general**: Other useful learnings

Guidelines:
- Only extract genuinely useful, non-obvious information
- Be concise but specific (include file paths, function names, etc. when relevant)
- Don't duplicate existing memories
- Return empty array if nothing worth remembering
- Each memory should be 1-2 sentences max

Respond with ONLY a JSON object in this exact format:
{"memories": [{"category": "pattern", "content": "..."}, ...]}

If there's nothing worth extracting, respond with:
{"memories": []}
`)

	return prompt.String()
}

func (e *Executor) runMemoryExtraction(ctx context.Context, task *db.Task, prompt string) ([]ExtractedMemory, error) {
	// Use a timeout for extraction
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	projectDir := e.getProjectDir(task.Project)

	jsonSchema := `{"type":"object","properties":{"memories":{"type":"array","items":{"type":"object","properties":{"category":{"type":"string"},"content":{"type":"string"}},"required":["category","content"]}}},"required":["memories"]}`

	args := []string{
		"-p",
		"--output-format", "json",
		"--json-schema", jsonSchema,
		prompt,
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = projectDir

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("claude execution: %w", err)
	}

	// Parse the JSON response - with --output-format json and --json-schema,
	// Claude returns structured output directly in the structured_output field
	var response struct {
		StructuredOutput MemoryExtractionResult `json:"structured_output"`
		IsError          bool                   `json:"is_error"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("parse claude response: %w", err)
	}

	if response.IsError {
		return nil, fmt.Errorf("claude returned error")
	}

	return response.StructuredOutput.Memories, nil
}

func normalizeCategory(category string) string {
	category = strings.ToLower(strings.TrimSpace(category))
	switch category {
	case "pattern", "patterns":
		return db.MemoryCategoryPattern
	case "context":
		return db.MemoryCategoryContext
	case "decision", "decisions":
		return db.MemoryCategoryDecision
	case "gotcha", "gotchas", "pitfall", "pitfalls":
		return db.MemoryCategoryGotcha
	case "general":
		return db.MemoryCategoryGeneral
	default:
		return db.MemoryCategoryGeneral
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// GenerateMemoriesMD creates or updates a .claude/memories.md file in the project directory
// with accumulated project memories. Claude Code reads files in the .claude/ directory,
// so this provides context without clobbering any existing CLAUDE.md in the project root.
func (e *Executor) GenerateMemoriesMD(project string) error {
	if project == "" {
		return nil
	}

	projectDir := e.getProjectDir(project)
	if projectDir == "" {
		return fmt.Errorf("project directory not found: %s", project)
	}

	// Use .claude/memories.md to avoid clobbering any existing CLAUDE.md
	claudeDir := filepath.Join(projectDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return fmt.Errorf("create .claude dir: %w", err)
	}
	memoriesPath := filepath.Join(claudeDir, "memories.md")

	// Get all project memories
	memories, err := e.db.GetProjectMemories(project, 100)
	if err != nil {
		return fmt.Errorf("get project memories: %w", err)
	}

	// Build memories.md content
	var content strings.Builder

	content.WriteString("# Project Memories\n\n")
	content.WriteString("This file is auto-generated from task completions to help Claude understand the codebase.\n")
	content.WriteString("Do not edit manually - changes will be overwritten.\n\n")

	if len(memories) == 0 {
		content.WriteString("No project memories have been captured yet. They will appear here as tasks are completed.\n")
	} else {
		// Group memories by category
		byCategory := make(map[string][]*db.ProjectMemory)
		for _, m := range memories {
			byCategory[m.Category] = append(byCategory[m.Category], m)
		}

		// Category order and labels (same as getProjectMemoriesSection)
		categoryOrder := []string{
			db.MemoryCategoryPattern,
			db.MemoryCategoryContext,
			db.MemoryCategoryDecision,
			db.MemoryCategoryGotcha,
			db.MemoryCategoryGeneral,
		}
		categoryLabels := map[string]string{
			db.MemoryCategoryPattern:  "Patterns & Conventions",
			db.MemoryCategoryContext:  "Project Context",
			db.MemoryCategoryDecision: "Key Decisions",
			db.MemoryCategoryGotcha:   "Known Gotchas",
			db.MemoryCategoryGeneral:  "General Notes",
		}

		for _, cat := range categoryOrder {
			mems := byCategory[cat]
			if len(mems) == 0 {
				continue
			}

			label := categoryLabels[cat]
			if label == "" {
				label = cat
			}

			content.WriteString(fmt.Sprintf("## %s\n\n", label))
			for _, m := range mems {
				content.WriteString(fmt.Sprintf("- %s\n", m.Content))
			}
			content.WriteString("\n")
		}
	}

	// Write the file
	if err := os.WriteFile(memoriesPath, []byte(content.String()), 0644); err != nil {
		return fmt.Errorf("write memories.md: %w", err)
	}

	e.logger.Debug("Generated .claude/memories.md", "project", project, "memories", len(memories))
	return nil
}
