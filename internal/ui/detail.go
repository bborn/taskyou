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
	executor *executor.Executor
	viewport viewport.Model
	width    int
	height   int
	ready    bool
	prInfo   *github.PRInfo

	// Task position in column (1-indexed)
	positionInColumn int
	totalInColumn    int

	// Track joined tmux panes
	claudePaneID    string // The Claude Code pane (middle-left)
	workdirPaneID   string // The workdir shell pane (middle-right)
	daemonSessionID string // The daemon session the Claude pane came from
	tuiPaneID       string // The TUI/Details pane (top)

	// Cached tmux window target (set once on creation, cleared on kill)
	cachedWindowTarget string

	// Cached Claude process memory (updated on Refresh)
	claudeMemoryMB int

	// Initial pane dimensions (to detect user resizing)
	initialDetailHeight int // percentage when panes were joined
	initialShellWidth   int // percentage when panes were joined

	// Shell pane visibility state
	shellPaneHidden bool

	// Focus state - true when the detail pane is the active tmux pane
	focused bool
}

func (m *DetailModel) executorDisplayName() string {
	if m.executor != nil {
		return m.executor.DisplayName()
	}
	return executor.DefaultExecutorName()
}

// UpdateTask updates the task and refreshes the view.
func (m *DetailModel) UpdateTask(t *db.Task) {
	m.task = t
	if m.ready {
		m.viewport.SetContent(m.renderContent())
	}
}

// SetPosition updates the task's position in its column.
func (m *DetailModel) SetPosition(position, total int) {
	m.positionInColumn = position
	m.totalInColumn = total
	// Update tmux pane title when position changes
	m.updateTmuxPaneTitle()
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
		m.logs = logs

		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}
	}

	// Update Claude memory usage
	m.claudeMemoryMB = m.getClaudeMemoryMB()

	// Update Claude pane title with memory info
	if m.claudePaneID != "" {
		title := m.executorDisplayName()
		if m.claudeMemoryMB > 0 {
			title = fmt.Sprintf("%s (%d MB)", title, m.claudeMemoryMB)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.claudePaneID, "-T", title).Run()
		cancel()
	}

	// Check if the detail pane is focused
	m.checkFocusState()

	// Ensure tmux panes are joined if available (handles external close/detach)
	m.ensureTmuxPanesJoined()
}

// Task returns the current task.
func (m *DetailModel) Task() *db.Task {
	return m.task
}

// ClaudePaneID returns the tmux pane ID where Claude is running.
func (m *DetailModel) ClaudePaneID() string {
	return m.claudePaneID
}

// NewDetailModel creates a new detail model.
func NewDetailModel(t *db.Task, database *db.DB, exec *executor.Executor, width, height int) *DetailModel {
	m := &DetailModel{
		task:     t,
		database: database,
		executor: exec,
		width:    width,
		height:   height,
		focused:  true, // Initially focused when viewing details
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
		// Only use stored session ID - no file-based fallback to avoid cross-task contamination
		sessionID := m.task.ClaudeSessionID
		if sessionID != "" {
			// Start a new tmux window resuming the previous session
			m.startResumableSession(sessionID)
			// Refresh the cached window target
			m.cachedWindowTarget = m.findTaskWindow()
			if m.cachedWindowTarget != "" {
				m.joinTmuxPane()
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
		// 'k' is now handled by app.go with confirmation dialog
		// Pane switching (Shift+Arrow) is handled by tmux keybindings

		m.viewport, cmd = m.viewport.Update(keyMsg)
	}

	return m, cmd
}

// Cleanup should be called when leaving detail view.
// It saves the current pane height before breaking the panes.
func (m *DetailModel) Cleanup() {
	if m.claudePaneID != "" || m.workdirPaneID != "" {
		m.breakTmuxPanes(true)
	}
}

// CleanupWithoutSaving cleans up panes without saving the height.
// Use this during task transitions (prev/next) to avoid rounding errors
// that accumulate with each transition and cause the pane to shrink.
func (m *DetailModel) CleanupWithoutSaving() {
	if m.claudePaneID != "" || m.workdirPaneID != "" {
		m.breakTmuxPanes(false)
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

// findTaskWindow searches tmux sessions for a window matching this task.
// Returns the full window target (session:window_index) or empty string if not found.
// Uses window index instead of name to avoid ambiguity when duplicate names exist.
// Prefers the task's stored daemon_session if available, to avoid connecting to stale windows.
func (m *DetailModel) findTaskWindow() string {
	if m.task == nil {
		return ""
	}
	windowName := executor.TmuxWindowName(m.task.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// If task has a stored daemon session, check there first
	if m.task.DaemonSession != "" {
		err := exec.CommandContext(ctx, "tmux", "has-session", "-t", m.task.DaemonSession).Run()
		if err == nil {
			// List windows with index to get unambiguous target
			out, err := exec.CommandContext(ctx, "tmux", "list-windows", "-t", m.task.DaemonSession, "-F", "#{window_index}:#{window_name}").Output()
			if err == nil {
				for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 && parts[1] == windowName {
						// Return session:index format which is unambiguous
						return m.task.DaemonSession + ":" + parts[0]
					}
				}
			}
		}
	}

	// Fall back to searching all sessions (for backwards compatibility or if stored session is gone)
	// Use session:index:name format to get unambiguous targets
	out, err := exec.CommandContext(ctx, "tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_index}:#{window_name}").Output()
	if err != nil {
		return ""
	}

	// Search for our window, return session:index format
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[2] == windowName {
			// Return session:index format which is unambiguous
			return parts[0] + ":" + parts[1]
		}
	}
	return ""
}

// startResumableSession starts a new tmux window with claude --resume for a previous session.
// This reconnects to a Claude session that was previously running but whose tmux window was killed.
func (m *DetailModel) startResumableSession(sessionID string) {
	if m.task == nil || sessionID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	windowName := executor.TmuxWindowName(m.task.ID)

	// IMPORTANT: Check if a window with this name already exists in ANY daemon session
	// to avoid creating duplicates. If found, update the cached target and return.
	existingOut, err := exec.CommandContext(ctx, "tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_index}:#{window_name}").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(existingOut)), "\n") {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) == 3 && parts[2] == windowName && strings.HasPrefix(parts[0], "task-daemon-") {
				// Window already exists - use session:index format to avoid ambiguity
				m.cachedWindowTarget = parts[0] + ":" + parts[1]
				return
			}
		}
	}

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
		// Use "tail -f /dev/null" to keep placeholder alive (empty windows exit immediately)
		err := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", daemonSession, "-n", "_placeholder", "tail", "-f", "/dev/null").Run()
		if err != nil {
			return
		}
	}

	workDir := m.getWorkdir()

	// Build the claude command with --resume
	// Check for dangerous mode - prefer task's stored mode, fall back to global env var
	dangerousFlag := ""
	if m.task.DangerousMode || os.Getenv("WORKTREE_DANGEROUS_MODE") == "1" {
		dangerousFlag = "--dangerously-skip-permissions "
	}

	// Get the session ID for environment
	worktreeSessionID := strings.TrimPrefix(daemonSession, "task-daemon-")

	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s claude %s--chrome --resume %s`,
		m.task.ID, worktreeSessionID, dangerousFlag, sessionID)

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

	// Create shell pane alongside Claude
	// Use user's default shell, fallback to zsh
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	windowTarget := daemonSession + ":" + windowName
	exec.CommandContext(ctx, "tmux", "split-window",
		"-h",                    // horizontal split
		"-t", windowTarget+".0", // split from Claude pane
		"-c", workDir, // start in task workdir
		shell).Run() // user's shell to prevent immediate exit

	// Set pane titles
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", windowTarget+".0", "-T", m.executorDisplayName()).Run()
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", windowTarget+".1", "-T", "Shell").Run()
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

// getPaneTitle returns the title for the detail pane (e.g., "Task 123 (2/5)").
func (m *DetailModel) getPaneTitle() string {
	if m.task == nil {
		return "Task"
	}
	title := fmt.Sprintf("Task %d", m.task.ID)
	if m.positionInColumn > 0 && m.totalInColumn > 0 {
		title += fmt.Sprintf(" (%d/%d)", m.positionInColumn, m.totalInColumn)
	}
	return title
}

// updateTmuxPaneTitle updates the tmux pane title to show task info.
func (m *DetailModel) updateTmuxPaneTitle() {
	if m.tuiPaneID == "" || os.Getenv("TMUX") == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.tuiPaneID, "-T", m.getPaneTitle()).Run()
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

	// Set the pane title and ensure it has focus
	if tuiPaneID != "" {
		m.tuiPaneID = tuiPaneID
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", tuiPaneID, "-T", m.getPaneTitle()).Run()
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

// getCurrentDetailPaneHeight returns the current detail pane height as a percentage (0-100).
// Returns 0 on error.
func (m *DetailModel) getCurrentDetailPaneHeight(tuiPaneID string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Get the current height of the TUI pane
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", tuiPaneID, "#{pane_height}")
	heightOut, err := cmd.Output()
	if err != nil {
		return 0
	}

	paneHeight, err := strconv.Atoi(strings.TrimSpace(string(heightOut)))
	if err != nil || paneHeight <= 0 {
		return 0
	}

	// Get the total window height
	cmd = exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{window_height}")
	totalHeightOut, err := cmd.Output()
	if err != nil {
		return 0
	}

	totalHeight, err := strconv.Atoi(strings.TrimSpace(string(totalHeightOut)))
	if err != nil || totalHeight <= 0 {
		return 0
	}

	// Calculate the percentage
	return (paneHeight * 100) / totalHeight
}

// getCurrentShellPaneWidth returns the current shell pane width as a percentage (0-100).
// Returns 0 on error.
func (m *DetailModel) getCurrentShellPaneWidth() int {
	if m.workdirPaneID == "" || m.claudePaneID == "" {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Get the width of the shell pane
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", m.workdirPaneID, "#{pane_width}")
	shellWidthOut, err := cmd.Output()
	if err != nil {
		return 0
	}

	shellWidth, err := strconv.Atoi(strings.TrimSpace(string(shellWidthOut)))
	if err != nil || shellWidth <= 0 {
		return 0
	}

	// Get the width of the claude pane
	cmd = exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", m.claudePaneID, "#{pane_width}")
	claudeWidthOut, err := cmd.Output()
	if err != nil {
		return 0
	}

	claudeWidth, err := strconv.Atoi(strings.TrimSpace(string(claudeWidthOut)))
	if err != nil || claudeWidth <= 0 {
		return 0
	}

	// Calculate total width and shell percentage
	totalWidth := shellWidth + claudeWidth
	return (shellWidth * 100) / totalWidth
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

	// Clean up any leftover panes from previous tasks (except pane .0 which is the TUI)
	// This prevents accumulation of orphaned shell panes
	listPanesCmd := exec.CommandContext(ctx, "tmux", "list-panes", "-t", "task-ui", "-F", "#{pane_index}")
	if paneListOut, err := listPanesCmd.Output(); err == nil {
		hadExtraPanes := false
		for _, paneIdx := range strings.Split(strings.TrimSpace(string(paneListOut)), "\n") {
			if paneIdx != "" && paneIdx != "0" {
				// Kill any pane that's not the TUI pane
				exec.CommandContext(ctx, "tmux", "kill-pane", "-t", "task-ui:."+paneIdx).Run()
				hadExtraPanes = true
			}
		}
		// If we killed panes, resize TUI pane to full size before joining new ones
		if hadExtraPanes {
			exec.CommandContext(ctx, "tmux", "resize-pane", "-t", "task-ui:.0", "-y", "100%").Run()
		}
	}

	// Check daemon window pane count BEFORE joining anything
	// IMPORTANT: We must check window_panes count, not try to access .1 directly,
	// because tmux returns success even when .1 doesn't exist!
	countCmd := exec.CommandContext(ctx, "tmux", "display-message", "-t", windowTarget, "-p", "#{window_panes}")
	countOut, _ := countCmd.Output()
	daemonPaneCount := strings.TrimSpace(string(countOut))
	hasShellPane := daemonPaneCount == "2"

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

	// Set Claude pane title with memory info
	claudeTitle := m.executorDisplayName()
	if m.claudeMemoryMB > 0 {
		claudeTitle = fmt.Sprintf("%s (%d MB)", claudeTitle, m.claudeMemoryMB)
	}
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.claudePaneID, "-T", claudeTitle).Run()

	// Check if shell pane should be hidden (persisted setting)
	shellHiddenSetting, _ := m.database.GetSetting(config.SettingShellPaneHidden)
	m.shellPaneHidden = shellHiddenSetting == "true"

	// Step 2: Join or create the Shell pane to the right of Claude (unless hidden)
	shellWidth := m.getShellPaneWidth()

	if m.shellPaneHidden {
		// Shell pane is hidden - kill any existing shell pane from daemon
		if hasShellPane {
			exec.CommandContext(ctx, "tmux", "kill-pane", "-t", windowTarget+".0").Run()
		}
		m.workdirPaneID = ""
	} else if hasShellPane {
		// Daemon had 2 panes. After joining Claude (.0), the Shell pane is now .0 in daemon
		err = exec.CommandContext(ctx, "tmux", "join-pane",
			"-h", "-l", shellWidth,
			"-s", windowTarget+".0", // Shell is now .0 after Claude was joined away
			"-t", m.claudePaneID).Run()
		if err != nil {
			m.workdirPaneID = ""
		} else {
			// Get the joined shell pane ID
			workdirPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
			workdirPaneOut, _ := workdirPaneCmd.Output()
			m.workdirPaneID = strings.TrimSpace(string(workdirPaneOut))
			exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.workdirPaneID, "-T", "Shell").Run()
			// Set environment variables in the shell pane (they should already be set from daemon, but ensure they're fresh)
			envCmd := fmt.Sprintf("export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=%q", m.task.ID, m.task.Port, m.task.WorktreePath)
			exec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, envCmd, "Enter").Run()
			exec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, "clear", "Enter").Run()
		}
	} else {
		// Daemon only had Claude pane (old task). Create shell pane directly in task-ui
		workdir := m.getWorkdir()
		// Use user's default shell, fallback to zsh
		userShell := os.Getenv("SHELL")
		if userShell == "" {
			userShell = "/bin/zsh"
		}
		err = exec.CommandContext(ctx, "tmux", "split-window",
			"-h", "-l", shellWidth,
			"-t", m.claudePaneID,
			"-c", workdir,
			userShell).Run() // user's shell to prevent immediate exit
		if err != nil {
			m.workdirPaneID = ""
		} else {
			// Get the new shell pane ID
			workdirPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
			workdirPaneOut, _ := workdirPaneCmd.Output()
			m.workdirPaneID = strings.TrimSpace(string(workdirPaneOut))
			exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.workdirPaneID, "-T", "Shell").Run()
			// Set environment variables in the newly created shell pane
			envCmd := fmt.Sprintf("export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=%q", m.task.ID, m.task.Port, m.task.WorktreePath)
			exec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, envCmd, "Enter").Run()
			exec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, "clear", "Enter").Run()
		}
	}

	// Select back to the TUI pane, set its title, and ensure it has focus
	if tuiPaneID != "" {
		m.tuiPaneID = tuiPaneID
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", tuiPaneID, "-T", m.getPaneTitle()).Run()
		// Ensure the TUI pane has focus for keyboard interaction
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", tuiPaneID).Run()
		m.focused = true
	}

	// Update status bar with navigation hints
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status", "on").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status-style", "bg=#3b82f6,fg=white").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status-left", " TASK UI ").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status-right", " drag borders to resize ").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status-right-length", "80").Run()

	// Style pane borders - active pane gets theme color outline
	// Use heavy border lines to make them more visible and indicate they're draggable
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-lines", "heavy").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-indicators", "arrows").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-style", "fg=#374151").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-active-border-style", "fg=#61AFEF").Run()

	// De-emphasize inactive panes - dim text and remove colors
	// This makes the focused pane more visually prominent
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "window-style", "fg=#6b7280").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "window-active-style", "fg=terminal").Run()

	// Resize TUI pane to configured height (default 20%)
	detailHeight := m.getDetailPaneHeight()
	exec.CommandContext(ctx, "tmux", "resize-pane", "-t", tuiPaneID, "-y", detailHeight).Run()

	// Bind Shift+Arrow keys to cycle through panes from any pane
	// Down/Right = next pane, Up/Left = previous pane
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Down", "select-pane", "-t", ":.+").Run()
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Right", "select-pane", "-t", ":.+").Run()
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Up", "select-pane", "-t", ":.-").Run()
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Left", "select-pane", "-t", ":.-").Run()

	// Bind Shift+X then Arrow to jump to next/prev task while focusing executor pane
	// This allows quick task navigation from any pane (especially useful from Claude pane):
	// 1. Shift+X enters "task-nav" key table
	// 2. Down/Up navigates to next/prev task and focuses executor pane
	// The sequence: select TUI pane -> send navigation key -> wait for re-join -> select executor pane
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "X", "switch-client", "-T", "task-nav").Run()
	nextTaskCmd := "tmux select-pane -t :.0 && tmux send-keys Down && sleep 0.3 && tmux select-pane -t :.1"
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "task-nav", "Down", "run-shell", nextTaskCmd).Run()
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "task-nav", "S-Down", "run-shell", nextTaskCmd).Run()
	prevTaskCmd := "tmux select-pane -t :.0 && tmux send-keys Up && sleep 0.3 && tmux select-pane -t :.1"
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "task-nav", "Up", "run-shell", prevTaskCmd).Run()
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "task-nav", "S-Up", "run-shell", prevTaskCmd).Run()

	// Capture initial dimensions so we can detect user resizing later
	// This allows us to save only when the user has actually dragged to resize
	m.initialDetailHeight = m.getCurrentDetailPaneHeight(tuiPaneID)
	m.initialShellWidth = m.getCurrentShellPaneWidth()
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
	// Try project directory if task has a project
	if m.task.Project != "" && m.executor != nil {
		if projectDir := m.executor.GetProjectDir(m.task.Project); projectDir != "" {
			return projectDir
		}
	}
	// Fallback to home directory
	home, _ := os.UserHomeDir()
	return home
}

// breakTmuxPanes breaks both joined panes - kills workdir, returns Claude to task-daemon.
// If saveHeight is true, the current pane height is always saved to settings.
// If saveHeight is false, dimensions are only saved if the user has resized them
// (to avoid rounding error accumulation during task transitions when dimensions haven't changed).
func (m *DetailModel) breakTmuxPanes(saveHeight bool) {
	// Use timeout for all tmux operations to prevent blocking UI
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get current dimensions to check if user has resized
	currentHeight := m.getCurrentDetailPaneHeight(m.tuiPaneID)
	currentWidth := m.getCurrentShellPaneWidth()

	// Determine if user has resized the panes (with 2% tolerance for rounding)
	heightChanged := currentHeight > 0 && m.initialDetailHeight > 0 &&
		(currentHeight < m.initialDetailHeight-2 || currentHeight > m.initialDetailHeight+2)
	widthChanged := currentWidth > 0 && m.initialShellWidth > 0 &&
		(currentWidth < m.initialShellWidth-2 || currentWidth > m.initialShellWidth+2)

	// Save pane positions before breaking (must save width before killing workdir pane)
	// Save shell width if explicitly requested OR if user has resized
	if saveHeight || widthChanged {
		m.saveShellPaneWidth()
	}

	// Save detail pane height if explicitly requested OR if user has resized
	// This ensures user's manual resize is preserved even during task transitions
	if m.tuiPaneID != "" && (saveHeight || heightChanged) {
		// Use the stored TUI pane ID, not the currently focused pane.
		// The user may have Tab'd to Claude or Shell pane before pressing Escape,
		// so #{pane_id} could return the wrong pane.
		m.saveDetailPaneHeight(m.tuiPaneID)
	}

	// Reset status bar and pane styling
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "status-right", " ").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-lines", "single").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-indicators", "off").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-style", "fg=#374151").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-active-border-style", "fg=#61AFEF").Run()

	// Reset window styling (remove inactive pane de-emphasis)
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "window-style", "default").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "window-active-style", "default").Run()

	// Unbind Shift+Arrow keybindings that were set in joinTmuxPanes
	exec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "S-Down").Run()
	exec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "S-Right").Run()
	exec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "S-Up").Run()
	exec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "S-Left").Run()

	// Unbind Shift+X task navigation keybindings
	exec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "X").Run()
	exec.CommandContext(ctx, "tmux", "unbind-key", "-T", "task-nav", "Down").Run()
	exec.CommandContext(ctx, "tmux", "unbind-key", "-T", "task-nav", "S-Down").Run()
	exec.CommandContext(ctx, "tmux", "unbind-key", "-T", "task-nav", "Up").Run()
	exec.CommandContext(ctx, "tmux", "unbind-key", "-T", "task-nav", "S-Up").Run()

	// Reset pane title back to main view label
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", "task-ui:.0", "-T", "Tasks").Run()

	// Break the Claude pane back to task-daemon
	if m.claudePaneID == "" {
		// Even if we don't have a Claude pane, we may have a shell pane that needs cleanup
		if m.workdirPaneID != "" {
			// Kill the orphaned shell pane since we have no Claude pane to join it with
			exec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.workdirPaneID).Run()
			m.workdirPaneID = ""
		}
		// Ensure TUI pane is full size
		exec.CommandContext(ctx, "tmux", "resize-pane", "-t", "task-ui:.0", "-y", "100%").Run()
		return
	}

	windowName := executor.TmuxWindowName(m.task.ID)

	// Determine the daemon session to break back to
	daemonSession := m.daemonSessionID
	if daemonSession == "" {
		// Fallback to the constant if we don't have a stored session
		daemonSession = executor.TmuxDaemonSession
	}

	// Break the Claude pane back to task-daemon as a new window with the task name
	// -d: don't switch to the new window
	// -s: source pane (the one we joined)
	// -t: target session
	// -n: name for the new window
	breakErr := exec.CommandContext(ctx, "tmux", "break-pane",
		"-d",
		"-s", m.claudePaneID,
		"-t", daemonSession+":",
		"-n", windowName).Run()
	if breakErr != nil {
		// Break failed - kill both panes to avoid them persisting in task-ui
		exec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.claudePaneID).Run()
		if m.workdirPaneID != "" {
			exec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.workdirPaneID).Run()
			m.workdirPaneID = ""
		}
		m.claudePaneID = ""
		m.daemonSessionID = ""
		exec.CommandContext(ctx, "tmux", "resize-pane", "-t", "task-ui:.0", "-y", "100%").Run()
		return
	}

	// If we have a workdir pane, join it to the task-daemon window alongside Claude
	// This preserves any running processes (Rails servers, watchers, etc.)
	if m.workdirPaneID != "" {
		// Find the actual window index to avoid ambiguity with duplicate window names
		// The window we just created should be the one with our name
		var targetWindow string
		listOut, listErr := exec.CommandContext(ctx, "tmux", "list-windows", "-t", daemonSession, "-F", "#{window_index}:#{window_name}").Output()
		if listErr == nil {
			// Find our window by name and get its index (take the last match as it's the newest)
			var windowIndex string
			for _, line := range strings.Split(strings.TrimSpace(string(listOut)), "\n") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 && parts[1] == windowName {
					windowIndex = parts[0]
				}
			}
			if windowIndex != "" {
				targetWindow = daemonSession + ":" + windowIndex
			}
		}
		// Fallback to name-based targeting if we couldn't get the index
		if targetWindow == "" {
			targetWindow = daemonSession + ":" + windowName
		}

		// Join the workdir pane horizontally to the right of the Claude pane
		// -h: horizontal split (side by side)
		// -d: don't switch focus
		// -s: source pane (the workdir pane)
		// -t: target window (the newly broken Claude window)
		joinErr := exec.CommandContext(ctx, "tmux", "join-pane",
			"-h",
			"-d",
			"-s", m.workdirPaneID,
			"-t", targetWindow+".0").Run()
		if joinErr != nil {
			// Join failed - kill the pane to avoid it persisting in task-ui
			exec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.workdirPaneID).Run()
			m.workdirPaneID = ""
		}
		// Note: We don't clear m.workdirPaneID on success because the pane still exists
		// and will be rejoined when we navigate back to this task
	}

	// Resize the TUI pane back to full window size now that the splits are gone
	// This ensures the kanban view has the full window to render
	exec.CommandContext(ctx, "tmux", "resize-pane", "-t", "task-ui:.0", "-y", "100%").Run()

	m.claudePaneID = ""
	m.daemonSessionID = ""
}

// ToggleShellPane toggles the visibility of the shell pane.
// When hidden, the Claude pane expands to full width.
// When shown, the shell pane is recreated with the saved width.
func (m *DetailModel) ToggleShellPane() {
	if os.Getenv("TMUX") == "" || m.claudePaneID == "" {
		return // Not in tmux or no Claude pane
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if m.shellPaneHidden {
		// Show the shell pane - create a new one to the right of Claude
		workdir := m.getWorkdir()
		userShell := os.Getenv("SHELL")
		if userShell == "" {
			userShell = "/bin/zsh"
		}
		shellWidth := m.getShellPaneWidth()

		err := exec.CommandContext(ctx, "tmux", "split-window",
			"-h", "-l", shellWidth,
			"-t", m.claudePaneID,
			"-c", workdir,
			userShell).Run()
		if err != nil {
			return
		}

		// Get the new shell pane ID
		workdirPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
		workdirPaneOut, _ := workdirPaneCmd.Output()
		m.workdirPaneID = strings.TrimSpace(string(workdirPaneOut))
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.workdirPaneID, "-T", "Shell").Run()

		// Set environment variables in the shell pane
		if m.task != nil {
			envCmd := fmt.Sprintf("export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=%q", m.task.ID, m.task.Port, m.task.WorktreePath)
			exec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, envCmd, "Enter").Run()
			exec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, "clear", "Enter").Run()
		}

		// Return focus to TUI pane
		if m.tuiPaneID != "" {
			exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.tuiPaneID).Run()
		}

		m.shellPaneHidden = false
		m.database.SetSetting(config.SettingShellPaneHidden, "false")
	} else {
		// Hide the shell pane - save width first, then kill it
		if m.workdirPaneID != "" {
			m.saveShellPaneWidth()
			exec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.workdirPaneID).Run()
			m.workdirPaneID = ""
		}
		m.shellPaneHidden = true
		m.database.SetSetting(config.SettingShellPaneHidden, "true")

		// Return focus to TUI pane
		if m.tuiPaneID != "" {
			exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.tuiPaneID).Run()
		}
	}
}

// IsShellPaneHidden returns true if the shell pane is currently hidden.
func (m *DetailModel) IsShellPaneHidden() bool {
	return m.shellPaneHidden
}

// IsFocused returns true if the detail pane is the active tmux pane.
func (m *DetailModel) IsFocused() bool {
	return m.focused
}

// checkFocusState checks if the TUI pane is the active pane in tmux.
func (m *DetailModel) checkFocusState() {
	// Default to focused if not in tmux or no panes joined
	if os.Getenv("TMUX") == "" || m.tuiPaneID == "" {
		m.focused = true
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Get the currently active pane ID
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}").Output()
	if err != nil {
		m.focused = true // Default to focused on error
		return
	}

	activePaneID := strings.TrimSpace(string(out))
	m.focused = activePaneID == m.tuiPaneID
}

// View renders the detail view.
func (m *DetailModel) View() string {
	if !m.ready {
		return "\n  Loading..."
	}

	header := m.renderHeader()
	content := m.viewport.View()

	// Use dimmed border when unfocused
	borderColor := ColorPrimary
	if !m.focused {
		borderColor = lipgloss.Color("#4B5563") // Muted gray
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(m.width-2).
		Padding(0, 1)

	help := m.renderHelp()
	return lipgloss.JoinVertical(lipgloss.Left,
		box.Render(lipgloss.JoinVertical(lipgloss.Left, header, content)),
		help,
	)
}

func (m *DetailModel) renderHeader() string {
	t := m.task

	// When unfocused, use muted styles for badges
	dimmedBg := lipgloss.Color("#4B5563")     // Muted gray background
	dimmedFg := lipgloss.Color("#9CA3AF")     // Muted gray foreground
	dimmedTextFg := lipgloss.Color("#6B7280") // Even more muted for text

	// Task title (ID and position are shown in the panel border)
	var subtitle string
	if m.focused {
		subtitle = Bold.Render(t.Title)
	} else {
		subtitle = lipgloss.NewStyle().Foreground(dimmedTextFg).Render(t.Title)
	}

	var meta strings.Builder

	// Status badge
	var statusStyle lipgloss.Style
	if m.focused {
		statusStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Background(StatusColor(t.Status)).
			Foreground(lipgloss.Color("#FFFFFF"))
	} else {
		statusStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Background(dimmedBg).
			Foreground(dimmedFg)
	}
	meta.WriteString(statusStyle.Render(t.Status))
	meta.WriteString("  ")

	// Dangerous mode badge (only shown when in dangerous mode and task is active)
	if t.DangerousMode && (t.Status == db.StatusProcessing || t.Status == db.StatusBlocked) {
		var dangerousStyle lipgloss.Style
		if m.focused {
			dangerousStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(lipgloss.Color("196")). // Red
				Foreground(lipgloss.Color("#FFFFFF")).
				Bold(true)
		} else {
			dangerousStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(dimmedBg).
				Foreground(dimmedFg)
		}
		meta.WriteString(dangerousStyle.Render("DANGEROUS"))
		meta.WriteString("  ")
	}

	// Project
	if t.Project != "" {
		var projectStyle lipgloss.Style
		if m.focused {
			projectStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(ProjectColor(t.Project)).
				Foreground(lipgloss.Color("#FFFFFF"))
		} else {
			projectStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(dimmedBg).
				Foreground(dimmedFg)
		}
		meta.WriteString(projectStyle.Render(t.Project))
		meta.WriteString("  ")
	}

	// Type
	if t.Type != "" {
		var typeStyle lipgloss.Style
		if m.focused {
			typeStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(ColorCode).
				Foreground(lipgloss.Color("#FFFFFF"))
		} else {
			typeStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(dimmedBg).
				Foreground(dimmedFg)
		}
		meta.WriteString(typeStyle.Render(t.Type))
	}

	// PR status
	if m.prInfo != nil {
		meta.WriteString("  ")
		if m.focused {
			meta.WriteString(PRStatusBadge(m.prInfo))
		} else {
			// Dimmed PR badge
			prBadgeStyle := lipgloss.NewStyle().
				Padding(0, 1).
				Background(dimmedBg).
				Foreground(dimmedFg)
			meta.WriteString(prBadgeStyle.Render(string(m.prInfo.State)))
		}
		meta.WriteString(" ")
		prDesc := lipgloss.NewStyle().
			Foreground(dimmedTextFg).
			Render(m.prInfo.StatusDescription())
		meta.WriteString(prDesc)
	}

	// Schedule info - show if scheduled OR recurring
	if t.IsScheduled() || t.IsRecurring() {
		meta.WriteString("  ")
		var scheduleStyle lipgloss.Style
		if m.focused {
			scheduleStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(lipgloss.Color("214")). // Orange
				Foreground(lipgloss.Color("#000000"))
		} else {
			scheduleStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(dimmedBg).
				Foreground(dimmedFg)
		}
		icon := "⏰"
		if t.IsRecurring() {
			icon = "🔁"
		}
		var scheduleText string
		if t.IsScheduled() {
			scheduleText = icon + " " + formatScheduleTime(t.ScheduledAt.Time)
		} else {
			scheduleText = icon
		}
		if t.IsRecurring() {
			scheduleText += " (" + t.Recurrence + ")"
		}
		meta.WriteString(scheduleStyle.Render(scheduleText))

		// Show last run info for recurring tasks
		if t.LastRunAt != nil {
			var lastRunStyle lipgloss.Style
			if m.focused {
				lastRunStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("214"))
			} else {
				lastRunStyle = lipgloss.NewStyle().
					Foreground(dimmedTextFg)
			}
			lastRunText := fmt.Sprintf(" Last: %s", t.LastRunAt.Time.Format("Jan 2 3:04pm"))
			meta.WriteString(lastRunStyle.Render(lastRunText))
		}
	}

	// PR link if available
	var prLine string
	if m.prInfo != nil && m.prInfo.URL != "" {
		if m.focused {
			prLine = Dim.Render(fmt.Sprintf("PR #%d: %s", m.prInfo.Number, m.prInfo.URL))
		} else {
			prLine = lipgloss.NewStyle().Foreground(dimmedTextFg).Render(fmt.Sprintf("PR #%d: %s", m.prInfo.Number, m.prInfo.URL))
		}
	}

	lines := []string{subtitle, meta.String()}
	if prLine != "" {
		lines = append(lines, prLine)
	}
	lines = append(lines, "")

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m *DetailModel) renderContent() string {
	t := m.task
	var b strings.Builder

	// Dimmed style for unfocused content
	dimmedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))

	// Description
	if t.Body != "" && strings.TrimSpace(t.Body) != "" {
		if m.focused {
			b.WriteString(Bold.Render("Description"))
		} else {
			b.WriteString(dimmedStyle.Render("Description"))
		}
		b.WriteString("\n\n")

		// Create a renderer with the correct width for the viewport
		// Use different style paths for focused vs unfocused
		stylePath := "dark"
		if !m.focused {
			stylePath = "notty" // More muted style for unfocused state
		}
		renderer, err := glamour.NewTermRenderer(
			glamour.WithStylePath(stylePath),
			glamour.WithWordWrap(m.width-4), // Match viewport width
		)
		if err != nil {
			if m.focused {
				b.WriteString(t.Body)
			} else {
				b.WriteString(dimmedStyle.Render(t.Body))
			}
		} else {
			rendered, err := renderer.Render(t.Body)
			if err != nil {
				if m.focused {
					b.WriteString(t.Body)
				} else {
					b.WriteString(dimmedStyle.Render(t.Body))
				}
			} else {
				if m.focused {
					b.WriteString(strings.TrimSpace(rendered))
				} else {
					// Apply dimmed style to the entire rendered content
					b.WriteString(dimmedStyle.Render(strings.TrimSpace(rendered)))
				}
			}
		}
		b.WriteString("\n")
	}

	// Execution logs
	if len(m.logs) > 0 {
		b.WriteString("\n")
		if m.focused {
			b.WriteString(Bold.Render("Execution Log"))
		} else {
			b.WriteString(dimmedStyle.Render("Execution Log"))
		}
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

			var line string
			if m.focused {
				line = fmt.Sprintf("%s %s %s",
					Dim.Render(log.CreatedAt.Format("15:04:05")),
					icon,
					log.Content,
				)
			} else {
				line = fmt.Sprintf("%s %s %s",
					dimmedStyle.Render(log.CreatedAt.Format("15:04:05")),
					icon,
					dimmedStyle.Render(log.Content),
				)
			}
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
		{"↑/↓", "prev/next task"},
	}

	// Only show execute/retry when Claude is not running
	claudeRunning := m.claudeMemoryMB > 0
	if !claudeRunning {
		keys = append(keys, struct {
			key  string
			desc string
		}{"x", "execute"})
	}

	hasPanes := m.claudePaneID != "" || m.workdirPaneID != ""

	keys = append(keys, struct {
		key  string
		desc string
	}{"e", "edit"})

	// Only show retry when Claude is not running
	if !claudeRunning {
		keys = append(keys, struct {
			key  string
			desc string
		}{"r", "retry"})
	}

	// Always show status change option
	keys = append(keys, struct {
		key  string
		desc string
	}{"S", "status"})

	// Show dangerous mode toggle when task is processing or blocked
	if m.task != nil && (m.task.Status == db.StatusProcessing || m.task.Status == db.StatusBlocked) {
		toggleDesc := "dangerous mode"
		if m.task.DangerousMode {
			toggleDesc = "safe mode"
		}
		keys = append(keys, struct {
			key  string
			desc string
		}{"!", toggleDesc})
	}

	// Show executor resume shortcut only when the agent is not running but has a session
	if m.task != nil && m.task.ClaudeSessionID != "" && m.claudeMemoryMB == 0 {
		keys = append(keys, struct {
			key  string
			desc string
		}{"R", fmt.Sprintf("resume %s", m.executorDisplayName())})
	}

	// Show pane navigation shortcut when panes are visible
	if hasPanes && os.Getenv("TMUX") != "" {
		keys = append(keys, struct {
			key  string
			desc string
		}{"shift+↑↓", "switch pane"})
	}

	// Show toggle shell shortcut when Claude pane is visible
	if m.claudePaneID != "" && os.Getenv("TMUX") != "" {
		toggleDesc := "hide shell"
		if m.shellPaneHidden {
			toggleDesc = "show shell"
		}
		keys = append(keys, struct {
			key  string
			desc string
		}{"\\", toggleDesc})
	}

	keys = append(keys, []struct {
		key  string
		desc string
	}{
		{"c", "close"},
		{"a", "archive"},
		{"d", "delete"},
		{"esc", "back"},
	}...)

	var help string
	dimmedKeyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	dimmedDescStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#4B5563"))

	for i, k := range keys {
		if i > 0 {
			help += "  "
		}
		if m.focused {
			help += HelpKey.Render(k.key) + " " + HelpDesc.Render(k.desc)
		} else {
			help += dimmedKeyStyle.Render(k.key) + " " + dimmedDescStyle.Render(k.desc)
		}
	}

	return HelpBar.Render(help)
}

// getClaudeMemoryMB returns the memory usage in MB for the Claude process running this task.
// Returns 0 if no process is found or on error.
func (m *DetailModel) getClaudeMemoryMB() int {
	if m.task == nil {
		return 0
	}

	// Use joined pane ID if available, otherwise fall back to cached window target
	paneTarget := m.claudePaneID
	if paneTarget == "" {
		paneTarget = m.cachedWindowTarget
	}
	if paneTarget == "" {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Get the shell PID from the tmux pane
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-t", paneTarget, "-p", "#{pane_pid}").Output()
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
