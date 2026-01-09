package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
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
	ViewWatch
	ViewSettings
	ViewRetry
	ViewMemories
	ViewAttachments
)

// KeyMap defines key bindings.
type KeyMap struct {
	Left         key.Binding
	Right        key.Binding
	Up           key.Binding
	Down         key.Binding
	Enter        key.Binding
	Back         key.Binding
	New          key.Binding
	Queue        key.Binding
	Retry        key.Binding
	Close        key.Binding
	Delete       key.Binding
	Watch        key.Binding
	Attach       key.Binding
	Interrupt    key.Binding
	Filter       key.Binding
	Refresh      key.Binding
	Settings     key.Binding
	Memories     key.Binding
	Open         key.Binding
	Files        key.Binding
	Help         key.Binding
	Quit         key.Binding
	// Column focus shortcuts
	FocusBacklog     key.Binding
	FocusInProgress  key.Binding
	FocusBlocked     key.Binding
	FocusDone        key.Binding
}

// ShortHelp returns key bindings to show in the mini help.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Left, k.Right, k.Up, k.Down, k.Enter, k.New, k.Queue, k.Attach, k.Filter, k.Quit}
}

// FullHelp returns keybindings for the expanded help view.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Left, k.Right, k.Up, k.Down},
		{k.FocusBacklog, k.FocusInProgress, k.FocusBlocked, k.FocusDone},
		{k.Enter, k.New, k.Queue, k.Close},
		{k.Retry, k.Watch, k.Attach, k.Interrupt, k.Delete},
		{k.Filter, k.Settings, k.Memories, k.Open, k.Files},
		{k.Refresh, k.Help, k.Quit},
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
		Watch: key.NewBinding(
			key.WithKeys("w"),
			key.WithHelp("w", "watch"),
		),
		Attach: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "attach"),
		),
		Interrupt: key.NewBinding(
			key.WithKeys("i"),
			key.WithHelp("i", "interrupt"),
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
		Open: key.NewBinding(
			key.WithKeys("o"),
			key.WithHelp("o", "open dir"),
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

	// Number filter for quick task ID jump
	numberFilter string

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

	// Watch view state
	watchView *WatchModel

	// Settings view state
	settingsView *SettingsModel

	// Retry view state
	retryView *RetryModel

	// Memories view state
	memoriesView *MemoriesModel

	// Attachments view state
	attachmentsView *AttachmentsModel

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
	}
}

// Init initializes the model.
func (m *AppModel) Init() tea.Cmd {
	// Subscribe to real-time task events
	m.eventCh = m.executor.SubscribeTaskEvents()
	// Initialize interrupt key state (disabled until we know tasks are executing)
	m.keys.Interrupt.SetEnabled(len(m.executor.RunningTasks()) > 0)

	// Start watching database file for changes
	m.startDatabaseWatcher()

	return tea.Batch(m.loadTasks(), m.waitForTaskEvent(), m.waitForDBChange(), m.tick())
}

// Update handles messages.
func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Handle form updates first (needs all message types)
	if m.currentView == ViewNewTask && m.newTaskForm != nil {
		return m.updateNewTaskForm(msg)
	}
	if m.currentView == ViewNewTaskConfirm && m.queueConfirm != nil {
		return m.updateNewTaskConfirm(msg)
	}
	if m.currentView == ViewSettings && m.settingsView != nil {
		return m.updateSettings(msg)
	}
	if m.currentView == ViewRetry && m.retryView != nil {
		return m.updateRetry(msg)
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
		case ViewWatch:
			return m.updateWatch(msg)
		case ViewMemories:
			return m.updateMemories(msg)
		case ViewAttachments:
			return m.updateAttachments(msg)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = msg.Width
		m.kanban.SetSize(msg.Width, msg.Height-4)
		if m.detailView != nil {
			m.detailView.SetSize(msg.Width, msg.Height)
		}
		if m.watchView != nil {
			m.watchView.SetSize(msg.Width, msg.Height)
		}
		if m.settingsView != nil {
			m.settingsView.SetSize(msg.Width, msg.Height)
		}
		if m.retryView != nil {
			m.retryView.SetSize(msg.Width, msg.Height)
		}

	case tasksLoadedMsg:
		m.loading = false
		m.tasks = msg.tasks
		m.err = msg.err

		// Update interrupt key state based on whether any task is executing
		m.updateInterruptKey()

		// Check for newly blocked/done tasks and notify
		for _, t := range m.tasks {
			prevStatus := m.prevStatuses[t.ID]
			if prevStatus != "" && prevStatus != t.Status {
				if t.Status == db.StatusBlocked {
					// Task just became blocked - ring bell and show notification
					m.notification = fmt.Sprintf("⚠ Task #%d needs input: %s", t.ID, t.Title)
					m.notifyUntil = time.Now().Add(10 * time.Second)
					fmt.Print("\a") // Ring terminal bell
				} else if t.Status == db.StatusDone && db.IsInProgress(prevStatus) {
					// Task completed - ring bell and show notification
					m.notification = fmt.Sprintf("✓ Task #%d complete: %s", t.ID, t.Title)
					m.notifyUntil = time.Now().Add(5 * time.Second)
					fmt.Print("\a") // Ring terminal bell
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

	case taskLoadedMsg:
		if msg.err == nil {
			m.selectedTask = msg.task
			m.detailView = NewDetailModel(msg.task, m.db, m.width, m.height)
			m.previousView = m.currentView
			m.currentView = ViewDetail
			// Start tmux output ticker if session is active
			if tickerCmd := m.detailView.StartTmuxTicker(); tickerCmd != nil {
				cmds = append(cmds, tickerCmd)
			}
		} else {
			m.err = msg.err
		}

	case taskCreatedMsg:
		if msg.err == nil {
			m.currentView = ViewDashboard
			m.newTaskForm = nil
			cmds = append(cmds, m.loadTasks())
		} else {
			m.err = msg.err
		}

	case taskQueuedMsg, taskClosedMsg, taskDeletedMsg, taskRetriedMsg, taskInterruptedMsg:
		cmds = append(cmds, m.loadTasks())

	case attachDoneMsg:
		// Returned from tmux attach - refresh tasks
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
							fmt.Print("\a") // Ring terminal bell
						} else if event.Task.Status == db.StatusDone && db.IsInProgress(prevStatus) {
							m.notification = fmt.Sprintf("✓ Task #%d complete: %s", event.TaskID, event.Task.Title)
							m.notifyUntil = time.Now().Add(5 * time.Second)
							fmt.Print("\a") // Ring terminal bell
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
			
			// Update interrupt key state based on whether any task is executing
			m.updateInterruptKey()
			
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
	case ViewNewTaskConfirm:
		return m.viewNewTaskConfirm()
	case ViewWatch:
		if m.watchView != nil {
			return m.watchView.View()
		}
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

	// Handle number filter input
	keyStr := msg.String()
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
			return m, m.deleteTask(task.ID)
		}

	case key.Matches(msg, m.keys.Watch):
		// Watch the selected task if it's in progress
		if task := m.kanban.SelectedTask(); task != nil {
			if db.IsInProgress(task.Status) || m.executor.IsRunning(task.ID) {
				m.watchView = NewWatchModel(m.db, m.executor, task.ID, m.width, m.height)
				m.previousView = m.currentView
				m.currentView = ViewWatch
				return m, m.watchView.Init()
			}
		}

	case key.Matches(msg, m.keys.Attach):
		// Attach to tmux session for selected task
		if task := m.kanban.SelectedTask(); task != nil {
			if db.IsInProgress(task.Status) {
				sessionName := executor.TmuxSessionName(task.ID)
				return m, tea.ExecProcess(
					exec.Command("tmux", "attach-session", "-t", sessionName),
					func(err error) tea.Msg { return attachDoneMsg{err: err} },
				)
			}
		}

	case key.Matches(msg, m.keys.Interrupt):
		// Interrupt the selected task if it's in progress
		if task := m.kanban.SelectedTask(); task != nil {
			if db.IsInProgress(task.Status) || m.executor.IsRunning(task.ID) {
				return m, m.interruptTask(task.ID)
			}
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

	case key.Matches(msg, m.keys.Refresh):
		m.loading = true
		return m, m.loadTasks()

	case key.Matches(msg, m.keys.Open):
		if task := m.kanban.SelectedTask(); task != nil {
			return m, m.openTaskDir(task)
		}

	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil

	case key.Matches(msg, m.keys.Back):
		return m, tea.Quit
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
		m.currentView = ViewDashboard
		if m.detailView != nil {
			m.detailView.Cleanup()
			m.detailView = nil
		}
		return m, nil
	}

	// Handle queue/close/retry from detail view
	if key.Matches(keyMsg, m.keys.Queue) && m.selectedTask != nil {
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
			m.retryView = NewRetryModel(task, m.db, m.width, m.height)
			m.previousView = m.currentView
			m.currentView = ViewRetry
			return m, m.retryView.Init()
		}
	}
	if key.Matches(keyMsg, m.keys.Close) && m.selectedTask != nil {
		// Immediately update UI for responsiveness
		m.selectedTask.Status = db.StatusDone
		if m.detailView != nil {
			m.detailView.UpdateTask(m.selectedTask)
		}
		// Update task in the list and kanban
		m.updateTaskInList(m.selectedTask)
		m.currentView = ViewDashboard
		return m, m.closeTask(m.selectedTask.ID)
	}
	if key.Matches(keyMsg, m.keys.Attach) && m.selectedTask != nil {
		// Attach if tmux session exists
		sessionName := executor.TmuxSessionName(m.selectedTask.ID)
		if exec.Command("tmux", "has-session", "-t", sessionName).Run() == nil {
			return m, tea.ExecProcess(
				exec.Command("tmux", "attach-session", "-t", sessionName),
				func(err error) tea.Msg { return attachDoneMsg{err: err} },
			)
		}
	}
	if key.Matches(keyMsg, m.keys.Interrupt) && m.selectedTask != nil {
		// Interrupt if tmux session exists or executor is running
		sessionName := executor.TmuxSessionName(m.selectedTask.ID)
		if exec.Command("tmux", "has-session", "-t", sessionName).Run() == nil || m.executor.IsRunning(m.selectedTask.ID) {
			return m, m.interruptTask(m.selectedTask.ID)
		}
	}
	if key.Matches(keyMsg, m.keys.Open) && m.selectedTask != nil {
		return m, m.openTaskDir(m.selectedTask)
	}
	if key.Matches(keyMsg, m.keys.Files) && m.selectedTask != nil {
		m.attachmentsView = NewAttachmentsModel(m.selectedTask, m.db, m.width, m.height)
		m.previousView = m.currentView
		m.currentView = ViewAttachments
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

func (m *AppModel) updateWatch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) {
		m.currentView = ViewDashboard
		if m.watchView != nil {
			m.watchView.Cleanup()
		}
		m.watchView = nil
		return m, nil
	}

	if m.watchView != nil {
		var cmd tea.Cmd
		m.watchView, cmd = m.watchView.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *AppModel) updateSettings(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle escape to go back
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "q" || keyMsg.String() == "esc" {
			// Only exit if not in edit mode or browsing
			if m.settingsView != nil && !m.settingsView.editing && !m.settingsView.browsing {
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

type taskInterruptedMsg struct {
	err error
}

type taskEventMsg struct {
	event executor.TaskEvent
}

type attachDoneMsg struct {
	err error
}

type openDirDoneMsg struct {
	err error
}

type tickMsg time.Time

type dbChangeMsg struct{}

func (m *AppModel) loadTasks() tea.Cmd {
	return func() tea.Msg {
		tasks, err := m.db.ListTasks(db.ListTasksOptions{Limit: 50, IncludeClosed: true})
		return tasksLoadedMsg{tasks: tasks, err: err}
	}
}

func (m *AppModel) loadTask(id int64) tea.Cmd {
	return func() tea.Msg {
		task, err := m.db.GetTask(id)
		return taskLoadedMsg{task: task, err: err}
	}
}

func (m *AppModel) createTask(t *db.Task) tea.Cmd {
	exec := m.executor
	return func() tea.Msg {
		err := m.db.CreateTask(t)
		if err == nil {
			exec.NotifyTaskChange("created", t)
		}
		return taskCreatedMsg{task: t, err: err}
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
		err := m.db.DeleteTask(id)
		return taskDeletedMsg{err: err}
	}
}

func (m *AppModel) retryTask(id int64, feedback string) tea.Cmd {
	return func() tea.Msg {
		err := m.db.RetryTask(id, feedback)
		return taskRetriedMsg{err: err}
	}
}

func (m *AppModel) retryTaskWithAttachments(id int64, feedback string, attachmentPaths []string) tea.Cmd {
	database := m.db
	return func() tea.Msg {
		// Add attachments first if provided
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
		if err := exec.Command("tmux", "has-session", "-t", sessionName).Run(); err == nil {
			// Session alive - just send feedback via send-keys
			if feedback != "" {
				database.AppendTaskLog(id, "text", "Feedback: "+feedback)
				exec.Command("tmux", "send-keys", "-t", sessionName, feedback, "Enter").Run()
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

func (m *AppModel) interruptTask(id int64) tea.Cmd {
	return func() tea.Msg {
		m.executor.Interrupt(id)
		return taskInterruptedMsg{}
	}
}

func (m *AppModel) openTaskDir(task *db.Task) tea.Cmd {
	// Determine directory to open: worktree > project > cwd
	dir := task.WorktreePath
	if dir == "" {
		dir = m.executor.GetProjectDir(task.Project)
	}
	if dir == "" {
		return nil
	}

	// Use tmux to create a new window in the task directory
	if os.Getenv("TMUX") != "" {
		cmd := exec.Command("tmux", "new-window", "-c", dir)
		go cmd.Run()
		return func() tea.Msg { return openDirDoneMsg{} }
	}

	// Detect terminal emulator and open new window
	termProgram := os.Getenv("TERM_PROGRAM")
	switch termProgram {
	case "iTerm.app":
		script := fmt.Sprintf(`tell application "iTerm2"
			tell current window
				create tab with default profile
				tell current session
					write text "cd %q"
				end tell
			end tell
		end tell`, dir)
		cmd := exec.Command("osascript", "-e", script)
		go cmd.Run()
		return func() tea.Msg { return openDirDoneMsg{} }
	case "Apple_Terminal":
		cmd := exec.Command("open", "-a", "Terminal", dir)
		go cmd.Run()
		return func() tea.Msg { return openDirDoneMsg{} }
	case "WezTerm":
		cmd := exec.Command("wezterm", "cli", "spawn", "--cwd", dir)
		go cmd.Run()
		return func() tea.Msg { return openDirDoneMsg{} }
	case "Alacritty":
		cmd := exec.Command("alacritty", "--working-directory", dir)
		go cmd.Run()
		return func() tea.Msg { return openDirDoneMsg{} }
	case "kitty":
		// kitty requires remote control to be enabled; spawn new instance otherwise
		cmd := exec.Command("kitty", "--directory", dir)
		go cmd.Run()
		return func() tea.Msg { return openDirDoneMsg{} }
	}

	// Check for GNOME Terminal
	if os.Getenv("GNOME_TERMINAL_SERVICE") != "" {
		cmd := exec.Command("gnome-terminal", "--working-directory", dir)
		go cmd.Run()
		return func() tea.Msg { return openDirDoneMsg{} }
	}

	// Fallback: spawn shell in-place (user exits to return to TUI)
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "sh"
	}
	cmd := exec.Command(shell)
	cmd.Dir = dir
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return openDirDoneMsg{err: err}
	})
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

// updateInterruptKey enables or disables the interrupt key based on whether any task is executing.
func (m *AppModel) updateInterruptKey() {
	hasExecuting := len(m.executor.RunningTasks()) > 0
	if !hasExecuting {
		// Also check if any task in the list is in progress status
		for _, t := range m.tasks {
			if db.IsInProgress(t.Status) {
				hasExecuting = true
				break
			}
		}
	}
	m.keys.Interrupt.SetEnabled(hasExecuting)
}
