package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// DetailModel represents the task detail view.
type DetailModel struct {
	task     *db.Task
	logs     []*db.TaskLog
	database *db.DB
	viewport viewport.Model
	width    int
	height   int
	ready    bool
}

// UpdateTask updates the task and refreshes the view.
func (m *DetailModel) UpdateTask(t *db.Task) {
	m.task = t
	if m.ready {
		m.viewport.SetContent(m.renderContent())
	}
}

// Task returns the current task.
func (m *DetailModel) Task() *db.Task {
	return m.task
}

// NewDetailModel creates a new detail model.
func NewDetailModel(t *db.Task, database *db.DB, width, height int) *DetailModel {
	m := &DetailModel{
		task:     t,
		database: database,
		width:    width,
		height:   height,
	}

	// Load logs
	logs, _ := database.GetTaskLogs(t.ID, 100)
	m.logs = logs

	m.initViewport()
	return m
}

func (m *DetailModel) initViewport() {
	headerHeight := 8
	footerHeight := 3

	m.viewport = viewport.New(m.width-4, m.height-headerHeight-footerHeight)
	m.viewport.YPosition = headerHeight
	m.viewport.SetContent(m.renderContent())
	m.viewport.GotoBottom()
	m.ready = true
}

// SetSize updates the viewport size.
func (m *DetailModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	if m.ready {
		headerHeight := 8
		footerHeight := 3
		m.viewport.Width = width - 4
		m.viewport.Height = height - headerHeight - footerHeight
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoBottom()
	}
}

// Update handles messages.
func (m *DetailModel) Update(msg tea.KeyMsg) (*DetailModel, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// View renders the detail view.
func (m *DetailModel) View() string {
	if !m.ready {
		return "\n  Loading..."
	}

	header := m.renderHeader()
	content := m.viewport.View()
	help := m.renderHelp()

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Width(m.width-2).
		Padding(0, 1)

	return lipgloss.JoinVertical(lipgloss.Left,
		box.Render(lipgloss.JoinVertical(lipgloss.Left, header, content)),
		help,
	)
}

func (m *DetailModel) renderHeader() string {
	t := m.task

	title := Title.Render(fmt.Sprintf("Task #%d", t.ID))
	subtitle := Bold.Render(t.Title)

	var meta strings.Builder

	// Status badge
	statusStyle := lipgloss.NewStyle().
		Padding(0, 1).
		Background(StatusColor(t.Status)).
		Foreground(lipgloss.Color("#FFFFFF"))
	meta.WriteString(statusStyle.Render(t.Status))
	meta.WriteString("  ")

	// Project
	if t.Project != "" {
		projectStyle := lipgloss.NewStyle().
			Padding(0, 1).
			Background(ProjectColor(t.Project)).
			Foreground(lipgloss.Color("#FFFFFF"))
		meta.WriteString(projectStyle.Render(t.Project))
		meta.WriteString("  ")
	}

	// Type
	if t.Type != "" {
		typeStyle := lipgloss.NewStyle().
			Padding(0, 1).
			Background(ColorCode).
			Foreground(lipgloss.Color("#FFFFFF"))
		meta.WriteString(typeStyle.Render(t.Type))
		meta.WriteString("  ")
	}

	// Priority
	if t.Priority == "high" {
		priorityStyle := lipgloss.NewStyle().
			Padding(0, 1).
			Background(ColorError).
			Foreground(lipgloss.Color("#FFFFFF"))
		meta.WriteString(priorityStyle.Render("high priority"))
	}

	timeStr := Dim.Render(fmt.Sprintf("Created %s", humanizeTime(t.CreatedAt.Time)))

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		subtitle,
		"",
		meta.String(),
		timeStr,
		"",
	)
}

func (m *DetailModel) renderContent() string {
	t := m.task
	var b strings.Builder

	// Description
	if t.Body != "" && strings.TrimSpace(t.Body) != "" {
		b.WriteString(Bold.Render("Description"))
		b.WriteString("\n\n")

		rendered, err := glamour.Render(t.Body, "dark")
		if err != nil {
			b.WriteString(t.Body)
		} else {
			b.WriteString(strings.TrimSpace(rendered))
		}
		b.WriteString("\n")
	}

	// Execution logs
	if len(m.logs) > 0 {
		b.WriteString("\n")
		b.WriteString(Bold.Render("Execution Log"))
		b.WriteString("\n\n")

		for _, log := range m.logs {
			icon := "  "
			switch log.LineType {
			case "system":
				icon = "🔵"
			case "text":
				icon = "💬"
			case "tool":
				icon = "🔧"
			case "error":
				icon = "❌"
			case "question":
				icon = "❓"
			}

			line := fmt.Sprintf("%s %s %s",
				Dim.Render(log.CreatedAt.Format("15:04:05")),
				icon,
				log.Content,
			)
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (m *DetailModel) renderHelp() string {
	keys := []struct {
		key  string
		desc string
	}{
		{"↑/↓", "scroll"},
		{"x", "execute"},
		{"a", "attach"},
	}

	// Only show interrupt if task is executing
	if db.IsInProgress(m.task.Status) {
		keys = append(keys, struct {
			key  string
			desc string
		}{"i", "interrupt"})
	}

	keys = append(keys, []struct {
		key  string
		desc string
	}{
		{"r", "retry"},
		{"c", "close"},
		{"q/esc", "back"},
	}...)

	var help string
	for i, k := range keys {
		if i > 0 {
			help += "  "
		}
		help += HelpKey.Render(k.key) + " " + HelpDesc.Render(k.desc)
	}

	return HelpBar.Render(help)
}

func humanizeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	now := time.Now()
	diff := now.Sub(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case diff < 7*24*time.Hour:
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "yesterday"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}
