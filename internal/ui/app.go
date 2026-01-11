package ui

import (
	"fmt"
	"os"
	osExec "os/exec"
	"path/filepath"
	"time"

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
)

// View represents the current view.
type View int

const (
	ViewDashboard View = iota
	ViewDetail
	ViewNewTask
	ViewNewTaskConfirm
	ViewEditTask
	ViewDeleteConfirm
	ViewKillConfirm
	ViewQuitConfirm
	ViewSettings
	ViewRetry
	ViewMemories
	ViewAttachments
	ViewChangeStatus
	ViewCommandPalette
)

// KeyMap defines key bindings.
type KeyMap struct {
	Left           key.Binding
	Right          key.Binding
	Up             key.Binding
	Down           key.Binding
	Enter          key.Binding
	Back           key.Binding
	New            key.Binding
	Edit           key.Binding
	Queue          key.Binding
	Retry          key.Binding
	Close          key.Binding
	Delete         key.Binding
	Kill           key.Binding
	Filter         key.Binding
	Refresh        key.Binding
	Settings       key.Binding
	Memories       key.Binding
	Files          key.Binding
	Help           key.Binding
	Quit           key.Binding
	ChangeStatus   key.Binding
	CommandPalette key.Binding
	// Column focus shortcuts
	FocusBacklog    key.Binding
	FocusInProgress key.Binding
	FocusBlocked    key.Binding
	FocusDone       key.Binding
}

// ShortHelp returns key bindings to show in the mini help.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Left, k.Right, k.Up, k.Down, k.Enter, k.New, k.Queue, k.Filter, k.Quit}
}

// FullHelp returns keybindings for the expanded help view.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Left, k.Right, k.Up, k.Down},
		{k.FocusBacklog, k.FocusInProgress, k.FocusBlocked, k.FocusDone},
		{k.Enter, k.New, k.Queue, k.Close},
		{k.Retry, k.Delete, k.Kill},
		{k.Filter, k.CommandPalette, k.Settings, k.Memories},
		{k.Files, k.ChangeStatus, k.Refresh, k.Help, k.Quit},
	}
}

// DefaultKeyMap returns the default key bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Left: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("←/h", "prev col"),
		),
		Right: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("→/l", "next col"),
		),
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "view"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc", "q"),
			key.WithHelp("q/esc", "back"),
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
		Delete: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "delete"),
		),
		Kill: key.NewBinding(
			key.WithKeys("k"),
			key.WithHelp("k", "kill"),
		),
		Filter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "filter"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("R"),
			key.WithHelp("R", "refresh"),
		),
		Settings: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "settings"),
		),
		Memories: key.NewBinding(
			key.WithKeys("m"),
			key.WithHelp("m", "memories"),
		),
		Files: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "files"),
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
	}
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

	// Track task statuses to detect changes
	prevStatuses map[int64]string

	// Real-time event subscription
	eventCh chan executor.TaskEvent

	// File watcher for database changes
	watcher   *fsnotify.Watcher
	dbChangeCh chan struct{}

	// PR status cache
	prCache *github.PRCache

	// Number filter for quick task ID jump
	numberFilter string

	// Shortcut mode for #<id> direct task jump
	shortcutMode   bool
	shortcutBuffer string

	// Text filter input
	filterInput  textinput.Model
	filtering    bool

	// Detail view state
	selectedTask *db.Task
	detailView   *DetailModel

	// New task form state
	newTaskForm        *FormModel
	pendingTask        *db.Task
	pendingAttachments []string
	queueConfirm       *huh.Form
	queueValue         bool

	// Edit task form state
	editTaskForm *FormModel
	editingTask  *db.Task

	// Delete confirmation state
	deleteConfirm      *huh.Form
	deleteConfirmValue bool
	pendingDeleteTask  *db.Task

	// Kill confirmation state
	killConfirm      *huh.Form
	killConfirmValue bool
	pendingKillTask  *db.Task

	// Quit confirmation state
	quitConfirm      *huh.Form
	quitConfirmValue bool

	// Settings view state
	settingsView *SettingsModel

	// Retry view state
	retryView *RetryModel

	// Memories view state
	memoriesView *MemoriesModel

	// Attachments view state
	attachmentsView *AttachmentsModel

	// Change status view state
	changeStatusForm        *huh.Form
	changeStatusValue       string
	pendingChangeStatusTask *db.Task

	// Command palette view state
	commandPaletteView *CommandPaletteModel

	// Window size
	width  int
	height int
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
	// Load saved theme from database
	LoadThemeFromDB(database.GetSetting)

	// Start with zero size - will be set by WindowSizeMsg
	kanban := NewKanbanBoard(0, 0)

	// Setup filter input
	fi := textinput.New()
	fi.Placeholder = "Filter tasks..."
	fi.CharLimit = 50
	fi.Width = 30

	// Setup help
	h := help.New()
	h.ShowAll = false

	// Setup file watcher for database changes
	watcher, _ := fsnotify.NewWatcher()
	dbChangeCh := make(chan struct{}, 1)

	return &AppModel{
		db:           database,
		executor:     exec,
		workingDir:   workingDir,
		keys:         DefaultKeyMap(),
		help:         h,
		currentView:  ViewDashboard,
		kanban:       kanban,
		filterInput:  fi,
		loading:      true,
		prevStatuses: make(map[int64]string),
		watcher:      watcher,
		dbChangeCh:   dbChangeCh,
		prCache:      github.NewPRCache(),
	}
}

// Init initializes the model.
func (m *AppModel) Init() tea.Cmd {
	// Subscribe to real-time task events
	m.eventCh = m.executor.SubscribeTaskEvents()

	// Start watching database file for changes
	m.startDatabaseWatcher()

	// Enable mouse support for click-to-focus on tmux panes
	if os.Getenv("TMUX") != "" {
		osExec.Command("tmux", "set-option", "-t", "task-ui", "mouse", "on").Run()
	}

	return tea.Batch(m.loadTasks(), m.waitForTaskEvent(), m.waitForDBChange(), m.tick(), m.prRefreshTick(), m.refreshAllPRs())
}

// Update handles messages.
func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

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
	if m.currentView == ViewDeleteConfirm && m.deleteConfirm != nil {
		return m.updateDeleteConfirm(msg)
	}
	if m.currentView == ViewKillConfirm && m.killConfirm != nil {
		return m.updateKillConfirm(msg)
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

		// Route to current view
		switch m.currentView {
		case ViewDashboard:
			return m.updateDashboard(msg)
		case ViewDetail:
			return m.updateDetail(msg)
		case ViewMemories:
			return m.updateMemories(msg)
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

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = msg.Width
		m.kanban.SetSize(msg.Width, msg.Height-4)
		if m.detailView != nil {
			m.detailView.SetSize(msg.Width, msg.Height)
		}
		if m.settingsView != nil {
			m.settingsView.SetSize(msg.Width, msg.Height)
		}
		if m.retryView != nil {
			m.retryView.SetSize(msg.Width, msg.Height)
		}
		if m.commandPaletteView != nil {
			m.commandPaletteView.SetSize(msg.Width, msg.Height)
		}

	case tasksLoadedMsg:
		m.loading = false
		m.tasks = msg.tasks
		m.err = msg.err

		// Check for newly blocked/done tasks and notify
		for _, t := range m.tasks {
			prevStatus := m.prevStatuses[t.ID]
			if prevStatus != "" && prevStatus != t.Status {
				if t.Status == db.StatusBlocked {
					// Task just became blocked - ring bell and show notification
					m.notification = fmt.Sprintf("⚠ Task #%d needs input: %s", t.ID, t.Title)
					m.notifyUntil = time.Now().Add(10 * time.Second)
					RingBell() // Ring terminal bell (writes to /dev/tty to bypass TUI)
				} else if t.Status == db.StatusDone && db.IsInProgress(prevStatus) {
					// Task completed - ring bell and show notification
					m.notification = fmt.Sprintf("✓ Task #%d complete: %s", t.ID, t.Title)
					m.notifyUntil = time.Now().Add(5 * time.Second)
					RingBell() // Ring terminal bell (writes to /dev/tty to bypass TUI)
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

		m.kanban.SetTasks(m.tasks)

		// PR info is fetched separately via prRefreshTick, not on every task load

	case taskLoadedMsg:
		if msg.err == nil {
			m.selectedTask = msg.task
			// Resume task if it was suspended (blocked idle tasks get suspended to save memory)
			if m.executor.IsSuspended(msg.task.ID) {
				m.executor.ResumeTask(msg.task.ID)
			}
			m.detailView = NewDetailModel(msg.task, m.db, m.width, m.height)
			m.previousView = m.currentView
			m.currentView = ViewDetail
			// Start tmux output ticker if session is active
			if tickerCmd := m.detailView.StartTmuxTicker(); tickerCmd != nil {
				cmds = append(cmds, tickerCmd)
			}
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

	case taskQueuedMsg, taskClosedMsg, taskDeletedMsg, taskRetriedMsg, taskKilledMsg, taskStatusChangedMsg:
		cmds = append(cmds, m.loadTasks())

	case taskEventMsg:
		// Real-time task update from executor
		event := msg.event
		if event.Task != nil {
			// Update task in our list
			for i, t := range m.tasks {
				if t.ID == event.TaskID {
					prevStatus := t.Status
					m.tasks[i] = event.Task
					
					// Show notification for status changes
					if prevStatus != event.Task.Status {
						if event.Task.Status == db.StatusBlocked {
							m.notification = fmt.Sprintf("⚠ Task #%d needs input: %s", event.TaskID, event.Task.Title)
							m.notifyUntil = time.Now().Add(10 * time.Second)
							RingBell() // Ring terminal bell (writes to /dev/tty to bypass TUI)
						} else if event.Task.Status == db.StatusDone && db.IsInProgress(prevStatus) {
							m.notification = fmt.Sprintf("✓ Task #%d complete: %s", event.TaskID, event.Task.Title)
							m.notifyUntil = time.Now().Add(5 * time.Second)
							RingBell() // Ring terminal bell (writes to /dev/tty to bypass TUI)
						} else if db.IsInProgress(event.Task.Status) {
							m.notification = fmt.Sprintf("▶ Task #%d started: %s", event.TaskID, event.Task.Title)
							m.notifyUntil = time.Now().Add(3 * time.Second)
						}
						m.prevStatuses[event.TaskID] = event.Task.Status
					}
					break
				}
			}
			m.kanban.SetTasks(m.tasks)

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
		}
		// Refresh detail view if active (for logs which may update frequently)
		if m.currentView == ViewDetail && m.detailView != nil {
			m.detailView.Refresh()
		}
		// Poll database for task changes (hooks run in separate process)
		if m.currentView == ViewDashboard && !m.loading {
			cmds = append(cmds, m.loadTasks())
		}
		cmds = append(cmds, m.tick())

	case dbChangeMsg:
		// Database file changed - reload tasks
		cmds = append(cmds, m.loadTasks())
		// Continue watching for more changes
		cmds = append(cmds, m.waitForDBChange())
	}

	return m, tea.Batch(cmds...)
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
	case ViewDeleteConfirm:
		return m.viewDeleteConfirm()
	case ViewKillConfirm:
		return m.viewKillConfirm()
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
	case ViewMemories:
		if m.memoriesView != nil {
			return m.memoriesView.View()
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
	}

	// Show current processing tasks if any
	if runningIDs := m.executor.RunningTasks(); len(runningIDs) > 0 {
		statusBar := lipgloss.NewStyle().
			Foreground(ColorInProgress).
			Render(fmt.Sprintf("⋯ Processing %d task(s)", len(runningIDs)))
		headerParts = append(headerParts, statusBar)
	}

	// Shortcut mode display (#<id> jump)
	if m.shortcutMode {
		shortcutStyle := lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true)
		headerParts = append(headerParts, shortcutStyle.Render(fmt.Sprintf("Jump to: #%s_", m.shortcutBuffer)))
	}

	// Filter display
	if m.filtering {
		filterStyle := lipgloss.NewStyle().Foreground(ColorSecondary)
		headerParts = append(headerParts, filterStyle.Render("Filter: ")+m.filterInput.View())
	} else if m.kanban.GetFilter() != "" {
		filterStyle := lipgloss.NewStyle().Foreground(ColorMuted)
		headerParts = append(headerParts, filterStyle.Render(fmt.Sprintf("Filter: %s", m.kanban.GetFilter())))
	}

	// Calculate heights dynamically
	headerHeight := len(headerParts)

	// Render help to measure its actual height
	helpView := m.renderHelp()
	helpHeight := lipgloss.Height(helpView)

	kanbanHeight := m.height - headerHeight - helpHeight

	// Update kanban size
	m.kanban.SetSize(m.width, kanbanHeight)

	// Build the view
	header := ""
	if len(headerParts) > 0 {
		header = lipgloss.JoinVertical(lipgloss.Left, headerParts...)
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		header,
		m.kanban.View(),
		helpView,
	)

	// Use Place to fill the entire terminal
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, content)
}

func (m *AppModel) renderHelp() string {
	return m.help.View(m.keys)
}

func (m *AppModel) updateDashboard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle filter input mode
	if m.filtering {
		switch msg.String() {
		case "esc":
			m.filtering = false
			m.filterInput.SetValue("")
			m.kanban.SetFilter("")
			return m, nil
		case "enter":
			m.filtering = false
			return m, nil
		default:
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			m.kanban.SetFilter(m.filterInput.Value())
			return m, cmd
		}
	}

	keyStr := msg.String()

	// Handle shortcut mode (#<id> to jump directly to task)
	if m.shortcutMode {
		// Accumulate digits
		if len(keyStr) == 1 && keyStr[0] >= '0' && keyStr[0] <= '9' {
			m.shortcutBuffer += keyStr
			return m, nil
		}
		// Backspace removes last digit
		if keyStr == "backspace" && m.shortcutBuffer != "" {
			m.shortcutBuffer = m.shortcutBuffer[:len(m.shortcutBuffer)-1]
			return m, nil
		}
		// Enter confirms and loads the task
		if keyStr == "enter" && m.shortcutBuffer != "" {
			var taskID int64
			if _, err := fmt.Sscanf(m.shortcutBuffer, "%d", &taskID); err == nil {
				m.shortcutMode = false
				m.shortcutBuffer = ""
				return m, m.loadTask(taskID)
			}
		}
		// Escape cancels shortcut mode
		if keyStr == "esc" {
			m.shortcutMode = false
			m.shortcutBuffer = ""
			return m, nil
		}
		// Any other key cancels shortcut mode
		m.shortcutMode = false
		m.shortcutBuffer = ""
		// Fall through to normal key handling
	}

	// Start shortcut mode with '#'
	if keyStr == "#" {
		m.shortcutMode = true
		m.shortcutBuffer = ""
		m.numberFilter = "" // Clear any existing number filter
		m.kanban.ApplyNumberFilter("")
		return m, nil
	}

	// Handle number filter input
	if len(keyStr) == 1 && keyStr[0] >= '0' && keyStr[0] <= '9' {
		m.numberFilter += keyStr
		m.kanban.ApplyNumberFilter(m.numberFilter)
		return m, nil
	}

	// Handle backspace for number filter
	if keyStr == "backspace" && m.numberFilter != "" {
		m.numberFilter = m.numberFilter[:len(m.numberFilter)-1]
		m.kanban.ApplyNumberFilter(m.numberFilter)
		return m, nil
	}

	// Clear number filter on escape (but don't quit)
	if keyStr == "esc" && m.numberFilter != "" {
		m.numberFilter = ""
		m.kanban.ApplyNumberFilter("")
		return m, nil
	}

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

	case key.Matches(msg, m.keys.Enter):
		if task := m.kanban.SelectedTask(); task != nil {
			return m, m.loadTask(task.ID)
		}

	case key.Matches(msg, m.keys.New):
		m.newTaskForm = NewFormModel(m.db, m.width, m.height, m.workingDir)
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
			// Immediately update UI for responsiveness
			task.Status = db.StatusDone
			m.updateTaskInList(task)
			return m, m.closeTask(task.ID)
		}

	case key.Matches(msg, m.keys.Delete):
		if task := m.kanban.SelectedTask(); task != nil {
			return m.showDeleteConfirm(task)
		}

	case key.Matches(msg, m.keys.Filter):
		m.filtering = true
		m.filterInput.Focus()
		return m, textinput.Blink

	case key.Matches(msg, m.keys.Settings):
		m.settingsView = NewSettingsModel(m.db, m.width, m.height)
		m.previousView = m.currentView
		m.currentView = ViewSettings
		return m, m.settingsView.Init()

	case key.Matches(msg, m.keys.Memories):
		m.memoriesView = NewMemoriesModel(m.db, m.width, m.height)
		m.previousView = m.currentView
		m.currentView = ViewMemories
		return m, nil

	case key.Matches(msg, m.keys.CommandPalette):
		m.commandPaletteView = NewCommandPaletteModel(m.db, m.tasks, m.width, m.height)
		m.previousView = m.currentView
		m.currentView = ViewCommandPalette
		return m, m.commandPaletteView.Init()

	case key.Matches(msg, m.keys.Refresh):
		m.loading = true
		return m, m.loadTasks()

	case key.Matches(msg, m.keys.ChangeStatus):
		if task := m.kanban.SelectedTask(); task != nil {
			// Don't allow changing status if task is currently processing
			if task.Status == db.StatusProcessing {
				return m, nil
			}
			return m.showChangeStatus(task)
		}

	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil

	case key.Matches(msg, m.keys.Back):
		return m.showQuitConfirm()
	}

	return m, nil
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
		// Check PR state asynchronously (don't block UI)
		taskID := m.selectedTask.ID
		go m.executor.CheckPRStateAndUpdateTask(taskID)

		// Immediately update UI for responsiveness
		m.selectedTask.Status = db.StatusDone
		if m.detailView != nil {
			m.detailView.UpdateTask(m.selectedTask)
			// Clean up panes before leaving detail view
			m.detailView.Cleanup()
			m.detailView = nil
		}
		// Update task in the list and kanban
		m.updateTaskInList(m.selectedTask)
		m.currentView = ViewDashboard
		return m, m.closeTask(m.selectedTask.ID)
	}
	if key.Matches(keyMsg, m.keys.Delete) && m.selectedTask != nil {
		// Clean up detail view first
		if m.detailView != nil {
			m.detailView.Cleanup()
			m.detailView = nil
		}
		return m.showDeleteConfirm(m.selectedTask)
	}
	if key.Matches(keyMsg, m.keys.Kill) && m.selectedTask != nil {
		// Only allow kill if there's an active tmux session
		sessionName := executor.TmuxSessionName(m.selectedTask.ID)
		if osExec.Command("tmux", "has-session", "-t", sessionName).Run() == nil {
			return m.showKillConfirm(m.selectedTask)
		}
	}
	if key.Matches(keyMsg, m.keys.Files) && m.selectedTask != nil {
		// Clean up panes before leaving detail view
		if m.detailView != nil {
			m.detailView.Cleanup()
		}
		m.attachmentsView = NewAttachmentsModel(m.selectedTask, m.db, m.width, m.height)
		m.previousView = m.currentView
		m.currentView = ViewAttachments
		return m, nil
	}
	if key.Matches(keyMsg, m.keys.Edit) && m.selectedTask != nil {
		m.editingTask = m.selectedTask
		m.editTaskForm = NewEditFormModel(m.db, m.selectedTask, m.width, m.height)
		m.previousView = m.currentView
		m.currentView = ViewEditTask
		return m, m.editTaskForm.Init()
	}
	if key.Matches(keyMsg, m.keys.ChangeStatus) && m.selectedTask != nil {
		// Don't allow changing status if task is currently processing
		if m.selectedTask.Status == db.StatusProcessing {
			return m, nil
		}
		return m.showChangeStatus(m.selectedTask)
	}

	if m.detailView != nil {
		var cmd tea.Cmd
		m.detailView, cmd = m.detailView.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *AppModel) updateNewTaskForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle escape to cancel
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.currentView = ViewDashboard
			m.newTaskForm = nil
			return m, nil
		}
	}

	// Pass all messages to the form
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
	// Handle escape to cancel
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.currentView = m.previousView
			m.editTaskForm = nil
			m.editingTask = nil
			return m, nil
		}
	}

	// Pass all messages to the form
	model, cmd := m.editTaskForm.Update(msg)
	if form, ok := model.(*FormModel); ok {
		m.editTaskForm = form
		if form.submitted {
			// Get updated task data from form
			updatedTask := form.GetDBTask()
			// Preserve the original task's ID and other fields
			updatedTask.ID = m.editingTask.ID
			updatedTask.Status = m.editingTask.Status
			updatedTask.WorktreePath = m.editingTask.WorktreePath
			updatedTask.BranchName = m.editingTask.BranchName
			updatedTask.CreatedAt = m.editingTask.CreatedAt
			updatedTask.StartedAt = m.editingTask.StartedAt
			updatedTask.CompletedAt = m.editingTask.CompletedAt

			m.editTaskForm = nil
			m.editingTask = nil
			m.currentView = m.previousView
			return m, m.updateTask(updatedTask)
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
		Render("⚠ Confirm Delete")

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
			m.currentView = ViewDashboard
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
			m.currentView = ViewDashboard
			return m, m.deleteTask(taskID)
		}
		// Cancelled
		m.pendingDeleteTask = nil
		m.deleteConfirm = nil
		m.currentView = ViewDashboard
		return m, nil
	}

	return m, cmd
}

func (m *AppModel) showKillConfirm(task *db.Task) (tea.Model, tea.Cmd) {
	m.pendingKillTask = task
	m.killConfirmValue = false
	modalWidth := min(50, m.width-8)
	m.killConfirm = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Key("kill").
				Title(fmt.Sprintf("Kill task #%d?", task.ID)).
				Description("This will stop the Claude session and move task to backlog").
				Affirmative("Kill").
				Negative("Cancel").
				Value(&m.killConfirmValue),
		),
	).WithTheme(huh.ThemeDracula()).
		WithWidth(modalWidth - 6). // Account for modal padding and border
		WithShowHelp(true)
	m.currentView = ViewKillConfirm
	return m, m.killConfirm.Init()
}

func (m *AppModel) viewKillConfirm() string {
	if m.killConfirm == nil {
		return ""
	}

	// Modal header with warning icon
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorWarning).
		MarginBottom(1).
		Render("⚠ Confirm Kill")

	formView := m.killConfirm.View()

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

func (m *AppModel) updateKillConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle escape to cancel
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.currentView = ViewDetail
			m.killConfirm = nil
			m.pendingKillTask = nil
			return m, nil
		}
	}

	// Update the huh form
	form, cmd := m.killConfirm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.killConfirm = f
	}

	// Check if form completed
	if m.killConfirm.State == huh.StateCompleted {
		if m.pendingKillTask != nil && m.killConfirmValue {
			taskID := m.pendingKillTask.ID
			// Clean up detail view panes before killing
			if m.detailView != nil {
				m.detailView.Cleanup()
			}
			m.pendingKillTask = nil
			m.killConfirm = nil
			m.currentView = ViewDetail
			return m, m.killTask(taskID)
		}
		// Cancelled
		m.pendingKillTask = nil
		m.killConfirm = nil
		m.currentView = ViewDetail
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

func (m *AppModel) showChangeStatus(task *db.Task) (tea.Model, tea.Cmd) {
	m.pendingChangeStatusTask = task
	m.changeStatusValue = task.Status

	// Build status options - exclude processing (can't manually set to processing)
	// and exclude the current status
	statusOptions := []huh.Option[string]{}
	allStatuses := []struct {
		value string
		label string
	}{
		{db.StatusBacklog, "◦ Backlog"},
		{db.StatusQueued, "▶ Queued"},
		{db.StatusBlocked, "⚠ Blocked"},
		{db.StatusDone, "✓ Done"},
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
		err := database.UpdateTaskStatus(id, status)
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

func (m *AppModel) updateMemories(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) {
		m.currentView = ViewDashboard
		m.memoriesView = nil
		return m, nil
	}

	if m.memoriesView != nil {
		var cmd tea.Cmd
		m.memoriesView, cmd = m.memoriesView.Update(msg)
		return m, cmd
	}

	return m, nil
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

	return m, cmd
}

// Messages
type tasksLoadedMsg struct {
	tasks []*db.Task
	err   error
}

type taskLoadedMsg struct {
	task *db.Task
	err  error
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

type taskDeletedMsg struct {
	err error
}

type taskRetriedMsg struct {
	err error
}

type taskKilledMsg struct{}

type taskEventMsg struct {
	event executor.TaskEvent
}

type tickMsg time.Time

type prRefreshTickMsg time.Time

type dbChangeMsg struct{}

type prInfoMsg struct {
	taskID int64
	info   *github.PRInfo
}

func (m *AppModel) loadTasks() tea.Cmd {
	return func() tea.Msg {
		tasks, err := m.db.ListTasks(db.ListTasksOptions{Limit: 50, IncludeClosed: true})
		// Note: PR/merge status is now checked via batch refresh (prRefreshTick)
		// to avoid spawning processes for every task on every tick
		return tasksLoadedMsg{tasks: tasks, err: err}
	}
}

func (m *AppModel) loadTask(id int64) tea.Cmd {
	// Check PR state asynchronously (don't block UI)
	go m.executor.CheckPRStateAndUpdateTask(id)

	return func() tea.Msg {
		task, err := m.db.GetTask(id)
		return taskLoadedMsg{task: task, err: err}
	}
}

func (m *AppModel) updateTask(t *db.Task) tea.Cmd {
	database := m.db
	exec := m.executor
	return func() tea.Msg {
		err := database.UpdateTask(t)
		if err == nil {
			exec.NotifyTaskChange("updated", t)
		}
		return taskUpdatedMsg{task: t, err: err}
	}
}

func (m *AppModel) createTaskWithAttachments(t *db.Task, attachmentPaths []string) tea.Cmd {
	exec := m.executor
	database := m.db
	return func() tea.Msg {
		err := database.CreateTask(t)
		if err != nil {
			return taskCreatedMsg{task: t, err: err}
		}

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
		err := database.UpdateTaskStatus(id, db.StatusDone)
		if err == nil {
			if task, _ := database.GetTask(id); task != nil {
				exec.NotifyTaskChange("status_changed", task)
			}
		}
		return taskClosedMsg{err: err}
	}
}

func (m *AppModel) deleteTask(id int64) tea.Cmd {
	return func() tea.Msg {
		// Get task to check for worktree
		task, err := m.db.GetTask(id)
		if err != nil {
			return taskDeletedMsg{err: err}
		}

		// Kill Claude session if running (ignore errors)
		windowTarget := executor.TmuxSessionName(id)
		osExec.Command("tmux", "kill-window", "-t", windowTarget).Run()

		// Clean up worktree if it exists
		if task != nil && task.WorktreePath != "" {
			m.executor.CleanupWorktree(task)
		}

		// Delete from database
		err = m.db.DeleteTask(id)
		return taskDeletedMsg{err: err}
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

func (m *AppModel) killTask(id int64) tea.Cmd {
	return func() tea.Msg {
		// Interrupt the task (sets status to backlog)
		m.executor.Interrupt(id)

		// Log the kill action
		m.db.AppendTaskLog(id, "user", "→ [Kill] Session terminated")

		// Kill the tmux window
		windowTarget := executor.TmuxSessionName(id)
		osExec.Command("tmux", "kill-window", "-t", windowTarget).Run()

		return taskKilledMsg{}
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
