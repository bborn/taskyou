// Package exporter converts tasks to markdown for QMD indexing.
package exporter

import (
	"fmt"
	"strings"

	"github.com/bborn/workflow/extensions/ty-qmd/internal/tasks"
)

// Exporter converts tasks to markdown format.
type Exporter struct {
	includeLogs bool
	maxLogLines int
}

// New creates a new exporter.
func New(includeLogs bool, maxLogLines int) *Exporter {
	return &Exporter{
		includeLogs: includeLogs,
		maxLogLines: maxLogLines,
	}
}

// Export converts a task to markdown.
func (e *Exporter) Export(t tasks.Task) string {
	var sb strings.Builder

	// YAML frontmatter
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("task_id: %d\n", t.ID))
	if t.Project != "" {
		sb.WriteString(fmt.Sprintf("project: %s\n", t.Project))
	}
	sb.WriteString(fmt.Sprintf("status: %s\n", t.Status))
	if t.Type != "" {
		sb.WriteString(fmt.Sprintf("type: %s\n", t.Type))
	}
	sb.WriteString(fmt.Sprintf("created: %s\n", t.CreatedAt.Format("2006-01-02")))
	if t.CompletedAt != nil {
		sb.WriteString(fmt.Sprintf("completed: %s\n", t.CompletedAt.Format("2006-01-02")))
	}
	if t.Tags != "" {
		// Convert comma-separated tags to YAML list
		tags := strings.Split(t.Tags, ",")
		sb.WriteString("tags:\n")
		for _, tag := range tags {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				sb.WriteString(fmt.Sprintf("  - %s\n", tag))
			}
		}
	}
	sb.WriteString("---\n\n")

	// Title
	sb.WriteString(fmt.Sprintf("# %s\n\n", t.Title))

	// Description
	if t.Body != "" {
		sb.WriteString("## Description\n\n")
		sb.WriteString(t.Body)
		sb.WriteString("\n\n")
	}

	// Summary (if available)
	if t.Summary != "" {
		sb.WriteString("## Summary\n\n")
		sb.WriteString(t.Summary)
		sb.WriteString("\n\n")
	}

	// Logs (if enabled)
	if e.includeLogs && len(t.Logs) > 0 {
		sb.WriteString("## Activity Log\n\n")

		count := len(t.Logs)
		if e.maxLogLines > 0 && count > e.maxLogLines {
			count = e.maxLogLines
		}

		for i := 0; i < count; i++ {
			log := t.Logs[i]
			timestamp := log.Time.Format("2006-01-02 15:04")
			// Truncate long messages
			msg := log.Message
			if len(msg) > 500 {
				msg = msg[:500] + "..."
			}
			sb.WriteString(fmt.Sprintf("- **%s** [%s]: %s\n", timestamp, log.Type, msg))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// ExportSummary creates a brief summary for search context.
func (e *Exporter) ExportSummary(t tasks.Task) string {
	var parts []string

	parts = append(parts, fmt.Sprintf("Task #%d: %s", t.ID, t.Title))

	if t.Project != "" {
		parts = append(parts, fmt.Sprintf("Project: %s", t.Project))
	}

	parts = append(parts, fmt.Sprintf("Status: %s", t.Status))

	if t.Body != "" {
		body := t.Body
		if len(body) > 200 {
			body = body[:200] + "..."
		}
		parts = append(parts, body)
	}

	return strings.Join(parts, "\n")
}
