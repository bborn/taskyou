// Package executor provides memory management for completed tasks.
package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bborn/workflow/internal/db"
)

// normalizeCategory converts a category string to a valid memory category.
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
