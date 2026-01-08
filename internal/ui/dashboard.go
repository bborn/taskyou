package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TaskDelegate is a custom delegate for rendering task items.
type TaskDelegate struct {
	styles TaskDelegateStyles
}

// TaskDelegateStyles defines the styles for the task delegate.
type TaskDelegateStyles struct {
	Normal   lipgloss.Style
	Selected lipgloss.Style
}

// NewTaskDelegate creates a new task delegate.
func NewTaskDelegate() TaskDelegate {
	return TaskDelegate{
		styles: TaskDelegateStyles{
			Normal: lipgloss.NewStyle().
				PaddingLeft(2),
			Selected: lipgloss.NewStyle().
				PaddingLeft(0).
				Border(lipgloss.ThickBorder(), false, false, false, true).
				BorderForeground(ColorPrimary),
		},
	}
}

// Height returns the item height.
func (d TaskDelegate) Height() int {
	return 1
}

// Spacing returns the spacing between items.
func (d TaskDelegate) Spacing() int {
	return 0
}

// Update handles item updates.
func (d TaskDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd {
	return nil
}

// Render renders a list item.
func (d TaskDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	taskItem, ok := item.(TaskItem)
	if !ok {
		return
	}

	t := taskItem.task
	isSelected := index == m.Index()

	// Build the line
	var b strings.Builder

	// Status icon
	switch t.Status {
	case db.StatusPending:
		b.WriteString(lipgloss.NewStyle().Foreground(ColorMuted).Render("·"))
	case db.StatusQueued:
		b.WriteString(lipgloss.NewStyle().Foreground(ColorQueued).Render("○"))
	case db.StatusProcessing:
		b.WriteString(lipgloss.NewStyle().Foreground(ColorProcessing).Render("⋯"))
	case db.StatusReady:
		b.WriteString(lipgloss.NewStyle().Foreground(ColorReady).Render("✓"))
	case db.StatusBlocked:
		b.WriteString(lipgloss.NewStyle().Foreground(ColorBlocked).Render("!"))
	case db.StatusInterrupted:
		b.WriteString(lipgloss.NewStyle().Foreground(ColorWarning).Render("⊘"))
	case db.StatusClosed:
		b.WriteString(lipgloss.NewStyle().Foreground(ColorMuted).Render("×"))
	default:
		b.WriteString(lipgloss.NewStyle().Foreground(ColorMuted).Render("·"))
	}

	// Task ID
	b.WriteString(" ")
	b.WriteString(Dim.Render(fmt.Sprintf("#%-4d", t.ID)))

	// Priority indicator
	if t.Priority == "high" {
		b.WriteString(" ")
		b.WriteString(PriorityHigh.Render())
	} else {
		b.WriteString("  ")
	}

	// Project tag
	if t.Project != "" {
		projectStyle := lipgloss.NewStyle().Foreground(ProjectColor(t.Project))
		shortProject := t.Project
		if len(shortProject) > 2 {
			switch t.Project {
			case "offerlab":
				shortProject = "ol"
			case "influencekit":
				shortProject = "ik"
			}
		}
		b.WriteString(projectStyle.Render(fmt.Sprintf("[%s]", shortProject)))
		b.WriteString(" ")
	}

	// Title (truncate if needed)
	maxTitleLen := m.Width() - 40
	if maxTitleLen < 20 {
		maxTitleLen = 20
	}
	title := t.Title
	if len(title) > maxTitleLen {
		title = title[:maxTitleLen-1] + "…"
	}

	if isSelected {
		b.WriteString(Bold.Render(title))
	} else {
		b.WriteString(title)
	}

	// Type tag
	if t.Type != "" {
		shortType := t.Type
		switch t.Type {
		case db.TypeWriting:
			shortType = "write"
		case db.TypeThinking:
			shortType = "think"
		}
		b.WriteString(" ")
		b.WriteString(TypeTag.Render(shortType))
	}

	// Apply style
	line := b.String()
	if isSelected {
		line = d.styles.Selected.Render(line)
	} else {
		line = d.styles.Normal.Render(line)
	}

	fmt.Fprint(w, line)
}
