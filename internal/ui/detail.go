package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
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

	// Track if we've joined the tmux pane
	joinedPaneID string
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

	// If we're in tmux and task has active session, join it as a split pane
	if os.Getenv("TMUX") != "" && m.hasActiveTmuxSession() {
		m.joinTmuxPane()
	}

	return m
}

func (m *DetailModel) initViewport() {
	headerHeight := 6
	footerHeight := 2

	// If we have a joined pane, we have less height (tmux split takes space)
	vpHeight := m.height - headerHeight - footerHeight
	if m.joinedPaneID != "" {
		// The tmux split takes roughly half, but we don't control that here
		// Just use full height - the TUI pane will be resized by tmux
	}

	m.viewport = viewport.New(m.width-4, vpHeight)
	m.viewport.SetContent(m.renderContent())
	m.viewport.GotoBottom()
	m.ready = true
}

// SetSize updates the viewport size.
func (m *DetailModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	if m.ready {
		headerHeight := 6
		footerHeight := 2
		m.viewport.Width = width - 4
		m.viewport.Height = height - headerHeight - footerHeight
		m.viewport.SetContent(m.renderContent())
	}
}

// Update handles messages.
func (m *DetailModel) Update(msg tea.Msg) (*DetailModel, tea.Cmd) {
	var cmd tea.Cmd

	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		hasSession := m.hasActiveTmuxSession()

		// 'k' to kill the tmux session
		if keyMsg.String() == "k" && hasSession {
			m.killTmuxSession()
			return m, nil
		}

		// 't' to toggle the Claude pane
		if keyMsg.String() == "t" && hasSession && os.Getenv("TMUX") != "" {
			m.toggleTmuxPane()
			return m, nil
		}

		m.viewport, cmd = m.viewport.Update(keyMsg)
	}

	return m, cmd
}

// Cleanup should be called when leaving detail view.
func (m *DetailModel) Cleanup() {
	if m.joinedPaneID != "" {
		m.breakTmuxPane()
	}
}

// InFeedbackMode returns false - use tmux pane for interaction.
func (m *DetailModel) InFeedbackMode() bool {
	return false
}

// StartTmuxTicker is no longer needed - real tmux pane handles display.
func (m *DetailModel) StartTmuxTicker() tea.Cmd {
	return nil
}

// hasActiveTmuxSession checks if this task has an active tmux window in task-daemon.
func (m *DetailModel) hasActiveTmuxSession() bool {
	if m.task == nil {
		return false
	}
	windowTarget := executor.TmuxSessionName(m.task.ID)
	// Check if the window exists by trying to get info about it
	err := exec.Command("tmux", "list-panes", "-t", windowTarget).Run()
	return err == nil
}

// joinTmuxPane joins the task's tmux pane into the current window as a split.
func (m *DetailModel) joinTmuxPane() {
	windowTarget := executor.TmuxSessionName(m.task.ID)

	// Get current pane ID before joining (so we can select it after)
	currentPaneCmd := exec.Command("tmux", "display-message", "-p", "#{pane_id}")
	currentPaneOut, _ := currentPaneCmd.Output()
	currentPaneID := strings.TrimSpace(string(currentPaneOut))

	// Join the task window's pane into our window as a vertical split at bottom
	// -v: vertical split (pane below)
	// -l 25%: Claude pane takes only 25% of height (task details get more space)
	// -s: source pane (from task-daemon window)
	err := exec.Command("tmux", "join-pane",
		"-v", "-l", "25%",
		"-s", windowTarget+".0").Run()
	if err != nil {
		return
	}

	// Get the new pane ID (it's now the active pane after join)
	newPaneCmd := exec.Command("tmux", "display-message", "-p", "#{pane_id}")
	newPaneOut, _ := newPaneCmd.Output()
	m.joinedPaneID = strings.TrimSpace(string(newPaneOut))

	// Select back to the TUI pane (join-pane switches focus to the new pane)
	if currentPaneID != "" {
		exec.Command("tmux", "select-pane", "-t", currentPaneID).Run()
	}

	// Update status bar with navigation hints
	exec.Command("tmux", "set-option", "-t", "task-ui", "status", "on").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "status-style", "bg=#3b82f6,fg=white").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "status-left", " TASK UI ").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "status-right", " Ctrl+B ↑↓ switch panes │ Ctrl+B D detach ").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "status-right-length", "50").Run()

	// Style pane borders - active pane gets theme color outline
	exec.Command("tmux", "set-option", "-t", "task-ui", "pane-border-style", "fg=#374151").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "pane-active-border-style", "fg=#61AFEF").Run()
}

// breakTmuxPane breaks the joined pane back to task-daemon.
func (m *DetailModel) breakTmuxPane() {
	// Reset status bar and pane styling
	exec.Command("tmux", "set-option", "-t", "task-ui", "status-right", "").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "pane-border-style", "default").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "pane-active-border-style", "default").Run()

	if m.joinedPaneID == "" {
		return
	}

	windowName := executor.TmuxWindowName(m.task.ID)

	// Break the pane back to task-daemon as a new window with the task name
	// -d: don't switch to the new window
	// -s: source pane (the one we joined)
	// -t: target session
	// -n: name for the new window
	exec.Command("tmux", "break-pane",
		"-d",
		"-s", m.joinedPaneID,
		"-t", executor.TmuxDaemonSession+":",
		"-n", windowName).Run()

	m.joinedPaneID = ""
}

// killTmuxSession kills the Claude tmux window.
func (m *DetailModel) killTmuxSession() {
	windowTarget := executor.TmuxSessionName(m.task.ID)
	m.database.AppendTaskLog(m.task.ID, "user", "→ [Kill] Session terminated")

	// Reset pane styling first
	exec.Command("tmux", "set-option", "-t", "task-ui", "status-right", "").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "pane-border-style", "default").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "pane-active-border-style", "default").Run()

	// If we have a joined pane, it will be killed with the window
	m.joinedPaneID = ""

	exec.Command("tmux", "kill-window", "-t", windowTarget).Run()
	m.Refresh()
}

// toggleTmuxPane toggles the Claude pane visibility.
func (m *DetailModel) toggleTmuxPane() {
	if m.joinedPaneID != "" {
		// Pane is open, close it
		m.breakTmuxPane()
	} else {
		// Pane is closed, open it
		m.joinTmuxPane()
	}
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
		Width(m.width - 2).
		Padding(0, 1)

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
	}

	// Tmux hint if session is active
	if m.joinedPaneID != "" {
		meta.WriteString("  ")
		tmuxHint := lipgloss.NewStyle().
			Foreground(ColorSecondary).
			Render("(Ctrl+B ↓ to interact with Claude)")
		meta.WriteString(tmuxHint)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		subtitle,
		meta.String(),
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

	hasSession := m.hasActiveTmuxSession()
	if hasSession && os.Getenv("TMUX") != "" {
		toggleDesc := "show pane"
		if m.joinedPaneID != "" {
			toggleDesc = "hide pane"
		}
		keys = append(keys, struct {
			key  string
			desc string
		}{"t", toggleDesc})
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
		{"d", "delete"},
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
