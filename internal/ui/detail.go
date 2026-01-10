package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/github"
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
	prInfo   *github.PRInfo

	// Track joined tmux panes
	claudePaneID    string // The Claude Code pane (middle-left)
	workdirPaneID   string // The workdir shell pane (middle-right)
	daemonSessionID string // The daemon session the Claude pane came from

	// Cached tmux window target (set once on creation, cleared on kill)
	cachedWindowTarget string
}

// UpdateTask updates the task and refreshes the view.
func (m *DetailModel) UpdateTask(t *db.Task) {
	m.task = t
	if m.ready {
		m.viewport.SetContent(m.renderContent())
	}
}

// SetPRInfo sets the PR info for this task.
func (m *DetailModel) SetPRInfo(prInfo *github.PRInfo) {
	m.prInfo = prInfo
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

	// Cache the tmux window target once (expensive operation)
	m.cachedWindowTarget = m.findTaskWindow()

	// If we're in tmux and task has active session, join it as a split pane
	if os.Getenv("TMUX") != "" && m.cachedWindowTarget != "" {
		m.joinTmuxPane()
	} else if os.Getenv("TMUX") != "" {
		// Even without joined panes, ensure Details pane has focus
		m.focusDetailsPane()
	}

	return m
}

func (m *DetailModel) initViewport() {
	headerHeight := 6
	footerHeight := 2

	// If we have joined panes, we have less height (tmux split takes space)
	vpHeight := m.height - headerHeight - footerHeight
	if m.claudePaneID != "" || m.workdirPaneID != "" {
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
	if m.claudePaneID != "" || m.workdirPaneID != "" {
		// Save the current pane height before breaking panes
		currentPaneCmd := exec.Command("tmux", "display-message", "-p", "#{pane_id}")
		if currentPaneOut, err := currentPaneCmd.Output(); err == nil {
			tuiPaneID := strings.TrimSpace(string(currentPaneOut))
			m.saveDetailPaneHeight(tuiPaneID)
		}
		m.breakTmuxPanes()
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

// findTaskWindow searches all tmux sessions for a window matching this task.
// Returns the full window target (session:window) or empty string if not found.
func (m *DetailModel) findTaskWindow() string {
	if m.task == nil {
		return ""
	}
	windowName := executor.TmuxWindowName(m.task.ID)

	// List all windows across all sessions
	out, err := exec.Command("tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_name}").Output()
	if err != nil {
		return ""
	}

	// Search for our window
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && parts[1] == windowName {
			return line // Returns session:window
		}
	}
	return ""
}

// hasActiveTmuxSession checks if this task has an active tmux window in any task-daemon session.
// Uses cached value for performance (set on creation, cleared on kill).
func (m *DetailModel) hasActiveTmuxSession() bool {
	return m.cachedWindowTarget != ""
}

// focusDetailsPane sets focus to the current TUI pane (Details pane).
func (m *DetailModel) focusDetailsPane() {
	// Get current pane ID
	currentPaneCmd := exec.Command("tmux", "display-message", "-p", "#{pane_id}")
	currentPaneOut, err := currentPaneCmd.Output()
	if err != nil {
		return
	}
	tuiPaneID := strings.TrimSpace(string(currentPaneOut))

	// Set the pane title to "Details" and ensure it has focus
	if tuiPaneID != "" {
		exec.Command("tmux", "select-pane", "-t", tuiPaneID, "-T", "Details").Run()
	}
}

// getDetailPaneHeight returns the configured detail pane height percentage.
// Default is 20% for better visibility of task details.
func (m *DetailModel) getDetailPaneHeight() string {
	heightStr, err := m.database.GetSetting(config.SettingDetailPaneHeight)
	if err != nil || heightStr == "" {
		return "20%"
	}
	// Validate the height is a valid percentage (1-50%)
	if strings.HasSuffix(heightStr, "%") {
		percentStr := strings.TrimSuffix(heightStr, "%")
		if percent, err := strconv.Atoi(percentStr); err == nil && percent >= 1 && percent <= 50 {
			return heightStr
		}
	}
	return "20%"
}

// saveDetailPaneHeight saves the current detail pane height to settings.
func (m *DetailModel) saveDetailPaneHeight(tuiPaneID string) {
	// Get the current height of the TUI pane
	cmd := exec.Command("tmux", "display-message", "-p", "-t", tuiPaneID, "#{pane_height}")
	heightOut, err := cmd.Output()
	if err != nil {
		return
	}

	paneHeight, err := strconv.Atoi(strings.TrimSpace(string(heightOut)))
	if err != nil || paneHeight <= 0 {
		return
	}

	// Get the total window height
	cmd = exec.Command("tmux", "display-message", "-p", "#{window_height}")
	totalHeightOut, err := cmd.Output()
	if err != nil {
		return
	}

	totalHeight, err := strconv.Atoi(strings.TrimSpace(string(totalHeightOut)))
	if err != nil || totalHeight <= 0 {
		return
	}

	// Calculate the percentage
	percentage := (paneHeight * 100) / totalHeight
	if percentage >= 1 && percentage <= 50 {
		heightStr := fmt.Sprintf("%d%%", percentage)
		m.database.SetSetting(config.SettingDetailPaneHeight, heightStr)
	}
}

// joinTmuxPanes joins the task's Claude pane and creates a workdir shell pane.
// Layout:
//   - Top (configurable, default 20%): Task details (TUI)
//   - Bottom: Claude Code (left) + Workdir shell (right) side-by-side
func (m *DetailModel) joinTmuxPanes() {
	// Use cached window target to avoid expensive tmux lookup
	windowTarget := m.cachedWindowTarget
	if windowTarget == "" {
		return
	}

	// Extract the daemon session name from the window target (session:window)
	parts := strings.SplitN(windowTarget, ":", 2)
	if len(parts) >= 1 {
		m.daemonSessionID = parts[0]
	}

	// Get current pane ID before joining (so we can select it after)
	currentPaneCmd := exec.Command("tmux", "display-message", "-p", "#{pane_id}")
	currentPaneOut, _ := currentPaneCmd.Output()
	tuiPaneID := strings.TrimSpace(string(currentPaneOut))

	// Step 1: Join the Claude pane below the TUI pane (vertical split)
	err := exec.Command("tmux", "join-pane",
		"-v",
		"-s", windowTarget+".0").Run()
	if err != nil {
		return
	}

	// Get the Claude pane ID (it's now the active pane after join)
	claudePaneCmd := exec.Command("tmux", "display-message", "-p", "#{pane_id}")
	claudePaneOut, _ := claudePaneCmd.Output()
	m.claudePaneID = strings.TrimSpace(string(claudePaneOut))

	// Set Claude pane title
	exec.Command("tmux", "select-pane", "-t", m.claudePaneID, "-T", "Claude").Run()

	// Step 2: Create a new pane to the right of Claude for the workdir
	// -h: horizontal split (right side)
	// -l 50%: workdir takes 50% of the bottom area
	workdir := m.getWorkdir()
	err = exec.Command("tmux", "split-window",
		"-h", "-l", "50%",
		"-t", m.claudePaneID,
		"-c", workdir).Run()
	if err != nil {
		m.workdirPaneID = ""
	} else {
		// Get the workdir pane ID (it's now active after split)
		workdirPaneCmd := exec.Command("tmux", "display-message", "-p", "#{pane_id}")
		workdirPaneOut, _ := workdirPaneCmd.Output()
		m.workdirPaneID = strings.TrimSpace(string(workdirPaneOut))

		// Set Shell pane title
		exec.Command("tmux", "select-pane", "-t", m.workdirPaneID, "-T", "Shell").Run()
	}

	// Select back to the TUI pane, set its title, and ensure it has focus
	if tuiPaneID != "" {
		exec.Command("tmux", "select-pane", "-t", tuiPaneID, "-T", "Details").Run()
		// Ensure the TUI pane has focus for keyboard interaction
		exec.Command("tmux", "select-pane", "-t", tuiPaneID).Run()
	}

	// Update status bar with navigation hints
	exec.Command("tmux", "set-option", "-t", "task-ui", "status", "on").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "status-style", "bg=#3b82f6,fg=white").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "status-left", " TASK UI ").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "status-right", " Ctrl+B ↑↓←→ switch panes │ drag borders to resize ").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "status-right-length", "60").Run()

	// Style pane borders - active pane gets theme color outline
	exec.Command("tmux", "set-option", "-t", "task-ui", "pane-border-style", "fg=#374151").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "pane-active-border-style", "fg=#61AFEF").Run()

	// Resize TUI pane to configured height (default 18%)
	detailHeight := m.getDetailPaneHeight()
	exec.Command("tmux", "resize-pane", "-t", tuiPaneID, "-y", detailHeight).Run()
}

// joinTmuxPane is a compatibility wrapper for joinTmuxPanes.
func (m *DetailModel) joinTmuxPane() {
	m.joinTmuxPanes()
}

// getWorkdir returns the working directory for the task.
func (m *DetailModel) getWorkdir() string {
	if m.task.WorktreePath != "" {
		return m.task.WorktreePath
	}
	// Fallback to home directory
	home, _ := os.UserHomeDir()
	return home
}

// breakTmuxPanes breaks both joined panes - kills workdir, returns Claude to task-daemon.
func (m *DetailModel) breakTmuxPanes() {
	// Save the current detail pane height before breaking
	currentPaneCmd := exec.Command("tmux", "display-message", "-p", "#{pane_id}")
	if currentPaneOut, err := currentPaneCmd.Output(); err == nil {
		tuiPaneID := strings.TrimSpace(string(currentPaneOut))
		m.saveDetailPaneHeight(tuiPaneID)
	}

	// Reset status bar and pane styling
	exec.Command("tmux", "set-option", "-t", "task-ui", "status-right", " ").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "pane-border-style", "fg=#374151").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "pane-active-border-style", "fg=#61AFEF").Run()

	// Reset pane title back to main view label
	exec.Command("tmux", "select-pane", "-t", "task-ui:.0", "-T", "Tasks").Run()

	// Kill the workdir pane first (it's not from task-daemon, just a shell we created)
	if m.workdirPaneID != "" {
		exec.Command("tmux", "kill-pane", "-t", m.workdirPaneID).Run()
		m.workdirPaneID = ""
	}

	// Break the Claude pane back to task-daemon
	if m.claudePaneID == "" {
		return
	}

	windowName := executor.TmuxWindowName(m.task.ID)

	// Determine the daemon session to break back to
	daemonSession := m.daemonSessionID
	if daemonSession == "" {
		// Fallback to the constant if we don't have a stored session
		daemonSession = executor.TmuxDaemonSession
	}

	// Break the pane back to task-daemon as a new window with the task name
	// -d: don't switch to the new window
	// -s: source pane (the one we joined)
	// -t: target session
	// -n: name for the new window
	exec.Command("tmux", "break-pane",
		"-d",
		"-s", m.claudePaneID,
		"-t", daemonSession+":",
		"-n", windowName).Run()

	m.claudePaneID = ""
	m.daemonSessionID = ""
}

// breakTmuxPane is a compatibility wrapper for breakTmuxPanes.
func (m *DetailModel) breakTmuxPane() {
	m.breakTmuxPanes()
}

// killTmuxSession kills the Claude tmux window and workdir pane.
func (m *DetailModel) killTmuxSession() {
	windowTarget := m.cachedWindowTarget
	if windowTarget == "" {
		return
	}
	m.database.AppendTaskLog(m.task.ID, "user", "→ [Kill] Session terminated")

	// Reset pane styling first
	exec.Command("tmux", "set-option", "-t", "task-ui", "status-right", " ").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "pane-border-style", "fg=#374151").Run()
	exec.Command("tmux", "set-option", "-t", "task-ui", "pane-active-border-style", "fg=#61AFEF").Run()

	// Reset pane title back to main view label
	exec.Command("tmux", "select-pane", "-t", "task-ui:.0", "-T", "Tasks").Run()

	// Kill the workdir pane first (it's a separate pane we created)
	if m.workdirPaneID != "" {
		exec.Command("tmux", "kill-pane", "-t", m.workdirPaneID).Run()
		m.workdirPaneID = ""
	}

	// If we have a joined Claude pane, it will be killed with the window
	m.claudePaneID = ""

	exec.Command("tmux", "kill-window", "-t", windowTarget).Run()

	// Clear cached window target since session is now killed
	m.cachedWindowTarget = ""

	m.Refresh()
}

// toggleTmuxPanes toggles the Claude and workdir pane visibility.
func (m *DetailModel) toggleTmuxPanes() {
	if m.claudePaneID != "" || m.workdirPaneID != "" {
		// Panes are open, close them
		m.breakTmuxPanes()
	} else {
		// Panes are closed, open them
		m.joinTmuxPanes()
	}
}

// toggleTmuxPane is a compatibility wrapper for toggleTmuxPanes.
func (m *DetailModel) toggleTmuxPane() {
	m.toggleTmuxPanes()
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

	// PR status
	if m.prInfo != nil {
		meta.WriteString("  ")
		meta.WriteString(PRStatusBadge(m.prInfo))
		meta.WriteString(" ")
		prDesc := lipgloss.NewStyle().
			Foreground(ColorMuted).
			Render(m.prInfo.StatusDescription())
		meta.WriteString(prDesc)
	}

	// Tmux hint if session is active
	if m.claudePaneID != "" {
		meta.WriteString("  ")
		tmuxHint := lipgloss.NewStyle().
			Foreground(ColorSecondary).
			Render("(Ctrl+B ↓ to interact with Claude)")
		meta.WriteString(tmuxHint)
	}

	// PR link if available
	var prLine string
	if m.prInfo != nil && m.prInfo.URL != "" {
		prLine = Dim.Render(fmt.Sprintf("PR #%d: %s", m.prInfo.Number, m.prInfo.URL))
	}

	lines := []string{title, subtitle, meta.String()}
	if prLine != "" {
		lines = append(lines, prLine)
	}
	lines = append(lines, "")

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
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
		toggleDesc := "show panes"
		if m.claudePaneID != "" || m.workdirPaneID != "" {
			toggleDesc = "hide panes"
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
		{"e", "edit"},
		{"r", "retry"},
		{"c", "close"},
		{"d", "delete"},
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
