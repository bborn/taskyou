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

	// Track if join-pane has failed to prevent retry loop
	joinPaneFailed bool
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
	log := GetLogger()
	log.Info("NewDetailModel: creating for task %d (%s)", t.ID, t.Title)
	log.Debug("NewDetailModel: TMUX env=%q, DaemonSession=%q, ClaudeSessionID=%q",
		os.Getenv("TMUX"), t.DaemonSession, t.ClaudeSessionID)

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
	log.Debug("NewDetailModel: calling findTaskWindow...")
	m.cachedWindowTarget = m.findTaskWindow()
	log.Info("NewDetailModel: cachedWindowTarget=%q", m.cachedWindowTarget)

	// Fetch initial memory usage
	m.claudeMemoryMB = m.getClaudeMemoryMB()

	// If we're in tmux and task has active session, join it as a split pane
	if os.Getenv("TMUX") != "" && m.cachedWindowTarget != "" {
		log.Info("NewDetailModel: in tmux with active session, calling joinTmuxPane")
		m.joinTmuxPane()
		log.Info("NewDetailModel: after joinTmuxPane, claudePaneID=%q, workdirPaneID=%q",
			m.claudePaneID, m.workdirPaneID)
	} else if os.Getenv("TMUX") != "" {
		// No active tmux window - check if we can resume a previous session
		// Only use stored session ID - no file-based fallback to avoid cross-task contamination
		sessionID := m.task.ClaudeSessionID
		log.Info("NewDetailModel: in tmux but no window target, ClaudeSessionID=%q", sessionID)
		if sessionID != "" {
			// Start a new tmux window resuming the previous session
			log.Info("NewDetailModel: calling startResumableSession with %q", sessionID)
			m.startResumableSession(sessionID)
			// Refresh the cached window target
			m.cachedWindowTarget = m.findTaskWindow()
			log.Info("NewDetailModel: after startResumableSession, cachedWindowTarget=%q", m.cachedWindowTarget)
			if m.cachedWindowTarget != "" {
				log.Info("NewDetailModel: calling joinTmuxPane after resume")
				m.joinTmuxPane()
				log.Info("NewDetailModel: after joinTmuxPane (resume), claudePaneID=%q, workdirPaneID=%q",
					m.claudePaneID, m.workdirPaneID)
			}
		}
		// Even without joined panes, ensure Details pane has focus
		if m.claudePaneID == "" {
			log.Debug("NewDetailModel: no claudePaneID, calling focusDetailsPane")
			m.focusDetailsPane()
		}
	} else {
		log.Info("NewDetailModel: not in tmux, skipping pane operations")
	}

	log.Info("NewDetailModel: completed for task %d", t.ID)
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

	// Pass all messages to viewport for scrolling support
	// This enables:
	// - Page Up/Page Down for keyboard scrolling
	// - Mouse wheel scrolling (tea.MouseMsg)
	// Note: Up/Down arrow keys are handled by app.go for task navigation
	m.viewport, cmd = m.viewport.Update(msg)

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
	log := GetLogger()
	if m.task == nil {
		log.Debug("findTaskWindow: task is nil, returning empty")
		return ""
	}
	windowName := executor.TmuxWindowName(m.task.ID)
	log.Debug("findTaskWindow: looking for window %q for task %d", windowName, m.task.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// If task has a stored daemon session, check there first
	if m.task.DaemonSession != "" {
		log.Debug("findTaskWindow: checking stored DaemonSession %q", m.task.DaemonSession)
		err := exec.CommandContext(ctx, "tmux", "has-session", "-t", m.task.DaemonSession).Run()
		if err == nil {
			// List windows with index to get unambiguous target
			out, err := exec.CommandContext(ctx, "tmux", "list-windows", "-t", m.task.DaemonSession, "-F", "#{window_index}:#{window_name}").Output()
			if err == nil {
				log.Debug("findTaskWindow: windows in session: %q", strings.TrimSpace(string(out)))
				for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 && parts[1] == windowName {
						// Return session:index format which is unambiguous
						result := m.task.DaemonSession + ":" + parts[0]
						log.Info("findTaskWindow: found in stored session -> %q", result)
						return result
					}
				}
			} else {
				log.Debug("findTaskWindow: list-windows failed: %v", err)
			}
		} else {
			log.Debug("findTaskWindow: stored session %q does not exist: %v", m.task.DaemonSession, err)
		}
	}

	// Fall back to searching all sessions (for backwards compatibility or if stored session is gone)
	// Use session:index:name format to get unambiguous targets
	log.Debug("findTaskWindow: searching all sessions")
	out, err := exec.CommandContext(ctx, "tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_index}:#{window_name}").Output()
	if err != nil {
		log.Error("findTaskWindow: list-windows -a failed: %v", err)
		return ""
	}
	log.Debug("findTaskWindow: all windows: %q", strings.TrimSpace(string(out)))

	// Search for our window, return session:index format
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[2] == windowName {
			// Return session:index format which is unambiguous
			result := parts[0] + ":" + parts[1]
			log.Info("findTaskWindow: found in all sessions search -> %q", result)
			return result
		}
	}
	log.Info("findTaskWindow: window not found for task %d", m.task.ID)
	return ""
}

// startResumableSession starts a new tmux window with claude --resume for a previous session.
// This reconnects to a Claude session that was previously running but whose tmux window was killed.
func (m *DetailModel) startResumableSession(sessionID string) {
	log := GetLogger()
	log.Info("startResumableSession: called with sessionID=%q for task %d", sessionID, m.task.ID)

	if m.task == nil || sessionID == "" {
		log.Debug("startResumableSession: early return (task=%v, sessionID=%q)", m.task != nil, sessionID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	windowName := executor.TmuxWindowName(m.task.ID)
	log.Debug("startResumableSession: windowName=%q", windowName)

	// IMPORTANT: Check if a window with this name already exists in ANY daemon session
	// to avoid creating duplicates. If found, update the cached target and return.
	existingOut, err := exec.CommandContext(ctx, "tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_index}:#{window_name}").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(existingOut)), "\n") {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) == 3 && parts[2] == windowName && strings.HasPrefix(parts[0], "task-daemon-") {
				// Window already exists - use session:index format to avoid ambiguity
				m.cachedWindowTarget = parts[0] + ":" + parts[1]
				log.Info("startResumableSession: window already exists at %q, reusing", m.cachedWindowTarget)
				return
			}
		}
	}

	// Find or create a task-daemon session to put the window in
	// First, look for any existing task-daemon-* session
	out, err := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		log.Error("startResumableSession: list-sessions failed: %v", err)
		return
	}
	log.Debug("startResumableSession: sessions: %q", strings.TrimSpace(string(out)))

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
		log.Info("startResumableSession: creating new daemon session %q", daemonSession)
		// Use "tail -f /dev/null" to keep placeholder alive (empty windows exit immediately)
		err := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", daemonSession, "-n", "_placeholder", "tail", "-f", "/dev/null").Run()
		if err != nil {
			log.Error("startResumableSession: new-session failed: %v", err)
			return
		}
	} else {
		log.Debug("startResumableSession: using existing daemon session %q", daemonSession)
	}

	workDir := m.getWorkdir()
	log.Debug("startResumableSession: workDir=%q", workDir)

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
	log.Debug("startResumableSession: script=%q", script)

	// Log the reconnection
	m.database.AppendTaskLog(m.task.ID, "system", fmt.Sprintf("Reconnecting to session %s", sessionID))

	// Create new window in the daemon session
	log.Info("startResumableSession: creating new window in %q", daemonSession)
	err = exec.CommandContext(ctx, "tmux", "new-window", "-d",
		"-t", daemonSession,
		"-n", windowName,
		"-c", workDir,
		"sh", "-c", script).Run()
	if err != nil {
		log.Error("startResumableSession: new-window failed: %v", err)
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
	log.Debug("startResumableSession: creating shell pane at %q", windowTarget)
	err = exec.CommandContext(ctx, "tmux", "split-window",
		"-h",                    // horizontal split
		"-t", windowTarget+".0", // split from Claude pane
		"-c", workDir, // start in task workdir
		shell).Run() // user's shell to prevent immediate exit
	if err != nil {
		log.Warn("startResumableSession: split-window for shell failed: %v", err)
	}

	// Set pane titles
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", windowTarget+".0", "-T", m.executorDisplayName()).Run()
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", windowTarget+".1", "-T", "Shell").Run()
	log.Info("startResumableSession: completed for task %d", m.task.ID)
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

	// Don't retry if join has already failed for this task
	if m.joinPaneFailed {
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

// getActualPaneHeight returns the actual pane height in lines.
// Returns 0 on error.
func (m *DetailModel) getActualPaneHeight(tuiPaneID string) int {
	if tuiPaneID == "" {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Get the current height of the TUI pane in lines
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", tuiPaneID, "#{pane_height}")
	heightOut, err := cmd.Output()
	if err != nil {
		return 0
	}

	paneHeight, err := strconv.Atoi(strings.TrimSpace(string(heightOut)))
	if err != nil || paneHeight <= 0 {
		return 0
	}

	return paneHeight
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
	log := GetLogger()
	log.Info("joinTmuxPanes: starting for task %d", m.task.ID)

	// Use cached window target to avoid expensive tmux lookup
	windowTarget := m.cachedWindowTarget
	if windowTarget == "" {
		log.Warn("joinTmuxPanes: cachedWindowTarget is empty, returning early")
		return
	}
	log.Debug("joinTmuxPanes: windowTarget=%q", windowTarget)

	// Use timeout for all tmux operations to prevent blocking UI
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Extract the daemon session name from the window target (session:window)
	parts := strings.SplitN(windowTarget, ":", 2)
	if len(parts) >= 1 {
		m.daemonSessionID = parts[0]
	}
	log.Debug("joinTmuxPanes: daemonSessionID=%q", m.daemonSessionID)

	// Get current pane ID before joining (so we can select it after)
	currentPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	currentPaneOut, err := currentPaneCmd.Output()
	if err != nil {
		log.Error("joinTmuxPanes: failed to get current pane ID: %v", err)
		return
	}
	tuiPaneID := strings.TrimSpace(string(currentPaneOut))
	m.tuiPaneID = tuiPaneID
	log.Debug("joinTmuxPanes: tuiPaneID=%q", tuiPaneID)

	// Clean up any leftover panes from previous tasks (except the TUI pane)
	// This prevents accumulation of orphaned shell panes
	// Use pane IDs instead of indices since the TUI pane may not be at index 0
	listPanesCmd := exec.CommandContext(ctx, "tmux", "list-panes", "-t", "task-ui", "-F", "#{pane_id}")
	if paneListOut, err := listPanesCmd.Output(); err == nil {
		log.Debug("joinTmuxPanes: existing panes in task-ui: %q (tuiPaneID=%q)", strings.TrimSpace(string(paneListOut)), tuiPaneID)
		hadExtraPanes := false
		for _, paneID := range strings.Split(strings.TrimSpace(string(paneListOut)), "\n") {
			if paneID != "" && paneID != tuiPaneID {
				// Kill any pane that's not the TUI pane
				log.Debug("joinTmuxPanes: killing leftover pane %s", paneID)
				exec.CommandContext(ctx, "tmux", "kill-pane", "-t", paneID).Run()
				hadExtraPanes = true
			}
		}
		// If we killed panes, resize TUI pane to full size before joining new ones
		if hadExtraPanes {
			log.Debug("joinTmuxPanes: resizing TUI pane to full size after cleanup")
			exec.CommandContext(ctx, "tmux", "resize-pane", "-t", tuiPaneID, "-y", "100%").Run()
		}
	} else {
		log.Debug("joinTmuxPanes: list-panes failed (expected if task-ui doesn't exist): %v", err)
	}

	// Get list of panes in daemon window to find actual pane indices
	// Pane indices may not start at 0 if panes were killed
	daemonPanesCmd := exec.CommandContext(ctx, "tmux", "list-panes", "-t", windowTarget, "-F", "#{pane_index}")
	daemonPanesOut, err := daemonPanesCmd.Output()
	if err != nil {
		log.Error("joinTmuxPanes: failed to list panes for %q: %v", windowTarget, err)
		m.joinPaneFailed = true
		return
	}
	daemonPaneIndices := strings.Split(strings.TrimSpace(string(daemonPanesOut)), "\n")
	if len(daemonPaneIndices) == 0 || daemonPaneIndices[0] == "" {
		log.Error("joinTmuxPanes: no panes found in %q", windowTarget)
		m.joinPaneFailed = true
		return
	}
	hasShellPane := len(daemonPaneIndices) >= 2
	firstPaneIndex := daemonPaneIndices[0]
	log.Debug("joinTmuxPanes: daemon panes=%v, hasShellPane=%v, firstPaneIndex=%q", daemonPaneIndices, hasShellPane, firstPaneIndex)

	// Step 1: Join the Claude pane below the TUI pane (vertical split)
	claudeSource := windowTarget + "." + firstPaneIndex
	log.Info("joinTmuxPanes: joining Claude pane from %q", claudeSource)
	joinCmd := exec.CommandContext(ctx, "tmux", "join-pane",
		"-v",
		"-s", claudeSource)
	joinOutput, err := joinCmd.CombinedOutput()
	if err != nil {
		log.Error("joinTmuxPanes: join-pane failed: %v, output: %s", err, string(joinOutput))
		m.joinPaneFailed = true // Prevent retry loop
		return
	}
	log.Debug("joinTmuxPanes: join-pane succeeded")

	// Get the Claude pane ID (it's now the active pane after join)
	claudePaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	claudePaneOut, err := claudePaneCmd.Output()
	if err != nil {
		log.Error("joinTmuxPanes: failed to get Claude pane ID: %v", err)
		return
	}
	m.claudePaneID = strings.TrimSpace(string(claudePaneOut))
	log.Info("joinTmuxPanes: claudePaneID=%q", m.claudePaneID)

	// Set Claude pane title with memory info
	claudeTitle := m.executorDisplayName()
	if m.claudeMemoryMB > 0 {
		claudeTitle = fmt.Sprintf("%s (%d MB)", claudeTitle, m.claudeMemoryMB)
	}
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.claudePaneID, "-T", claudeTitle).Run()

	// Check if shell pane should be hidden (persisted setting)
	shellHiddenSetting, _ := m.database.GetSetting(config.SettingShellPaneHidden)
	m.shellPaneHidden = shellHiddenSetting == "true"
	log.Debug("joinTmuxPanes: shellPaneHidden=%v", m.shellPaneHidden)

	// Step 2: Join or create the Shell pane to the right of Claude (unless hidden)
	shellWidth := m.getShellPaneWidth()
	log.Debug("joinTmuxPanes: shellWidth=%q, hasShellPane=%v", shellWidth, hasShellPane)

	// Get second pane index if shell pane exists (used below)
	var secondPaneIndex string
	if hasShellPane && len(daemonPaneIndices) >= 2 {
		secondPaneIndex = daemonPaneIndices[1]
	}

	if m.shellPaneHidden {
		// Shell pane is hidden - kill any existing shell pane from daemon
		log.Debug("joinTmuxPanes: shell pane hidden, killing if exists")
		if hasShellPane && secondPaneIndex != "" {
			exec.CommandContext(ctx, "tmux", "kill-pane", "-t", windowTarget+"."+secondPaneIndex).Run()
		}
		m.workdirPaneID = ""
	} else if hasShellPane && secondPaneIndex != "" {
		// Daemon had 2 panes. After joining Claude, the Shell pane is still at its original index
		shellSource := windowTarget + "." + secondPaneIndex
		log.Debug("joinTmuxPanes: joining existing shell pane from %q", shellSource)
		err = exec.CommandContext(ctx, "tmux", "join-pane",
			"-h", "-l", shellWidth,
			"-s", shellSource,
			"-t", m.claudePaneID).Run()
		if err != nil {
			log.Error("joinTmuxPanes: join shell pane failed: %v", err)
			m.workdirPaneID = ""
		} else {
			// Get the joined shell pane ID
			workdirPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
			workdirPaneOut, _ := workdirPaneCmd.Output()
			m.workdirPaneID = strings.TrimSpace(string(workdirPaneOut))
			log.Debug("joinTmuxPanes: joined shell pane, workdirPaneID=%q", m.workdirPaneID)
			exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.workdirPaneID, "-T", "Shell").Run()
			// Set environment variables in the shell pane (they should already be set from daemon, but ensure they're fresh)
			envCmd := fmt.Sprintf("export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=%q", m.task.ID, m.task.Port, m.task.WorktreePath)
			exec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, envCmd, "Enter").Run()
			exec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, "clear", "Enter").Run()
		}
	} else {
		// Daemon only had Claude pane (old task). Create shell pane directly in task-ui
		log.Debug("joinTmuxPanes: creating new shell pane (daemon had no shell)")
		workdir := m.getWorkdir()
		// Use user's default shell, fallback to zsh
		userShell := os.Getenv("SHELL")
		if userShell == "" {
			userShell = "/bin/zsh"
		}
		log.Debug("joinTmuxPanes: split-window for shell, workdir=%q, shell=%q", workdir, userShell)
		err = exec.CommandContext(ctx, "tmux", "split-window",
			"-h", "-l", shellWidth,
			"-t", m.claudePaneID,
			"-c", workdir,
			userShell).Run() // user's shell to prevent immediate exit
		if err != nil {
			log.Error("joinTmuxPanes: split-window for shell failed: %v", err)
			m.workdirPaneID = ""
		} else {
			// Get the new shell pane ID
			workdirPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
			workdirPaneOut, _ := workdirPaneCmd.Output()
			m.workdirPaneID = strings.TrimSpace(string(workdirPaneOut))
			log.Debug("joinTmuxPanes: created shell pane, workdirPaneID=%q", m.workdirPaneID)
			exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.workdirPaneID, "-T", "Shell").Run()
			// Set environment variables in the newly created shell pane
			envCmd := fmt.Sprintf("export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=%q", m.task.ID, m.task.Port, m.task.WorktreePath)
			exec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, envCmd, "Enter").Run()
			exec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, "clear", "Enter").Run()
		}
	}

	// Select back to the TUI pane, set its title, and ensure it has focus
	log.Debug("joinTmuxPanes: selecting TUI pane %q", tuiPaneID)
	if tuiPaneID != "" {
		m.tuiPaneID = tuiPaneID
		err = exec.CommandContext(ctx, "tmux", "select-pane", "-t", tuiPaneID, "-T", m.getPaneTitle()).Run()
		if err != nil {
			log.Error("joinTmuxPanes: select-pane for TUI title failed: %v", err)
		}
		// Ensure the TUI pane has focus for keyboard interaction
		err = exec.CommandContext(ctx, "tmux", "select-pane", "-t", tuiPaneID).Run()
		if err != nil {
			log.Error("joinTmuxPanes: select-pane for TUI focus failed: %v", err)
		}
		m.focused = true
	}

	// Update status bar with navigation hints
	log.Debug("joinTmuxPanes: configuring status bar and pane styles")
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
	log.Debug("joinTmuxPanes: resizing TUI pane to %q", detailHeight)
	exec.CommandContext(ctx, "tmux", "resize-pane", "-t", tuiPaneID, "-y", detailHeight).Run()

	// Bind Shift+Arrow keys to cycle through panes from any pane
	// Down/Right = next pane, Up/Left = previous pane
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Down", "select-pane", "-t", ":.+").Run()
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Right", "select-pane", "-t", ":.+").Run()
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Up", "select-pane", "-t", ":.-").Run()
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Left", "select-pane", "-t", ":.-").Run()

	// Bind Alt+Shift+Up/Down to jump to prev/next task while focusing executor pane
	// This allows quick task navigation from any pane (especially useful from Claude pane)
	// Using Alt+Shift mirrors the Shift+Arrow pane switching, and doesn't produce printable chars
	// The sequence: select TUI pane -> send navigation key -> wait for re-join -> select executor pane
	prevTaskCmd := "tmux select-pane -t :.0 && tmux send-keys Up && sleep 0.3 && tmux select-pane -t :.1"
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "M-S-Up", "run-shell", prevTaskCmd).Run()
	nextTaskCmd := "tmux select-pane -t :.0 && tmux send-keys Down && sleep 0.3 && tmux select-pane -t :.1"
	exec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "M-S-Down", "run-shell", nextTaskCmd).Run()

	// Capture initial dimensions so we can detect user resizing later
	// This allows us to save only when the user has actually dragged to resize
	m.initialDetailHeight = m.getCurrentDetailPaneHeight(tuiPaneID)
	m.initialShellWidth = m.getCurrentShellPaneWidth()

	// Update viewport height to match the actual pane height after resize
	// This prevents the header (with task title) from being pushed off screen
	if actualHeight := m.getActualPaneHeight(tuiPaneID); actualHeight > 0 {
		m.height = actualHeight
		headerHeight := 6
		footerHeight := 2
		vpHeight := m.height - headerHeight - footerHeight
		if vpHeight > 0 && m.ready {
			m.viewport.Height = vpHeight
			m.viewport.SetContent(m.renderContent())
		}
	}

	log.Info("joinTmuxPanes: completed for task %d, claudePaneID=%q, workdirPaneID=%q, tuiPaneID=%q",
		m.task.ID, m.claudePaneID, m.workdirPaneID, m.tuiPaneID)
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
	log := GetLogger()
	log.Info("breakTmuxPanes: starting for task %d, saveHeight=%v", m.task.ID, saveHeight)
	log.Debug("breakTmuxPanes: claudePaneID=%q, workdirPaneID=%q, tuiPaneID=%q, daemonSessionID=%q",
		m.claudePaneID, m.workdirPaneID, m.tuiPaneID, m.daemonSessionID)

	// Use timeout for all tmux operations to prevent blocking UI
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get current dimensions to check if user has resized
	currentHeight := m.getCurrentDetailPaneHeight(m.tuiPaneID)
	currentWidth := m.getCurrentShellPaneWidth()
	log.Debug("breakTmuxPanes: currentHeight=%d, currentWidth=%d, initialHeight=%d, initialWidth=%d",
		currentHeight, currentWidth, m.initialDetailHeight, m.initialShellWidth)

	// Determine if user has resized the panes (with 2% tolerance for rounding)
	heightChanged := currentHeight > 0 && m.initialDetailHeight > 0 &&
		(currentHeight < m.initialDetailHeight-2 || currentHeight > m.initialDetailHeight+2)
	widthChanged := currentWidth > 0 && m.initialShellWidth > 0 &&
		(currentWidth < m.initialShellWidth-2 || currentWidth > m.initialShellWidth+2)
	log.Debug("breakTmuxPanes: heightChanged=%v, widthChanged=%v", heightChanged, widthChanged)

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
	log.Debug("breakTmuxPanes: resetting status bar and pane styling")
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

	// Unbind Alt+Shift+Arrow task navigation keybindings
	exec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "M-S-Up").Run()
	exec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "M-S-Down").Run()

	// Reset pane title back to main view label
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", "task-ui:.0", "-T", "Tasks").Run()

	// Break the Claude pane back to task-daemon
	if m.claudePaneID == "" {
		log.Debug("breakTmuxPanes: no Claude pane, cleaning up")
		// Even if we don't have a Claude pane, we may have a shell pane that needs cleanup
		if m.workdirPaneID != "" {
			// Kill the orphaned shell pane since we have no Claude pane to join it with
			log.Debug("breakTmuxPanes: killing orphaned shell pane %q", m.workdirPaneID)
			exec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.workdirPaneID).Run()
			m.workdirPaneID = ""
		}
		// Ensure TUI pane is full size
		exec.CommandContext(ctx, "tmux", "resize-pane", "-t", "task-ui:.0", "-y", "100%").Run()
		log.Info("breakTmuxPanes: completed (no Claude pane)")
		return
	}

	windowName := executor.TmuxWindowName(m.task.ID)
	log.Debug("breakTmuxPanes: windowName=%q", windowName)

	// Determine the daemon session to break back to
	daemonSession := m.daemonSessionID
	if daemonSession == "" {
		// Fallback to the constant if we don't have a stored session
		daemonSession = executor.TmuxDaemonSession
		log.Debug("breakTmuxPanes: using fallback daemon session %q", daemonSession)
	}

	// Break the Claude pane back to task-daemon as a new window with the task name
	// -d: don't switch to the new window
	// -s: source pane (the one we joined)
	// -t: target session
	// -n: name for the new window
	log.Info("breakTmuxPanes: breaking Claude pane %q back to %q", m.claudePaneID, daemonSession)
	breakErr := exec.CommandContext(ctx, "tmux", "break-pane",
		"-d",
		"-s", m.claudePaneID,
		"-t", daemonSession+":",
		"-n", windowName).Run()
	if breakErr != nil {
		log.Error("breakTmuxPanes: break-pane failed: %v", breakErr)
		// Break failed - kill both panes to avoid them persisting in task-ui
		exec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.claudePaneID).Run()
		if m.workdirPaneID != "" {
			exec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.workdirPaneID).Run()
			m.workdirPaneID = ""
		}
		m.claudePaneID = ""
		m.daemonSessionID = ""
		exec.CommandContext(ctx, "tmux", "resize-pane", "-t", "task-ui:.0", "-y", "100%").Run()
		log.Info("breakTmuxPanes: completed with error cleanup")
		return
	}
	log.Debug("breakTmuxPanes: break-pane succeeded")

	// If we have a workdir pane, join it to the task-daemon window alongside Claude
	// This preserves any running processes (Rails servers, watchers, etc.)
	if m.workdirPaneID != "" {
		log.Debug("breakTmuxPanes: joining workdir pane %q to daemon window", m.workdirPaneID)
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
		log.Debug("breakTmuxPanes: targetWindow=%q", targetWindow)

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
			log.Error("breakTmuxPanes: join-pane for workdir failed: %v", joinErr)
			// Join failed - kill the pane to avoid it persisting in task-ui
			exec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.workdirPaneID).Run()
			m.workdirPaneID = ""
		} else {
			log.Debug("breakTmuxPanes: workdir pane joined successfully")
		}
		// Note: We don't clear m.workdirPaneID on success because the pane still exists
		// and will be rejoined when we navigate back to this task
	}

	// Resize the TUI pane back to full window size now that the splits are gone
	// This ensures the kanban view has the full window to render
	exec.CommandContext(ctx, "tmux", "resize-pane", "-t", "task-ui:.0", "-y", "100%").Run()

	m.claudePaneID = ""
	m.daemonSessionID = ""
	log.Info("breakTmuxPanes: completed for task %d", m.task.ID)
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
		// Show the shell pane - try to re-join from daemon first, create new only if needed
		shellWidth := m.getShellPaneWidth()

		// Get the daemon session and window name
		daemonSession := m.daemonSessionID
		if daemonSession == "" {
			daemonSession = executor.TmuxDaemonSession
		}
		windowName := executor.TmuxWindowName(m.task.ID)

		// Find the task window in the daemon session and check if it has a shell pane
		var targetWindow string
		listOut, listErr := exec.CommandContext(ctx, "tmux", "list-windows", "-t", daemonSession, "-F", "#{window_index}:#{window_name}").Output()
		if listErr == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(listOut)), "\n") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 && parts[1] == windowName {
					targetWindow = daemonSession + ":" + parts[0]
				}
			}
		}
		if targetWindow == "" {
			targetWindow = daemonSession + ":" + windowName
		}

		// Check if there's a shell pane in the daemon window (pane count > 0)
		countCmd := exec.CommandContext(ctx, "tmux", "display-message", "-t", targetWindow, "-p", "#{window_panes}")
		countOut, countErr := countCmd.Output()
		hasShellPane := countErr == nil && strings.TrimSpace(string(countOut)) == "1" // 1 pane means shell is there

		if hasShellPane {
			// Re-join the shell pane from daemon
			err := exec.CommandContext(ctx, "tmux", "join-pane",
				"-h", "-l", shellWidth,
				"-s", targetWindow+".0",
				"-t", m.claudePaneID).Run()
			if err == nil {
				// Get the joined shell pane ID
				workdirPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
				workdirPaneOut, _ := workdirPaneCmd.Output()
				m.workdirPaneID = strings.TrimSpace(string(workdirPaneOut))
				exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.workdirPaneID, "-T", "Shell").Run()
			} else {
				hasShellPane = false // Fall through to create new
			}
		}

		if !hasShellPane {
			// No shell pane in daemon - create a new one
			workdir := m.getWorkdir()
			userShell := os.Getenv("SHELL")
			if userShell == "" {
				userShell = "/bin/zsh"
			}

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

			// Set environment variables in the shell pane (only for new shells)
			if m.task != nil {
				envCmd := fmt.Sprintf("export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=%q", m.task.ID, m.task.Port, m.task.WorktreePath)
				exec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, envCmd, "Enter").Run()
				exec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, "clear", "Enter").Run()
			}
		}

		// Return focus to TUI pane
		if m.tuiPaneID != "" {
			exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.tuiPaneID).Run()
		}

		m.shellPaneHidden = false
		m.database.SetSetting(config.SettingShellPaneHidden, "false")
	} else {
		// Hide the shell pane - save width first, then break it back to daemon (preserving processes)
		if m.workdirPaneID != "" {
			m.saveShellPaneWidth()

			// Get the daemon session and window name
			daemonSession := m.daemonSessionID
			if daemonSession == "" {
				daemonSession = executor.TmuxDaemonSession
			}
			windowName := executor.TmuxWindowName(m.task.ID)

			// Find the task window in the daemon session
			var targetWindow string
			listOut, listErr := exec.CommandContext(ctx, "tmux", "list-windows", "-t", daemonSession, "-F", "#{window_index}:#{window_name}").Output()
			if listErr == nil {
				for _, line := range strings.Split(strings.TrimSpace(string(listOut)), "\n") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 && parts[1] == windowName {
						targetWindow = daemonSession + ":" + parts[0]
					}
				}
			}
			if targetWindow == "" {
				targetWindow = daemonSession + ":" + windowName
			}

			// Break the shell pane back to the daemon window, joining it next to Claude
			// -h: horizontal split (side by side)
			// -d: don't switch focus
			// -s: source pane (the workdir pane)
			// -t: target window (the daemon window with Claude)
			joinErr := exec.CommandContext(ctx, "tmux", "join-pane",
				"-h",
				"-d",
				"-s", m.workdirPaneID,
				"-t", targetWindow+".0").Run()
			if joinErr != nil {
				// Join failed - kill the pane as fallback
				exec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.workdirPaneID).Run()
			}
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

// HasRunningShellProcess returns true if the shell pane has a running process.
func (m *DetailModel) HasRunningShellProcess() bool {
	if m.task == nil {
		return false
	}

	// Get user's default shell for comparison
	userShell := os.Getenv("SHELL")
	if userShell == "" {
		userShell = "/bin/zsh"
	}
	if idx := strings.LastIndex(userShell, "/"); idx >= 0 {
		userShell = userShell[idx+1:]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Check the shell pane - could be in task-ui (visible) or daemon (hidden)
	var paneToCheck string
	if m.workdirPaneID != "" {
		// Shell pane is visible in task-ui
		paneToCheck = m.workdirPaneID
	} else if m.shellPaneHidden {
		// Shell pane is in daemon - find it
		daemonSession := m.daemonSessionID
		if daemonSession == "" {
			daemonSession = executor.TmuxDaemonSession
		}
		windowName := executor.TmuxWindowName(m.task.ID)
		paneToCheck = daemonSession + ":" + windowName + ".1"
	} else {
		return false
	}

	// Get the current command in the shell pane
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-t", paneToCheck, "-p", "#{pane_current_command}").Output()
	if err != nil {
		return false
	}

	command := strings.TrimSpace(string(out))
	return command != "" && command != userShell
}

// IsFocused returns true if the detail pane is the active tmux pane.
func (m *DetailModel) IsFocused() bool {
	return m.focused
}

// RefreshFocusState is a lightweight refresh that only updates focus state.
// Used by the fast focus tick for responsive dimming without full refresh overhead.
func (m *DetailModel) RefreshFocusState() {
	m.checkFocusState()
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

	// Add scroll indicator if content is scrollable
	var scrollIndicator string
	if m.viewport.TotalLineCount() > m.viewport.VisibleLineCount() {
		scrollPercent := 0
		if m.viewport.TotalLineCount() > 0 {
			scrollPercent = int(float64(m.viewport.YOffset+m.viewport.VisibleLineCount()) / float64(m.viewport.TotalLineCount()) * 100)
			if scrollPercent > 100 {
				scrollPercent = 100
			}
		}
		indicatorStyle := lipgloss.NewStyle().Foreground(ColorMuted)
		if !m.focused {
			indicatorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#4B5563"))
		}
		scrollIndicator = indicatorStyle.Render(fmt.Sprintf(" %d%% ", scrollPercent))
	}

	help := m.renderHelp()
	boxContent := lipgloss.JoinVertical(lipgloss.Left, header, content)
	if scrollIndicator != "" {
		boxContent = lipgloss.JoinVertical(lipgloss.Left, header, content, scrollIndicator)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		box.Render(boxContent),
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
			// Dimmed PR badge - use same icon as focused, just dimmed
			prBadgeStyle := lipgloss.NewStyle().
				Padding(0, 0).
				Background(dimmedBg).
				Foreground(dimmedFg).
				Bold(true)
			meta.WriteString(prBadgeStyle.Render(PRStatusIcon(m.prInfo)))
		}
		meta.WriteString(" ")
		prDesc := lipgloss.NewStyle().
			Foreground(dimmedTextFg).
			Render(m.prInfo.StatusDescription())
		meta.WriteString(prDesc)
	}

	// Running process indicator
	if m.HasRunningShellProcess() {
		meta.WriteString("  ")
		var processStyle lipgloss.Style
		if m.focused {
			processStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("46")) // Bright green
		} else {
			processStyle = lipgloss.NewStyle().Foreground(dimmedFg)
		}
		meta.WriteString(processStyle.Render("●"))
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

	// Show scroll hint when content is scrollable
	if m.viewport.TotalLineCount() > m.viewport.VisibleLineCount() {
		keys = append(keys, struct {
			key  string
			desc string
		}{"PgUp/Dn", "scroll"})
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
