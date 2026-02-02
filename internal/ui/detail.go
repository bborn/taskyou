package ui

import (
	"context"
	"fmt"
	"os"
	osExec "os/exec"
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

// shouldSkipAutoExecutor returns true if the task should NOT automatically
// start the executor when viewed in the TUI. Tasks in backlog status are
// explicitly not ready for execution, and done/archived tasks are finished.
func shouldSkipAutoExecutor(task *db.Task) bool {
	switch task.Status {
	case db.StatusBacklog, db.StatusDone, db.StatusArchived:
		return true
	default:
		return false
	}
}

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
	uiSessionName   string // The full UI session name (e.g., task-ui-12345)

	// Cached tmux window target (set once on creation, cleared on kill)
	cachedWindowTarget string

	// Cached Claude process memory (updated on Refresh)
	claudeMemoryMB int

	// Initial pane dimensions (to detect user resizing)
	initialDetailHeight int // percentage when panes were joined
	initialShellWidth   int // percentage when panes were joined

	// Focus state - true when the detail pane is the active tmux pane
	focused bool

	// Track if join-pane has failed to prevent retry loop
	joinPaneFailed bool

	// Cached Glamour renderers (created once, reused)
	glamourRendererFocused   *glamour.TermRenderer
	glamourRendererUnfocused *glamour.TermRenderer
	glamourWidth             int // Width the renderers were created for

	// Content caching to avoid unnecessary re-renders
	lastRenderedBody    string
	lastRenderedLogHash uint64
	lastRenderedFocused bool
	cachedContent       string

	// Log count tracking for smarter refreshes
	lastLogCount int

	// Memory check throttling (don't check every refresh)
	lastMemoryCheck time.Time

	// Pane join check throttling
	lastPaneCheck time.Time

	// Async pane loading state
	paneLoading      bool      // true while panes are being set up asynchronously
	paneLoadingStart time.Time // when loading started (for spinner animation)
	paneError        string    // user-visible error when panes fail to open

	// Notification state (passed from app)
	notification string // current notification message
	notifyTaskID int64  // task that triggered the notification
	notifyUntil  time.Time

	// Focus executor pane after joining (e.g., when jumping from notification)
	focusExecutorOnJoin bool

	// Shell pane visibility toggle
	shellPaneHidden bool // true when shell pane is collapsed to daemon
}

// Message types for async pane loading
type panesJoinedMsg struct {
	claudePaneID    string
	workdirPaneID   string
	daemonSessionID string
	windowTarget    string
	userMessage     string
	err             error
}

type spinnerTickMsg struct{}

// Spinner frames for loading animation
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m *DetailModel) executorDisplayName() string {
	// Use the task's executor field if available (each task can have a different executor)
	if m.task != nil && m.task.Executor != "" {
		switch m.task.Executor {
		case db.ExecutorCodex:
			return "Codex"
		case db.ExecutorClaude:
			return "Claude"
		case db.ExecutorGemini:
			return "Gemini"
		case db.ExecutorOpenClaw:
			return "OpenClaw"
		default:
			// Unknown executor, capitalize first letter
			if len(m.task.Executor) > 0 {
				return strings.ToUpper(m.task.Executor[:1]) + m.task.Executor[1:]
			}
		}
	}
	// Fallback to the global executor's display name
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

// SetNotification updates the notification state from the app.
func (m *DetailModel) SetNotification(msg string, taskID int64, until time.Time) {
	m.notification = msg
	m.notifyTaskID = taskID
	m.notifyUntil = until
}

// HasNotification returns true if there is an active notification for a different task.
func (m *DetailModel) HasNotification() bool {
	if m.notification == "" || m.notifyTaskID == 0 {
		return false
	}
	// Only show notification if it's for a different task
	if m.task != nil && m.notifyTaskID == m.task.ID {
		return false
	}
	return time.Now().Before(m.notifyUntil)
}

// NotifyTaskID returns the task ID that triggered the notification.
func (m *DetailModel) NotifyTaskID() int64 {
	return m.notifyTaskID
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

	// Check log count first to avoid loading all logs if unchanged
	logCount, err := m.database.GetTaskLogCount(m.task.ID)
	if err == nil && logCount != m.lastLogCount {
		// Log count changed, reload logs
		logs, err := m.database.GetTaskLogs(m.task.ID, 500)
		if err == nil {
			m.logs = logs
			m.lastLogCount = logCount

			if m.ready {
				m.viewport.SetContent(m.renderContent())
			}
		}
	}

	// Throttle memory checks to every 3 seconds (expensive: 3 shell commands)
	if time.Since(m.lastMemoryCheck) >= 3*time.Second {
		m.claudeMemoryMB = m.getClaudeMemoryMB()
		m.lastMemoryCheck = time.Now()

		// Update Claude pane title with memory info
		if m.claudePaneID != "" {
			title := m.executorDisplayName()
			if m.claudeMemoryMB > 0 {
				title = fmt.Sprintf("%s (%d MB)", title, m.claudeMemoryMB)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			osExec.CommandContext(ctx, "tmux", "select-pane", "-t", m.claudePaneID, "-T", title).Run()
			cancel()
		}
	}

	// Note: Focus state is checked by focusTick every 200ms, no need to duplicate here

	// Throttle pane join checks to every 5 seconds (runs tmux commands)
	if time.Since(m.lastPaneCheck) >= 5*time.Second {
		m.lastPaneCheck = time.Now()
		// Ensure tmux panes are joined if available (handles external close/detach)
		m.ensureTmuxPanesJoined()
	}
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
// Returns the model and an optional command for async pane setup.
// If focusExecutor is true, the executor pane will be focused after panes are joined.
func NewDetailModel(t *db.Task, database *db.DB, exec *executor.Executor, width, height int, focusExecutor bool) (*DetailModel, tea.Cmd) {
	log := GetLogger()
	log.Info("NewDetailModel: creating for task %d (%s), focusExecutor=%v", t.ID, t.Title, focusExecutor)
	log.Debug("NewDetailModel: TMUX env=%q, DaemonSession=%q, ClaudeSessionID=%q",
		os.Getenv("TMUX"), t.DaemonSession, t.ClaudeSessionID)

	m := &DetailModel{
		task:                t,
		database:            database,
		executor:            exec,
		width:               width,
		height:              height,
		focused:             true, // Initially focused when viewing details
		focusExecutorOnJoin: focusExecutor,
	}

	// Load shell pane visibility preference from settings
	if hiddenStr, err := database.GetSetting(config.SettingShellPaneHidden); err == nil && hiddenStr == "true" {
		m.shellPaneHidden = true
	}

	// Load logs
	logs, _ := database.GetTaskLogs(t.ID, 100)
	m.logs = logs

	m.initViewport()

	// Skip initial memory check - it's expensive (3 shell commands)
	// and will be fetched on the first Refresh() call instead
	m.claudeMemoryMB = 0

	// Check if we're in tmux
	if os.Getenv("TMUX") == "" {
		log.Info("NewDetailModel: not in tmux, skipping pane operations")
		log.Info("NewDetailModel: completed for task %d", t.ID)
		return m, nil
	}

	// Get the actual UI session name (avoid prefix-matching wrong session)
	if out, err := osExec.Command("tmux", "display-message", "-p", "#{session_name}").Output(); err == nil {
		m.uiSessionName = strings.TrimSpace(string(out))
	} else {
		m.uiSessionName = "task-ui" // fallback
	}

	// Cache the tmux window target once (expensive operation)
	log.Debug("NewDetailModel: calling findTaskWindow...")
	m.cachedWindowTarget = m.findTaskWindow()
	log.Info("NewDetailModel: cachedWindowTarget=%q", m.cachedWindowTarget)

	// Fast path: If task has active session, join it synchronously (already running)
	if m.cachedWindowTarget != "" {
		log.Info("NewDetailModel: in tmux with active session, calling joinTmuxPane")
		m.joinTmuxPane()
		log.Info("NewDetailModel: after joinTmuxPane, claudePaneID=%q, workdirPaneID=%q",
			m.claudePaneID, m.workdirPaneID)
		log.Info("NewDetailModel: completed for task %d", t.ID)
		return m, nil
	}

	// Don't auto-start executor for backlog/done/archived tasks
	// Backlog tasks haven't been queued for execution yet, and done/archived tasks are finished
	if shouldSkipAutoExecutor(t) {
		log.Info("NewDetailModel: skipping auto-executor for %s task", t.Status)
		log.Info("NewDetailModel: completed for task %d", t.ID)
		return m, nil
	}

	// Don't start panes for queued tasks without a worktree - let executor handle it
	// This prevents a race condition where detail view creates panes with project dir,
	// then executor kills them and creates new panes with worktree dir
	if t.Status == db.StatusQueued && t.WorktreePath == "" {
		log.Info("NewDetailModel: task is queued without worktree, waiting for executor")
		m.paneLoading = true
		m.paneLoadingStart = time.Now()
		// Don't start panes, just show loading spinner and let ensureTmuxPanesJoined
		// pick up the panes once executor creates them
		return m, m.spinnerTick()
	}

	// Slow path: No active window - start session asynchronously
	log.Info("NewDetailModel: no window target, starting async pane setup")
	m.paneLoading = true
	m.paneError = ""
	m.paneLoadingStart = time.Now()

	// Return immediately with a command to start panes in background
	log.Info("NewDetailModel: completed for task %d (async pane setup pending)", t.ID)
	return m, tea.Batch(m.startPanesAsync(), m.spinnerTick())
}

// startPanesAsync returns a command that starts the Claude session and joins panes in the background.
func (m *DetailModel) startPanesAsync() tea.Cmd {
	// Capture values needed for the goroutine
	taskID := m.task.ID
	sessionID := m.task.ClaudeSessionID

	return func() tea.Msg {
		log := GetLogger()
		log.Info("startPanesAsync: starting for task %d", taskID)

		// Start the Claude session (creates tmux window)
		if err := m.startResumableSession(sessionID); err != nil {
			userMsg := m.executorFailureMessage(err.Error())
			m.logExecutorFailure(userMsg)
			return panesJoinedMsg{err: err, userMessage: userMsg}
		}

		// Find the window target
		windowTarget := m.findTaskWindow()
		log.Info("startPanesAsync: after startResumableSession, windowTarget=%q", windowTarget)

		if windowTarget == "" {
			log.Error("startPanesAsync: failed to find window after starting session")
			err := fmt.Errorf("%s session window not found", m.executorDisplayName())
			userMsg := m.executorFailureMessage("the executor exited before panes could be created")
			m.logExecutorFailure(userMsg)
			return panesJoinedMsg{err: err, userMessage: userMsg}
		}

		// Join the panes
		m.cachedWindowTarget = windowTarget
		m.joinTmuxPane()

		log.Info("startPanesAsync: completed, claudePaneID=%q, workdirPaneID=%q",
			m.claudePaneID, m.workdirPaneID)

		return panesJoinedMsg{
			claudePaneID:    m.claudePaneID,
			workdirPaneID:   m.workdirPaneID,
			daemonSessionID: m.daemonSessionID,
			windowTarget:    windowTarget,
		}
	}
}

// logExecutorFailure writes a system log entry explaining why the executor panes
// failed to open so that the user can see the reason without digging into tmux.
func (m *DetailModel) logExecutorFailure(message string) {
	if message == "" || m.database == nil || m.task == nil {
		return
	}
	// Best effort - ignore error, logs table already handles concurrency.
	m.database.AppendTaskLog(m.task.ID, "error", message)
}

// executorFailureMessage formats a user-friendly error string for the header.
func (m *DetailModel) executorFailureMessage(details string) string {
	executorName := m.executorDisplayName()
	base := fmt.Sprintf("%s failed to start", executorName)
	if details != "" {
		base = fmt.Sprintf("%s: %s", base, details)
	}
	return base + ". Check your executor configuration."
}

// spinnerTick returns a command that ticks the loading spinner.
func (m *DetailModel) spinnerTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
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

	switch msg := msg.(type) {
	case panesJoinedMsg:
		// Async pane setup completed
		log := GetLogger()
		m.paneLoading = false
		if msg.err != nil {
			log.Error("panesJoinedMsg: error=%v", msg.err)
			m.paneError = msg.userMessage
		} else {
			log.Info("panesJoinedMsg: claudePaneID=%q, workdirPaneID=%q",
				msg.claudePaneID, msg.workdirPaneID)
			m.claudePaneID = msg.claudePaneID
			m.workdirPaneID = msg.workdirPaneID
			m.daemonSessionID = msg.daemonSessionID
			m.cachedWindowTarget = msg.windowTarget
			m.paneError = ""
			// Focus executor pane if requested (e.g., when jumping from notification)
			if m.focusExecutorOnJoin && m.claudePaneID != "" {
				m.focusExecutorPane()
			}
		}
		m.viewport.SetContent(m.renderContent())
		return m, nil

	case spinnerTickMsg:
		// Update spinner animation while loading
		if m.paneLoading {
			m.viewport.SetContent(m.renderContent())
			return m, m.spinnerTick()
		}
		return m, nil

	case panesRefreshMsg:
		// Panes need to be refreshed (e.g., after dangerous mode toggle recreated the window)
		log := GetLogger()
		log.Info("panesRefreshMsg: refreshing panes for task %d", m.task.ID)
		// Re-start the async pane setup
		return m, m.startPanesAsync()
	}

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
		m.breakTmuxPanes(true, true) // saveHeight=true, resizeTUI=true
	}
}

// CleanupWithoutSaving cleans up panes without saving the height.
// Use this during task transitions (prev/next) to avoid rounding errors
// that accumulate with each transition and cause the pane to shrink.
func (m *DetailModel) CleanupWithoutSaving() {
	if m.claudePaneID != "" || m.workdirPaneID != "" {
		m.breakTmuxPanes(false, true) // saveHeight=false, resizeTUI=true
	}
}

// ClearPaneState clears the cached pane state without breaking panes.
// Use this when the tmux window has been recreated externally (e.g., dangerous mode toggle).
func (m *DetailModel) ClearPaneState() {
	m.claudePaneID = ""
	m.workdirPaneID = ""
	m.daemonSessionID = ""
	m.cachedWindowTarget = ""
	m.joinPaneFailed = false
}

// RefreshPanesCmd returns a command to refresh the tmux panes.
// Use this after ClearPaneState() to rejoin panes to a recreated window.
func (m *DetailModel) RefreshPanesCmd() tea.Cmd {
	return func() tea.Msg {
		// Small delay to allow the new tmux window to be created
		time.Sleep(300 * time.Millisecond)
		return panesRefreshMsg{}
	}
}

// panesRefreshMsg triggers a pane refresh in the detail view.
type panesRefreshMsg struct{}

// InFeedbackMode returns false - use tmux pane for interaction.
func (m *DetailModel) InFeedbackMode() bool {
	return false
}

// StartTmuxTicker is no longer needed - real tmux pane handles display.
func (m *DetailModel) StartTmuxTicker() tea.Cmd {
	return nil
}

// findTaskWindow searches tmux sessions for a window matching this task.
// Returns the full window target (window ID like @1234, or session:window_index) or empty string if not found.
// Priority order:
// 1. Stored window ID (most reliable - globally unique)
// 2. Search by name in stored daemon session
// 3. Fall back to searching all sessions
func (m *DetailModel) findTaskWindow() string {
	log := GetLogger()
	if m.task == nil {
		log.Debug("findTaskWindow: task is nil, returning empty")
		return ""
	}
	windowName := executor.TmuxWindowName(m.task.ID)
	log.Debug("findTaskWindow: looking for window %q for task %d (stored ID: %q)", windowName, m.task.ID, m.task.TmuxWindowID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Search all daemon sessions for the task window
	// Always return "session:windowID" format for consistency
	log.Debug("findTaskWindow: searching all sessions")
	out, err := osExec.CommandContext(ctx, "tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_id}:#{window_name}").Output()
	if err != nil {
		log.Error("findTaskWindow: list-windows -a failed: %v", err)
		return ""
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		sessionName, windowID, name := parts[0], parts[1], parts[2]

		// Only look in daemon sessions
		if !strings.HasPrefix(sessionName, "task-daemon-") {
			continue
		}

		if name == windowName {
			// Store the window ID for future use
			if m.database != nil && windowID != "" {
				m.database.UpdateTaskWindowID(m.task.ID, windowID)
				m.task.TmuxWindowID = windowID
			}
			result := sessionName + ":" + windowID
			log.Info("findTaskWindow: found -> %q", result)
			return result
		}
	}
	log.Info("findTaskWindow: window not found for task %d", m.task.ID)
	return ""
}

// startResumableSession starts a new tmux window with the task's executor.
// This reconnects to a session that was previously running but whose tmux window was killed.
func (m *DetailModel) startResumableSession(sessionID string) error {
	log := GetLogger()
	log.Info("startResumableSession: called with sessionID=%q for task %d", sessionID, m.task.ID)

	if m.task == nil {
		log.Debug("startResumableSession: early return (task is nil)")
		return fmt.Errorf("task not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	windowName := executor.TmuxWindowName(m.task.ID)
	log.Debug("startResumableSession: windowName=%q", windowName)

	// IMPORTANT: Check if a window with this name already exists in ANY daemon session
	// to avoid creating duplicates. If found, update the cached target and return.
	existingOut, err := osExec.CommandContext(ctx, "tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_index}:#{window_name}").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(existingOut)), "\n") {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) == 3 && parts[2] == windowName && strings.HasPrefix(parts[0], "task-daemon-") {
				// Window already exists - use session:index format to avoid ambiguity
				m.cachedWindowTarget = parts[0] + ":" + parts[1]
				log.Info("startResumableSession: window already exists at %q, reusing", m.cachedWindowTarget)
				return nil
			}
		}
	}

	// Find or create a task-daemon session to put the window in
	// First, look for any existing task-daemon-* session
	out, err := osExec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		log.Error("startResumableSession: list-sessions failed: %v", err)
		return fmt.Errorf("tmux list-sessions failed: %w", err)
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
		err := osExec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", daemonSession, "-n", "_placeholder", "tail", "-f", "/dev/null").Run()
		if err != nil {
			log.Error("startResumableSession: new-session failed: %v", err)
			return fmt.Errorf("tmux new-session failed: %w", err)
		}
	} else {
		log.Debug("startResumableSession: using existing daemon session %q", daemonSession)
	}

	workDir := m.getWorkdir()
	log.Debug("startResumableSession: workDir=%q", workDir)

	// Get the appropriate executor for this task
	taskExecutor := m.executor.GetTaskExecutor(m.task)
	if taskExecutor == nil {
		log.Error("startResumableSession: no executor found for task")
		return fmt.Errorf("no executor configured for task")
	}

	// Build prompt with task details (for executors that need it)
	var prompt string
	if sessionID == "" {
		// No session to resume - build prompt from task
		var promptBuilder strings.Builder
		promptBuilder.WriteString(fmt.Sprintf("# Task: %s\n\n", m.task.Title))
		if m.task.Body != "" {
			promptBuilder.WriteString(m.task.Body)
			promptBuilder.WriteString("\n")
		}
		prompt = promptBuilder.String()
	}

	// Get the command from the executor
	script := taskExecutor.BuildCommand(m.task, sessionID, prompt)
	log.Debug("startResumableSession: script=%q", script)

	// Log the session start
	executorName := taskExecutor.Name()
	if sessionID != "" {
		m.database.AppendTaskLog(m.task.ID, "system", fmt.Sprintf("Reconnecting to %s session %s", executorName, sessionID))
	} else {
		m.database.AppendTaskLog(m.task.ID, "system", fmt.Sprintf("Starting new %s session", executorName))
	}

	// Create new window in the daemon session
	log.Info("startResumableSession: creating new window in %q", daemonSession)
	err = osExec.CommandContext(ctx, "tmux", "new-window", "-d",
		"-t", daemonSession,
		"-n", windowName,
		"-c", workDir,
		"sh", "-c", script).Run()
	if err != nil {
		log.Error("startResumableSession: new-window failed: %v", err)
		return fmt.Errorf("tmux new-window failed: %w", err)
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
	err = osExec.CommandContext(ctx, "tmux", "split-window",
		"-h",                    // horizontal split
		"-t", windowTarget+".0", // split from Claude pane
		"-c", workDir, // start in task workdir
		shell).Run() // user's shell to prevent immediate exit
	if err != nil {
		log.Warn("startResumableSession: split-window for shell failed: %v", err)
	}

	// Set pane titles
	osExec.CommandContext(ctx, "tmux", "select-pane", "-t", windowTarget+".0", "-T", m.executorDisplayName()).Run()
	osExec.CommandContext(ctx, "tmux", "select-pane", "-t", windowTarget+".1", "-T", "Shell").Run()
	log.Info("startResumableSession: completed for task %d", m.task.ID)
	return nil
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
		err := osExec.CommandContext(ctx, "tmux", "display-message", "-t", m.claudePaneID, "-p", "#{pane_id}").Run()
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

// getPaneTitle returns the title for the detail pane (e.g., "Task 123: some task title (2/5)").
// We don't truncate the title - tmux will handle truncation based on pane width.
func (m *DetailModel) getPaneTitle() string {
	if m.task == nil {
		return "Task"
	}

	// Build position suffix if available
	positionSuffix := ""
	if m.positionInColumn > 0 && m.totalInColumn > 0 {
		positionSuffix = fmt.Sprintf(" (%d/%d)", m.positionInColumn, m.totalInColumn)
	}

	prefix := fmt.Sprintf("Task %d", m.task.ID)
	if m.task.Title == "" {
		return prefix + positionSuffix
	}

	taskTitle := m.task.Title
	// Replace newlines with spaces for single-line display
	taskTitle = strings.ReplaceAll(taskTitle, "\n", " ")
	taskTitle = strings.ReplaceAll(taskTitle, "\r", "")

	return fmt.Sprintf("%s: %s%s", prefix, taskTitle, positionSuffix)
}

// updateTmuxPaneTitle updates the tmux pane title to show task info.
func (m *DetailModel) updateTmuxPaneTitle() {
	if m.tuiPaneID == "" || os.Getenv("TMUX") == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	osExec.CommandContext(ctx, "tmux", "select-pane", "-t", m.tuiPaneID, "-T", m.getPaneTitle()).Run()
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
	cmd := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", tuiPaneID, "#{pane_height}")
	heightOut, err := cmd.Output()
	if err != nil {
		return 0
	}

	paneHeight, err := strconv.Atoi(strings.TrimSpace(string(heightOut)))
	if err != nil || paneHeight <= 0 {
		return 0
	}

	// Get the total window height
	cmd = osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{window_height}")
	totalHeightOut, err := cmd.Output()
	if err != nil {
		return 0
	}

	totalHeight, err := strconv.Atoi(strings.TrimSpace(string(totalHeightOut)))
	if err != nil || totalHeight <= 0 {
		return 0
	}

	// Calculate the percentage with proper rounding to avoid truncation errors
	// that cause the pane to progressively shrink over time
	return (paneHeight*100 + totalHeight/2) / totalHeight
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
	cmd := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", tuiPaneID, "#{pane_height}")
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
	cmd := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", m.workdirPaneID, "#{pane_width}")
	shellWidthOut, err := cmd.Output()
	if err != nil {
		return 0
	}

	shellWidth, err := strconv.Atoi(strings.TrimSpace(string(shellWidthOut)))
	if err != nil || shellWidth <= 0 {
		return 0
	}

	// Get the width of the claude pane
	cmd = osExec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", m.claudePaneID, "#{pane_width}")
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
	// Use proper rounding to avoid truncation errors
	return (shellWidth*100 + totalWidth/2) / totalWidth
}

// saveDetailPaneHeight saves the current detail pane height to settings.
func (m *DetailModel) saveDetailPaneHeight(tuiPaneID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Get the current height of the TUI pane
	cmd := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", tuiPaneID, "#{pane_height}")
	heightOut, err := cmd.Output()
	if err != nil {
		return
	}

	paneHeight, err := strconv.Atoi(strings.TrimSpace(string(heightOut)))
	if err != nil || paneHeight <= 0 {
		return
	}

	// Get the total window height
	cmd = osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{window_height}")
	totalHeightOut, err := cmd.Output()
	if err != nil {
		return
	}

	totalHeight, err := strconv.Atoi(strings.TrimSpace(string(totalHeightOut)))
	if err != nil || totalHeight <= 0 {
		return
	}

	// Calculate the percentage with proper rounding to avoid truncation errors
	// that cause the pane to progressively shrink over time
	percentage := (paneHeight*100 + totalHeight/2) / totalHeight
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
	cmd := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", m.workdirPaneID, "#{pane_width}")
	shellWidthOut, err := cmd.Output()
	if err != nil {
		return
	}

	shellWidth, err := strconv.Atoi(strings.TrimSpace(string(shellWidthOut)))
	if err != nil || shellWidth <= 0 {
		return
	}

	// Get the width of the claude pane
	cmd = osExec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", m.claudePaneID, "#{pane_width}")
	claudeWidthOut, err := cmd.Output()
	if err != nil {
		return
	}

	claudeWidth, err := strconv.Atoi(strings.TrimSpace(string(claudeWidthOut)))
	if err != nil || claudeWidth <= 0 {
		return
	}

	// Calculate total width and shell percentage with proper rounding
	totalWidth := shellWidth + claudeWidth
	percentage := (shellWidth*100 + totalWidth/2) / totalWidth
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

	// Extract the daemon session name from the window target (format: "session:windowID")
	parts := strings.SplitN(windowTarget, ":", 2)
	if len(parts) == 2 {
		m.daemonSessionID = parts[0]
	}
	log.Debug("joinTmuxPanes: daemonSessionID=%q", m.daemonSessionID)

	// Get current pane ID before joining (so we can select it after)
	currentPaneCmd := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
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
	listPanesCmd := osExec.CommandContext(ctx, "tmux", "list-panes", "-t", m.uiSessionName, "-F", "#{pane_id}")
	if paneListOut, err := listPanesCmd.Output(); err == nil {
		log.Debug("joinTmuxPanes: existing panes in task-ui: %q (tuiPaneID=%q)", strings.TrimSpace(string(paneListOut)), tuiPaneID)
		hadExtraPanes := false
		for _, paneID := range strings.Split(strings.TrimSpace(string(paneListOut)), "\n") {
			if paneID != "" && paneID != tuiPaneID {
				// Kill any pane that's not the TUI pane, including its process
				log.Debug("joinTmuxPanes: killing leftover pane %s", paneID)
				m.killPaneWithProcess(ctx, paneID)
				hadExtraPanes = true
			}
		}
		// If we killed panes, resize TUI pane to full size before joining new ones
		if hadExtraPanes {
			log.Debug("joinTmuxPanes: resizing TUI pane to full size after cleanup")
			osExec.CommandContext(ctx, "tmux", "resize-pane", "-t", tuiPaneID, "-y", "100%").Run()
		}
	} else {
		log.Debug("joinTmuxPanes: list-panes failed (expected if task-ui doesn't exist): %v", err)
	}

	// Use stored pane IDs if available for deterministic pane identification
	// This prevents the bug where panes could be swapped if indices change
	storedClaudePaneID := m.task.ClaudePaneID
	storedShellPaneID := m.task.ShellPaneID
	log.Debug("joinTmuxPanes: stored pane IDs: claude=%q, shell=%q", storedClaudePaneID, storedShellPaneID)

	// Get list of panes in daemon window to verify they exist
	daemonPanesCmd := osExec.CommandContext(ctx, "tmux", "list-panes", "-t", windowTarget, "-F", "#{pane_id}")
	daemonPanesOut, err := daemonPanesCmd.Output()
	if err != nil {
		log.Error("joinTmuxPanes: failed to list panes for %q: %v", windowTarget, err)
		// Window doesn't exist - clear stale window ID from database
		if m.database != nil && m.task != nil {
			m.database.UpdateTaskWindowID(m.task.ID, "")
			m.task.TmuxWindowID = ""
		}
		m.joinPaneFailed = true
		return
	}
	daemonPaneIDs := strings.Split(strings.TrimSpace(string(daemonPanesOut)), "\n")
	if len(daemonPaneIDs) == 0 || daemonPaneIDs[0] == "" {
		log.Error("joinTmuxPanes: no panes found in %q", windowTarget)
		m.joinPaneFailed = true
		return
	}
	log.Debug("joinTmuxPanes: daemon pane IDs=%v", daemonPaneIDs)

	// Determine which pane ID to use for Claude
	// Priority: stored ID (if still valid) > first pane in daemon
	claudeSourcePaneID := ""
	if storedClaudePaneID != "" {
		// Verify stored Claude pane ID is still in the daemon window
		for _, pid := range daemonPaneIDs {
			if pid == storedClaudePaneID {
				claudeSourcePaneID = storedClaudePaneID
				log.Debug("joinTmuxPanes: using stored Claude pane ID %q", claudeSourcePaneID)
				break
			}
		}
	}
	if claudeSourcePaneID == "" {
		// Fall back to first pane in daemon (legacy behavior)
		claudeSourcePaneID = daemonPaneIDs[0]
		log.Debug("joinTmuxPanes: falling back to first daemon pane ID %q for Claude", claudeSourcePaneID)
	}

	// Step 1: Join the Claude pane below the TUI pane (vertical split)
	log.Info("joinTmuxPanes: joining Claude pane %q", claudeSourcePaneID)
	joinCmd := osExec.CommandContext(ctx, "tmux", "join-pane",
		"-v",
		"-s", claudeSourcePaneID)
	joinOutput, err := joinCmd.CombinedOutput()
	if err != nil {
		log.Error("joinTmuxPanes: join-pane failed: %v, output: %s", err, string(joinOutput))
		m.joinPaneFailed = true // Prevent retry loop
		return
	}
	log.Debug("joinTmuxPanes: join-pane succeeded")

	// Get the Claude pane ID (it's now the active pane after join)
	claudePaneCmd := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
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
	osExec.CommandContext(ctx, "tmux", "select-pane", "-t", m.claudePaneID, "-T", claudeTitle).Run()

	// Step 2: Join or create the Shell pane to the right of Claude (unless hidden)
	if m.shellPaneHidden {
		log.Debug("joinTmuxPanes: shell pane is hidden, skipping shell join")
		// Keep the stored shell pane ID so we can bring it back when user toggles to show
		// The shell pane is still in the hidden window (_hidden_shell_<task_id>)
		if storedShellPaneID != "" {
			m.workdirPaneID = storedShellPaneID
			log.Debug("joinTmuxPanes: preserved hidden shell pane ID %q", m.workdirPaneID)
		}
	} else {
		shellWidth := m.getShellPaneWidth()

		// Determine which pane ID to use for Shell
		// Priority: stored ID (if still valid) > remaining pane in daemon
		shellSourcePaneID := ""
		hasShellPane := len(daemonPaneIDs) >= 2

		if storedShellPaneID != "" && hasShellPane {
			// Verify stored Shell pane ID is still in the daemon window (and not the Claude pane we just joined)
			for _, pid := range daemonPaneIDs {
				if pid == storedShellPaneID && pid != claudeSourcePaneID {
					shellSourcePaneID = storedShellPaneID
					log.Debug("joinTmuxPanes: using stored Shell pane ID %q", shellSourcePaneID)
					break
				}
			}
		}
		if shellSourcePaneID == "" && hasShellPane {
			// Fall back to the other pane in daemon (the one we didn't use for Claude)
			for _, pid := range daemonPaneIDs {
				if pid != claudeSourcePaneID {
					shellSourcePaneID = pid
					log.Debug("joinTmuxPanes: falling back to remaining daemon pane ID %q for Shell", shellSourcePaneID)
					break
				}
			}
		}

		log.Debug("joinTmuxPanes: shellWidth=%q, hasShellPane=%v, shellSourcePaneID=%q", shellWidth, hasShellPane, shellSourcePaneID)

		if shellSourcePaneID != "" {
			// Join the shell pane from daemon
			log.Debug("joinTmuxPanes: joining shell pane %q", shellSourcePaneID)
			err = osExec.CommandContext(ctx, "tmux", "join-pane",
				"-h", "-l", shellWidth,
				"-s", shellSourcePaneID,
				"-t", m.claudePaneID).Run()
			if err != nil {
				log.Error("joinTmuxPanes: join shell pane failed: %v", err)
				m.workdirPaneID = ""
			} else {
				// Get the joined shell pane ID
				workdirPaneCmd := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
				workdirPaneOut, _ := workdirPaneCmd.Output()
				m.workdirPaneID = strings.TrimSpace(string(workdirPaneOut))
				log.Debug("joinTmuxPanes: joined shell pane, workdirPaneID=%q", m.workdirPaneID)
				osExec.CommandContext(ctx, "tmux", "select-pane", "-t", m.workdirPaneID, "-T", "Shell").Run()
				// Set environment variables in the shell pane (they should already be set from daemon, but ensure they're fresh)
				envCmd := fmt.Sprintf("export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=%q", m.task.ID, m.task.Port, m.task.WorktreePath)
				osExec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, envCmd, "Enter").Run()
				osExec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, "clear", "Enter").Run()
			}
		} else {
			// Daemon only had Claude pane - create a new shell pane
			// (Shell and Claude always travel together, no separate -shell windows)
			log.Debug("joinTmuxPanes: no shell in daemon window, creating new shell pane")
			workdir := m.getWorkdir()
			// Use user's default shell, fallback to zsh
			userShell := os.Getenv("SHELL")
			if userShell == "" {
				userShell = "/bin/zsh"
			}
			log.Debug("joinTmuxPanes: split-window for shell, workdir=%q, shell=%q", workdir, userShell)
			err = osExec.CommandContext(ctx, "tmux", "split-window",
				"-h", "-l", shellWidth,
				"-t", m.claudePaneID,
				"-c", workdir,
				userShell).Run() // user's shell to prevent immediate exit
			if err != nil {
				log.Error("joinTmuxPanes: split-window for shell failed: %v", err)
				m.workdirPaneID = ""
			} else {
				// Get the new shell pane ID
				workdirPaneCmd := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
				workdirPaneOut, _ := workdirPaneCmd.Output()
				m.workdirPaneID = strings.TrimSpace(string(workdirPaneOut))
				log.Debug("joinTmuxPanes: created shell pane, workdirPaneID=%q", m.workdirPaneID)
				osExec.CommandContext(ctx, "tmux", "select-pane", "-t", m.workdirPaneID, "-T", "Shell").Run()
				// Set environment variables in the newly created shell pane
				envCmd := fmt.Sprintf("export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=%q", m.task.ID, m.task.Port, m.task.WorktreePath)
				osExec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, envCmd, "Enter").Run()
				osExec.CommandContext(ctx, "tmux", "send-keys", "-t", m.workdirPaneID, "clear", "Enter").Run()
			}
		}
	}

	// Select back to the TUI pane, set its title, and ensure it has focus
	log.Debug("joinTmuxPanes: selecting TUI pane %q", tuiPaneID)
	if tuiPaneID != "" {
		m.tuiPaneID = tuiPaneID
		err = osExec.CommandContext(ctx, "tmux", "select-pane", "-t", tuiPaneID, "-T", m.getPaneTitle()).Run()
		if err != nil {
			log.Error("joinTmuxPanes: select-pane for TUI title failed: %v", err)
		}
		// Ensure the TUI pane has focus for keyboard interaction
		err = osExec.CommandContext(ctx, "tmux", "select-pane", "-t", tuiPaneID).Run()
		if err != nil {
			log.Error("joinTmuxPanes: select-pane for TUI focus failed: %v", err)
		}
		m.focused = true
	}

	// Update status bar with navigation hints
	log.Debug("joinTmuxPanes: configuring status bar and pane styles")
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "status", "on").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "status-style", "bg=#3b82f6,fg=white").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "status-left", " TASK UI ").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "status-right", " drag borders to resize ").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "status-right-length", "80").Run()

	// Style pane borders - active pane gets theme color outline
	// Use heavy border lines to make them more visible and indicate they're draggable
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "pane-border-lines", "heavy").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "pane-border-indicators", "arrows").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "pane-border-style", "fg=#374151").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "pane-active-border-style", "fg=#61AFEF").Run()

	// De-emphasize inactive panes - dim text and remove colors
	// This makes the focused pane more visually prominent
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "window-style", "fg=#6b7280").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "window-active-style", "fg=terminal").Run()

	// Resize TUI pane to configured height (default 20%)
	detailHeight := m.getDetailPaneHeight()
	log.Debug("joinTmuxPanes: resizing TUI pane to %q", detailHeight)
	osExec.CommandContext(ctx, "tmux", "resize-pane", "-t", tuiPaneID, "-y", detailHeight).Run()

	// Bind Shift+Arrow keys to cycle through panes from any pane
	// Down/Right = next pane, Up/Left = previous pane
	osExec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Down", "select-pane", "-t", ":.+").Run()
	osExec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Right", "select-pane", "-t", ":.+").Run()
	osExec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Up", "select-pane", "-t", ":.-").Run()
	osExec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Left", "select-pane", "-t", ":.-").Run()

	// Bind Alt+Shift+Up/Down to jump to prev/next task while focusing executor pane
	// This allows quick task navigation from any pane (especially useful from Claude pane)
	// Using Alt+Shift mirrors the Shift+Arrow pane switching, and doesn't produce printable chars
	// The sequence: select TUI pane -> send navigation key -> wait for re-join -> select executor pane
	prevTaskCmd := "tmux select-pane -t :.0 && tmux send-keys Up && sleep 0.3 && tmux select-pane -t :.1"
	osExec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "M-S-Up", "run-shell", prevTaskCmd).Run()
	nextTaskCmd := "tmux select-pane -t :.0 && tmux send-keys Down && sleep 0.3 && tmux select-pane -t :.1"
	osExec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "M-S-Down", "run-shell", nextTaskCmd).Run()

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

	// Focus executor pane if requested (e.g., when jumping from notification)
	if m.focusExecutorOnJoin && m.claudePaneID != "" {
		m.focusExecutorPane()
	}
}

// focusExecutorPane focuses the executor (Claude) pane.
func (m *DetailModel) focusExecutorPane() {
	if m.claudePaneID == "" {
		return
	}
	log := GetLogger()
	log.Info("focusExecutorPane: focusing Claude pane %q", m.claudePaneID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := osExec.CommandContext(ctx, "tmux", "select-pane", "-t", m.claudePaneID).Run()
	if err != nil {
		log.Error("focusExecutorPane: select-pane failed: %v", err)
	} else {
		m.focused = false // TUI is no longer focused, executor is
	}
}

// ToggleShellPane toggles the visibility of the shell pane.
// When hidden, the shell pane is moved to the daemon window and Claude expands to full width.
// When shown, the shell pane is rejoined from the daemon or a new one is created.
func (m *DetailModel) ToggleShellPane() {
	log := GetLogger()
	log.Info("ToggleShellPane: shellPaneHidden=%v, workdirPaneID=%q, claudePaneID=%q",
		m.shellPaneHidden, m.workdirPaneID, m.claudePaneID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if m.shellPaneHidden {
		// Show shell pane - rejoin it from daemon or create new
		m.showShellPane(ctx)
	} else {
		// Hide shell pane - move it to daemon and expand Claude
		m.hideShellPane(ctx)
	}

	// Save preference to settings
	hiddenStr := "false"
	if m.shellPaneHidden {
		hiddenStr = "true"
	}
	m.database.SetSetting(config.SettingShellPaneHidden, hiddenStr)
	log.Info("ToggleShellPane: saved shellPaneHidden=%v", m.shellPaneHidden)
}

// hideShellPane moves the shell pane to a hidden background window (preserves process).
func (m *DetailModel) hideShellPane(ctx context.Context) {
	log := GetLogger()

	if m.workdirPaneID == "" {
		log.Debug("hideShellPane: no shell pane to hide")
		m.shellPaneHidden = true
		return
	}

	// Save shell width before hiding so we can restore it later
	m.saveShellPaneWidth()

	// Use break-pane to move shell to a hidden background window (keeps process running)
	// -d: don't switch to the new window
	// -n: name the new window so we can find it
	hiddenWindowName := fmt.Sprintf("_hidden_shell_%d", m.task.ID)
	log.Info("hideShellPane: breaking shell pane %q to hidden window %q", m.workdirPaneID, hiddenWindowName)
	err := osExec.CommandContext(ctx, "tmux", "break-pane",
		"-d",
		"-n", hiddenWindowName,
		"-s", m.workdirPaneID,
	).Run()
	if err != nil {
		log.Error("hideShellPane: break-pane failed: %v", err)
		return
	}

	// The pane ID stays the same after break-pane, we just need to track that it's hidden
	m.shellPaneHidden = true
	log.Info("hideShellPane: shell pane hidden successfully")
}

// showShellPane joins the hidden shell pane back, or creates one if needed.
func (m *DetailModel) showShellPane(ctx context.Context) {
	log := GetLogger()

	if m.claudePaneID == "" {
		log.Debug("showShellPane: no Claude pane, cannot show shell")
		return
	}

	shellWidth := m.getShellPaneWidth()

	// Try to join back the hidden shell pane
	if m.workdirPaneID != "" {
		log.Info("showShellPane: joining shell pane %q back to Claude pane", m.workdirPaneID)
		err := osExec.CommandContext(ctx, "tmux", "join-pane",
			"-h", "-l", shellWidth,
			"-s", m.workdirPaneID,
			"-t", m.claudePaneID,
		).Run()
		if err != nil {
			log.Error("showShellPane: join-pane failed: %v, will create new shell", err)
			m.workdirPaneID = "" // Fall through to create new
		} else {
			// Set pane title
			osExec.CommandContext(ctx, "tmux", "select-pane", "-t", m.workdirPaneID, "-T", "Shell").Run()
		}
	}

	// Create new shell if needed
	if m.workdirPaneID == "" {
		log.Info("showShellPane: creating new shell pane")
		workdir := m.getWorkdir()
		userShell := os.Getenv("SHELL")
		if userShell == "" {
			userShell = "/bin/zsh"
		}
		err := osExec.CommandContext(ctx, "tmux", "split-window",
			"-h", "-l", shellWidth,
			"-t", m.claudePaneID,
			"-c", workdir,
			userShell,
		).Run()
		if err != nil {
			log.Error("showShellPane: split-window failed: %v", err)
			return
		}
		// Get the new shell pane ID
		workdirPaneCmd := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
		if workdirPaneOut, err := workdirPaneCmd.Output(); err == nil {
			m.workdirPaneID = strings.TrimSpace(string(workdirPaneOut))
			osExec.CommandContext(ctx, "tmux", "select-pane", "-t", m.workdirPaneID, "-T", "Shell").Run()
		}
	}

	// Select back to TUI pane
	if m.tuiPaneID != "" {
		osExec.CommandContext(ctx, "tmux", "select-pane", "-t", m.tuiPaneID).Run()
	}

	m.shellPaneHidden = false

	// Update stored pane IDs
	if m.database != nil && m.task != nil {
		m.database.UpdateTaskPaneIDs(m.task.ID, m.claudePaneID, m.workdirPaneID)
	}

	log.Info("showShellPane: shell pane shown, workdirPaneID=%q", m.workdirPaneID)
}

// IsShellPaneHidden returns true if the shell pane is currently hidden.
func (m *DetailModel) IsShellPaneHidden() bool {
	return m.shellPaneHidden
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

// findOrCreateTaskWindow finds an existing task window or creates a placeholder.
// Returns the window ID (e.g., "@1234") that can be used for join-pane targeting.
// This prevents duplicate windows by always targeting the same canonical window.
func (m *DetailModel) findOrCreateTaskWindow(ctx context.Context, daemonSession, windowName string) string {
	log := GetLogger()

	// Priority 1: Use stored window ID if available and valid
	if m.task != nil && m.task.TmuxWindowID != "" {
		// Verify window still exists by checking the output (not just exit code)
		// tmux display-message returns exit code 0 even for non-existent windows
		out, err := osExec.CommandContext(ctx, "tmux", "display-message",
			"-t", m.task.TmuxWindowID, "-p", "#{window_id}").Output()
		if err == nil && strings.TrimSpace(string(out)) == m.task.TmuxWindowID {
			log.Debug("findOrCreateTaskWindow: using stored window ID %q", m.task.TmuxWindowID)
			return m.task.TmuxWindowID
		}
		log.Debug("findOrCreateTaskWindow: stored window ID %q is stale (output: %q)", m.task.TmuxWindowID, strings.TrimSpace(string(out)))
		// Clear stale ID
		if m.database != nil {
			m.database.UpdateTaskWindowID(m.task.ID, "")
		}
	}

	// Priority 2: Search for existing window by name (return first match's ID)
	out, err := osExec.CommandContext(ctx, "tmux", "list-windows",
		"-t", daemonSession, "-F", "#{window_id}:#{window_name}").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && parts[1] == windowName {
				windowID := parts[0]
				log.Debug("findOrCreateTaskWindow: found existing window %q", windowID)
				// Update stored ID
				if m.database != nil && m.task != nil {
					m.database.UpdateTaskWindowID(m.task.ID, windowID)
				}
				return windowID
			}
		}
	}

	// Priority 3: Create a new placeholder window
	// We'll use a placeholder command that stays alive
	workDir := m.getWorkdir()
	createErr := osExec.CommandContext(ctx, "tmux", "new-window",
		"-d",
		"-t", daemonSession+":",
		"-n", windowName,
		"-c", workDir,
		"tail", "-f", "/dev/null").Run()
	if createErr != nil {
		log.Error("findOrCreateTaskWindow: failed to create placeholder window: %v", createErr)
		return ""
	}

	// Get the ID of the newly created window
	out, err = osExec.CommandContext(ctx, "tmux", "list-windows",
		"-t", daemonSession, "-F", "#{window_id}:#{window_name}").Output()
	if err == nil {
		// Return LAST match (most recently created)
		var windowID string
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && parts[1] == windowName {
				windowID = parts[0]
			}
		}
		if windowID != "" {
			log.Info("findOrCreateTaskWindow: created new window %q", windowID)
			// Update stored ID
			if m.database != nil && m.task != nil {
				m.database.UpdateTaskWindowID(m.task.ID, windowID)
			}
			return windowID
		}
	}

	log.Error("findOrCreateTaskWindow: could not create or find window")
	return ""
}

// breakTmuxPanes breaks both joined panes - kills workdir, returns Claude to task-daemon.
// If saveHeight is true, the current pane height is always saved to settings.
// If saveHeight is false, dimensions are only saved if the user has resized them
// (to avoid rounding error accumulation during task transitions when dimensions haven't changed).
// If resizeTUI is true, the TUI pane is resized to 100% after breaking. Set to false during
// task switching to avoid layout thrashing (since new panes will be joined immediately).
func (m *DetailModel) breakTmuxPanes(saveHeight bool, resizeTUI bool) {
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
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "status-right", " ").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "pane-border-lines", "single").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "pane-border-indicators", "off").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "pane-border-style", "fg=#374151").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "pane-active-border-style", "fg=#61AFEF").Run()

	// Reset window styling (remove inactive pane de-emphasis)
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "window-style", "default").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", m.uiSessionName, "window-active-style", "default").Run()

	// Unbind Shift+Arrow keybindings that were set in joinTmuxPanes
	osExec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "S-Down").Run()
	osExec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "S-Right").Run()
	osExec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "S-Up").Run()
	osExec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "S-Left").Run()

	// Unbind Alt+Shift+Arrow task navigation keybindings
	osExec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "M-S-Up").Run()
	osExec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "M-S-Down").Run()

	// Reset pane title back to main view label
	osExec.CommandContext(ctx, "tmux", "select-pane", "-t", "task-ui:.0", "-T", "Tasks").Run()

	// Break the Claude pane back to task-daemon
	if m.claudePaneID == "" {
		log.Debug("breakTmuxPanes: no Claude pane, cleaning up")
		// Even if we don't have a Claude pane, we may have a shell pane that needs cleanup
		if m.workdirPaneID != "" {
			// Kill the orphaned shell pane and its process
			log.Debug("breakTmuxPanes: killing orphaned shell pane %q", m.workdirPaneID)
			m.killPaneWithProcess(ctx, m.workdirPaneID)
			m.workdirPaneID = ""
		}
		// Ensure TUI pane is full size (only when returning to dashboard)
		if resizeTUI {
			osExec.CommandContext(ctx, "tmux", "resize-pane", "-t", "task-ui:.0", "-y", "100%").Run()
		}
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

	// Find or create the canonical task window (prevents duplicates)
	// Uses stored TmuxWindowID if valid, otherwise searches by name or creates new
	targetWindowID := m.findOrCreateTaskWindow(ctx, daemonSession, windowName)
	if targetWindowID == "" {
		log.Error("breakTmuxPanes: could not find or create task window")
		// Fallback: kill panes AND their processes to avoid orphans
		m.killPaneWithProcess(ctx, m.claudePaneID)
		if m.workdirPaneID != "" {
			m.killPaneWithProcess(ctx, m.workdirPaneID)
			m.workdirPaneID = ""
		}
		m.claudePaneID = ""
		m.daemonSessionID = ""
		if resizeTUI {
			osExec.CommandContext(ctx, "tmux", "resize-pane", "-t", "task-ui:.0", "-y", "100%").Run()
		}
		log.Info("breakTmuxPanes: completed with error cleanup")
		return
	}
	log.Debug("breakTmuxPanes: using window ID %q", targetWindowID)

	// Check if the target window has a placeholder pane (from findOrCreateTaskWindow)
	// If so, we need to kill it after joining our panes
	paneCountOut, _ := osExec.CommandContext(ctx, "tmux", "display-message", "-t", targetWindowID, "-p", "#{window_panes}").Output()
	hadPlaceholder := strings.TrimSpace(string(paneCountOut)) == "1"

	// Join the Claude pane to the task window using window ID (not name - avoids duplicates)
	// -d: don't switch focus
	// -s: source pane (the Claude pane currently in task-ui)
	// -t: target window (the canonical task window)
	log.Info("breakTmuxPanes: joining Claude pane %q to window %q", m.claudePaneID, targetWindowID)
	joinErr := osExec.CommandContext(ctx, "tmux", "join-pane",
		"-d",
		"-s", m.claudePaneID,
		"-t", targetWindowID).Run()
	if joinErr != nil {
		log.Error("breakTmuxPanes: join-pane for Claude failed: %v", joinErr)
		// Join failed - but DON'T kill the Claude process!
		// Preserving the user's running Claude session is more important than clean UI.
		// Instead, try to break the pane to a new window in the daemon session.
		log.Info("breakTmuxPanes: attempting to create new window for Claude pane")
		newWindowErr := osExec.CommandContext(ctx, "tmux", "break-pane",
			"-d", // don't switch focus
			"-s", m.claudePaneID,
			"-t", daemonSession+":",
			"-n", windowName).Run()
		if newWindowErr != nil {
			log.Warn("breakTmuxPanes: break-pane also failed: %v - leaving pane in place", newWindowErr)
			// Last resort: leave panes in task-ui rather than kill Claude
			// The panes will be visible but Claude keeps running
		} else {
			log.Info("breakTmuxPanes: created new window for Claude via break-pane")
		}
		// Clean up shell pane (it's just a shell, safe to kill)
		if m.workdirPaneID != "" {
			osExec.CommandContext(ctx, "tmux", "kill-pane", "-t", m.workdirPaneID).Run()
			m.workdirPaneID = ""
		}
		m.claudePaneID = ""
		m.daemonSessionID = ""
		if resizeTUI {
			osExec.CommandContext(ctx, "tmux", "resize-pane", "-t", "task-ui:.0", "-y", "100%").Run()
		}
		log.Info("breakTmuxPanes: completed with graceful error handling")
		return
	}
	log.Debug("breakTmuxPanes: Claude pane joined successfully")

	// If we had a placeholder, kill it now (it's pane .0, Claude is now .1)
	if hadPlaceholder {
		osExec.CommandContext(ctx, "tmux", "kill-pane", "-t", targetWindowID+".0").Run()
		log.Debug("breakTmuxPanes: killed placeholder pane")
	}

	// If we have a workdir pane, join it to the task window alongside Claude
	// This preserves any running processes (Rails servers, watchers, etc.)
	if m.workdirPaneID != "" {
		// If the shell pane is hidden, leave it in the hidden window
		// The user explicitly hid it to preserve their running process
		if m.shellPaneHidden {
			log.Info("breakTmuxPanes: shell pane is hidden, leaving in hidden window to preserve process")
			// Don't clear workdirPaneID - we still track the pane for later
		} else {
			log.Debug("breakTmuxPanes: joining workdir pane %q to window %q", m.workdirPaneID, targetWindowID)

			// Join the workdir pane horizontally to the right of the Claude pane
			// -h: horizontal split (side by side)
			// -d: don't switch focus
			// -s: source pane (the workdir pane)
			// -t: target window's first pane (Claude)
			joinErr := osExec.CommandContext(ctx, "tmux", "join-pane",
				"-h",
				"-d",
				"-s", m.workdirPaneID,
				"-t", targetWindowID+".0").Run()
			if joinErr != nil {
				log.Error("breakTmuxPanes: join-pane for workdir failed: %v", joinErr)
				// Join failed - DO NOT kill the process! The user may have important work running.
				// Just log the error and leave the pane where it is. It may be orphaned but
				// preserving the user's process is more important than cleanup.
				log.Warn("breakTmuxPanes: leaving workdir pane in place to preserve running process")
			} else {
				log.Debug("breakTmuxPanes: workdir pane joined successfully")
			}
		}
	}

	// Save the new pane IDs in the daemon window to the database
	// This ensures we can reliably identify panes when joining again later
	m.saveDaemonPaneIDs(ctx, targetWindowID)

	// Resize the TUI pane back to full window size now that the splits are gone
	// This ensures the kanban view has the full window to render
	// Skip during task switching to avoid layout thrashing
	if resizeTUI {
		osExec.CommandContext(ctx, "tmux", "resize-pane", "-t", "task-ui:.0", "-y", "100%").Run()
	}

	m.claudePaneID = ""
	m.daemonSessionID = ""
	log.Info("breakTmuxPanes: completed for task %d", m.task.ID)
}

// saveDaemonPaneIDs saves the pane IDs to the database.
// This uses the already-known pane IDs (m.claudePaneID, m.workdirPaneID) rather than
// querying by index, since tmux pane indices can change when panes are reordered.
func (m *DetailModel) saveDaemonPaneIDs(ctx context.Context, targetWindowID string) {
	log := GetLogger()

	if m.task == nil || m.database == nil {
		return
	}

	// Use the pane IDs we already know - don't re-query by index since indices can change
	claudePaneID := m.claudePaneID
	shellPaneID := m.workdirPaneID

	if claudePaneID == "" {
		log.Warn("saveDaemonPaneIDs: no Claude pane ID to save")
		return
	}

	// Save to database
	if err := m.database.UpdateTaskPaneIDs(m.task.ID, claudePaneID, shellPaneID); err != nil {
		log.Warn("saveDaemonPaneIDs: failed to save pane IDs: %v", err)
		return
	}

	// Update local task object
	m.task.ClaudePaneID = claudePaneID
	m.task.ShellPaneID = shellPaneID

	log.Debug("saveDaemonPaneIDs: saved pane IDs for task %d: claude=%q, shell=%q", m.task.ID, claudePaneID, shellPaneID)
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

	// Check the shell pane
	if m.workdirPaneID == "" {
		return false
	}
	paneToCheck := m.workdirPaneID

	// Get the current command in the shell pane
	out, err := osExec.CommandContext(ctx, "tmux", "display-message", "-t", paneToCheck, "-p", "#{pane_current_command}").Output()
	if err != nil {
		return false
	}

	command := strings.TrimSpace(string(out))
	return command != "" && command != userShell
}

// killPaneWithProcess kills a tmux pane AND the process running inside it.
// This prevents orphaned processes when panes are closed.
func (m *DetailModel) killPaneWithProcess(ctx context.Context, paneID string) {
	if paneID == "" {
		return
	}
	log := GetLogger()

	// Get the PID of the process running in the pane
	pidCmd := osExec.CommandContext(ctx, "tmux", "display-message", "-t", paneID, "-p", "#{pane_pid}")
	if pidOut, err := pidCmd.Output(); err == nil {
		pid := strings.TrimSpace(string(pidOut))
		if pid != "" {
			log.Debug("killPaneWithProcess: killing process %s in pane %s", pid, paneID)
			// Use SIGKILL - Claude processes ignore SIGTERM
			osExec.CommandContext(ctx, "kill", "-9", pid).Run()
		}
	}

	// Now kill the pane
	osExec.CommandContext(ctx, "tmux", "kill-pane", "-t", paneID).Run()
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
	out, err := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}").Output()
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

	// Global dangerous mode banner
	var dangerBanner string
	if IsGlobalDangerousMode() {
		dangerStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#E06C75")). // Red background
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true).
			Padding(0, 2).
			Width(m.width)
		dangerBanner = dangerStyle.Render(IconBlocked() + " DANGEROUS MODE ENABLED")
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

	renderedBox := box.Render(boxContent)

	// When shell pane is hidden, show a collapsed indicator on the right
	if m.shellPaneHidden && os.Getenv("TMUX") != "" {
		// Create vertical "Shell" label - each character on its own line
		shellLabel := "S\nh\ne\nl\nl"

		// Style for the collapsed shell tab
		tabStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#3B4252")). // Dark muted background
			Foreground(lipgloss.Color("#88C0D0")). // Teal text (matches shell theme)
			Bold(true).
			Padding(1, 0).
			Align(lipgloss.Center)

		// Calculate height to match the box
		boxHeight := lipgloss.Height(renderedBox)
		shellTab := tabStyle.Height(boxHeight).Render(shellLabel)

		// Join the box and the shell tab horizontally
		renderedBox = lipgloss.JoinHorizontal(lipgloss.Top, renderedBox, shellTab)
	}

	// Build view parts
	var viewParts []string
	if dangerBanner != "" {
		viewParts = append(viewParts, dangerBanner)
	}
	viewParts = append(viewParts, renderedBox, help)

	return lipgloss.JoinVertical(lipgloss.Left, viewParts...)
}

func (m *DetailModel) renderHeader() string {
	t := m.task

	// When unfocused, use muted styles for badges
	dimmedBg := lipgloss.Color("#4B5563")     // Muted gray background
	dimmedFg := lipgloss.Color("#9CA3AF")     // Muted gray foreground
	dimmedTextFg := lipgloss.Color("#6B7280") // Even more muted for text

	// Task title is shown in the tmux pane border, so we don't duplicate it here

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

	if t.Pinned {
		var pinStyle lipgloss.Style
		if m.focused {
			pinStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(ColorWarning).
				Foreground(lipgloss.Color("#000000")).
				Bold(true)
		} else {
			pinStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(dimmedBg).
				Foreground(dimmedFg)
		}
		meta.WriteString(pinStyle.Render("PINNED"))
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

	// Pane loading indicator
	if m.paneLoading {
		meta.WriteString("  ")
		elapsed := time.Since(m.paneLoadingStart)
		frameIndex := int(elapsed.Milliseconds()/100) % len(spinnerFrames)
		spinner := spinnerFrames[frameIndex]
		var loadingStyle lipgloss.Style
		if m.focused {
			loadingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // Orange
		} else {
			loadingStyle = lipgloss.NewStyle().Foreground(dimmedFg)
		}
		loadingText := fmt.Sprintf("%s Starting %s...", spinner, m.executorDisplayName())
		meta.WriteString(loadingStyle.Render(loadingText))
	}

	// Executor failure indicator
	if m.paneError != "" {
		meta.WriteString("  ")
		var errorStyle lipgloss.Style
		if m.focused {
			errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
		} else {
			errorStyle = lipgloss.NewStyle().Foreground(dimmedFg)
		}
		meta.WriteString(errorStyle.Render("⚠ " + m.paneError))
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

	// Build the first line with optional notification indicator
	metaStr := meta.String()

	// Add notification indicator if there's an active notification for a different task
	var firstLine string
	if m.HasNotification() {
		// Create a subtle notification indicator
		notifyStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#4B5563")). // Muted gray background
			Foreground(lipgloss.Color("#FFCC00")). // Yellow text
			Padding(0, 1)
		notifyIndicator := notifyStyle.Render(fmt.Sprintf("%s Task #%d (ctrl+g)", IconBlocked(), m.notifyTaskID))

		// Add notification with spacing
		firstLine = metaStr + "  " + notifyIndicator
	} else {
		firstLine = metaStr
	}

	// Create a block for the right-aligned content
	rightContent := []string{firstLine}
	if prLine != "" {
		rightContent = append(rightContent, prLine)
	}
	rightBlock := lipgloss.JoinVertical(lipgloss.Right, rightContent...)

	// Render the block aligned to the right of the available space
	headerLayout := lipgloss.NewStyle().
		Width(m.width - 4).
		Align(lipgloss.Right).
		Render(rightBlock)

	return lipgloss.JoinVertical(lipgloss.Left, headerLayout, "")
}

// getGlamourRenderer returns a cached Glamour renderer, creating it if needed.
// Renderers are cached separately for focused and unfocused states.
func (m *DetailModel) getGlamourRenderer(focused bool) *glamour.TermRenderer {
	targetWidth := m.width - 4

	// Invalidate cache if width changed
	if m.glamourWidth != targetWidth {
		m.glamourRendererFocused = nil
		m.glamourRendererUnfocused = nil
		m.glamourWidth = targetWidth
	}

	if focused {
		if m.glamourRendererFocused == nil {
			renderer, err := glamour.NewTermRenderer(
				glamour.WithStylePath("dark"),
				glamour.WithWordWrap(targetWidth),
			)
			if err == nil {
				m.glamourRendererFocused = renderer
			}
		}
		return m.glamourRendererFocused
	}

	if m.glamourRendererUnfocused == nil {
		renderer, err := glamour.NewTermRenderer(
			glamour.WithStylePath("notty"),
			glamour.WithWordWrap(targetWidth),
		)
		if err == nil {
			m.glamourRendererUnfocused = renderer
		}
	}
	return m.glamourRendererUnfocused
}

// computeLogHash computes a simple hash of the logs for change detection.
func (m *DetailModel) computeLogHash() uint64 {
	if len(m.logs) == 0 {
		return 0
	}
	// Use length and last log timestamp as a fast proxy for changes
	hash := uint64(len(m.logs))
	if len(m.logs) > 0 {
		hash ^= uint64(m.logs[len(m.logs)-1].CreatedAt.UnixNano())
	}
	return hash
}

func (m *DetailModel) renderContent() string {
	t := m.task

	// Check if we can use cached content
	logHash := m.computeLogHash()
	if m.cachedContent != "" &&
		m.lastRenderedBody == t.Body &&
		m.lastRenderedLogHash == logHash &&
		m.lastRenderedFocused == m.focused {
		return m.cachedContent
	}

	var b strings.Builder

	// Dimmed style for unfocused content
	dimmedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))

	// Description
	if t.Body != "" && strings.TrimSpace(t.Body) != "" {
		// Labels always use full opacity for clarity and accessibility
		b.WriteString(Bold.Render("Description"))
		b.WriteString("\n\n")

		// Use cached renderer
		renderer := m.getGlamourRenderer(m.focused)
		if renderer == nil {
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
					b.WriteString(dimmedStyle.Render(strings.TrimSpace(rendered)))
				}
			}
		}
		b.WriteString("\n")
	}

	// Execution logs
	if len(m.logs) > 0 {
		b.WriteString("\n")
		// Labels always use full opacity for clarity and accessibility
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

	content := b.String()

	// Cache the rendered content
	m.lastRenderedBody = t.Body
	m.lastRenderedLogHash = logHash
	m.lastRenderedFocused = m.focused
	m.cachedContent = content

	return content
}

func (m *DetailModel) renderHelp() string {
	type helpKey struct {
		key      string
		desc     string
		disabled bool // When disabled, always show grayed out
	}

	// Check if navigation is available (more than 1 task in column)
	hasNavigation := m.totalInColumn > 1

	keys := []helpKey{
		{IconArrowUp() + "/" + IconArrowDown(), "prev/next task", !hasNavigation},
	}

	// Show scroll hint when content is scrollable
	if m.viewport.TotalLineCount() > m.viewport.VisibleLineCount() {
		keys = append(keys, helpKey{"PgUp/Dn", "scroll", false})
	}

	// Only show execute/retry when Claude is not running
	claudeRunning := m.claudeMemoryMB > 0
	if !claudeRunning {
		keys = append(keys, helpKey{"x", "execute", false})
	}

	hasPanes := m.claudePaneID != "" || m.workdirPaneID != ""

	keys = append(keys, helpKey{"e", "edit", false})

	// Only show retry when Claude is not running
	if !claudeRunning {
		keys = append(keys, helpKey{"r", "retry", false})
	}

	// Always show status change option
	keys = append(keys, helpKey{"S", "status", false})

	if m.task != nil {
		pinDesc := "pin task"
		if m.task.Pinned {
			pinDesc = "unpin task"
		}
		keys = append(keys, helpKey{"t", pinDesc, false})
	}

	// Show dangerous mode toggle when task is processing or blocked
	if m.task != nil && (m.task.Status == db.StatusProcessing || m.task.Status == db.StatusBlocked) {
		toggleDesc := "dangerous mode"
		if m.task.DangerousMode {
			toggleDesc = "safe mode"
		}
		keys = append(keys, helpKey{"!", toggleDesc, false})
	}

	// Show pane navigation shortcut when panes are visible
	if hasPanes && os.Getenv("TMUX") != "" {
		keys = append(keys, helpKey{"shift+" + IconArrowUp() + IconArrowDown(), "switch pane", false})
		// Show shell pane toggle shortcut
		toggleDesc := "hide shell"
		if m.shellPaneHidden {
			toggleDesc = "show shell"
		}
		keys = append(keys, helpKey{"\\", toggleDesc, false})
	}

	// Show jump to notification shortcut when there's an active notification
	if m.HasNotification() {
		keys = append(keys, helpKey{"ctrl+g", "jump to notification", false})
	}

	keys = append(keys, []helpKey{
		{"c", "close", false},
		{"a", "archive", false},
		{"d", "delete", false},
		{"esc", "back", false},
	}...)

	var help string
	dimmedKeyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	dimmedDescStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#4B5563"))

	for i, k := range keys {
		if i > 0 {
			help += "  "
		}
		// Disabled keys are always dimmed, regardless of focus
		if k.disabled || !m.focused {
			help += dimmedKeyStyle.Render(k.key) + " " + dimmedDescStyle.Render(k.desc)
		} else {
			help += HelpKey.Render(k.key) + " " + HelpDesc.Render(k.desc)
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
	out, err := osExec.CommandContext(ctx, "tmux", "display-message", "-t", paneTarget, "-p", "#{pane_pid}").Output()
	if err != nil {
		return 0
	}

	shellPID := strings.TrimSpace(string(out))
	if shellPID == "" {
		return 0
	}

	// Find claude child process
	childOut, err := osExec.CommandContext(ctx, "pgrep", "-P", shellPID, "claude").Output()
	var pid string
	if err == nil && len(childOut) > 0 {
		pid = strings.TrimSpace(string(childOut))
	} else {
		pid = shellPID // fallback to shell
	}

	// Get RSS in KB from ps
	psOut, err := osExec.CommandContext(ctx, "ps", "-o", "rss=", "-p", pid).Output()
	if err != nil {
		return 0
	}

	rssKB, err := strconv.Atoi(strings.TrimSpace(string(psOut)))
	if err != nil {
		return 0
	}

	return rssKB / 1024 // Convert to MB
}
