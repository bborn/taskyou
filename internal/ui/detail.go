package ui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/charmbracelet/bubbles/textinput"
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

	// Feedback input for sending to claude
	feedbackInput textinput.Model
	feedbackMode  bool
}

// UpdateTask updates the task and refreshes the view.
func (m *DetailModel) UpdateTask(t *db.Task) {
	m.task = t
	if m.ready {
		m.viewport.SetContent(m.renderContent())
	}
}

// Refresh reloads task and logs from database.
func (m *DetailModel) Refresh() {
	if m.task == nil || m.database == nil {
		return
	}

	// Reload task
	task, err := m.database.GetTask(m.task.ID)
	if err == nil && task != nil {
		m.task = task
	}

	// Reload logs
	logs, err := m.database.GetTaskLogs(m.task.ID, 500)
	if err == nil {
		wasAtBottom := m.viewport.AtBottom()
		prevLogCount := len(m.logs)
		m.logs = logs

		if m.ready {
			m.viewport.SetContent(m.renderContent())
			// Auto-scroll to bottom if we were at bottom or new logs arrived
			if wasAtBottom || len(logs) > prevLogCount {
				m.viewport.GotoBottom()
			}
		}
	}
}

// Task returns the current task.
func (m *DetailModel) Task() *db.Task {
	return m.task
}

// NewDetailModel creates a new detail model.
func NewDetailModel(t *db.Task, database *db.DB, width, height int) *DetailModel {
	// Setup feedback input
	fi := textinput.New()
	fi.Placeholder = "Type feedback for claude..."
	fi.CharLimit = 500
	fi.Width = width - 20

	m := &DetailModel{
		task:          t,
		database:      database,
		width:         width,
		height:        height,
		feedbackInput: fi,
	}

	// Load logs
	logs, _ := database.GetTaskLogs(t.ID, 100)
	m.logs = logs

	m.initViewport()
	return m
}

func (m *DetailModel) initViewport() {
	headerHeight := 9
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
		headerHeight := 9
		footerHeight := 3
		m.viewport.Width = width - 4
		m.viewport.Height = height - headerHeight - footerHeight
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoBottom()
	}
}

// Update handles messages.
func (m *DetailModel) Update(msg tea.Msg) (*DetailModel, tea.Cmd) {
	var cmd tea.Cmd

	// Handle feedback mode
	if m.feedbackMode {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.Type {
			case tea.KeyEsc:
				m.feedbackMode = false
				m.feedbackInput.Blur()
				return m, nil
			case tea.KeyEnter:
				feedback := m.feedbackInput.Value()
				if feedback != "" {
					m.sendFeedbackToTmux(feedback)
					m.feedbackInput.SetValue("")
				}
				m.feedbackMode = false
				m.feedbackInput.Blur()
				return m, nil
			}
		}
		m.feedbackInput, cmd = m.feedbackInput.Update(msg)
		return m, cmd
	}

	// Normal mode - handle key messages
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		hasSession := m.hasActiveTmuxSession()

		// 's' to enter feedback/send mode (only when tmux session is active)
		if keyMsg.String() == "s" && hasSession {
			m.feedbackMode = true
			m.feedbackInput.Focus()
			return m, textinput.Blink
		}

		// 'k' to kill the tmux session
		if keyMsg.String() == "k" && hasSession {
			m.killTmuxSession()
			return m, nil
		}

		m.viewport, cmd = m.viewport.Update(keyMsg)
	}

	return m, cmd
}

// InFeedbackMode returns true if the detail view is in feedback input mode.
func (m *DetailModel) InFeedbackMode() bool {
	return m.feedbackMode
}

// hasActiveTmuxSession checks if this task has an active tmux session.
func (m *DetailModel) hasActiveTmuxSession() bool {
	sessionName := executor.TmuxSessionName(m.task.ID)
	err := exec.Command("tmux", "has-session", "-t", sessionName).Run()
	return err == nil
}

// sendFeedbackToTmux sends text to the task's tmux session and submits it.
func (m *DetailModel) sendFeedbackToTmux(feedback string) {
	sessionName := executor.TmuxSessionName(m.task.ID)
	// Log the interaction
	m.database.AppendTaskLog(m.task.ID, "user", fmt.Sprintf("→ %s", feedback))
	// Send the feedback text as literal (-l) to avoid special char interpretation
	exec.Command("tmux", "send-keys", "-t", sessionName, "-l", feedback).Run()
	// Send Enter key to submit
	exec.Command("tmux", "send-keys", "-t", sessionName, "Enter").Run()
	// Refresh to show the new log entry
	m.Refresh()
}

// killTmuxSession kills the Claude tmux session for this task.
func (m *DetailModel) killTmuxSession() {
	sessionName := executor.TmuxSessionName(m.task.ID)
	// Log the interaction
	m.database.AppendTaskLog(m.task.ID, "user", "→ [Kill] Session terminated")
	// Kill the tmux session
	exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	// Refresh to show the new log entry
	m.Refresh()
}

// View renders the detail view.
func (m *DetailModel) View() string {
	if !m.ready {
		return "\n  Loading..."
	}

	header := m.renderHeader()
	content := m.viewport.View()

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Width(m.width-2).
		Padding(0, 1)

	// Show feedback input when in feedback mode
	if m.feedbackMode {
		inputStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorSecondary).
			Padding(0, 1).
			Width(m.width - 4)

		feedbackBar := inputStyle.Render(
			Bold.Render("Send to claude: ") + m.feedbackInput.View(),
		)

		return lipgloss.JoinVertical(lipgloss.Left,
			box.Render(lipgloss.JoinVertical(lipgloss.Left, header, content)),
			feedbackBar,
		)
	}

	help := m.renderHelp()
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
		meta.WriteString("  ")
	}

	// Attachment count
	if count, err := m.database.CountAttachments(t.ID); err == nil && count > 0 {
		attachStyle := lipgloss.NewStyle().
			Padding(0, 1).
			Background(lipgloss.Color("#666666")).
			Foreground(lipgloss.Color("#FFFFFF"))
		meta.WriteString(attachStyle.Render(fmt.Sprintf("📎 %d", count)))
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
			case "user":
				icon = "👤"
			case "output":
				icon = "📤"
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
	}

	// Show tmux-related options if session is active
	hasSession := m.hasActiveTmuxSession()
	if hasSession {
		keys = append(keys, struct {
			key  string
			desc string
		}{"a", "attach"})
		keys = append(keys, struct {
			key  string
			desc string
		}{"s", "send"})
		keys = append(keys, struct {
			key  string
			desc string
		}{"k", "kill"})
	}

	keys = append(keys, []struct {
		key  string
		desc string
	}{
		{"r", "retry"},
		{"c", "close"},
		{"o", "open dir"},
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
