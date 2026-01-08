// Package executor provides task execution and triage functionality.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/bborn/workflow/internal/db"
)

// TriageResult contains the decisions made by the triage process.
type TriageResult struct {
	// Task classification
	Project  string `json:"project,omitempty"`
	Type     string `json:"type,omitempty"`
	Priority string `json:"priority,omitempty"`

	// Enhanced description
	EnhancedTitle string `json:"enhanced_title,omitempty"`
	EnhancedBody  string `json:"enhanced_body,omitempty"`

	// Flags
	NeedsMoreInfo bool   `json:"needs_more_info,omitempty"`
	Question      string `json:"question,omitempty"`

	// Processing instructions from project config
	ProcessingNotes string `json:"processing_notes,omitempty"`
}

// TriageTask runs a fast triage process on a task to determine its classification
// and enhance its description before full execution.
func (e *Executor) TriageTask(ctx context.Context, task *db.Task) (*TriageResult, error) {
	// Skip triage if task is already well-defined
	if e.taskIsWellDefined(task) {
		return nil, nil
	}

	e.logLine(task.ID, "system", "Starting task triage...")

	// Build triage prompt
	prompt := e.buildTriagePrompt(task)

	// Run quick triage with Claude
	result, err := e.runTriageClaude(ctx, task.ID, prompt)
	if err != nil {
		e.logLine(task.ID, "error", fmt.Sprintf("Triage failed: %v", err))
		return nil, err
	}

	// Apply triage results to task
	if result != nil {
		e.applyTriageResult(task, result)
	}

	e.logLine(task.ID, "system", "Triage complete")
	return result, nil
}

// taskIsWellDefined checks if a task has enough info to skip triage.
func (e *Executor) taskIsWellDefined(task *db.Task) bool {
	// Must have project and type
	if task.Project == "" || task.Type == "" {
		return false
	}

	// Must have some description beyond just a title
	if len(task.Body) < 20 && len(task.Title) < 50 {
		return false
	}

	return true
}

// buildTriagePrompt creates the prompt for task triage.
func (e *Executor) buildTriagePrompt(task *db.Task) string {
	var sb strings.Builder

	sb.WriteString("Help me categorize and enhance this task.\n\n")

	// Add available projects context
	projects, _ := e.db.ListProjects()
	if len(projects) > 0 {
		sb.WriteString("## Available Projects\n\n")
		for _, p := range projects {
			sb.WriteString(fmt.Sprintf("- **%s**: path=%s", p.Name, p.Path))
			if p.Aliases != "" {
				sb.WriteString(fmt.Sprintf(" (aliases: %s)", p.Aliases))
			}
			sb.WriteString("\n")
			if p.Instructions != "" {
				sb.WriteString(fmt.Sprintf("  Instructions: %s\n", p.Instructions))
			}
		}
		sb.WriteString("\n")
	}

	// Add task types
	sb.WriteString("## Task Types\n\n")
	sb.WriteString("- **code**: Programming/development tasks\n")
	sb.WriteString("- **writing**: Content creation, documentation\n")
	sb.WriteString("- **thinking**: Analysis, strategy, planning\n\n")

	// Add the task to triage
	sb.WriteString("## Task to Triage\n\n")
	sb.WriteString(fmt.Sprintf("**Title:** %s\n", task.Title))
	if task.Body != "" {
		sb.WriteString(fmt.Sprintf("**Body:** %s\n", task.Body))
	}
	if task.Project != "" {
		sb.WriteString(fmt.Sprintf("**Current Project:** %s\n", task.Project))
	}
	if task.Type != "" {
		sb.WriteString(fmt.Sprintf("**Current Type:** %s\n", task.Type))
	}
	if task.Priority != "" {
		sb.WriteString(fmt.Sprintf("**Current Priority:** %s\n", task.Priority))
	}

	// Add attachment info
	attachments, _ := e.db.ListAttachments(task.ID)
	if len(attachments) > 0 {
		sb.WriteString("\n**Attachments:**\n")
		for _, a := range attachments {
			sb.WriteString(fmt.Sprintf("- %s (%s, %d bytes)\n", a.Filename, a.MimeType, a.Size))
		}
	}

	sb.WriteString("\n## Instructions\n\n")
	sb.WriteString(`Analyze the task and respond with a JSON object containing your decisions:

{
  "project": "project-name or empty if unclear",
  "type": "code|writing|thinking or empty if unclear",
  "priority": "high|normal|low or empty to keep current",
  "enhanced_title": "clearer title if original is vague, or empty to keep",
  "enhanced_body": "fleshed out description with acceptance criteria if original is sparse, or empty to keep",
  "needs_more_info": true/false,
  "question": "question to ask user if needs_more_info is true",
  "processing_notes": "any special handling instructions based on project instructions"
}

Rules:
1. Only set fields that need to change
2. If the task mentions a specific project or codebase, set project
3. If the task is clearly code/writing/thinking, set type
4. If the description is vague (< 50 chars, no clear deliverable), enhance it
5. If you can't determine key info, set needs_more_info=true with a specific question
6. If the task has attachments, assume they provide the needed context - don't ask for more info unless truly necessary
7. Check project instructions for any orchestration/processing requirements

Respond with ONLY the JSON object, no other text.`)

	return sb.String()
}

// runTriageClaude runs a quick Claude call for triage.
func (e *Executor) runTriageClaude(ctx context.Context, taskID int64, prompt string) (*TriageResult, error) {
	// Use crush with quiet mode for triage
	args := []string{
		"run",
		"-q", // quiet mode
		prompt,
	}

	cmd := exec.CommandContext(ctx, "crush", args...)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("crush triage: %w", err)
	}

	// Parse JSON from output
	result := &TriageResult{}
	outputStr := string(output)

	// Find JSON in output (may be wrapped in markdown code blocks)
	jsonStr := extractJSON(outputStr)
	if jsonStr == "" {
		e.logLine(taskID, "text", "Triage output: "+outputStr)
		return nil, nil // No valid JSON, skip triage
	}

	if err := json.Unmarshal([]byte(jsonStr), result); err != nil {
		e.logLine(taskID, "text", "Triage output: "+outputStr)
		return nil, nil // Invalid JSON, skip triage
	}

	return result, nil
}

// extractJSON finds and extracts JSON from potentially wrapped text.
func extractJSON(text string) string {
	// Try direct parse first
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "{") {
		// Find matching closing brace
		depth := 0
		for i, c := range text {
			if c == '{' {
				depth++
			} else if c == '}' {
				depth--
				if depth == 0 {
					return text[:i+1]
				}
			}
		}
	}

	// Try to extract from code blocks
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(text[start:], "```"); end >= 0 {
			return strings.TrimSpace(text[start : start+end])
		}
	}
	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + 3
		// Skip any language identifier
		if newline := strings.Index(text[start:], "\n"); newline >= 0 {
			start += newline + 1
		}
		if end := strings.Index(text[start:], "```"); end >= 0 {
			return strings.TrimSpace(text[start : start+end])
		}
	}

	return ""
}

// applyTriageResult updates a task with triage decisions.
func (e *Executor) applyTriageResult(task *db.Task, result *TriageResult) {
	changes := []string{}

	if result.Project != "" && task.Project == "" {
		task.Project = result.Project
		changes = append(changes, fmt.Sprintf("project=%s", result.Project))
	}

	if result.Type != "" && task.Type == "" {
		task.Type = result.Type
		changes = append(changes, fmt.Sprintf("type=%s", result.Type))
	}

	if result.Priority != "" && result.Priority != task.Priority {
		task.Priority = result.Priority
		changes = append(changes, fmt.Sprintf("priority=%s", result.Priority))
	}

	if result.EnhancedTitle != "" && result.EnhancedTitle != task.Title {
		task.Title = result.EnhancedTitle
		changes = append(changes, "title enhanced")
	}

	if result.EnhancedBody != "" {
		if task.Body == "" {
			task.Body = result.EnhancedBody
		} else {
			task.Body = task.Body + "\n\n---\n\n" + result.EnhancedBody
		}
		changes = append(changes, "body enhanced")
	}

	// Save changes to database
	if len(changes) > 0 {
		e.db.UpdateTask(task)
		e.logLine(task.ID, "system", fmt.Sprintf("Triage applied: %s", strings.Join(changes, ", ")))
	}

	// Handle needs_more_info by logging the question
	if result.NeedsMoreInfo && result.Question != "" {
		e.logLine(task.ID, "question", result.Question)
	}

	// Store processing notes as a log entry for the executor
	if result.ProcessingNotes != "" {
		e.logLine(task.ID, "text", fmt.Sprintf("Processing notes: %s", result.ProcessingNotes))
	}
}

// NeedsTriage returns true if the task should go through triage before execution.
func NeedsTriage(task *db.Task) bool {
	// Empty project or type needs triage
	if task.Project == "" || task.Type == "" {
		return true
	}

	// Very short descriptions may need enhancement
	if len(task.Body) < 20 && len(task.Title) < 30 {
		return true
	}

	return false
}
