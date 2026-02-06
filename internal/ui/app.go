package ui

import (
	"context"
	"fmt"
	"os"
	osExec "os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/ai"
	"github.com/bborn/workflow/internal/autocomplete"
	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/github"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"
	"github.com/muesli/termenv"
)

// View represents the current view.
type View int

const (
	ViewDashboard View = iota
	ViewDetail
	ViewNewTask
	ViewNewTaskConfirm
	ViewEditTask
	ViewProjectChangeConfirm // Confirmation when changing a task's project
	ViewDeleteConfirm
	ViewCloseConfirm
	ViewArchiveConfirm
	ViewQuitConfirm
	ViewSettings
	ViewRetry
	ViewAttachments
	ViewChangeStatus
	ViewCommandPalette
)

// KeyMap defines key bindings.
type KeyMap struct {
	Left            key.Binding
	Right           key.Binding
	Up              key.Binding
	Down            key.Binding
	Enter           key.Binding
	Back            key.Binding
	New             key.Binding
	Edit            key.Binding
	Queue           key.Binding
	Retry           key.Binding
	Close           key.Binding
	Archive         key.Binding
	Delete          key.Binding
	Refresh         key.Binding
	Settings        key.Binding
	Help            key.Binding
	Quit            key.Binding
	ChangeStatus    key.Binding
	CommandPalette  key.Binding
	ToggleDangerous key.Binding
	TogglePin       key.Binding
	Filter       key.Binding
	OpenWorktree key.Binding
	ToggleShellPane          key.Binding
	JumpToNotification key.Binding
	// Column focus shortcuts
	FocusBacklog    key.Binding
	FocusInProgress key.Binding
	FocusBlocked    key.Binding
	FocusDone       key.Binding
	// Jump to pinned/unpinned tasks
	JumpToPinned   key.Binding
	JumpToUnpinned key.Binding
	// Open browser
	OpenBrowser key.Binding
}

// ShortHelp returns key bindings to show in the mini help.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Left, k.Right, k.Up, k.Down, k.Enter, k.New, k.Queue, k.Filter, k.CommandPalette, k.OpenBrowser, k.Quit}
}

// FullHelp returns keybindings for the expanded help view.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Left, k.Right, k.Up, k.Down},
		{k.JumpToPinned, k.JumpToUnpinned},
		{k.FocusBacklog, k.FocusInProgress, k.FocusBlocked, k.FocusDone},
		{k.Enter, k.New, k.Queue, k.Close},
		{k.Retry, k.Archive, k.Delete, k.OpenWorktree, k.OpenBrowser},
		{k.Filter, k.CommandPalette, k.Settings},
		{k.ChangeStatus, k.TogglePin, k.Refresh, k.Help},
		{k.Quit},
	}
}

// DefaultKeyMap returns the default key bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Left: key.NewBinding(
			key.WithKeys("left"),
			key.WithHelp(IconArrowLeft(), "prev col"),
		),
		Right: key.NewBinding(
			key.WithKeys("right"),
			key.WithHelp(IconArrowRight(), "next col"),
		),
		Up: key.NewBinding(
			key.WithKeys("up"),
			key.WithHelp(IconArrowUp(), "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down"),
			key.WithHelp(IconArrowDown(), "down"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "view"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		New: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "new"),
		),
		Edit: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "edit"),
		),
		Queue: key.NewBinding(
			key.WithKeys("x"),
			key.WithHelp("x", "execute"),
		),
		Retry: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "retry"),
		),
		Close: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "close"),
		),
		Archive: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "archive"),
		),
		Delete: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "delete"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("R"),
			key.WithHelp("R", "refresh"),
		),
		Settings: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "settings"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		ChangeStatus: key.NewBinding(
			key.WithKeys("S"),
			key.WithHelp("S", "status"),
		),
		CommandPalette: key.NewBinding(
			key.WithKeys("p", "ctrl+p"),
			key.WithHelp("p/ctrl+p", "go to task"),
		),
		ToggleDangerous: key.NewBinding(
			key.WithKeys("!"),
			key.WithHelp("!", "dangerous mode"),
		),
		TogglePin: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "pin/unpin"),
		),
		Filter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "filter"),
		),
		OpenWorktree: key.NewBinding(
			key.WithKeys("o"),
			key.WithHelp("o", "open in editor"),
		),
		ToggleShellPane: key.NewBinding(
			key.WithKeys("\\"),
			key.WithHelp("\\", "toggle shell"),
		),
		JumpToNotification: key.NewBinding(
			key.WithKeys("g"),
			key.WithHelp("g", "go to notification"),
		),
		FocusBacklog: key.NewBinding(
			key.WithKeys("B"),
			key.WithHelp("B", "backlog"),
		),
		FocusInProgress: key.NewBinding(
			key.WithKeys("P"),
			key.WithHelp("P", "in progress"),
		),
		FocusBlocked: key.NewBinding(
			key.WithKeys("L"),
			key.WithHelp("L", "blocked"),
		),
		FocusDone: key.NewBinding(
			key.WithKeys("D"),
			key.WithHelp("D", "done"),
		),
		JumpToPinned: key.NewBinding(
			key.WithKeys("shift+up"),
			key.WithHelp(IconShiftUp(), "jump to pinned"),
		),
		JumpToUnpinned: key.NewBinding(
			key.WithKeys("shift+down"),
			key.WithHelp(IconShiftDown(), "jump to unpinned"),
		),
		OpenBrowser: key.NewBinding(
			key.WithKeys("b"),
			key.WithHelp("b", "open in browser"),
		),
	}
}

// ApplyKeybindingsConfig applies custom keybindings from a config to the KeyMap.
// Only non-nil config values override the defaults.
func ApplyKeybindingsConfig(km KeyMap, cfg *config.KeybindingsConfig) KeyMap {
	if cfg == nil {
		return km
	}

	// Helper to create a key binding from config
	applyBinding := func(current key.Binding, kc *config.KeybindingConfig) key.Binding {
		if kc == nil || len(kc.Keys) == 0 {
			return current
		}
		help := kc.Help
		if help == "" {
			// Preserve existing help text if not specified
			help = current.Help().Key
		}
		return key.NewBinding(
			key.WithKeys(kc.Keys...),
			key.WithHelp(kc.Keys[0], help),
		)
	}

	// Apply each binding if configured
	km.Left = applyBinding(km.Left, cfg.Left)
	km.Right = applyBinding(km.Right, cfg.Right)
	km.Up = applyBinding(km.Up, cfg.Up)
	km.Down = applyBinding(km.Down, cfg.Down)
	km.Enter = applyBinding(km.Enter, cfg.Enter)
	km.Back = applyBinding(km.Back, cfg.Back)
	km.New = applyBinding(km.New, cfg.New)
	km.Edit = applyBinding(km.Edit, cfg.Edit)
	km.Queue = applyBinding(km.Queue, cfg.Queue)
	km.Retry = applyBinding(km.Retry, cfg.Retry)
	km.Close = applyBinding(km.Close, cfg.Close)
	km.Archive = applyBinding(km.Archive, cfg.Archive)
	km.Delete = applyBinding(km.Delete, cfg.Delete)
	km.Refresh = applyBinding(km.Refresh, cfg.Refresh)
	km.Settings = applyBinding(km.Settings, cfg.Settings)
	km.Help = applyBinding(km.Help, cfg.Help)
	km.Quit = applyBinding(km.Quit, cfg.Quit)
	km.ChangeStatus = applyBinding(km.ChangeStatus, cfg.ChangeStatus)
	km.CommandPalette = applyBinding(km.CommandPalette, cfg.CommandPalette)
	km.ToggleDangerous = applyBinding(km.ToggleDangerous, cfg.ToggleDangerous)
	km.TogglePin = applyBinding(km.TogglePin, cfg.TogglePin)
	km.Filter = applyBinding(km.Filter, cfg.Filter)
	km.OpenWorktree = applyBinding(km.OpenWorktree, cfg.OpenWorktree)
	km.ToggleShellPane = applyBinding(km.ToggleShellPane, cfg.ToggleShellPane)
	km.JumpToNotification = applyBinding(km.JumpToNotification, cfg.JumpToNotification)
	km.FocusBacklog = applyBinding(km.FocusBacklog, cfg.FocusBacklog)
	km.FocusInProgress = applyBinding(km.FocusInProgress, cfg.FocusInProgress)
	km.FocusBlocked = applyBinding(km.FocusBlocked, cfg.FocusBlocked)
	km.FocusDone = applyBinding(km.FocusDone, cfg.FocusDone)
	km.JumpToPinned = applyBinding(km.JumpToPinned, cfg.JumpToPinned)
	km.JumpToUnpinned = applyBinding(km.JumpToUnpinned, cfg.JumpToUnpinned)
	km.OpenBrowser = applyBinding(km.OpenBrowser, cfg.OpenBrowser)

	return km
}

// LoadKeyMap loads the KeyMap from the config file, falling back to defaults.
func LoadKeyMap() KeyMap {
	km := DefaultKeyMap()

	cfg, err := config.LoadKeybindings()
	if err != nil {
		// Log error but continue with defaults
		log := GetLogger()
		log.Error("Failed to load keybindings config: %v", err)
		return km
	}

	return ApplyKeybindingsConfig(km, cfg)
}

// AppModel is the main application model.
type AppModel struct {
	db       *db.DB
	executor *executor.Executor
	keys     KeyMap
	help     help.Model

	// Working directory context (for project detection)
	workingDir string

	currentView  View
	previousView View

	// Dashboard state
	tasks        []*db.Task
	kanban       *KanbanBoard
	loading      bool
	err          error
	notification string    // Notification banner text
	notifyUntil  time.Time // When to hide notification
	notifyTaskID int64     // Task ID that triggered the notification (for jumping to it)

	// Track task statuses to detect changes
	prevStatuses map[int64]string
	// Track tasks with active input notifications (for UI highlighting)
	tasksNeedingInput map[int64]bool
	// Track tasks the user closed manually (suppress notification for these)
	userClosedTaskIDs map[int64]bool

	// Real-time event subscription
	eventCh chan executor.TaskEvent

	// File watcher for database changes
	watcher    *fsnotify.Watcher
	dbChangeCh chan struct{}

	// PR status cache
	prCache              *github.PRCache
	initialPRRefreshDone bool // Track if initial PR refresh after load is done

	// Keyboard shortcut state for double-digit shortcuts (11, 22, etc.)
	pendingShortcutDigit string    // First digit of a potential double-digit shortcut
	shortcutTimeout      time.Time // When the pending digit expires

	// Detail view state
	selectedTask *db.Task
	detailView   *DetailModel
	// Prevent rapid arrow key navigation from causing duplicate panes
	taskTransitionInProgress bool
	// Grace period after task transition to prevent focus flashing
	taskTransitionGraceUntil time.Time

	// New task form state
	newTaskForm        *FormModel
	pendingTask        *db.Task
	pendingAttachments []string
	queueConfirm       *huh.Form
	queueValue         bool

	// Edit task form state
	editTaskForm *FormModel
	editingTask  *db.Task

	// Project change confirmation state (when changing a task's project)
	projectChangeConfirm      *huh.Form
	projectChangeConfirmValue bool
	pendingProjectChangeTask  *db.Task // The updated task data with new project
	originalProjectChangeTask *db.Task // The original task to delete

	// Delete confirmation state
	deleteConfirm      *huh.Form
	deleteConfirmValue bool
	pendingDeleteTask  *db.Task

	// Quit confirmation state
	quitConfirm      *huh.Form
	quitConfirmValue bool

	// Close confirmation state
	closeConfirm      *huh.Form
	closeConfirmValue bool
	pendingCloseTask  *db.Task

	// Archive confirmation state
	archiveConfirm      *huh.Form
	archiveConfirmValue bool
	pendingArchiveTask  *db.Task

	// Settings view state
	settingsView *SettingsModel

	// Retry view state
	retryView *RetryModel

	// Attachments view state
	attachmentsView *AttachmentsModel

	// Change status view state
	changeStatusForm        *huh.Form
	changeStatusValue       string
	pendingChangeStatusTask *db.Task

	// Command palette view state
	commandPaletteView *CommandPaletteModel

	// AI command service for natural language command interpretation
	aiCommandService *ai.CommandService

	// Filter state
	filterInput        textinput.Model
	filterActive       bool   // Whether filter mode is active (typing in filter)
	filterText         string // Current filter text (persists when not typing)
	filterAutocomplete *FilterAutocompleteModel
	showFilterDropdown bool // Whether to show the project autocomplete dropdown

	// Available executors (cached on startup)
	availableExecutors []string

	// Window size
	width  int
	height int

	// Debug state file path
	debugStatePath string

	// First-time experience
	isFirstLoad     bool // Track if this is the first load of tasks
	showWelcome     bool // Show welcome message when kanban is empty
	onboardingShown bool // Track if we've already shown the onboarding (to prevent double-triggering)
}

// taskExecutorDisplayName returns the display name for a task's executor.
// Uses the task's Executor field to determine the correct name.
func taskExecutorDisplayName(task *db.Task) string {
	if task == nil || task.Executor == "" {
		return executor.DefaultExecutorName()
	}
	switch task.Executor {
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
		if len(task.Executor) > 0 {
			return strings.ToUpper(task.Executor[:1]) + task.Executor[1:]
		}
		return executor.DefaultExecutorName()
	}
}

// updateTaskInList updates a task in the tasks list and refreshes the kanban.
func (m *AppModel) updateTaskInList(task *db.Task) {
	for i, t := range m.tasks {
		if t.ID == task.ID {
			m.tasks[i] = task
			break
		}
	}
	m.kanban.SetTasks(m.tasks)
}

// NewAppModel creates a new application model.
func NewAppModel(database *db.DB, exec *executor.Executor, workingDir string) *AppModel {
	// Initialize logger and log startup
	log := GetLogger()
	log.Info("=== TaskYou TUI starting ===")
	log.Info("NewAppModel: workingDir=%q", workingDir)

	// Force TrueColor mode to ensure our theme colors render correctly.
	// Without this, some terminals (especially when accessed via SSH) may
	// approximate colors incorrectly, causing issues like the blocked column
	// appearing gray instead of red.
	lipgloss.SetColorProfile(termenv.TrueColor)

	// Load saved theme from database
	LoadThemeFromDB(database.GetSetting)

	// Load project colors into cache
	LoadProjectColors(database)

	// Start with zero size - will be set by WindowSizeMsg
	kanban := NewKanbanBoard(0, 0)

	// Setup help
	h := help.New()
	h.ShowAll = false

	// Setup file watcher for database changes
	watcher, _ := fsnotify.NewWatcher()
	dbChangeCh := make(chan struct{}, 1)

	// Setup filter input
	filterInput := textinput.New()
	filterInput.Placeholder = "Filter text, #id, or [project..."
	filterInput.CharLimit = 50

	// Get available executors for form filtering and warnings
	var availableExecutors []string
	if exec != nil {
		availableExecutors = exec.AvailableExecutors()
	}

	// Create filter autocomplete for project suggestions
	filterAutocomplete := NewFilterAutocompleteModel(database)

	// Initialize AI command service (uses ANTHROPIC_API_KEY or database setting)
	var apiKey string
	if database != nil {
		apiKey, _ = database.GetSetting("anthropic_api_key")
	}
	aiCmdService := ai.NewCommandService(apiKey)

	model := &AppModel{
		db:                 database,
		executor:           exec,
		workingDir:         workingDir,
		keys:               LoadKeyMap(),
		help:               h,
		currentView:        ViewDashboard,
		kanban:             kanban,
		loading:            true,
		prevStatuses:       make(map[int64]string),
		tasksNeedingInput:  make(map[int64]bool),
		userClosedTaskIDs:  make(map[int64]bool),
		watcher:            watcher,
		dbChangeCh:         dbChangeCh,
		prCache:            github.NewPRCache(),
		filterInput:        filterInput,
		filterText:         "",
		filterAutocomplete: filterAutocomplete,
		availableExecutors: availableExecutors,
		aiCommandService:   aiCmdService,
		isFirstLoad:        true,  // Track first load for onboarding
		showWelcome:        false, // Will be set true when kanban is empty
	}

	return model
}

// SetTasks sets the tasks for the model (useful for debugging/testing).
func (m *AppModel) SetTasks(tasks []*db.Task) {
	m.tasks = tasks
	m.loading = false
	m.kanban.SetTasks(tasks)
}

// SetDebugStatePath sets the path for dumping debug state.
func (m *AppModel) SetDebugStatePath(path string) {
	m.debugStatePath = path
}

// Init initializes the model.
func (m *AppModel) Init() tea.Cmd {
	// Subscribe to real-time task events
	m.eventCh = m.executor.SubscribeTaskEvents()

	// Start watching database file for changes
	m.startDatabaseWatcher()

	// Enable mouse support for click-to-focus on tmux panes
	if os.Getenv("TMUX") != "" {
		// Get actual session name to avoid prefix-matching wrong session
		if out, err := osExec.Command("tmux", "display-message", "-p", "#{session_name}").Output(); err == nil {
			sessionName := strings.TrimSpace(string(out))
			osExec.Command("tmux", "set-option", "-t", sessionName, "mouse", "on").Run()
		}
	}

	return tea.Batch(m.loadTasks(), m.waitForTaskEvent(), m.waitForDBChange(), m.tick(), m.prRefreshTick())
}

// Update handles messages.
func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if sizeMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.applyWindowSize(sizeMsg.Width, sizeMsg.Height)
	}

	// Handle form updates first (needs all message types)
	if m.currentView == ViewNewTask && m.newTaskForm != nil {
		return m.updateNewTaskForm(msg)
	}
	if m.currentView == ViewEditTask && m.editTaskForm != nil {
		return m.updateEditTaskForm(msg)
	}
	if m.currentView == ViewNewTaskConfirm && m.queueConfirm != nil {
		return m.updateNewTaskConfirm(msg)
	}
	if m.currentView == ViewProjectChangeConfirm && m.projectChangeConfirm != nil {
		return m.updateProjectChangeConfirm(msg)
	}
	if m.currentView == ViewDeleteConfirm && m.deleteConfirm != nil {
		return m.updateDeleteConfirm(msg)
	}
	if m.currentView == ViewCloseConfirm && m.closeConfirm != nil {
		return m.updateCloseConfirm(msg)
	}
	if m.currentView == ViewArchiveConfirm && m.archiveConfirm != nil {
		return m.updateArchiveConfirm(msg)
	}
	if m.currentView == ViewQuitConfirm && m.quitConfirm != nil {
		return m.updateQuitConfirm(msg)
	}
	if m.currentView == ViewSettings && m.settingsView != nil {
		return m.updateSettings(msg)
	}
	if m.currentView == ViewRetry && m.retryView != nil {
		return m.updateRetry(msg)
	}
	if m.currentView == ViewChangeStatus && m.changeStatusForm != nil {
		return m.updateChangeStatus(msg)
	}
	if m.currentView == ViewCommandPalette && m.commandPaletteView != nil {
		return m.updateCommandPalette(msg)
	}
	// Handle detail view feedback mode (needs all message types for text input)
	if m.currentView == ViewDetail && m.detailView != nil && m.detailView.InFeedbackMode() {
		return m.updateDetail(msg)
	}

	// Handle filter input mode (needs all message types for text input)
	if m.currentView == ViewDashboard && m.filterActive {
		return m.updateFilterMode(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Global keys
		if key.Matches(msg, m.keys.Quit) {
			// Cleanup subscriptions and watchers
			if m.eventCh != nil {
				m.executor.UnsubscribeTaskEvents(m.eventCh)
			}
			m.stopDatabaseWatcher()
			return m, tea.Quit
		}

		// Command palette works from any view
		if key.Matches(msg, m.keys.CommandPalette) {
			m.commandPaletteView = NewCommandPaletteModel(m.db, m.tasks, m.width, m.height)
			m.previousView = m.currentView
			m.currentView = ViewCommandPalette
			return m, m.commandPaletteView.Init()
		}

		// Route to current view
		switch m.currentView {
		case ViewDashboard:
			return m.updateDashboard(msg)
		case ViewDetail:
			return m.updateDetail(msg)
		case ViewAttachments:
			return m.updateAttachments(msg)
		}

	case tea.MouseMsg:
		// Handle mouse clicks on dashboard view
		if m.currentView == ViewDashboard && msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft {
			// Check if clicking on a task card
			if task := m.kanban.HandleClick(msg.X, msg.Y); task != nil {
				return m, m.loadTask(task.ID)
			}
		}

	case tasksLoadedMsg:
		m.loading = false
		m.tasks = msg.tasks
		m.err = msg.err

		// First-time experience: auto-open new task form on first run when no tasks exist
		if m.isFirstLoad {
			m.isFirstLoad = false
			m.showWelcome = len(msg.tasks) == 0

			// If this is the very first run (no tasks, onboarding not completed), auto-open new task form
			if len(msg.tasks) == 0 && m.db.IsFirstRun() && !m.onboardingShown {
				m.onboardingShown = true
				// Auto-open the new task form to guide users to create their first task
				m.newTaskForm = NewFormModel(m.db, m.width, m.height, m.workingDir, m.availableExecutors)
				m.previousView = m.currentView
				m.currentView = ViewNewTask
				return m, m.newTaskForm.Init()
			}
		}

		// Update showWelcome based on current task count
		m.showWelcome = len(msg.tasks) == 0

		// Check for newly blocked/done tasks and notify
		for _, t := range m.tasks {
			prevStatus := m.prevStatuses[t.ID]
			if prevStatus != "" && prevStatus != t.Status {
				if t.Status == db.StatusBlocked {
					// Task just became blocked - ring bell and show notification
					m.notification = fmt.Sprintf("%s Task #%d needs input: %s (g to jump)", IconBlocked(), t.ID, t.Title)
					m.notifyUntil = time.Now().Add(10 * time.Second)
					m.notifyTaskID = t.ID
					RingBell() // Ring terminal bell (writes to /dev/tty to bypass TUI)
					// Mark task as needing input for kanban highlighting
					m.tasksNeedingInput[t.ID] = true
				} else if t.Status == db.StatusDone && db.IsInProgress(prevStatus) {
					// Task completed - ring bell and show notification
					m.notification = fmt.Sprintf("%s Task #%d complete: %s (g to jump)", IconDone(), t.ID, t.Title)
					m.notifyUntil = time.Now().Add(5 * time.Second)
					m.notifyTaskID = t.ID
					RingBell() // Ring terminal bell (writes to /dev/tty to bypass TUI)
				}
				// Clear needing input flag when task leaves blocked status
				if prevStatus == db.StatusBlocked && t.Status != db.StatusBlocked {
					delete(m.tasksNeedingInput, t.ID)
				}
			}
			m.prevStatuses[t.ID] = t.Status

			// Update detail view if showing this task
			if m.selectedTask != nil && m.selectedTask.ID == t.ID {
				m.selectedTask = t
				if m.detailView != nil {
					m.detailView.UpdateTask(t)
				}
			}
		}

		// Reapply filter if one is active
		m.applyFilter()
		m.kanban.SetHiddenDoneCount(msg.hiddenDoneCount)
		// Refresh running process indicators for all tasks
		running := executor.GetTasksWithRunningShellProcess()
		// Also check currently viewed task (its panes are in task-ui, not daemon)
		if m.selectedTask != nil && executor.HasRunningProcessInTaskUI() {
			running[m.selectedTask.ID] = true
		}
		m.kanban.SetRunningProcesses(running)
		m.kanban.SetTasksNeedingInput(m.tasksNeedingInput)
		m.kanban.SetBlockedByDeps(msg.blockedByDeps)

		// Trigger initial PR refresh after first task load (subsequent refreshes via prRefreshTick)
		if !m.initialPRRefreshDone {
			m.initialPRRefreshDone = true
			cmds = append(cmds, m.refreshAllPRs())
		}

	case taskLoadedMsg:
		// Reset transition flag now that task is loaded
		m.taskTransitionInProgress = false
		// Set grace period to prevent focus flashing during task switch
		// This allows the new detail view to settle before checking focus
		m.taskTransitionGraceUntil = time.Now().Add(500 * time.Millisecond)
		if msg.err == nil {
			m.selectedTask = msg.task
			// Update last_accessed_at in m.tasks for command palette sorting
			// The DB update happens async in loadTaskWithOptions, so we update the
			// in-memory list here to ensure Ctrl+P shows recently visited tasks first
			now := db.LocalTime{Time: time.Now()}
			msg.task.LastAccessedAt = &now
			for i, t := range m.tasks {
				if t.ID == msg.task.ID {
					m.tasks[i].LastAccessedAt = &now
					break
				}
			}
			// Clean up any duplicate tmux windows for this task before switching
			m.executor.CleanupDuplicateWindows(msg.task.ID)
			// Resume task if it was suspended (blocked idle tasks get suspended to save memory)
			if m.executor.IsSuspended(msg.task.ID) {
				m.executor.ResumeTask(msg.task.ID)
			}
			var initCmd tea.Cmd
			m.detailView, initCmd = NewDetailModel(msg.task, m.db, m.executor, m.width, m.height, msg.focusExecutor)
			// Set origin column for navigation if entering from dashboard
			// (preserve existing origin when navigating between tasks in detail view)
			if !m.kanban.HasOriginColumn() {
				m.kanban.SetOriginColumn()
			}
			// Set task position in column for display
			pos, total := m.kanban.GetTaskPosition()
			m.detailView.SetPosition(pos, total)
			m.previousView = m.currentView
			m.currentView = ViewDetail
			// Start async pane setup if needed
			if initCmd != nil {
				cmds = append(cmds, initCmd)
			}
			// Start tmux output ticker if session is active
			if tickerCmd := m.detailView.StartTmuxTicker(); tickerCmd != nil {
				cmds = append(cmds, tickerCmd)
			}
			// Start fast focus tick for responsive dimming
			cmds = append(cmds, m.focusTick())
			// Fetch PR info for the task
			if prCmd := m.fetchPRInfo(msg.task); prCmd != nil {
				cmds = append(cmds, prCmd)
			}
		} else {
			m.err = msg.err
		}

	case prInfoMsg:
		// Update PR info in kanban and detail view
		if msg.info != nil {
			m.kanban.SetPRInfo(msg.taskID, msg.info)
			// Update detail view if showing this task
			if m.detailView != nil && m.selectedTask != nil && m.selectedTask.ID == msg.taskID {
				m.detailView.SetPRInfo(msg.info)
			}
		}

	case prBatchMsg:
		// Batch PR update from refreshAllPRs
		for _, result := range msg.results {
			if result.info != nil {
				m.kanban.SetPRInfo(result.taskID, result.info)
				// Update detail view if showing this task
				if m.detailView != nil && m.selectedTask != nil && m.selectedTask.ID == result.taskID {
					m.detailView.SetPRInfo(result.info)
				}
			}
		}

	case prRefreshTickMsg:
		// Periodically refresh PR info (every 30 seconds)
		if m.currentView == ViewDashboard {
			cmds = append(cmds, m.refreshAllPRs())
		}
		cmds = append(cmds, m.prRefreshTick())

	case taskCreatedMsg:
		if msg.err == nil {
			m.currentView = ViewDashboard
			m.newTaskForm = nil
			m.showWelcome = false // Hide welcome message after first task is created
			cmds = append(cmds, m.loadTasks())
		} else {
			m.err = msg.err
		}

	case taskUpdatedMsg:
		if msg.err == nil {
			// Update the selected task if we're in detail view
			if m.selectedTask != nil && msg.task != nil && m.selectedTask.ID == msg.task.ID {
				m.selectedTask = msg.task
				if m.detailView != nil {
					m.detailView.UpdateTask(msg.task)
				}
			}
			cmds = append(cmds, m.loadTasks())
		} else {
			m.err = msg.err
		}

	case taskMovedMsg:
		if msg.err == nil {
			// Task was moved successfully
			m.selectedTask = msg.newTask
			m.notification = fmt.Sprintf("%s Task moved to %s as #%d", IconDone(), msg.newTask.Project, msg.newTask.ID)
			m.notifyUntil = time.Now().Add(5 * time.Second)
			cmds = append(cmds, m.loadTasks())
			// Navigate to the new task's detail view
			if m.selectedTask != nil {
				cmds = append(cmds, m.loadTask(m.selectedTask.ID))
			}
		} else {
			m.err = msg.err
		}

	case taskPinnedMsg:
		if msg.err != nil {
			m.err = msg.err
			break
		}
		if msg.task != nil {
			if m.selectedTask != nil && m.selectedTask.ID == msg.task.ID {
				m.selectedTask = msg.task
				if m.detailView != nil {
					m.detailView.UpdateTask(msg.task)
				}
			}
			if msg.task.Pinned {
				m.notification = fmt.Sprintf("%s Task #%d pinned", IconPin(), msg.task.ID)
			} else {
				m.notification = fmt.Sprintf("📍 Task #%d unpinned", msg.task.ID)
			}
			m.notifyUntil = time.Now().Add(3 * time.Second)
		}
		cmds = append(cmds, m.loadTasks())

	case taskQueuedMsg:
		if msg.err == nil && m.selectedTask != nil && m.detailView != nil {
			if task, err := m.db.GetTask(m.selectedTask.ID); err == nil && task != nil {
				m.selectedTask = task
				m.detailView.UpdateTask(task)
			}
		}
		cmds = append(cmds, m.loadTasks())

	case taskClosedMsg, taskArchivedMsg, taskUnarchivedMsg, taskDeletedMsg, taskRetriedMsg, taskStatusChangedMsg:
		cmds = append(cmds, m.loadTasks())

	case aiCommandMsg:
		if msg.err != nil {
			m.notification = fmt.Sprintf("%s %s", IconBlocked(), msg.err.Error())
			m.notifyUntil = time.Now().Add(5 * time.Second)
		} else if msg.cmd != nil {
			cmds = append(cmds, m.handleAICommand(msg.cmd))
		}

	case taskDangerousModeToggledMsg:
		cmds = append(cmds, m.loadTasks())
		// Refresh the detail view panes since the executor window was recreated
		if m.detailView != nil && m.selectedTask != nil {
			// Clear pane state so it will rejoin the new window
			m.detailView.ClearPaneState()
			// Trigger pane rejoin
			cmds = append(cmds, m.detailView.RefreshPanesCmd())
		}
		if msg.err != nil {
			m.notification = fmt.Sprintf("%s %s", IconBlocked(), msg.err.Error())
		} else {
			// Reload the task to get updated dangerous_mode flag
			if m.selectedTask != nil {
				task, _ := m.db.GetTask(m.selectedTask.ID)
				if task != nil {
					m.selectedTask = task
					if m.detailView != nil {
						m.detailView.UpdateTask(task)
					}
					if task.DangerousMode {
						m.notification = IconBlocked() + " Dangerous mode enabled"
					} else {
						m.notification = IconDone() + " Safe mode enabled"
					}
				}
			}
		}
		m.notifyUntil = time.Now().Add(3 * time.Second)

	case worktreeOpenedMsg:
		if msg.err != nil {
			m.notification = fmt.Sprintf("%s %s", IconBlocked(), msg.err.Error())
			m.notifyUntil = time.Now().Add(5 * time.Second)
		} else if msg.message != "" {
			m.notification = fmt.Sprintf("📂 %s", msg.message)
			m.notifyUntil = time.Now().Add(3 * time.Second)
		}

	case browserOpenedMsg:
		if msg.err != nil {
			m.notification = fmt.Sprintf("%s %s", IconBlocked(), msg.err.Error())
			m.notifyUntil = time.Now().Add(5 * time.Second)
		} else if msg.message != "" {
			m.notification = fmt.Sprintf("🌐 %s", msg.message)
			m.notifyUntil = time.Now().Add(3 * time.Second)
		}

	case taskEventMsg:
		// Real-time task update from executor
		event := msg.event
		if event.Task != nil {
			// Update task in our list
			for i, t := range m.tasks {
				if t.ID == event.TaskID {
					prevStatus := t.Status
					m.tasks[i] = event.Task

					// Show notification for status changes (skip if user closed this task manually)
					if prevStatus != event.Task.Status {
						userClosed := m.userClosedTaskIDs[event.TaskID]
						if userClosed {
							delete(m.userClosedTaskIDs, event.TaskID)
						}
						if event.Task.Status == db.StatusBlocked {
							m.notification = fmt.Sprintf("⚠ Task #%d needs input: %s (g to jump)", event.TaskID, event.Task.Title)
							m.notifyUntil = time.Now().Add(10 * time.Second)
							m.notifyTaskID = event.TaskID
							RingBell() // Ring terminal bell (writes to /dev/tty to bypass TUI)
							// Mark task as needing input for kanban highlighting
							m.tasksNeedingInput[event.TaskID] = true
						} else if event.Task.Status == db.StatusDone && db.IsInProgress(prevStatus) && !userClosed {
							m.notification = fmt.Sprintf("✓ Task #%d complete: %s (g to jump)", event.TaskID, event.Task.Title)
							m.notifyUntil = time.Now().Add(5 * time.Second)
							m.notifyTaskID = event.TaskID
							RingBell() // Ring terminal bell (writes to /dev/tty to bypass TUI)
						} else if db.IsInProgress(event.Task.Status) {
							m.notification = fmt.Sprintf("%s Task #%d started: %s (g to jump)", IconInProgress(), event.TaskID, event.Task.Title)
							m.notifyUntil = time.Now().Add(3 * time.Second)
							m.notifyTaskID = event.TaskID
						}
						// Clear needing input flag when task leaves blocked status
						if prevStatus == db.StatusBlocked && event.Task.Status != db.StatusBlocked {
							delete(m.tasksNeedingInput, event.TaskID)
						}
						m.prevStatuses[event.TaskID] = event.Task.Status
					}
					break
				}
			}
			m.kanban.SetTasks(m.tasks)
			m.kanban.SetTasksNeedingInput(m.tasksNeedingInput)

			// Update detail view if showing this task
			if m.selectedTask != nil && m.selectedTask.ID == event.TaskID {
				m.selectedTask = event.Task
				if m.detailView != nil {
					m.detailView.UpdateTask(event.Task)
				}
			}
		}
		// Wait for next event
		cmds = append(cmds, m.waitForTaskEvent())

	case tickMsg:
		// Clear expired notifications
		if !m.notifyUntil.IsZero() && time.Now().After(m.notifyUntil) {
			m.notification = ""
			m.notifyTaskID = 0
		}
		// Refresh detail view if active (for logs which may update frequently)
		if m.currentView == ViewDetail && m.detailView != nil {
			m.detailView.Refresh()
		}
		// Poll database for task changes (hooks run in separate process)
		if m.currentView == ViewDashboard && !m.loading {
			cmds = append(cmds, m.loadTasks())
			// Refresh running process indicators
			running := executor.GetTasksWithRunningShellProcess()
			// Also check currently viewed task (its panes are in task-ui, not daemon)
			if m.selectedTask != nil && executor.HasRunningProcessInTaskUI() {
				running[m.selectedTask.ID] = true
			}
			m.kanban.SetRunningProcesses(running)
		}
		cmds = append(cmds, m.tick())

	case focusTickMsg:
		// Fast tick for responsive focus state changes in detail view
		if m.currentView == ViewDetail && m.detailView != nil {
			// Skip focus checking during task transitions to prevent visual flashing
			// The grace period allows the new task to settle before checking focus
			if !m.taskTransitionInProgress && time.Now().After(m.taskTransitionGraceUntil) {
				m.detailView.RefreshFocusState()
			}
			cmds = append(cmds, m.focusTick())
		}

	case shortcutTimeoutMsg:
		// Only handle if this timeout matches the current pending shortcut
		// (prevents stale timeouts from firing after a successful double-digit)
		if m.pendingShortcutDigit == msg.digit && msg.timeout.Equal(m.shortcutTimeout) {
			m.pendingShortcutDigit = ""
			if idx := TaskIndexFromShortcut(msg.digit); idx > 0 {
				if task := m.kanban.SelectByShortcut(idx); task != nil {
					return m, m.loadTask(task.ID)
				}
			}
		}
		return m, nil

	case dbChangeMsg:
		// Database file changed - reload tasks
		cmds = append(cmds, m.loadTasks())
		// Continue watching for more changes
		cmds = append(cmds, m.waitForDBChange())

	default:
		// Route unknown messages to detail view if active
		// This handles async messages like panesJoinedMsg and spinnerTickMsg
		if m.currentView == ViewDetail && m.detailView != nil {
			var cmd tea.Cmd
			m.detailView, cmd = m.detailView.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	// Dump debug state if enabled
	if m.debugStatePath != "" {
		// Ignore errors during debug dump to avoid crashing UI
		_ = m.DumpDebugStateToFile(m.debugStatePath)
	}

	return m, tea.Batch(cmds...)
}

func (m *AppModel) applyWindowSize(width, height int) {
	m.width = width
	m.height = height
	m.help.Width = width
	m.kanban.SetSize(width, height-4)
	if m.detailView != nil {
		m.detailView.SetSize(width, height)
	}
	if m.settingsView != nil {
		m.settingsView.SetSize(width, height)
	}
	if m.retryView != nil {
		m.retryView.SetSize(width, height)
	}
	if m.commandPaletteView != nil {
		m.commandPaletteView.SetSize(width, height)
	}
	if m.newTaskForm != nil {
		m.newTaskForm.SetSize(width, height)
	}
	if m.editTaskForm != nil {
		m.editTaskForm.SetSize(width, height)
	}
}

// View renders the current view.
func (m *AppModel) View() string {
	// Wait for window size
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	if m.loading {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, "Loading tasks...")
	}

	if m.err != nil {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, fmt.Sprintf("Error: %s", m.err))
	}

	switch m.currentView {
	case ViewDashboard:
		return m.viewDashboard()
	case ViewDetail:
		if m.detailView != nil {
			return m.detailView.View()
		}
	case ViewNewTask:
		if m.newTaskForm != nil {
			return m.newTaskForm.View()
		}
	case ViewEditTask:
		if m.editTaskForm != nil {
			return m.editTaskForm.View()
		}
	case ViewNewTaskConfirm:
		return m.viewNewTaskConfirm()
	case ViewProjectChangeConfirm:
		return m.viewProjectChangeConfirm()
	case ViewDeleteConfirm:
		return m.viewDeleteConfirm()
	case ViewCloseConfirm:
		return m.viewCloseConfirm()
	case ViewArchiveConfirm:
		return m.viewArchiveConfirm()
	case ViewQuitConfirm:
		return m.viewQuitConfirm()
	case ViewSettings:
		if m.settingsView != nil {
			return m.settingsView.View()
		}
	case ViewRetry:
		if m.retryView != nil {
			return m.retryView.View()
		}
	case ViewAttachments:
		if m.attachmentsView != nil {
			return m.attachmentsView.View()
		}
	case ViewChangeStatus:
		return m.viewChangeStatus()
	case ViewCommandPalette:
		if m.commandPaletteView != nil {
			return m.commandPaletteView.View()
		}
	}

	return ""
}

func (m *AppModel) viewNewTaskConfirm() string {
	if m.queueConfirm == nil {
		return ""
	}

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		MarginBottom(1).
		Render("New Task")

	formView := m.queueConfirm.View()

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(m.width - 4)

	return box.Render(lipgloss.JoinVertical(lipgloss.Left, header, formView))
}

func (m *AppModel) viewDashboard() string {
	var headerParts []string

	// Show warning banner if no executors are available
	if len(m.availableExecutors) == 0 {
		warnStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#FFCC00")). // Yellow background
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 2).
			Width(m.width)
		headerParts = append(headerParts, warnStyle.Render(IconBlocked()+" No AI executor installed. See: https://code.claude.com/docs/en/overview"))
	}

	// Show global dangerous mode banner if the entire system is in dangerous mode
	if IsGlobalDangerousMode() {
		dangerStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#E06C75")). // Red background
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true).
			Padding(0, 2).
			Width(m.width)
		headerParts = append(headerParts, dangerStyle.Render(IconBlocked()+" DANGEROUS MODE ENABLED"))
	}

	// Show notification banner if active
	if m.notification != "" && time.Now().Before(m.notifyUntil) {
		notifyStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#FFCC00")).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 2)
		headerParts = append(headerParts, notifyStyle.Render(m.notification))
	} else {
		m.notification = "" // Clear expired notification
		m.notifyTaskID = 0
	}

	// Show current processing tasks if any
	if runningIDs := m.executor.RunningTasks(); len(runningIDs) > 0 {
		statusBar := lipgloss.NewStyle().
			Foreground(ColorInProgress).
			Render(fmt.Sprintf("%s Processing %d task(s)", IconProcessing(), len(runningIDs)))
		headerParts = append(headerParts, statusBar)
	}

	// Show filter bar if filter is active or has text
	filterBar := ""
	filterBarHeight := 0
	if m.filterActive || m.filterText != "" {
		filterBar = m.renderFilterBar()
		filterBarHeight = lipgloss.Height(filterBar)
	}

	// Calculate heights dynamically
	headerHeight := len(headerParts)

	// Render help to measure its actual height
	helpView := m.renderHelp()
	helpHeight := lipgloss.Height(helpView)

	kanbanHeight := m.height - headerHeight - filterBarHeight - helpHeight

	// Update kanban size
	m.kanban.SetSize(m.width, kanbanHeight)

	// Build the view
	header := ""
	if len(headerParts) > 0 {
		header = lipgloss.JoinVertical(lipgloss.Left, headerParts...)
	}

	var contentParts []string
	if header != "" {
		contentParts = append(contentParts, header)
	}
	if filterBar != "" {
		contentParts = append(contentParts, filterBar)
	}

	// Show welcome/getting started message when kanban is empty
	if m.showWelcome && m.kanban.IsEmpty() {
		welcomeView := m.renderWelcomeMessage(kanbanHeight)
		contentParts = append(contentParts, welcomeView, helpView)
	} else {
		contentParts = append(contentParts, m.kanban.View(), helpView)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, contentParts...)

	// Use Place to fill the entire terminal
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, content)
}

// renderWelcomeMessage renders a friendly getting started message for first-time users.
func (m *AppModel) renderWelcomeMessage(height int) string {
	// Welcome title
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		MarginBottom(1)

	// Subtitle style
	subtitleStyle := lipgloss.NewStyle().
		Foreground(ColorSecondary).
		MarginBottom(2)

	// Action style for keyboard shortcuts
	actionStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary)

	// Description style
	descStyle := lipgloss.NewStyle().
		Foreground(ColorMuted)

	// Build the welcome content
	var lines []string

	lines = append(lines, titleStyle.Render("Welcome to TaskYou!"))
	lines = append(lines, subtitleStyle.Render("Your AI-powered task management system"))
	lines = append(lines, "")

	// Key actions
	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top,
		actionStyle.Render("n"),
		descStyle.Render("  Create your first task"),
	))
	lines = append(lines, "")

	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top,
		actionStyle.Render("s"),
		descStyle.Render("  Open settings to configure projects"),
	))
	lines = append(lines, "")

	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top,
		actionStyle.Render("?"),
		descStyle.Render("  Show all keyboard shortcuts"),
	))
	lines = append(lines, "")

	// Executor status
	if len(m.availableExecutors) == 0 {
		warningStyle := lipgloss.NewStyle().
			Foreground(ColorWarning).
			MarginTop(1)
		lines = append(lines, warningStyle.Render(IconBlocked()+" Install an AI executor to run tasks"))
		lines = append(lines, descStyle.Render("   Visit: https://code.claude.com/docs/en/overview"))
	} else {
		readyStyle := lipgloss.NewStyle().
			Foreground(ColorDone).
			MarginTop(1)
		executorList := strings.Join(m.availableExecutors, ", ")
		lines = append(lines, readyStyle.Render(IconDone()+" Ready to run tasks with: "+executorList))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	// Center the content in a box
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(2, 4).
		Width(min(60, m.width-4))

	boxed := boxStyle.Render(content)

	// Center in available space
	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, boxed)
}

// renderFilterBar renders the filter input bar.
func (m *AppModel) renderFilterBar() string {
	// Build filter bar content
	var parts []string

	// Filter icon and label
	var filterIcon string
	if m.filterActive {
		filterIcon = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary).Render("/")
	} else {
		filterIcon = lipgloss.NewStyle().Foreground(ColorMuted).Render("/")
	}
	parts = append(parts, filterIcon)

	// Filter input or static text
	if m.filterActive {
		// Show active input
		m.filterInput.Width = min(40, m.width-10)
		parts = append(parts, m.filterInput.View())
	} else {
		// Show filter text with indicator that filter is active
		filterStyle := lipgloss.NewStyle().Foreground(ColorSecondary)
		parts = append(parts, filterStyle.Render(m.filterText))
	}

	// Help hint
	helpStyle := lipgloss.NewStyle().Foreground(ColorMuted).Italic(true)
	if m.filterActive {
		// Show different help based on whether autocomplete dropdown is showing
		if m.showFilterDropdown && m.filterAutocomplete.HasResults() {
			parts = append(parts, helpStyle.Render("  (Tab: select project, ↑↓: navigate)"))
		} else {
			navHelp := fmt.Sprintf("%s%s%s%s", IconArrowUp(), IconArrowDown(), IconArrowLeft(), IconArrowRight())
			parts = append(parts, helpStyle.Render(fmt.Sprintf("  (backspace: clear, Enter: done, %s: navigate, [: project)", navHelp)))
		}
	} else if m.filterText != "" {
		parts = append(parts, helpStyle.Render("  (/: edit, Esc: clear)"))
	}

	filterContent := lipgloss.JoinHorizontal(lipgloss.Center, parts...)

	// Wrap in a subtle box
	filterBarStyle := lipgloss.NewStyle().
		Padding(0, 1).
		Width(m.width)

	if m.filterActive {
		filterBarStyle = filterBarStyle.
			Background(lipgloss.Color("#333333"))
	}

	filterBar := filterBarStyle.Render(filterContent)

	// Add autocomplete dropdown below filter bar if showing
	if m.showFilterDropdown && m.filterAutocomplete.HasResults() {
		filterBar = lipgloss.JoinVertical(lipgloss.Left, filterBar, m.filterAutocomplete.View())
	}

	return filterBar
}

func (m *AppModel) renderHelp() string {
	return m.help.View(m.keys)
}

func (m *AppModel) updateDashboard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	// Column navigation
	case key.Matches(msg, m.keys.Left):
		m.kanban.MoveLeft()
		return m, nil

	case key.Matches(msg, m.keys.Right):
		m.kanban.MoveRight()
		return m, nil

	// Task navigation within column
	case key.Matches(msg, m.keys.Up):
		m.kanban.MoveUp()
		return m, nil

	case key.Matches(msg, m.keys.Down):
		m.kanban.MoveDown()
		return m, nil

	// Numeric shortcuts 1-9 and double-digits (11, 22, etc.) to select visible tasks
	case msg.String() == "1", msg.String() == "2", msg.String() == "3",
		msg.String() == "4", msg.String() == "5", msg.String() == "6",
		msg.String() == "7", msg.String() == "8", msg.String() == "9":
		digit := msg.String()

		// Check if this completes a double-digit shortcut
		if m.pendingShortcutDigit != "" && time.Now().Before(m.shortcutTimeout) {
			// Same digit pressed twice = double-digit shortcut (11, 22, etc.)
			if digit == m.pendingShortcutDigit {
				shortcut := digit + digit
				m.pendingShortcutDigit = ""
				if idx := TaskIndexFromShortcut(shortcut); idx > 0 {
					if task := m.kanban.SelectByShortcut(idx); task != nil {
						return m, m.loadTask(task.ID)
					}
				}
				return m, nil
			}
			// Different digit - first use the pending single digit, then start new
			if idx := TaskIndexFromShortcut(m.pendingShortcutDigit); idx > 0 {
				if task := m.kanban.SelectByShortcut(idx); task != nil {
					m.pendingShortcutDigit = ""
					return m, m.loadTask(task.ID)
				}
			}
		}

		// Start a new pending shortcut with 300ms timeout for double-digit
		m.pendingShortcutDigit = digit
		m.shortcutTimeout = time.Now().Add(300 * time.Millisecond)

		// Return a command that fires after timeout to execute single-digit shortcut
		return m, tea.Tick(300*time.Millisecond, func(t time.Time) tea.Msg {
			return shortcutTimeoutMsg{digit: digit, timeout: m.shortcutTimeout}
		})

	// Jump to pinned/unpinned tasks
	case key.Matches(msg, m.keys.JumpToPinned):
		m.kanban.JumpToPinned()
		return m, nil

	case key.Matches(msg, m.keys.JumpToUnpinned):
		m.kanban.JumpToUnpinned()
		return m, nil

	// Column focus shortcuts
	case key.Matches(msg, m.keys.FocusBacklog):
		m.kanban.FocusColumn(0)
		return m, nil

	case key.Matches(msg, m.keys.FocusInProgress):
		m.kanban.FocusColumn(1)
		return m, nil

	case key.Matches(msg, m.keys.FocusBlocked):
		m.kanban.FocusColumn(2)
		return m, nil

	case key.Matches(msg, m.keys.FocusDone):
		m.kanban.FocusColumn(3)
		return m, nil

	case key.Matches(msg, m.keys.JumpToNotification):
		// Jump to the task that triggered the notification
		if m.notifyTaskID > 0 && m.notification != "" {
			taskID := m.notifyTaskID
			m.kanban.SelectTask(taskID)
			// Clear notification after jumping
			m.notification = ""
			m.notifyTaskID = 0
			// Use loadTaskWithFocus to automatically focus the executor pane
			return m, m.loadTaskWithFocus(taskID)
		}
		return m, nil

	case key.Matches(msg, m.keys.Enter):
		if task := m.kanban.SelectedTask(); task != nil {
			return m, m.loadTask(task.ID)
		}

	case key.Matches(msg, m.keys.New):
		m.newTaskForm = NewFormModel(m.db, m.width, m.height, m.workingDir, m.availableExecutors)
		m.previousView = m.currentView
		m.currentView = ViewNewTask
		return m, m.newTaskForm.Init()

	case key.Matches(msg, m.keys.Queue):
		if task := m.kanban.SelectedTask(); task != nil {
			// Don't allow queueing if task is already processing
			if task.Status == db.StatusProcessing {
				return m, nil
			}
			// Immediately update UI for responsiveness
			task.Status = db.StatusQueued
			m.updateTaskInList(task)
			return m, m.queueTask(task.ID)
		}

	case key.Matches(msg, m.keys.TogglePin):
		if task := m.kanban.SelectedTask(); task != nil {
			return m, m.toggleTaskPinned(task.ID)
		}
		return m, nil

	case key.Matches(msg, m.keys.Retry):
		if task := m.kanban.SelectedTask(); task != nil {
			// Allow retry for blocked, done, or backlog tasks
			if task.Status == db.StatusBlocked || task.Status == db.StatusDone ||
				task.Status == db.StatusBacklog {
				m.selectedTask = task
				m.retryView = NewRetryModel(task, m.db, m.width, m.height)
				m.previousView = m.currentView
				m.currentView = ViewRetry
				return m, m.retryView.Init()
			}
		}

	case key.Matches(msg, m.keys.Close):
		if task := m.kanban.SelectedTask(); task != nil {
			return m.showCloseConfirm(task)
		}

	case key.Matches(msg, m.keys.Archive):
		if task := m.kanban.SelectedTask(); task != nil {
			if task.Status == db.StatusArchived {
				// Unarchive the task
				return m, m.unarchiveTask(task.ID)
			}
			return m.showArchiveConfirm(task)
		}

	case key.Matches(msg, m.keys.Delete):
		if task := m.kanban.SelectedTask(); task != nil {
			return m.showDeleteConfirm(task)
		}

	case key.Matches(msg, m.keys.OpenWorktree):
		if task := m.kanban.SelectedTask(); task != nil {
			return m, m.openWorktreeInEditor(task)
		}

	case key.Matches(msg, m.keys.OpenBrowser):
		if task := m.kanban.SelectedTask(); task != nil {
			return m, m.openBrowser(task)
		}

	case key.Matches(msg, m.keys.Settings):
		m.settingsView = NewSettingsModel(m.db, m.width, m.height)
		m.previousView = m.currentView
		m.currentView = ViewSettings
		return m, m.settingsView.Init()

	case key.Matches(msg, m.keys.Refresh):
		m.loading = true
		return m, m.loadTasks()

	case key.Matches(msg, m.keys.ChangeStatus):
		if task := m.kanban.SelectedTask(); task != nil {
			return m.showChangeStatus(task)
		}

	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil

	case key.Matches(msg, m.keys.Filter):
		// Enter filter mode
		m.filterActive = true
		m.filterInput.Focus()
		return m, textinput.Blink

	case key.Matches(msg, m.keys.Back):
		// If filter is set, clear it first
		if m.filterText != "" {
			m.filterText = ""
			m.filterInput.SetValue("")
			return m, m.loadTasks()
		}
		return m.showQuitConfirm()
	}

	return m, nil
}

// updateFilterMode handles input when filter mode is active.
// Tab accepts autocomplete suggestions for project names when typing "[project".
func (m *AppModel) updateFilterMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m.handleFilterInput(msg)
	}

	switch keyMsg.String() {
	case "esc":
		m.filterActive, m.filterText, m.showFilterDropdown = false, "", false
		m.filterInput.SetValue("")
		m.filterInput.Blur()
		m.filterAutocomplete.Reset()
		return m, m.loadTasks()

	case "backspace":
		if m.filterInput.Value() == "" {
			m.filterActive, m.showFilterDropdown = false, false
			m.filterInput.Blur()
			m.filterAutocomplete.Reset()
			return m, m.loadTasks()
		}
		return m.handleFilterInput(msg)

	case "tab", "enter":
		// Accept autocomplete if showing
		if m.showFilterDropdown && m.filterAutocomplete.HasResults() {
			if name := m.filterAutocomplete.Select(); name != "" {
				m.filterInput.SetValue("[" + name + "] ")
				m.filterInput.SetCursor(len(m.filterInput.Value()))
				m.filterText = m.filterInput.Value()
				m.applyFilter()
				m.showFilterDropdown = false
				m.filterAutocomplete.Reset()
				return m, nil
			}
		}
		if keyMsg.String() == "tab" {
			return m, nil // Tab: do nothing if no dropdown
		}
		// Enter: just exit filter mode (user can press Enter again on kanban to select task)
		m.filterActive, m.showFilterDropdown = false, false
		m.filterInput.Blur()
		return m, nil

	case "up", "down", "left", "right":
		// Autocomplete navigation
		if m.showFilterDropdown && m.filterAutocomplete.HasResults() {
			if keyMsg.String() == "up" {
				m.filterAutocomplete.MoveUp()
			} else if keyMsg.String() == "down" {
				m.filterAutocomplete.MoveDown()
			}
			if keyMsg.String() == "up" || keyMsg.String() == "down" {
				return m, nil
			}
		}
		// Exit filter and navigate kanban
		m.filterActive, m.showFilterDropdown = false, false
		m.filterInput.Blur()
		switch keyMsg.String() {
		case "up":
			m.kanban.MoveUp()
		case "down":
			m.kanban.MoveDown()
		case "left":
			m.kanban.MoveLeft()
		case "right":
			m.kanban.MoveRight()
		}
		return m, nil

	case "ctrl+c":
		if m.eventCh != nil {
			m.executor.UnsubscribeTaskEvents(m.eventCh)
		}
		m.stopDatabaseWatcher()
		return m, tea.Quit
	}

	return m.handleFilterInput(msg)
}

// handleFilterInput processes text input and updates autocomplete state.
func (m *AppModel) handleFilterInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)

	if newText := m.filterInput.Value(); newText != m.filterText {
		m.filterText = newText
		m.applyFilter()

		// Update autocomplete: show when typing "[project" but not after "] "
		if strings.HasPrefix(newText, "[") && !strings.Contains(newText, "] ") {
			query := strings.TrimSuffix(strings.TrimPrefix(newText, "["), "]")
			m.filterAutocomplete.SetQuery(query)
			m.showFilterDropdown = m.filterAutocomplete.HasResults()
		} else {
			m.showFilterDropdown = false
			m.filterAutocomplete.Reset()
		}
	}
	return m, cmd
}

// applyFilter filters the tasks based on current filter text using fuzzy matching.
// Uses the same matching logic as the command palette (Ctrl+P) for consistency.
func (m *AppModel) applyFilter() {
	if m.filterText == "" {
		// No filter, show all tasks
		m.kanban.SetTasks(m.tasks)
		return
	}

	queryLower := strings.ToLower(m.filterText)

	// Score all tasks using fuzzy matching
	var scored []scoredTask
	for _, task := range m.tasks {
		score := scoreTaskForFilter(task, queryLower)
		if score >= 0 {
			scored = append(scored, scoredTask{task: task, score: score})
		}
	}

	// Sort by score descending (best matches first)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Extract sorted tasks
	filtered := make([]*db.Task, len(scored))
	for i, st := range scored {
		filtered[i] = st.task
	}
	m.kanban.SetTasks(filtered)
}

// scoreTaskForFilter calculates a fuzzy match score for a task against the query.
// Special: "[project" filters by project, "[project] keyword" filters within project.
func scoreTaskForFilter(task *db.Task, query string) int {
	// Handle "[project..." syntax
	if strings.HasPrefix(query, "[") {
		// "[project] keyword" pattern
		if idx := strings.Index(query, "] "); idx != -1 {
			projectName := strings.TrimPrefix(query[:idx], "[")
			if !strings.EqualFold(task.Project, projectName) {
				return -1
			}
			keyword := strings.TrimSpace(query[idx+2:])
			if keyword == "" {
				return 100
			}
			return scoreTaskFields(task, keyword, false)
		}
		// Still typing project name
		projectQuery := strings.TrimSuffix(strings.TrimPrefix(query, "["), "]")
		if projectQuery == "" {
			if task.Project != "" {
				return 100
			}
			return -1
		}
		if s := fuzzyScore(task.Project, projectQuery); s > 0 {
			return s
		}
		return -1
	}

	return scoreTaskFields(task, query, true)
}

// scoreTaskFields scores a task against a query, optionally including project field.
func scoreTaskFields(task *db.Task, query string, includeProject bool) int {
	// ID match (highest priority)
	idStr := fmt.Sprintf("%d", task.ID)
	q := strings.TrimPrefix(query, "#")
	if strings.Contains(idStr, q) {
		return 1000
	}

	// PR number match
	if task.PRNumber > 0 && strings.Contains(fmt.Sprintf("%d", task.PRNumber), q) {
		return 900
	}

	// PR URL match
	if task.PRURL != "" && strings.Contains(strings.ToLower(task.PRURL), query) {
		return 800
	}

	best := -1
	if s := fuzzyScore(task.Title, query); s > best {
		best = s
	}
	if includeProject {
		if s := fuzzyScore(task.Project, query) - 50; s > best {
			best = s
		}
	}
	if s := fuzzyScore(task.Type, query) - 50; s > best {
		best = s
	}
	if strings.Contains(strings.ToLower(task.Status), query) && 100 > best {
		best = 100
	}
	return best
}

func (m *AppModel) updateDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	// If detail view is in feedback mode, route all messages there
	if m.detailView != nil && m.detailView.InFeedbackMode() {
		var cmd tea.Cmd
		m.detailView, cmd = m.detailView.Update(msg)
		return m, cmd
	}

	// Handle key messages
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		// Non-key messages go to detail view
		if m.detailView != nil {
			var cmd tea.Cmd
			m.detailView, cmd = m.detailView.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	if key.Matches(keyMsg, m.keys.Back) {
		// Check PR state asynchronously (don't block UI)
		if m.selectedTask != nil {
			taskID := m.selectedTask.ID
			go m.executor.CheckPRStateAndUpdateTask(taskID)
		}

		m.currentView = ViewDashboard
		// Clear origin column when exiting detail view
		m.kanban.ClearOriginColumn()
		if m.detailView != nil {
			m.detailView.Cleanup()
			m.detailView = nil
		}
		return m, nil
	}

	// Handle queue/close/retry from detail view
	if key.Matches(keyMsg, m.keys.Queue) && m.selectedTask != nil {
		// Don't allow queueing if task is already processing
		if m.selectedTask.Status == db.StatusProcessing {
			return m, nil
		}
		// Immediately update UI for responsiveness
		m.selectedTask.Status = db.StatusQueued
		if m.detailView != nil {
			m.detailView.UpdateTask(m.selectedTask)
		}
		// Update task in the list and kanban
		m.updateTaskInList(m.selectedTask)
		return m, m.queueTask(m.selectedTask.ID)
	}
	if key.Matches(keyMsg, m.keys.Retry) && m.selectedTask != nil {
		task := m.selectedTask
		if task.Status == db.StatusBlocked || task.Status == db.StatusDone ||
			task.Status == db.StatusBacklog {
			// Clean up panes before leaving detail view
			if m.detailView != nil {
				m.detailView.Cleanup()
			}
			m.retryView = NewRetryModel(task, m.db, m.width, m.height)
			m.previousView = m.currentView
			m.currentView = ViewRetry
			return m, m.retryView.Init()
		}
	}
	if key.Matches(keyMsg, m.keys.Close) && m.selectedTask != nil {
		// Don't cleanup detail view yet - wait for confirmation
		// If user cancels, we need to return to detail view
		return m.showCloseConfirm(m.selectedTask)
	}
	if key.Matches(keyMsg, m.keys.Archive) && m.selectedTask != nil {
		if m.selectedTask.Status == db.StatusArchived {
			// Unarchive the task - cleanup detail view since status will change
			if m.detailView != nil {
				m.detailView.Cleanup()
				m.detailView = nil
			}
			m.currentView = ViewDashboard
			return m, m.unarchiveTask(m.selectedTask.ID)
		}
		// Don't cleanup detail view yet - wait for confirmation
		// If user cancels, we need to return to detail view
		return m.showArchiveConfirm(m.selectedTask)
	}
	if key.Matches(keyMsg, m.keys.Delete) && m.selectedTask != nil {
		// Don't cleanup detail view yet - wait for confirmation
		// If user cancels, we need to return to detail view
		return m.showDeleteConfirm(m.selectedTask)
	}
	if key.Matches(keyMsg, m.keys.Edit) && m.selectedTask != nil {
		m.editingTask = m.selectedTask
		m.editTaskForm = NewEditFormModel(m.db, m.selectedTask, m.width, m.height, m.availableExecutors)
		m.previousView = m.currentView
		m.currentView = ViewEditTask
		return m, m.editTaskForm.Init()
	}
	if key.Matches(keyMsg, m.keys.ChangeStatus) && m.selectedTask != nil {
		return m.showChangeStatus(m.selectedTask)
	}
	if key.Matches(keyMsg, m.keys.TogglePin) && m.selectedTask != nil {
		return m, m.toggleTaskPinned(m.selectedTask.ID)
	}
	if key.Matches(keyMsg, m.keys.ToggleDangerous) && m.selectedTask != nil {
		// Only allow toggling dangerous mode if task is processing or blocked
		if m.selectedTask.Status == db.StatusProcessing || m.selectedTask.Status == db.StatusBlocked {
			// Break panes back to daemon BEFORE toggling so the executor can kill them.
			// If panes are joined to task-ui, killAllWindowsByNameAllSessions won't find them.
			if m.detailView != nil {
				m.detailView.Cleanup()
			}
			return m, m.toggleDangerousMode(m.selectedTask.ID)
		}
	}
	if key.Matches(keyMsg, m.keys.OpenWorktree) && m.selectedTask != nil {
		return m, m.openWorktreeInEditor(m.selectedTask)
	}
	if key.Matches(keyMsg, m.keys.OpenBrowser) && m.selectedTask != nil {
		return m, m.openBrowser(m.selectedTask)
	}
	if key.Matches(keyMsg, m.keys.ToggleShellPane) && m.detailView != nil {
		m.detailView.ToggleShellPane()
		return m, nil
	}

	// Arrow key navigation to prev/next task in the same column
	// Skip j/k in detail view - only use arrow keys (j/k reserved for other uses)
	if keyMsg.String() == "j" || keyMsg.String() == "k" {
		return m, nil
	}
	if key.Matches(keyMsg, m.keys.Up) {
		// Ignore if no previous task exists
		if !m.kanban.HasPrevTask() {
			return m, nil
		}
		// Ignore if transition already in progress to prevent duplicate panes
		if m.taskTransitionInProgress {
			return m, nil
		}
		m.taskTransitionInProgress = true
		// Clean up current detail view before switching (without saving height)
		if m.detailView != nil {
			m.detailView.CleanupWithoutSaving()
			m.detailView = nil
		}
		// Move selection up in the kanban
		m.kanban.MoveUp()
		// Load the new task
		if task := m.kanban.SelectedTask(); task != nil {
			return m, m.loadTask(task.ID)
		}
		m.taskTransitionInProgress = false
		return m, nil
	}
	if key.Matches(keyMsg, m.keys.Down) {
		// Ignore if no next task exists
		if !m.kanban.HasNextTask() {
			return m, nil
		}
		// Ignore if transition already in progress to prevent duplicate panes
		if m.taskTransitionInProgress {
			return m, nil
		}
		m.taskTransitionInProgress = true
		// Clean up current detail view before switching (without saving height)
		if m.detailView != nil {
			m.detailView.CleanupWithoutSaving()
			m.detailView = nil
		}
		// Move selection down in the kanban
		m.kanban.MoveDown()
		// Load the new task
		if task := m.kanban.SelectedTask(); task != nil {
			return m, m.loadTask(task.ID)
		}
		m.taskTransitionInProgress = false
		return m, nil
	}

	if m.detailView != nil {
		var cmd tea.Cmd
		m.detailView, cmd = m.detailView.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *AppModel) updateNewTaskForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Pass all messages to the form (form handles ESC with confirmation prompt)
	model, cmd := m.newTaskForm.Update(msg)
	if form, ok := model.(*FormModel); ok {
		m.newTaskForm = form
		if form.submitted {
			// Store pending task and create confirmation form
			m.pendingTask = form.GetDBTask()
			m.pendingAttachments = form.GetAttachments()
			m.queueValue = false
			m.queueConfirm = huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Key("queue").
						Title("Queue for execution?").
						Description("Start processing immediately").
						Affirmative("Yes").
						Negative("No").
						Value(&m.queueValue),
				),
			).WithTheme(huh.ThemeDracula()).
				WithWidth(m.width - 4).
				WithShowHelp(true)
			m.currentView = ViewNewTaskConfirm
			return m, m.queueConfirm.Init()
		}
		if form.cancelled {
			m.currentView = ViewDashboard
			m.newTaskForm = nil
			return m, nil
		}
	}
	return m, cmd
}

func (m *AppModel) updateNewTaskConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle escape to go back
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.currentView = ViewNewTask
			m.queueConfirm = nil
			return m, nil
		}
	}

	// Update the huh form
	form, cmd := m.queueConfirm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.queueConfirm = f
	}

	// Check if form completed
	if m.queueConfirm.State == huh.StateCompleted {
		if m.pendingTask != nil {
			if m.queueValue {
				m.pendingTask.Status = db.StatusQueued
			} else {
				m.pendingTask.Status = db.StatusBacklog
			}
			task := m.pendingTask
			attachments := m.pendingAttachments
			m.pendingTask = nil
			m.pendingAttachments = nil
			m.newTaskForm = nil
			m.queueConfirm = nil
			m.currentView = ViewDashboard
			return m, m.createTaskWithAttachments(task, attachments)
		}
	}

	return m, cmd
}

func (m *AppModel) updateEditTaskForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Pass all messages to the form (form handles ESC with confirmation prompt)
	model, cmd := m.editTaskForm.Update(msg)
	if form, ok := model.(*FormModel); ok {
		m.editTaskForm = form
		if form.submitted {
			// Get updated task data from form
			updatedTask := form.GetDBTask()

			// Check if project has changed - this requires special handling
			if form.ProjectChanged() {
				// Store the original task for the confirmation dialog
				originalTask := m.editingTask

				m.editTaskForm = nil
				m.editingTask = nil
				return m.showProjectChangeConfirm(updatedTask, originalTask)
			}

			// Preserve the original task's ID and other fields
			updatedTask.ID = m.editingTask.ID
			updatedTask.Status = m.editingTask.Status
			updatedTask.WorktreePath = m.editingTask.WorktreePath
			updatedTask.BranchName = m.editingTask.BranchName
			updatedTask.CreatedAt = m.editingTask.CreatedAt
			updatedTask.StartedAt = m.editingTask.StartedAt
			updatedTask.CompletedAt = m.editingTask.CompletedAt

			// Capture old title before clearing editingTask
			oldTitle := m.editingTask.Title

			m.editTaskForm = nil
			m.editingTask = nil
			m.currentView = m.previousView
			return m, m.updateTaskWithRename(updatedTask, oldTitle)
		}
		if form.cancelled {
			m.currentView = m.previousView
			m.editTaskForm = nil
			m.editingTask = nil
			return m, nil
		}
	}
	return m, cmd
}

func (m *AppModel) showDeleteConfirm(task *db.Task) (tea.Model, tea.Cmd) {
	m.pendingDeleteTask = task
	m.deleteConfirmValue = false
	modalWidth := min(50, m.width-8)
	m.deleteConfirm = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Key("delete").
				Title(fmt.Sprintf("Delete task #%d?", task.ID)).
				Description(task.Title).
				Affirmative("Delete").
				Negative("Cancel").
				Value(&m.deleteConfirmValue),
		),
	).WithTheme(huh.ThemeDracula()).
		WithWidth(modalWidth - 6). // Account for modal padding and border
		WithShowHelp(true)
	m.previousView = m.currentView
	m.currentView = ViewDeleteConfirm
	return m, m.deleteConfirm.Init()
}

func (m *AppModel) viewDeleteConfirm() string {
	if m.deleteConfirm == nil {
		return ""
	}

	// Modal header with warning icon
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorError).
		MarginBottom(1).
		Render(IconBlocked() + " Confirm Delete")

	formView := m.deleteConfirm.View()

	// Modal box with border
	modalWidth := min(50, m.width-8)
	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorError).
		Padding(1, 2).
		Width(modalWidth)

	modalContent := modalBox.Render(lipgloss.JoinVertical(lipgloss.Center, header, formView))

	// Center the modal on screen
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalContent)
}

func (m *AppModel) updateDeleteConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle escape to cancel
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.currentView = m.previousView
			m.deleteConfirm = nil
			m.pendingDeleteTask = nil
			return m, nil
		}
	}

	// Update the huh form
	form, cmd := m.deleteConfirm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.deleteConfirm = f
	}

	// Check if form completed
	if m.deleteConfirm.State == huh.StateCompleted {
		if m.pendingDeleteTask != nil && m.deleteConfirmValue {
			taskID := m.pendingDeleteTask.ID
			m.pendingDeleteTask = nil
			m.deleteConfirm = nil
			// Clean up detail view now that delete is confirmed
			if m.detailView != nil {
				m.detailView.Cleanup()
				m.detailView = nil
			}
			m.currentView = ViewDashboard
			return m, m.deleteTask(taskID)
		}
		// Cancelled - return to previous view
		m.pendingDeleteTask = nil
		m.deleteConfirm = nil
		m.currentView = m.previousView
		return m, nil
	}

	return m, cmd
}

func (m *AppModel) showProjectChangeConfirm(updatedTask, originalTask *db.Task) (tea.Model, tea.Cmd) {
	m.pendingProjectChangeTask = updatedTask
	m.originalProjectChangeTask = originalTask
	m.projectChangeConfirmValue = false
	modalWidth := min(60, m.width-8)
	m.projectChangeConfirm = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Key("project_change").
				Title(fmt.Sprintf("Move task #%d to %s?", originalTask.ID, updatedTask.Project)).
				Description(fmt.Sprintf("This will delete task #%d (including its worktree, branch, and executor) and create a new task in the %s project with the same details.\n\nThis action cannot be undone.", originalTask.ID, updatedTask.Project)).
				Affirmative("Move Task").
				Negative("Cancel").
				Value(&m.projectChangeConfirmValue),
		),
	).WithTheme(huh.ThemeDracula()).
		WithWidth(modalWidth - 6). // Account for modal padding and border
		WithShowHelp(true)
	m.previousView = m.currentView
	m.currentView = ViewProjectChangeConfirm
	return m, m.projectChangeConfirm.Init()
}

func (m *AppModel) viewProjectChangeConfirm() string {
	if m.projectChangeConfirm == nil {
		return ""
	}

	// Modal header with move icon
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorWarning).
		MarginBottom(1).
		Render(IconBlocked() + " Move Task to Different Project")

	formView := m.projectChangeConfirm.View()

	// Modal box with border
	modalWidth := min(60, m.width-8)
	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorWarning).
		Padding(1, 2).
		Width(modalWidth)

	modalContent := modalBox.Render(lipgloss.JoinVertical(lipgloss.Center, header, formView))

	// Center the modal on screen
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalContent)
}

func (m *AppModel) updateProjectChangeConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle escape to cancel
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.currentView = m.previousView
			m.projectChangeConfirm = nil
			m.pendingProjectChangeTask = nil
			m.originalProjectChangeTask = nil
			return m, nil
		}
	}

	// Update the form
	model, cmd := m.projectChangeConfirm.Update(msg)
	m.projectChangeConfirm = model.(*huh.Form)

	// Check if form completed
	if m.projectChangeConfirm.State == huh.StateCompleted {
		if m.pendingProjectChangeTask != nil && m.originalProjectChangeTask != nil && m.projectChangeConfirmValue {
			// User confirmed - perform the project change
			newTask := m.pendingProjectChangeTask
			oldTask := m.originalProjectChangeTask
			m.pendingProjectChangeTask = nil
			m.originalProjectChangeTask = nil
			m.projectChangeConfirm = nil
			m.currentView = ViewDashboard
			return m, m.moveTaskToProject(newTask, oldTask)
		}
		// Cancelled - return to previous view
		m.pendingProjectChangeTask = nil
		m.originalProjectChangeTask = nil
		m.projectChangeConfirm = nil
		m.currentView = m.previousView
		return m, nil
	}

	return m, cmd
}

func (m *AppModel) showQuitConfirm() (tea.Model, tea.Cmd) {
	m.quitConfirmValue = false
	modalWidth := min(50, m.width-8)
	m.quitConfirm = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Key("quit").
				Title("Quit Tasks?").
				Description("Are you sure you want to exit the application?").
				Affirmative("Quit").
				Negative("Cancel").
				Value(&m.quitConfirmValue),
		),
	).WithTheme(huh.ThemeDracula()).
		WithWidth(modalWidth - 6). // Account for modal padding and border
		WithShowHelp(true)
	m.currentView = ViewQuitConfirm
	return m, m.quitConfirm.Init()
}

func (m *AppModel) viewQuitConfirm() string {
	if m.quitConfirm == nil {
		return ""
	}

	// Modal header with exit icon
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorWarning).
		MarginBottom(1).
		Render("⏻ Confirm Exit")

	formView := m.quitConfirm.View()

	// Modal box with border
	modalWidth := min(50, m.width-8)
	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorWarning).
		Padding(1, 2).
		Width(modalWidth)

	modalContent := modalBox.Render(lipgloss.JoinVertical(lipgloss.Center, header, formView))

	// Center the modal on screen
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalContent)
}

func (m *AppModel) updateQuitConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle escape to cancel
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.currentView = ViewDashboard
			m.quitConfirm = nil
			return m, nil
		}
	}

	// Update the huh form
	form, cmd := m.quitConfirm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.quitConfirm = f
	}

	// Check if form completed
	if m.quitConfirm.State == huh.StateCompleted {
		if m.quitConfirmValue {
			// User confirmed quit - cleanup and exit
			if m.eventCh != nil {
				m.executor.UnsubscribeTaskEvents(m.eventCh)
			}
			m.stopDatabaseWatcher()
			return m, tea.Quit
		}
		// Cancelled
		m.quitConfirm = nil
		m.currentView = ViewDashboard
		return m, nil
	}

	return m, cmd
}

func (m *AppModel) showCloseConfirm(task *db.Task) (tea.Model, tea.Cmd) {
	m.pendingCloseTask = task
	m.closeConfirmValue = false
	modalWidth := min(50, m.width-8)
	m.closeConfirm = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Key("close").
				Title(fmt.Sprintf("Close task #%d?", task.ID)).
				Description(task.Title).
				Affirmative("Close").
				Negative("Cancel").
				Value(&m.closeConfirmValue),
		),
	).WithTheme(huh.ThemeDracula()).
		WithWidth(modalWidth - 6). // Account for modal padding and border
		WithShowHelp(true)
	m.previousView = m.currentView
	m.currentView = ViewCloseConfirm
	return m, m.closeConfirm.Init()
}

func (m *AppModel) viewCloseConfirm() string {
	if m.closeConfirm == nil {
		return ""
	}

	// Modal header with checkmark icon
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorSuccess).
		MarginBottom(1).
		Render(IconDone() + " Confirm Close")

	formView := m.closeConfirm.View()

	// Modal box with border
	modalWidth := min(50, m.width-8)
	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorSuccess).
		Padding(1, 2).
		Width(modalWidth)

	modalContent := modalBox.Render(lipgloss.JoinVertical(lipgloss.Center, header, formView))

	// Center the modal on screen
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalContent)
}

func (m *AppModel) updateCloseConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle escape to cancel
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.currentView = m.previousView
			m.closeConfirm = nil
			m.pendingCloseTask = nil
			return m, nil
		}
	}

	// Update the huh form
	form, cmd := m.closeConfirm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.closeConfirm = f
	}

	// Check if form completed
	if m.closeConfirm.State == huh.StateCompleted {
		if m.pendingCloseTask != nil && m.closeConfirmValue {
			taskID := m.pendingCloseTask.ID
			m.pendingCloseTask = nil
			m.closeConfirm = nil
			// Clean up detail view now that close is confirmed
			if m.detailView != nil {
				m.detailView.Cleanup()
				m.detailView = nil
			}
			m.currentView = ViewDashboard
			m.userClosedTaskIDs[taskID] = true
			return m, m.closeTask(taskID)
		}
		// Cancelled - return to previous view
		m.pendingCloseTask = nil
		m.closeConfirm = nil
		m.currentView = m.previousView
		return m, nil
	}

	return m, cmd
}

func (m *AppModel) showArchiveConfirm(task *db.Task) (tea.Model, tea.Cmd) {
	m.pendingArchiveTask = task
	m.archiveConfirmValue = false
	modalWidth := min(50, m.width-8)
	m.archiveConfirm = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Key("archive").
				Title(fmt.Sprintf("Archive task #%d?", task.ID)).
				Description(task.Title).
				Affirmative("Archive").
				Negative("Cancel").
				Value(&m.archiveConfirmValue),
		),
	).WithTheme(huh.ThemeDracula()).
		WithWidth(modalWidth - 6). // Account for modal padding and border
		WithShowHelp(true)
	m.previousView = m.currentView
	m.currentView = ViewArchiveConfirm
	return m, m.archiveConfirm.Init()
}

func (m *AppModel) viewArchiveConfirm() string {
	if m.archiveConfirm == nil {
		return ""
	}

	// Modal header with archive icon
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorSecondary).
		MarginBottom(1).
		Render("📦 Confirm Archive")

	formView := m.archiveConfirm.View()

	// Modal box with border
	modalWidth := min(50, m.width-8)
	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorSecondary).
		Padding(1, 2).
		Width(modalWidth)

	modalContent := modalBox.Render(lipgloss.JoinVertical(lipgloss.Center, header, formView))

	// Center the modal on screen
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalContent)
}

func (m *AppModel) updateArchiveConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle escape to cancel
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.currentView = m.previousView
			m.archiveConfirm = nil
			m.pendingArchiveTask = nil
			return m, nil
		}
	}

	// Update the huh form
	form, cmd := m.archiveConfirm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.archiveConfirm = f
	}

	// Check if form completed
	if m.archiveConfirm.State == huh.StateCompleted {
		if m.pendingArchiveTask != nil && m.archiveConfirmValue {
			taskID := m.pendingArchiveTask.ID
			m.pendingArchiveTask = nil
			m.archiveConfirm = nil
			// Clean up detail view now that archive is confirmed
			if m.detailView != nil {
				m.detailView.Cleanup()
				m.detailView = nil
			}
			m.currentView = ViewDashboard
			return m, m.archiveTask(taskID)
		}
		// Cancelled - return to previous view
		m.pendingArchiveTask = nil
		m.archiveConfirm = nil
		m.currentView = m.previousView
		return m, nil
	}

	return m, cmd
}

func (m *AppModel) showChangeStatus(task *db.Task) (tea.Model, tea.Cmd) {
	m.pendingChangeStatusTask = task
	m.changeStatusValue = task.Status

	// Build status options - exclude the current status
	// Only include statuses that map to Kanban columns (Processing is system-managed)
	statusOptions := []huh.Option[string]{}
	allStatuses := []struct {
		value string
		label string
	}{
		{db.StatusBacklog, IconBacklog() + " Backlog"},
		{db.StatusQueued, IconInProgress() + " In Progress"},
		{db.StatusBlocked, IconBlocked() + " Blocked"},
		{db.StatusDone, IconDone() + " Done"},
	}

	for _, s := range allStatuses {
		if s.value != task.Status {
			statusOptions = append(statusOptions, huh.NewOption(s.label, s.value))
		}
	}

	modalWidth := min(50, m.width-8)
	m.changeStatusForm = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Key("status").
				Title(fmt.Sprintf("Change status for task #%d", task.ID)).
				Description(task.Title).
				Options(statusOptions...).
				Value(&m.changeStatusValue),
		),
	).WithTheme(huh.ThemeDracula()).
		WithWidth(modalWidth - 6).
		WithShowHelp(true)
	m.previousView = m.currentView
	m.currentView = ViewChangeStatus
	return m, m.changeStatusForm.Init()
}

func (m *AppModel) viewChangeStatus() string {
	if m.changeStatusForm == nil {
		return ""
	}

	// Modal header
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorSecondary).
		MarginBottom(1).
		Render("⇄ Change Status")

	formView := m.changeStatusForm.View()

	// Modal box with border
	modalWidth := min(50, m.width-8)
	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorSecondary).
		Padding(1, 2).
		Width(modalWidth)

	modalContent := modalBox.Render(lipgloss.JoinVertical(lipgloss.Center, header, formView))

	// Center the modal on screen
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalContent)
}

func (m *AppModel) updateChangeStatus(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle escape to cancel
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.currentView = m.previousView
			m.changeStatusForm = nil
			m.pendingChangeStatusTask = nil
			return m, nil
		}
	}

	// Update the huh form
	form, cmd := m.changeStatusForm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.changeStatusForm = f
	}

	// Check if form completed
	if m.changeStatusForm.State == huh.StateCompleted {
		if m.pendingChangeStatusTask != nil && m.changeStatusValue != "" {
			taskID := m.pendingChangeStatusTask.ID
			newStatus := m.changeStatusValue

			// Update the task in the UI immediately for responsiveness
			m.pendingChangeStatusTask.Status = newStatus
			m.updateTaskInList(m.pendingChangeStatusTask)

			// Update detail view if showing this task
			if m.selectedTask != nil && m.selectedTask.ID == taskID {
				m.selectedTask.Status = newStatus
				if m.detailView != nil {
					m.detailView.UpdateTask(m.selectedTask)
				}
			}

			m.pendingChangeStatusTask = nil
			m.changeStatusForm = nil
			m.currentView = m.previousView
			return m, m.changeTaskStatus(taskID, newStatus)
		}
		// Cancelled or no selection
		m.pendingChangeStatusTask = nil
		m.changeStatusForm = nil
		m.currentView = m.previousView
		return m, nil
	}

	return m, cmd
}

func (m *AppModel) changeTaskStatus(id int64, status string) tea.Cmd {
	database := m.db
	exec := m.executor
	return func() tea.Msg {
		// Get current task to check if we're moving from done to active
		task, err := database.GetTask(id)
		if err != nil {
			return taskStatusChangedMsg{err: err}
		}

		// If moving from done to any active status (except backlog), set to queued
		// so the executor can resume Claude with the stored session
		actualStatus := status
		if task.Status == db.StatusDone && status != db.StatusBacklog && status != db.StatusDone {
			// If task has a Claude session, queue it so executor resumes Claude
			if task.ClaudeSessionID != "" {
				actualStatus = db.StatusQueued
			}
		}

		err = database.UpdateTaskStatus(id, actualStatus)
		if err == nil {
			if task, _ := database.GetTask(id); task != nil {
				exec.NotifyTaskChange("status_changed", task)
			}
		}
		return taskStatusChangedMsg{err: err}
	}
}

type taskStatusChangedMsg struct {
	err error
}

func (m *AppModel) updateSettings(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle escape to go back
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "q" || keyMsg.String() == "esc" {
			// Only exit if not in edit mode or browsing
			if m.settingsView != nil && !m.settingsView.editingProject && !m.settingsView.editingTaskType && !m.settingsView.browsing {
				m.currentView = ViewDashboard
				m.settingsView = nil
				// Refresh kanban theme colors after settings change
				m.kanban.RefreshTheme()
				return m, nil
			}
		}
	}

	if m.settingsView != nil {
		var cmd tea.Cmd
		m.settingsView, cmd = m.settingsView.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *AppModel) updateRetry(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.retryView == nil {
		return m, nil
	}

	var cmd tea.Cmd
	m.retryView, cmd = m.retryView.Update(msg)

	if m.retryView.cancelled {
		m.currentView = m.previousView
		m.retryView = nil
		return m, nil
	}

	if m.retryView.submitted {
		feedback := m.retryView.GetFeedback()
		attachments := m.retryView.GetAttachments()
		taskID := m.retryView.task.ID
		m.currentView = ViewDashboard
		m.retryView = nil
		if m.detailView != nil {
			m.detailView.Cleanup()
			m.detailView = nil
		}
		return m, m.retryTaskWithAttachments(taskID, feedback, attachments)
	}

	return m, cmd
}

func (m *AppModel) updateAttachments(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) {
		m.currentView = m.previousView
		m.attachmentsView = nil
		return m, nil
	}

	if m.attachmentsView != nil {
		var cmd tea.Cmd
		m.attachmentsView, cmd = m.attachmentsView.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *AppModel) updateCommandPalette(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.commandPaletteView == nil {
		return m, nil
	}

	var cmd tea.Cmd
	m.commandPaletteView, cmd = m.commandPaletteView.Update(msg)

	// Check if user cancelled
	if m.commandPaletteView.IsCancelled() {
		m.currentView = ViewDashboard
		m.commandPaletteView = nil
		return m, nil
	}

	// Check if user selected a task
	if selectedTask := m.commandPaletteView.SelectedTask(); selectedTask != nil {
		m.commandPaletteView = nil
		// Select the task on the kanban board and load its detail view
		m.kanban.SelectTask(selectedTask.ID)
		return m, m.loadTask(selectedTask.ID)
	}

	// Check if user requested an AI command
	if m.commandPaletteView.IsAICommandRequest() {
		rawInput := m.commandPaletteView.RawInput()
		projects := m.commandPaletteView.Projects()
		m.commandPaletteView = nil
		m.currentView = ViewDashboard
		m.notification = "Processing command..."
		m.notifyUntil = time.Now().Add(30 * time.Second)
		return m, m.executeAICommand(rawInput, projects)
	}

	return m, cmd
}

// Messages
type tasksLoadedMsg struct {
	tasks           []*db.Task
	err             error
	hiddenDoneCount int            // Number of done tasks not shown in kanban (older ones)
	blockedByDeps   map[int64]int  // Tasks blocked by dependencies (task ID -> open blocker count)
}

type taskLoadedMsg struct {
	task          *db.Task
	err           error
	focusExecutor bool // Focus executor pane after entering detail view (e.g., from notification jump)
}

type taskCreatedMsg struct {
	task *db.Task
	err  error
}

type taskUpdatedMsg struct {
	task *db.Task
	err  error
}

type taskQueuedMsg struct {
	err error
}

type taskClosedMsg struct {
	err error
}

type taskArchivedMsg struct {
	err error
}

type taskUnarchivedMsg struct {
	err error
}

type taskDeletedMsg struct {
	err error
}

type taskDangerousModeToggledMsg struct {
	err error
}

type taskPinnedMsg struct {
	task *db.Task
	err  error
}

type taskRetriedMsg struct {
	err error
}

type taskEventMsg struct {
	event executor.TaskEvent
}

type tickMsg time.Time

type focusTickMsg time.Time

type prRefreshTickMsg time.Time

type dbChangeMsg struct{}

type shortcutTimeoutMsg struct {
	digit   string    // The digit that was pressed
	timeout time.Time // The timeout value to compare
}

type prInfoMsg struct {
	taskID int64
	info   *github.PRInfo
}

type aiCommandMsg struct {
	cmd *ai.Command
	err error
}

const maxDoneTasksInKanban = 20

func (m *AppModel) loadTasks() tea.Cmd {
	return func() tea.Msg {
		// Load all non-done tasks (no limit)
		activeTasks, err := m.db.ListTasks(db.ListTasksOptions{Limit: 0, IncludeClosed: false})
		if err != nil {
			return tasksLoadedMsg{err: err}
		}

		// Load limited done tasks (most recent)
		doneTasks, err := m.db.ListTasks(db.ListTasksOptions{Status: db.StatusDone, Limit: maxDoneTasksInKanban})
		if err != nil {
			return tasksLoadedMsg{err: err}
		}

		// Count total done tasks to show "more" message
		totalDone, err := m.db.CountTasksByStatus(db.StatusDone)
		if err != nil {
			return tasksLoadedMsg{err: err}
		}

		// Combine active + limited done tasks
		tasks := append(activeTasks, doneTasks...)
		hiddenDone := totalDone - len(doneTasks)
		if hiddenDone < 0 {
			hiddenDone = 0
		}

		// Load dependency blocker counts for each task
		blockedByDeps := make(map[int64]int)
		for _, task := range tasks {
			count, err := m.db.GetOpenBlockerCount(task.ID)
			if err == nil && count > 0 {
				blockedByDeps[task.ID] = count
			}
		}

		// Note: PR/merge status is now checked via batch refresh (prRefreshTick)
		// to avoid spawning processes for every task on every tick
		return tasksLoadedMsg{tasks: tasks, err: err, hiddenDoneCount: hiddenDone, blockedByDeps: blockedByDeps}
	}
}

func (m *AppModel) loadTask(id int64) tea.Cmd {
	return m.loadTaskWithOptions(id, false)
}

// loadTaskWithFocus loads a task and focuses the executor pane when entering detail view.
// Used when jumping to a task from a notification.
func (m *AppModel) loadTaskWithFocus(id int64) tea.Cmd {
	return m.loadTaskWithOptions(id, true)
}

func (m *AppModel) loadTaskWithOptions(id int64, focusExecutor bool) tea.Cmd {
	// Check PR state asynchronously (don't block UI)
	if m.executor != nil {
		go m.executor.CheckPRStateAndUpdateTask(id)
	}

	// Update last accessed timestamp (async, don't block UI)
	if m.db != nil {
		database := m.db
		go database.UpdateTaskLastAccessedAt(id)
	}

	return func() tea.Msg {
		task, err := m.db.GetTask(id)
		return taskLoadedMsg{task: task, err: err, focusExecutor: focusExecutor}
	}
}

// updateTaskWithRename updates a task and renames the Claude session if the title changed.
func (m *AppModel) updateTaskWithRename(newTask *db.Task, oldTitle string) tea.Cmd {
	database := m.db
	exec := m.executor
	return func() tea.Msg {
		err := database.UpdateTask(newTask)
		if err == nil {
			exec.NotifyTaskChange("updated", newTask)

			// If title changed and task has a worktree, rename the Claude session
			if oldTitle != newTask.Title && newTask.WorktreePath != "" {
				exec.RenameClaudeSessionForTask(newTask, newTask.Title)
			}
		}
		return taskUpdatedMsg{task: newTask, err: err}
	}
}

func (m *AppModel) createTaskWithAttachments(t *db.Task, attachmentPaths []string) tea.Cmd {
	exec := m.executor
	database := m.db
	return func() tea.Msg {
		// Generate title from body if title is empty but body is provided
		if strings.TrimSpace(t.Title) == "" && strings.TrimSpace(t.Body) != "" {
			// Try to generate title using LLM
			var apiKey string
			if database != nil {
				apiKey, _ = database.GetSetting("anthropic_api_key")
			}
			svc := autocomplete.NewService(apiKey)
			if svc.IsAvailable() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if title, err := svc.GenerateTitle(ctx, t.Body, t.Project); err == nil && title != "" {
					t.Title = title
				}
			}
			// If generation failed, use a fallback
			if strings.TrimSpace(t.Title) == "" {
				// Use first line of body, truncated
				firstLine := strings.Split(strings.TrimSpace(t.Body), "\n")[0]
				if len(firstLine) > 50 {
					firstLine = firstLine[:50] + "..."
				}
				t.Title = firstLine
			}
		}

		err := database.CreateTask(t)
		if err != nil {
			return taskCreatedMsg{task: t, err: err}
		}

		// Mark onboarding as complete when first task is created
		database.CompleteOnboarding()

		// Add attachments if provided
		for _, attachmentPath := range attachmentPaths {
			if attachmentPath != "" {
				data, readErr := os.ReadFile(attachmentPath)
				if readErr == nil {
					mimeType := detectMimeType(attachmentPath)
					database.AddAttachment(t.ID, filepath.Base(attachmentPath), mimeType, data)
				}
			}
		}

		exec.NotifyTaskChange("created", t)
		return taskCreatedMsg{task: t, err: nil}
	}
}

func (m *AppModel) queueTask(id int64) tea.Cmd {
	database := m.db
	exec := m.executor
	return func() tea.Msg {
		err := database.UpdateTaskStatus(id, db.StatusQueued)
		if err == nil {
			if task, _ := database.GetTask(id); task != nil {
				exec.NotifyTaskChange("status_changed", task)
			}
		}
		return taskQueuedMsg{err: err}
	}
}

func (m *AppModel) closeTask(id int64) tea.Cmd {
	database := m.db
	exec := m.executor
	return func() tea.Msg {
		// Check PR state asynchronously (don't block UI)
		go exec.CheckPRStateAndUpdateTask(id)

		err := database.UpdateTaskStatus(id, db.StatusDone)
		if err == nil {
			if task, _ := database.GetTask(id); task != nil {
				exec.NotifyTaskChange("status_changed", task)
			}
		}

		// Kill the task window to clean up both Claude and workdir panes
		windowTarget := executor.TmuxSessionName(id)
		osExec.Command("tmux", "kill-window", "-t", windowTarget).Run()

		return taskClosedMsg{err: err}
	}
}

func (m *AppModel) archiveTask(id int64) tea.Cmd {
	database := m.db
	exec := m.executor
	return func() tea.Msg {
		// Get the task first
		task, err := database.GetTask(id)
		if err != nil {
			return taskArchivedMsg{err: err}
		}

		// Kill Claude process to free memory
		exec.KillClaudeProcess(id)

		// Kill the task window to clean up both Claude and workdir panes
		windowTarget := executor.TmuxSessionName(id)
		osExec.Command("tmux", "kill-window", "-t", windowTarget).Run()

		// Archive worktree (saves uncommitted changes and removes worktree)
		if task != nil && task.WorktreePath != "" {
			if err := exec.ArchiveWorktree(task); err != nil {
				// Log warning but continue with archiving
				fmt.Fprintf(os.Stderr, "Warning: could not archive worktree: %v\n", err)
			}
		}

		// Update status to archived
		err = database.UpdateTaskStatus(id, db.StatusArchived)
		if err == nil {
			if task, _ := database.GetTask(id); task != nil {
				exec.NotifyTaskChange("status_changed", task)
			}
		}

		return taskArchivedMsg{err: err}
	}
}

func (m *AppModel) unarchiveTask(id int64) tea.Cmd {
	database := m.db
	exec := m.executor
	return func() tea.Msg {
		// Get the task first
		task, err := database.GetTask(id)
		if err != nil {
			return taskUnarchivedMsg{err: err}
		}

		// Unarchive worktree if there's saved archive state
		if task != nil && task.HasArchiveState() {
			if err := exec.UnarchiveWorktree(task); err != nil {
				return taskUnarchivedMsg{err: fmt.Errorf("unarchive worktree: %w", err)}
			}
		}

		// Update status back to backlog (user can then queue it if they want)
		err = database.UpdateTaskStatus(id, db.StatusBacklog)
		if err == nil {
			if task, _ := database.GetTask(id); task != nil {
				exec.NotifyTaskChange("status_changed", task)
			}
		}

		return taskUnarchivedMsg{err: err}
	}
}

func (m *AppModel) deleteTask(id int64) tea.Cmd {
	return func() tea.Msg {
		// Get task to check for worktree
		task, err := m.db.GetTask(id)
		if err != nil {
			return taskDeletedMsg{err: err}
		}

		// Kill Claude process to free memory
		m.executor.KillClaudeProcess(id)

		// Kill tmux window (ignore errors)
		windowTarget := executor.TmuxSessionName(id)
		osExec.Command("tmux", "kill-window", "-t", windowTarget).Run()

		// Clean up worktree and Claude sessions if they exist
		if task != nil && task.WorktreePath != "" {
			projectConfigDir := ""
			if task.Project != "" {
				if project, err := m.db.GetProjectByName(task.Project); err == nil && project != nil {
					projectConfigDir = project.ClaudeConfigDir
				}
			}
			// Clean up Claude session files first (before worktree is removed)
			executor.CleanupClaudeSessions(task.WorktreePath, projectConfigDir)

			// Clean up worktree
			m.executor.CleanupWorktree(task)
		}

		// Delete from database
		err = m.db.DeleteTask(id)
		return taskDeletedMsg{err: err}
	}
}

// worktreeOpenedMsg is returned when attempting to open a worktree in the editor.
type worktreeOpenedMsg struct {
	message string
	err     error
}

// openWorktreeInEditor opens the task's worktree directory in the default editor.
// It checks VISUAL, then EDITOR environment variables, falling back to "open" on macOS.
func (m *AppModel) openWorktreeInEditor(task *db.Task) tea.Cmd {
	return func() tea.Msg {
		if task.WorktreePath == "" {
			return worktreeOpenedMsg{err: fmt.Errorf("no worktree for task #%d", task.ID)}
		}

		// Check if worktree directory exists
		if _, err := os.Stat(task.WorktreePath); os.IsNotExist(err) {
			return worktreeOpenedMsg{err: fmt.Errorf("worktree not found: %s", task.WorktreePath)}
		}

		// Try VISUAL, then EDITOR, then fall back to "open" command
		editor := os.Getenv("VISUAL")
		if editor == "" {
			editor = os.Getenv("EDITOR")
		}

		var cmd *osExec.Cmd
		if editor != "" {
			cmd = osExec.Command(editor, task.WorktreePath)
		} else {
			// Fall back to "open" command on macOS (opens in Finder or default app)
			cmd = osExec.Command("open", task.WorktreePath)
		}

		if err := cmd.Start(); err != nil {
			return worktreeOpenedMsg{err: fmt.Errorf("failed to open editor: %w", err)}
		}

		return worktreeOpenedMsg{message: fmt.Sprintf("Opened %s", filepath.Base(task.WorktreePath))}
	}
}

// browserOpenedMsg is returned when attempting to open the browser.
type browserOpenedMsg struct {
	message string
	err     error
}

// openBrowser opens the task's server URL in the default browser.
// The URL is {server_url}:{port} where server_url is configurable (default: http://localhost).
func (m *AppModel) openBrowser(task *db.Task) tea.Cmd {
	return func() tea.Msg {
		if task.Port == 0 {
			return browserOpenedMsg{err: fmt.Errorf("no port allocated for task #%d", task.ID)}
		}

		// Get server URL from settings, default to http://localhost
		serverURL := config.DefaultServerURL
		if url, err := m.db.GetSetting(config.SettingServerURL); err == nil && url != "" {
			serverURL = url
		}

		url := fmt.Sprintf("%s:%d", serverURL, task.Port)
		cmd := osExec.Command("open", url)

		if err := cmd.Start(); err != nil {
			return browserOpenedMsg{err: fmt.Errorf("failed to open browser: %w", err)}
		}

		return browserOpenedMsg{message: fmt.Sprintf("Opened %s", url)}
	}
}

// taskMovedMsg is returned when a task is moved to a different project.
type taskMovedMsg struct {
	newTask *db.Task
	oldID   int64
	err     error
}

// moveTaskToProject moves a task to a different project by creating a new task
// in the target project and deleting the old task (including its worktree).
func (m *AppModel) moveTaskToProject(newTaskData *db.Task, oldTask *db.Task) tea.Cmd {
	database := m.db
	exec := m.executor
	return func() tea.Msg {
		// First, clean up the old task's resources

		// Kill Claude process to free memory
		exec.KillClaudeProcess(oldTask.ID)

		// Kill tmux window (ignore errors)
		windowTarget := executor.TmuxSessionName(oldTask.ID)
		osExec.Command("tmux", "kill-window", "-t", windowTarget).Run()

		// Clean up worktree and Claude sessions if they exist
		if oldTask.WorktreePath != "" {
			oldConfigDir := ""
			if oldTask.Project != "" {
				if project, err := database.GetProjectByName(oldTask.Project); err == nil && project != nil {
					oldConfigDir = project.ClaudeConfigDir
				}
			}
			// Clean up Claude session files first (before worktree is removed)
			executor.CleanupClaudeSessions(oldTask.WorktreePath, oldConfigDir)

			// Clean up worktree
			exec.CleanupWorktree(oldTask)
		}

		// Delete the old task from database
		err := database.DeleteTask(oldTask.ID)
		if err != nil {
			return taskMovedMsg{err: fmt.Errorf("delete old task: %w", err)}
		}

		// Create the new task in the target project
		// Reset fields that should be fresh for the new task
		newTaskData.ID = 0
		newTaskData.WorktreePath = ""
		newTaskData.BranchName = ""
		newTaskData.Port = 0
		newTaskData.ClaudeSessionID = ""
		newTaskData.DaemonSession = ""
		newTaskData.StartedAt = nil
		newTaskData.CompletedAt = nil
		// Keep the status - if it was backlog, stay backlog; if queued, stay queued
		// But if it was processing/blocked, reset to backlog since the work is lost
		if newTaskData.Status == db.StatusProcessing || newTaskData.Status == db.StatusBlocked {
			newTaskData.Status = db.StatusBacklog
		}

		err = database.CreateTask(newTaskData)
		if err != nil {
			return taskMovedMsg{err: fmt.Errorf("create new task: %w", err)}
		}

		// Notify about the changes
		exec.NotifyTaskChange("deleted", oldTask)
		exec.NotifyTaskChange("created", newTaskData)

		return taskMovedMsg{newTask: newTaskData, oldID: oldTask.ID, err: nil}
	}
}

func (m *AppModel) toggleDangerousMode(id int64) tea.Cmd {
	exec := m.executor
	database := m.db
	return func() tea.Msg {
		// Get the task to check current dangerous mode state
		task, err := database.GetTask(id)
		if err != nil || task == nil {
			return taskDangerousModeToggledMsg{err: fmt.Errorf("failed to get task")}
		}

		executorName := taskExecutorDisplayName(task)
		var success bool
		if task.DangerousMode {
			// Currently in dangerous mode, switch to safe mode
			success = exec.ResumeSafe(id)
			if !success {
				return taskDangerousModeToggledMsg{err: fmt.Errorf("failed to restart %s in safe mode", executorName)}
			}
		} else {
			// Currently in safe mode, switch to dangerous mode
			success = exec.ResumeDangerous(id)
			if !success {
				return taskDangerousModeToggledMsg{err: fmt.Errorf("failed to restart %s in dangerous mode", executorName)}
			}
		}
		return taskDangerousModeToggledMsg{err: nil}
	}
}

func (m *AppModel) toggleTaskPinned(id int64) tea.Cmd {
	database := m.db
	return func() tea.Msg {
		task, err := database.GetTask(id)
		if err != nil || task == nil {
			return taskPinnedMsg{err: fmt.Errorf("failed to get task")}
		}

		newValue := !task.Pinned
		if err := database.UpdateTaskPinned(id, newValue); err != nil {
			return taskPinnedMsg{err: fmt.Errorf("toggle pin: %w", err)}
		}
		task.Pinned = newValue
		return taskPinnedMsg{task: task}
	}
}

func (m *AppModel) retryTaskWithAttachments(id int64, feedback string, attachmentPaths []string) tea.Cmd {
	database := m.db
	exec := m.executor
	return func() tea.Msg {
		// Get task to find worktree path
		task, _ := database.GetTask(id)

		// Add attachments to database first
		for _, attachmentPath := range attachmentPaths {
			if attachmentPath != "" {
				data, readErr := os.ReadFile(attachmentPath)
				if readErr == nil {
					mimeType := detectMimeType(attachmentPath)
					database.AddAttachment(id, filepath.Base(attachmentPath), mimeType, data)
				}
			}
		}

		// Check if tmux session is still alive
		sessionName := executor.TmuxSessionName(id)
		if err := osExec.Command("tmux", "has-session", "-t", sessionName).Run(); err == nil {
			// Session alive - prepare attachments and send feedback via send-keys
			feedbackToSend := feedback

			// If there are new attachments, write them to files and include paths in feedback
			if len(attachmentPaths) > 0 && task != nil {
				// Determine directory for attachments
				attachDir := ""
				if task.WorktreePath != "" {
					// Use a subdirectory within the worktree
					attachDir = filepath.Join(task.WorktreePath, ".task-attachments")
				} else if projectDir := exec.GetProjectDir(task.Project); projectDir != "" {
					attachDir = filepath.Join(projectDir, ".task-attachments")
				}

				if attachDir != "" {
					os.MkdirAll(attachDir, 0755)
					var writtenPaths []string
					for _, attachmentPath := range attachmentPaths {
						if attachmentPath != "" {
							data, readErr := os.ReadFile(attachmentPath)
							if readErr == nil {
								destPath := filepath.Join(attachDir, filepath.Base(attachmentPath))
								if writeErr := os.WriteFile(destPath, data, 0644); writeErr == nil {
									writtenPaths = append(writtenPaths, destPath)
								}
							}
						}
					}

					// Append attachment info to feedback
					if len(writtenPaths) > 0 {
						attachmentInfo := "\n\n[New attachments added - you can read these files using the Read tool:\n"
						for _, p := range writtenPaths {
							attachmentInfo += "- " + p + "\n"
						}
						attachmentInfo += "]"
						feedbackToSend = feedback + attachmentInfo
					}
				}
			}

			if feedbackToSend != "" {
				database.AppendTaskLog(id, "text", "Feedback: "+feedbackToSend)
				osExec.Command("tmux", "send-keys", "-t", sessionName, feedbackToSend, "Enter").Run()
			}
			// Update status to processing
			database.UpdateTaskStatus(id, db.StatusProcessing)
			return taskRetriedMsg{err: nil}
		}

		// Session dead - re-queue for executor to pick up with --resume
		err := database.RetryTask(id, feedback)
		return taskRetriedMsg{err: err}
	}
}

func (m *AppModel) waitForTaskEvent() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.eventCh
		if !ok {
			return nil // Channel closed
		}
		return taskEventMsg{event: event}
	}
}

func (m *AppModel) tick() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *AppModel) focusTick() tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(t time.Time) tea.Msg {
		return focusTickMsg(t)
	})
}

func (m *AppModel) prRefreshTick() tea.Cmd {
	return tea.Tick(30*time.Second, func(t time.Time) tea.Msg {
		return prRefreshTickMsg(t)
	})
}

// fetchPRInfo fetches PR info for a single task (used for detail view).
func (m *AppModel) fetchPRInfo(task *db.Task) tea.Cmd {
	if task.BranchName == "" || m.prCache == nil {
		return nil
	}

	// Get the repo directory for gh CLI (use project dir, not worktree)
	repoDir := m.executor.GetProjectDir(task.Project)
	if repoDir == "" {
		return nil
	}

	prCache := m.prCache
	taskID := task.ID
	branchName := task.BranchName

	return func() tea.Msg {
		// Try cache first, fall back to single fetch if needed
		info := prCache.GetCachedPR(repoDir, branchName)
		if info == nil {
			info = prCache.GetPRForBranch(repoDir, branchName)
		}
		return prInfoMsg{taskID: taskID, info: info}
	}
}

// refreshAllPRs fetches PR info for all repos in batch (much more efficient).
// Instead of N gh calls for N tasks, this makes M calls for M unique repos.
func (m *AppModel) refreshAllPRs() tea.Cmd {
	if m.prCache == nil {
		return nil
	}

	// Group tasks by repo directory
	repoTasks := make(map[string][]*db.Task)
	for _, task := range m.tasks {
		if task.BranchName == "" {
			continue
		}
		repoDir := m.executor.GetProjectDir(task.Project)
		if repoDir == "" {
			continue
		}
		repoTasks[repoDir] = append(repoTasks[repoDir], task)
	}

	if len(repoTasks) == 0 {
		return nil
	}

	prCache := m.prCache
	// Copy the map to avoid race conditions
	repoTasksCopy := make(map[string][]*db.Task)
	for k, v := range repoTasks {
		tasksCopy := make([]*db.Task, len(v))
		copy(tasksCopy, v)
		repoTasksCopy[k] = tasksCopy
	}

	return func() tea.Msg {
		var results []prInfoMsg

		// Fetch PRs for each repo sequentially (avoids memory spikes)
		for repoDir, tasks := range repoTasksCopy {
			prsByBranch := github.FetchAllPRsForRepo(repoDir)
			if prsByBranch != nil {
				// Update cache with batch results
				prCache.UpdateCacheForRepo(repoDir, prsByBranch)

				// Create messages for tasks in this repo
				for _, task := range tasks {
					info := prsByBranch[task.BranchName]
					results = append(results, prInfoMsg{taskID: task.ID, info: info})
				}
			}
		}

		return prBatchMsg{results: results}
	}
}

// prBatchMsg contains PR info for multiple tasks (from batch fetch).
type prBatchMsg struct {
	results []prInfoMsg
}

// startDatabaseWatcher starts watching the database file for changes.
func (m *AppModel) startDatabaseWatcher() {
	if m.watcher == nil {
		return
	}

	dbPath := m.db.Path()
	if dbPath == "" {
		return
	}

	// Watch both the main database file and the WAL file (SQLite WAL mode)
	m.watcher.Add(dbPath)
	m.watcher.Add(dbPath + "-wal")

	// Start goroutine to forward fsnotify events to the channel
	go func() {
		for {
			select {
			case event, ok := <-m.watcher.Events:
				if !ok {
					return
				}
				// Only trigger on write events
				if event.Op&fsnotify.Write == fsnotify.Write {
					// Non-blocking send to debounce rapid changes
					select {
					case m.dbChangeCh <- struct{}{}:
					default:
					}
				}
			case _, ok := <-m.watcher.Errors:
				if !ok {
					return
				}
				// Ignore errors, just keep watching
			}
		}
	}()
}

// waitForDBChange returns a command that waits for database file changes.
func (m *AppModel) waitForDBChange() tea.Cmd {
	return func() tea.Msg {
		_, ok := <-m.dbChangeCh
		if !ok {
			return nil
		}
		return dbChangeMsg{}
	}
}

// stopDatabaseWatcher stops the file watcher.
func (m *AppModel) stopDatabaseWatcher() {
	if m.watcher != nil {
		m.watcher.Close()
	}
	if m.dbChangeCh != nil {
		close(m.dbChangeCh)
	}
}

// executeAICommand sends the user input to the AI service for interpretation.
func (m *AppModel) executeAICommand(input string, projects []*db.Project) tea.Cmd {
	aiSvc := m.aiCommandService
	tasks := m.tasks
	return func() tea.Msg {
		if aiSvc == nil || !aiSvc.IsAvailable() {
			return aiCommandMsg{err: fmt.Errorf("AI command service not available (set ANTHROPIC_API_KEY)")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		cmdCtx := &ai.Context{
			Tasks:    tasks,
			Projects: projects,
		}

		cmd, err := aiSvc.InterpretCommand(ctx, input, cmdCtx)
		return aiCommandMsg{cmd: cmd, err: err}
	}
}

// handleAICommand executes the parsed AI command.
func (m *AppModel) handleAICommand(cmd *ai.Command) tea.Cmd {
	switch cmd.Type {
	case ai.CommandCreateTask:
		// Create a new task
		project := cmd.Project
		if project == "" {
			// Try to detect project from working directory or use last used project
			if m.workingDir != "" {
				for _, p := range m.getProjects() {
					if strings.HasPrefix(m.workingDir, p.Path) {
						project = p.Name
						break
					}
				}
			}
			if project == "" {
				lastProject, _ := m.db.GetSetting("last_used_project")
				if lastProject != "" {
					project = lastProject
				} else {
					project = "personal"
				}
			}
		}

		newTask := &db.Task{
			Title:   cmd.Title,
			Body:    cmd.Body,
			Status:  db.StatusBacklog,
			Project: project,
		}
		m.notification = fmt.Sprintf("%s %s", IconDone(), cmd.Message)
		m.notifyUntil = time.Now().Add(5 * time.Second)
		return m.createTaskWithAttachments(newTask, nil)

	case ai.CommandUpdateStatus:
		if cmd.TaskID == 0 {
			m.notification = fmt.Sprintf("%s Could not find task ID", IconBlocked())
			m.notifyUntil = time.Now().Add(3 * time.Second)
			return nil
		}

		m.notification = fmt.Sprintf("%s %s", IconDone(), cmd.Message)
		m.notifyUntil = time.Now().Add(3 * time.Second)

		database := m.db
		exec := m.executor
		return func() tea.Msg {
			err := database.UpdateTaskStatus(cmd.TaskID, cmd.Status)
			if err == nil {
				if task, _ := database.GetTask(cmd.TaskID); task != nil {
					exec.NotifyTaskChange("status_changed", task)
				}
			}
			return taskStatusChangedMsg{err: err}
		}

	case ai.CommandSelectTask:
		if cmd.TaskID == 0 {
			m.notification = fmt.Sprintf("%s Could not find task ID", IconBlocked())
			m.notifyUntil = time.Now().Add(3 * time.Second)
			return nil
		}

		m.notification = fmt.Sprintf("%s %s", IconDone(), cmd.Message)
		m.notifyUntil = time.Now().Add(2 * time.Second)
		m.kanban.SelectTask(cmd.TaskID)
		return m.loadTask(cmd.TaskID)

	case ai.CommandSearchTasks:
		// Apply the search as a filter
		m.filterText = cmd.Query
		m.filterInput.SetValue(cmd.Query)
		m.notification = fmt.Sprintf("%s %s", IconDone(), cmd.Message)
		m.notifyUntil = time.Now().Add(3 * time.Second)
		return m.loadTasks()

	default:
		m.notification = fmt.Sprintf("%s %s", IconBlocked(), cmd.Message)
		m.notifyUntil = time.Now().Add(5 * time.Second)
		return nil
	}
}

// getProjects returns the list of projects from the database.
func (m *AppModel) getProjects() []*db.Project {
	projects, _ := m.db.ListProjects()
	return projects
}
