// Package exporter converts tasks to markdown for QMD indexing.
package exporter

import (
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/db"
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
func (e *Exporter) Export(t *db.Task, logs []*db.TaskLog) string {
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
	if e.includeLogs && len(logs) > 0 {
		sb.WriteString("## Activity Log\n\n")

		count := len(logs)
		if e.maxLogLines > 0 && count > e.maxLogLines {
			count = e.maxLogLines
		}

		for i := 0; i < count; i++ {
			log := logs[i]
			timestamp := log.CreatedAt.Format("2006-01-02 15:04")
			// Truncate long messages
			msg := log.Content
			if len(msg) > 500 {
				msg = msg[:500] + "..."
			}
			sb.WriteString(fmt.Sprintf("- **%s** [%s]: %s\n", timestamp, log.LineType, msg))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
