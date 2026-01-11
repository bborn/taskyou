package ui

import (
	"context"
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
	tuiPaneID       string // The TUI/Details pane (top)

	// Cached tmux window target (set once on creation, cleared on kill)
	cachedWindowTarget string

	// Cached Claude process memory (updated on Refresh)
	claudeMemoryMB int
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

	// Update Claude memory usage
	m.claudeMemoryMB = m.getClaudeMemoryMB()

	// Ensure tmux panes are joined if available (handles external close/detach)
	m.ensureTmuxPanesJoined()
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

	// Fetch initial memory usage
	m.claudeMemoryMB = m.getClaudeMemoryMB()

	// If we're in tmux and task has active session, join it as a split pane
	if os.Getenv("TMUX") != "" && m.cachedWindowTarget != "" {
		m.joinTmuxPane()
	} else if os.Getenv("TMUX") != "" {
		// No active tmux window - check if we can resume a previous session
		if m.task.WorktreePath != "" {
			sessionID := executor.FindClaudeSessionID(m.task.WorktreePath)
			if sessionID != "" {
				// Start a new tmux window resuming the previous session
				m.startResumableSession(sessionID)
				// Refresh the cached window target
				m.cachedWindowTarget = m.findTaskWindow()
				if m.cachedWindowTarget != "" {
					m.joinTmuxPane()
				}
			}
		}
		// Even without joined panes, ensure Details pane has focus
		if m.claudePaneID == "" {
			m.focusDetailsPane()
		}
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
		hasPanes := m.claudePaneID != "" || m.workdirPaneID != ""

		// 'k' is now handled by app.go with confirmation dialog

		// Tab to cycle to next pane (Details -> Claude -> Shell -> Details)
		if keyMsg.String() == "tab" && hasPanes && os.Getenv("TMUX") != "" {
			m.focusNextPane()
			return m, nil
		}

		// Shift+Tab to cycle to previous pane (Details -> Shell -> Claude -> Details)
		if keyMsg.String() == "shift+tab" && hasPanes && os.Getenv("TMUX") != "" {
			m.focusPrevPane()
			return m, nil
		}

		m.viewport, cmd = m.viewport.Update(keyMsg)
	}

	return m, cmd
}

// Cleanup should be called when leaving detail view.
func (m *DetailModel) Cleanup() {
	if m.claudePaneID != "" || m.workdirPaneID != "" {
		// breakTmuxPanes saves pane positions before breaking
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

	// List all windows across all sessions (with timeout to prevent blocking UI)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_name}").Output()
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

// startResumableSession starts a new tmux window with claude --resume for a previous session.
// This reconnects to a Claude session that was previously running but whose tmux window was killed.
func (m *DetailModel) startResumableSession(sessionID string) {
	if m.task == nil || m.task.WorktreePath == "" || sessionID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Find or create a task-daemon session to put the window in
	// First, look for any existing task-daemon-* session
	out, err := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return
	}

	var daemonSession string
	for _, session := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(session, "task-daemon-") {
			daemonSession = session
			break
		}
	}

	// If no daemon session exists, create one
	if daemonSession == "" {
		daemonSession = fmt.Sprintf("task-daemon-%d", os.Getpid())
		err := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", daemonSession, "-n", "_placeholder").Run()
		if err != nil {
			return
		}
	}

	windowName := executor.TmuxWindowName(m.task.ID)
	workDir := m.task.WorktreePath

	// Build the claude command with --resume
	// Check for dangerous mode
	dangerousFlag := ""
	if os.Getenv("TASK_DANGEROUS_MODE") == "1" {
		dangerousFlag = "--dangerously-skip-permissions "
	}

	// Get the session ID for environment
	taskSessionID := strings.TrimPrefix(daemonSession, "task-daemon-")

	script := fmt.Sprintf(`TASK_ID=%d TASK_SESSION_ID=%s claude %s--chrome --resume %s`,
		m.task.ID, taskSessionID, dangerousFlag, sessionID)

	// Log the reconnection
	m.database.AppendTaskLog(m.task.ID, "system", fmt.Sprintf("Reconnecting to session %s", sessionID))

	// Create new window in the daemon session
	err = exec.CommandContext(ctx, "tmux", "new-window", "-d",
		"-t", daemonSession,
		"-n", windowName,
		"-c", workDir,
		"sh", "-c", script).Run()
	if err != nil {
		return
	}

	// Give tmux a moment to create the window
	time.Sleep(100 * time.Millisecond)
}

// hasActiveTmuxSession checks if this task has an active tmux window in any task-daemon session.
// Uses cached value for performance (set on creation, cleared on kill).
func (m *DetailModel) hasActiveTmuxSession() bool {
	return m.cachedWindowTarget != ""
}

// refreshTmuxWindowTarget re-checks for available tmux sessions.
// This is useful when the user wants to open tmux panes that were created
// after the detail view was opened, or if panes were closed externally.
func (m *DetailModel) refreshTmuxWindowTarget() bool {
	m.cachedWindowTarget = m.findTaskWindow()
	return m.cachedWindowTarget != ""
}

// ensureTmuxPanesJoined checks if tmux panes should be joined and joins them if needed.
// This handles cases where panes were externally closed or a session was created after opening the view.
func (m *DetailModel) ensureTmuxPanesJoined() {
	if os.Getenv("TMUX") == "" {
		return
	}

	// Check if we think we have panes joined but they no longer exist
	if m.claudePaneID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		// Verify the pane still exists
		err := exec.CommandContext(ctx, "tmux", "display-message", "-t", m.claudePaneID, "-p", "#{pane_id}").Run()
		if err != nil {
			// Pane no longer exists, clear our state
			m.claudePaneID = ""
			m.workdirPaneID = ""
		}
	}

	// If panes are already joined and valid, nothing to do
	if m.claudePaneID != "" {
		return
	}

	// Refresh the cache to check for available sessions
	m.refreshTmuxWindowTarget()

	// If we have a session available, join it
	if m.hasActiveTmuxSession() {
		m.joinTmuxPanes()
	}
}

// focusDetailsPane sets focus to the current TUI pane (Details pane).
func (m *DetailModel) focusDetailsPane() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Get current pane ID
	currentPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	currentPaneOut, err := currentPaneCmd.Output()
	if err != nil {
		return
	}
	tuiPaneID := strings.TrimSpace(string(currentPaneOut))

	// Set the pane title to "Details" and ensure it has focus
	if tuiPaneID != "" {
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", tuiPaneID, "-T", "Details").Run()
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

// getShellPaneWidth returns the configured shell pane width percentage.
// Default is 50% for equal split between Claude and Shell panes.
func (m *DetailModel) getShellPaneWidth() string {
	widthStr, err := m.database.GetSetting(config.SettingShellPaneWidth)
	if err != nil || widthStr == "" {
		return "50%"
	}
	// Validate the width is a valid percentage (10-90%)
	if strings.HasSuffix(widthStr, "%") {
		percentStr := strings.TrimSuffix(widthStr, "%")
		if percent, err := strconv.Atoi(percentStr); err == nil && percent >= 10 && percent <= 90 {
			return widthStr
		}
	}
	return "50%"
}

// saveDetailPaneHeight saves the current detail pane height to settings.
func (m *DetailModel) saveDetailPaneHeight(tuiPaneID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Get the current height of the TUI pane
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", tuiPaneID, "#{pane_height}")
	heightOut, err := cmd.Output()
	if err != nil {
		return
	}

	paneHeight, err := strconv.Atoi(strings.TrimSpace(string(heightOut)))
	if err != nil || paneHeight <= 0 {
		return
	}

	// Get the total window height
	cmd = exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{window_height}")
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

// saveShellPaneWidth saves the current shell pane width to settings.
func (m *DetailModel) saveShellPaneWidth() {
	if m.workdirPaneID == "" || m.claudePaneID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Get the width of the shell pane
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", m.workdirPaneID, "#{pane_width}")
	shellWidthOut, err := cmd.Output()
	if err != nil {
		return
	}

	shellWidth, err := strconv.Atoi(strings.TrimSpace(string(shellWidthOut)))
	if err != nil || shellWidth <= 0 {
		return
	}

	// Get the width of the claude pane
	cmd = exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", m.claudePaneID, "#{pane_width}")
	claudeWidthOut, err := cmd.Output()
	if err != nil {
		return
	}

	claudeWidth, err := strconv.Atoi(strings.TrimSpace(string(claudeWidthOut)))
	if err != nil || claudeWidth <= 0 {
		return
	}

	// Calculate total width and shell percentage
	totalWidth := shellWidth + claudeWidth
	percentage := (shellWidth * 100) / totalWidth
	if percentage >= 10 && percentage <= 90 {
		widthStr := fmt.Sprintf("%d%%", percentage)
		m.database.SetSetting(config.SettingShellPaneWidth, widthStr)
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

	// Use timeout for all tmux operations to prevent blocking UI
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Extract the daemon session name from the window target (session:window)
	parts := strings.SplitN(windowTarget, ":", 2)
	if len(parts) >= 1 {
		m.daemonSessionID = parts[0]
	}

	// Get current pane ID before joining (so we can select it after)
	currentPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	currentPaneOut, _ := currentPaneCmd.Output()
	tuiPaneID := strings.TrimSpace(string(currentPaneOut))
	m.tuiPaneID = tuiPaneID

	// Step 1: Join the Claude pane below the TUI pane (vertical split)
	err := exec.CommandContext(ctx, "tmux", "join-pane",
		"-v",
		"-s", windowTarget+".0").Run()
	if err != nil {
		return
	}

	// Get the Claude pane ID (it's now the active pane after join)
	claudePaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	claudePaneOut, _ := claudePaneCmd.Output()
	m.claudePaneID = strings.TrimSpace(string(claudePaneOut))

	// Set Claude pane title
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.claudePaneID, "-T", "Claude").Run()

	// Step 2: Create a new pane to the right of Claude for the workdir
	// -h: horizontal split (right side)
	// -l: workdir takes the configured percentage of the bottom area
	workdir := m.getWorkdir()
	shellWidth := m.getShellPaneWidth()
	err = exec.CommandContext(ctx, "tmux", "split-window",
		"-h", "-l", shellWidth,
		"-t", m.claudePaneID,
		"-c", workdir).Run()
	if err != nil {
		m.workdirPaneID = ""
	} else {
		// Get the workdir pane ID (it's now active after split)
		workdirPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
		workdirPaneOut, _ := workdirPaneCmd.Output()
		m.workdirPaneID = strings.TrimSpace(string(workdirPaneOut))

		// Set Shell pane title
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.workdirPaneID, "-T", "Shell").Run()
	}

	// Select back to the TUI pane, set its title, and ensure it has focus
	if tuiPaneID != "" {
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", tuiPaneID, "-T", "Details").Run()
		// Ensure the TUI pane has focus for keyboard interaction
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", tuiPaneID).Run()
	}

	// Update status bar with navigation hints
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status", "on").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status-style", "bg=#3b82f6,fg=white").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status-left", " TASK UI ").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status-right", " Tab to switch panes │ drag borders to resize ").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status-right-length", "60").Run()

	// Style pane borders - active pane gets theme color outline
	// Use heavy border lines to make them more visible and indicate they're draggable
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-lines", "heavy").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-indicators", "arrows").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-style", "fg=#374151").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-active-border-style", "fg=#61AFEF").Run()

	// Resize TUI pane to configured height (default 18%)
	detailHeight := m.getDetailPaneHeight()
	exec.CommandContext(ctx, "tmux", "resize-pane", "-t", tuiPaneID, "-y", detailHeight).Run()
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
	// Use timeout for all tmux operations to prevent blocking UI
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Save pane positions before breaking (must save width before killing workdir pane)
	m.saveShellPaneWidth()
	currentPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	if currentPaneOut, err := currentPaneCmd.Output(); err == nil {
		tuiPaneID := strings.TrimSpace(string(currentPaneOut))
		m.saveDetailPaneHeight(tuiPaneID)
	}

	// Reset status bar and pane styling
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status-right", " ").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-lines", "single").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-indicators", "off").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-style", "fg=#374151").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-active-border-style", "fg=#61AFEF").Run()

	// Reset pane title back to main view label
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", "task-ui:.0", "-T", "Tasks").Run()

	// Kill the workdir pane first (it's not from task-daemon, just a shell we created)
	if m.workdirPaneID != "" {
		exec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.workdirPaneID).Run()
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
	exec.CommandContext(ctx, "tmux", "break-pane",
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

	// Use timeout for all tmux operations to prevent blocking UI
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Save pane positions before killing (must save before panes are destroyed)
	m.saveShellPaneWidth()
	currentPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	if currentPaneOut, err := currentPaneCmd.Output(); err == nil {
		tuiPaneID := strings.TrimSpace(string(currentPaneOut))
		m.saveDetailPaneHeight(tuiPaneID)
	}

	m.database.AppendTaskLog(m.task.ID, "user", "→ [Kill] Session terminated")

	// Reset pane styling first
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status-right", " ").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-lines", "single").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-indicators", "off").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-style", "fg=#374151").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-active-border-style", "fg=#61AFEF").Run()

	// Reset pane title back to main view label
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", "task-ui:.0", "-T", "Tasks").Run()

	// Kill the workdir pane first (it's a separate pane we created)
	if m.workdirPaneID != "" {
		exec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.workdirPaneID).Run()
		m.workdirPaneID = ""
	}

	// If we have a joined Claude pane, it will be killed with the window
	m.claudePaneID = ""

	exec.CommandContext(ctx, "tmux", "kill-window", "-t", windowTarget).Run()

	// Clear cached window target since session is now killed
	m.cachedWindowTarget = ""

	m.Refresh()
}

// focusNextPane cycles focus to the next pane: Details -> Claude -> Shell -> Details.
func (m *DetailModel) focusNextPane() {
	if m.claudePaneID == "" && m.workdirPaneID == "" {
		return // No panes to cycle through
	}

	// Get the currently focused pane
	currentCmd := exec.Command("tmux", "display-message", "-p", "#{pane_id}")
	currentOut, err := currentCmd.Output()
	if err != nil {
		return
	}
	currentPane := strings.TrimSpace(string(currentOut))

	// Determine the next pane in cycle: Details -> Claude -> Shell -> Details
	var nextPane string
	switch currentPane {
	case m.tuiPaneID:
		if m.claudePaneID != "" {
			nextPane = m.claudePaneID
		} else if m.workdirPaneID != "" {
			nextPane = m.workdirPaneID
		}
	case m.claudePaneID:
		if m.workdirPaneID != "" {
			nextPane = m.workdirPaneID
		} else if m.tuiPaneID != "" {
			nextPane = m.tuiPaneID
		}
	case m.workdirPaneID:
		if m.tuiPaneID != "" {
			nextPane = m.tuiPaneID
		} else if m.claudePaneID != "" {
			nextPane = m.claudePaneID
		}
	default:
		// Unknown pane, go to Claude or Shell
		if m.claudePaneID != "" {
			nextPane = m.claudePaneID
		} else if m.workdirPaneID != "" {
			nextPane = m.workdirPaneID
		}
	}

	if nextPane != "" {
		exec.Command("tmux", "select-pane", "-t", nextPane).Run()
	}
}

// focusPrevPane cycles focus to the previous pane: Details -> Shell -> Claude -> Details.
func (m *DetailModel) focusPrevPane() {
	if m.claudePaneID == "" && m.workdirPaneID == "" {
		return // No panes to cycle through
	}

	// Get the currently focused pane
	currentCmd := exec.Command("tmux", "display-message", "-p", "#{pane_id}")
	currentOut, err := currentCmd.Output()
	if err != nil {
		return
	}
	currentPane := strings.TrimSpace(string(currentOut))

	// Determine the previous pane in cycle: Details -> Shell -> Claude -> Details
	var prevPane string
	switch currentPane {
	case m.tuiPaneID:
		if m.workdirPaneID != "" {
			prevPane = m.workdirPaneID
		} else if m.claudePaneID != "" {
			prevPane = m.claudePaneID
		}
	case m.claudePaneID:
		if m.tuiPaneID != "" {
			prevPane = m.tuiPaneID
		} else if m.workdirPaneID != "" {
			prevPane = m.workdirPaneID
		}
	case m.workdirPaneID:
		if m.claudePaneID != "" {
			prevPane = m.claudePaneID
		} else if m.tuiPaneID != "" {
			prevPane = m.tuiPaneID
		}
	default:
		// Unknown pane, go to Shell or Claude
		if m.workdirPaneID != "" {
			prevPane = m.workdirPaneID
		} else if m.claudePaneID != "" {
			prevPane = m.claudePaneID
		}
	}

	if prevPane != "" {
		exec.Command("tmux", "select-pane", "-t", prevPane).Run()
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

	// Memory usage if Claude is running
	if m.claudeMemoryMB > 0 {
		meta.WriteString("  ")
		memColor := ColorMuted
		if m.claudeMemoryMB > 1000 {
			memColor = lipgloss.Color("#EF4444") // red for >1GB
		} else if m.claudeMemoryMB > 500 {
			memColor = lipgloss.Color("#F59E0B") // amber for >500MB
		}
		memStyle := lipgloss.NewStyle().Foreground(memColor)
		meta.WriteString(memStyle.Render(fmt.Sprintf("%dMB", m.claudeMemoryMB)))
	}

	// Tmux hint if session is active
	if m.claudePaneID != "" {
		meta.WriteString("  ")
		tmuxHint := lipgloss.NewStyle().
			Foreground(ColorSecondary).
			Render("(Tab to interact with Claude)")
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
	}

	// Only show execute when task is not currently processing
	isProcessing := m.task != nil && m.task.Status == db.StatusProcessing
	if !isProcessing {
		keys = append(keys, struct {
			key  string
			desc string
		}{"x", "execute"})
	}

	hasSession := m.hasActiveTmuxSession()
	hasPanes := m.claudePaneID != "" || m.workdirPaneID != ""
	// Show kill option if there's an active tmux session
	if hasSession {
		keys = append(keys, struct {
			key  string
			desc string
		}{"k", "kill"})
	}

	keys = append(keys, struct {
		key  string
		desc string
	}{"e", "edit"})

	// Only show retry when task is not currently processing
	if !isProcessing {
		keys = append(keys, struct {
			key  string
			desc string
		}{"r", "retry"})
	}

	// Show Tab shortcut when panes are visible
	if hasPanes && os.Getenv("TMUX") != "" {
		keys = append(keys, struct {
			key  string
			desc string
		}{"Tab", "switch pane"})
	}

	keys = append(keys, []struct {
		key  string
		desc string
	}{
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

// getClaudeMemoryMB returns the memory usage in MB for the Claude process running this task.
// Returns 0 if no process is found or on error.
func (m *DetailModel) getClaudeMemoryMB() int {
	if m.task == nil {
		return 0
	}

	windowTarget := m.cachedWindowTarget
	if windowTarget == "" {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Get the shell PID from the tmux pane
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-t", windowTarget, "-p", "#{pane_pid}").Output()
	if err != nil {
		return 0
	}

	shellPID := strings.TrimSpace(string(out))
	if shellPID == "" {
		return 0
	}

	// Find claude child process
	childOut, err := exec.CommandContext(ctx, "pgrep", "-P", shellPID, "claude").Output()
	var pid string
	if err == nil && len(childOut) > 0 {
		pid = strings.TrimSpace(string(childOut))
	} else {
		pid = shellPID // fallback to shell
	}

	// Get RSS in KB from ps
	psOut, err := exec.CommandContext(ctx, "ps", "-o", "rss=", "-p", pid).Output()
	if err != nil {
		return 0
	}

	rssKB, err := strconv.Atoi(strings.TrimSpace(string(psOut)))
	if err != nil {
		return 0
	}

	return rssKB / 1024 // Convert to MB
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
