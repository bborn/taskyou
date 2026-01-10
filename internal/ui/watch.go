package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// WatchModel represents the task watch view.
type WatchModel struct {
	database  *db.DB
	executor  *executor.Executor
	taskID    int64
	task      *db.Task
	logs      []*db.TaskLog
	lastLogID int64
	viewport  viewport.Model
	spinner   spinner.Model
	width     int
	height    int
	ready     bool
	logCh     chan *db.TaskLog
}

// NewWatchModel creates a new watch model.
func NewWatchModel(database *db.DB, exec *executor.Executor, taskID int64, width, height int) *WatchModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(ColorPrimary)

	m := &WatchModel{
		database: database,
		executor: exec,
		taskID:   taskID,
		spinner:  s,
		width:    width,
		height:   height,
		logs:     make([]*db.TaskLog, 0),
	}
	m.initViewport()
	return m
}

func (m *WatchModel) initViewport() {
	headerHeight := 4
	footerHeight := 3
	m.viewport = viewport.New(m.width-4, m.height-headerHeight-footerHeight)
	m.viewport.YPosition = headerHeight
	m.ready = true
}

// SetSize updates the viewport size.
func (m *WatchModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	if m.ready {
		headerHeight := 4
		footerHeight := 3
		m.viewport.Width = width - 4
		m.viewport.Height = height - headerHeight - footerHeight
	}
}

// Init starts watching.
func (m *WatchModel) Init() tea.Cmd {
	// Load task
	task, _ := m.database.GetTask(m.taskID)
	m.task = task

	// Load existing logs
	logs, _ := m.database.GetTaskLogs(m.taskID, 100)
	m.logs = logs
	if len(logs) > 0 {
		m.lastLogID = logs[len(logs)-1].ID
	}
	m.updateViewport()

	// Subscribe to live updates
	m.logCh = m.executor.Subscribe(m.taskID)

	return tea.Batch(
		m.spinner.Tick,
		m.waitForLog(),
		m.pollLogs(),
	)
}

// Cleanup unsubscribes from log updates.
func (m *WatchModel) Cleanup() {
	if m.logCh != nil {
		m.executor.Unsubscribe(m.taskID, m.logCh)
		m.logCh = nil
	}
}

// Update handles messages.
func (m *WatchModel) Update(msg tea.Msg) (*WatchModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	case watchLogMsg:
		m.logs = append(m.logs, msg.log)
		m.lastLogID = msg.log.ID
		m.updateViewport()
		cmds = append(cmds, m.waitForLog())

	case watchPollMsg:
		// Check for new logs from database
		newLogs, _ := m.database.GetTaskLogsSince(m.taskID, m.lastLogID)
		for _, log := range newLogs {
			m.logs = append(m.logs, log)
			m.lastLogID = log.ID
		}
		if len(newLogs) > 0 {
			m.updateViewport()
		}

		// Refresh task status
		task, _ := m.database.GetTask(m.taskID)
		m.task = task

		cmds = append(cmds, m.pollLogs())
	}

	return m, tea.Batch(cmds...)
}

// View renders the watch view.
func (m *WatchModel) View() string {
	var header string
	if m.task != nil {
		status := m.task.Status
		if db.IsInProgress(status) {
			header = m.spinner.View() + " Processing: " + Bold.Render(m.task.Title)
		} else if status == db.StatusDone {
			header = Success.Render("✓ Completed: ") + Bold.Render(m.task.Title)
		} else if status == db.StatusBlocked {
			header = Error.Render("! Blocked: ") + Bold.Render(m.task.Title)
		} else {
			header = Dim.Render(fmt.Sprintf("Task #%d: ", m.taskID)) + Bold.Render(m.task.Title)
		}
	} else {
		header = m.spinner.View() + fmt.Sprintf(" Watching task #%d...", m.taskID)
	}
	header = lipgloss.NewStyle().Bold(true).Padding(1, 1).Render(header)

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

func (m *WatchModel) renderHelp() string {
	keys := []struct {
		key  string
		desc string
	}{
		{"↑/↓", "scroll"},
		{"q/esc", "back"},
	}

	var help string
	for i, k := range keys {
		if i > 0 {
			help += "  "
		}
		help += HelpKey.Render(k.key) + " " + HelpDesc.Render(k.desc)
	}

	return HelpBar.Render(help)
}

func (m *WatchModel) updateViewport() {
	// Check if user is at bottom before updating content
	wasAtBottom := m.viewport.AtBottom()

	var b strings.Builder
	for _, log := range m.logs {
		icon := "  "
		style := lipgloss.NewStyle()

		switch log.LineType {
		case "system":
			icon = "🔵"
			style = style.Foreground(ColorSecondary)
		case "text":
			icon = "💬"
		case "tool":
			icon = "🔧"
			style = style.Foreground(ColorPrimary)
		case "error":
			icon = "❌"
			style = style.Foreground(ColorError)
		case "output":
			icon = "  "
			style = Dim
		}

		timeStr := log.CreatedAt.Format("15:04:05")
		line := fmt.Sprintf("%s %s %s",
			Dim.Render(timeStr),
			icon,
			style.Render(log.Content),
		)
		b.WriteString(line)
		b.WriteString("\n")
	}
	m.viewport.SetContent(b.String())

	// Only auto-scroll if user was already at the bottom
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

// Messages
type watchLogMsg struct {
	log *db.TaskLog
}

type watchPollMsg struct{}

func (m *WatchModel) waitForLog() tea.Cmd {
	return func() tea.Msg {
		if m.logCh == nil {
			return nil
		}
		log, ok := <-m.logCh
		if !ok {
			return nil
		}
		return watchLogMsg{log: log}
	}
}

func (m *WatchModel) pollLogs() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return watchPollMsg{}
	})
}
