package ui

import (
	"fmt"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// View represents the current view.
type View int

const (
	ViewDashboard View = iota
	ViewDetail
	ViewNewTask
	ViewWatch
	ViewSettings
	ViewRetry
	ViewMemories
)

// KeyMap defines key bindings.
type KeyMap struct {
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
	Interrupt    key.Binding
	Filter       key.Binding
	Refresh      key.Binding
	Settings     key.Binding
	Memories     key.Binding
	ToggleClosed key.Binding
	Help         key.Binding
	Quit         key.Binding
}

// DefaultKeyMap returns the default key bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
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
			key.WithHelp("esc/q", "back"),
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
		ToggleClosed: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "show all"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
	}
}

// AppModel is the main application model.
type AppModel struct {
	db       *db.DB
	executor *executor.Executor
	keys     KeyMap

	currentView  View
	previousView View

	// Dashboard state
	tasks        []*db.Task
	list         list.Model
	loading      bool
	showClosed   bool      // Show closed tasks in the list
	err          error
	notification string    // Notification banner text
	notifyUntil  time.Time // When to hide notification

	// Track task statuses to detect changes
	prevStatuses map[int64]string

	// Detail view state
	selectedTask *db.Task
	detailView   *DetailModel

	// New task form state
	newTaskForm *FormModel

	// Watch view state
	watchView *WatchModel

	// Settings view state
	settingsView *SettingsModel

	// Retry view state
	retryView *RetryModel

	// Memories view state
	memoriesView *MemoriesModel

	// Window size
	width  int
	height int
}

// TaskItem wraps a task for the list.
type TaskItem struct {
	task *db.Task
}

func (t TaskItem) Title() string {
	return t.task.Title
}

func (t TaskItem) Description() string {
	return ""
}

func (t TaskItem) FilterValue() string {
	return t.task.Title
}

// NewAppModel creates a new application model.
func NewAppModel(database *db.DB, exec *executor.Executor, width, height int) *AppModel {
	delegate := NewTaskDelegate()
	l := list.New([]list.Item{}, delegate, width, height-4)
	l.Title = "Tasks"
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.Styles.Title = Header

	return &AppModel{
		db:           database,
		executor:     exec,
		keys:         DefaultKeyMap(),
		currentView:  ViewDashboard,
		list:         l,
		loading:      true,
		width:        width,
		height:       height,
		prevStatuses: make(map[int64]string),
	}
}

// Init initializes the model.
func (m *AppModel) Init() tea.Cmd {
	return m.loadTasks()
}

// Update handles messages.
func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Handle form updates first (needs all message types)
	if m.currentView == ViewNewTask && m.newTaskForm != nil {
		return m.updateNewTaskForm(msg)
	}
	if m.currentView == ViewSettings && m.settingsView != nil {
		return m.updateSettings(msg)
	}
	if m.currentView == ViewRetry && m.retryView != nil {
		return m.updateRetry(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Global keys
		if key.Matches(msg, m.keys.Quit) {
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
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.SetSize(msg.Width, msg.Height-4)
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

		// Check for newly blocked/ready tasks and notify
		for _, t := range m.tasks {
			prevStatus := m.prevStatuses[t.ID]
			if prevStatus != "" && prevStatus != t.Status {
				if t.Status == db.StatusBlocked {
					// Task just became blocked - ring bell and show notification
					m.notification = fmt.Sprintf("⚠ Task #%d needs input: %s", t.ID, t.Title)
					m.notifyUntil = time.Now().Add(10 * time.Second)
					fmt.Print("\a") // Ring terminal bell
				} else if t.Status == db.StatusReady && prevStatus == db.StatusProcessing {
					// Task completed
					m.notification = fmt.Sprintf("✓ Task #%d complete: %s", t.ID, t.Title)
					m.notifyUntil = time.Now().Add(5 * time.Second)
				}
			}
			m.prevStatuses[t.ID] = t.Status
		}

		items := make([]list.Item, len(m.tasks))
		for i, t := range m.tasks {
			items[i] = TaskItem{task: t}
		}
		m.list.SetItems(items)

	case taskLoadedMsg:
		if msg.err == nil {
			m.selectedTask = msg.task
			m.detailView = NewDetailModel(msg.task, m.db, m.width, m.height)
			m.previousView = m.currentView
			m.currentView = ViewDetail
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

	case tickMsg:
		// Periodic refresh for status updates
		cmds = append(cmds, m.loadTasks())
		cmds = append(cmds, m.tick())
	}

	return m, tea.Batch(cmds...)
}

// View renders the current view.
func (m *AppModel) View() string {
	if m.loading {
		return "\n  Loading tasks..."
	}

	if m.err != nil {
		return fmt.Sprintf("\n  Error: %s", m.err)
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
	}

	return ""
}

func (m *AppModel) viewDashboard() string {
	var parts []string

	// Show notification banner if active
	if m.notification != "" && time.Now().Before(m.notifyUntil) {
		notifyStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#FFCC00")).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 2).
			Width(m.width)
		parts = append(parts, notifyStyle.Render(m.notification))
	} else {
		m.notification = "" // Clear expired notification
	}

	// Show current processing tasks if any
	if runningIDs := m.executor.RunningTasks(); len(runningIDs) > 0 {
		statusBar := lipgloss.NewStyle().
			Foreground(ColorProcessing).
			Render(fmt.Sprintf("⋯ Processing %d task(s)", len(runningIDs)))
		parts = append(parts, lipgloss.NewStyle().Padding(0, 2).Render(statusBar))
	}

	parts = append(parts, m.list.View())
	parts = append(parts, m.renderHelp())

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *AppModel) renderHelp() string {
	keys := []struct {
		key  string
		desc string
	}{
		{"↑/↓", "navigate"},
		{"enter", "view"},
		{"n", "new"},
		{"x", "execute"},
		{"r", "retry"},
		{"c", "close"},
		{"w", "watch"},
		{"i", "interrupt"},
		{"s", "settings"},
		{"q", "quit"},
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

func (m *AppModel) updateDashboard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// If the list is filtering, let it handle all key events
	// This prevents shortcuts like 's' from triggering while typing in the filter
	if m.list.SettingFilter() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	switch {
	case key.Matches(msg, m.keys.Enter):
		if item, ok := m.list.SelectedItem().(TaskItem); ok {
			return m, m.loadTask(item.task.ID)
		}

	case key.Matches(msg, m.keys.New):
		m.newTaskForm = NewFormModel(m.db, m.width, m.height)
		m.previousView = m.currentView
		m.currentView = ViewNewTask
		return m, m.newTaskForm.Init()

	case key.Matches(msg, m.keys.Queue):
		if item, ok := m.list.SelectedItem().(TaskItem); ok {
			return m, m.queueTask(item.task.ID)
		}

	case key.Matches(msg, m.keys.Retry):
		if item, ok := m.list.SelectedItem().(TaskItem); ok {
			task := item.task
			// Allow retry for blocked, ready, pending, or stuck processing tasks
			if task.Status == db.StatusBlocked || task.Status == db.StatusReady ||
				task.Status == db.StatusPending || task.Status == db.StatusProcessing {
				m.selectedTask = task
				m.retryView = NewRetryModel(task, m.db, m.width, m.height)
				m.previousView = m.currentView
				m.currentView = ViewRetry
				return m, m.retryView.Init()
			}
		}

	case key.Matches(msg, m.keys.Close):
		if item, ok := m.list.SelectedItem().(TaskItem); ok {
			return m, m.closeTask(item.task.ID)
		}

	case key.Matches(msg, m.keys.Delete):
		if item, ok := m.list.SelectedItem().(TaskItem); ok {
			return m, m.deleteTask(item.task.ID)
		}

	case key.Matches(msg, m.keys.Watch):
		// Watch the selected task if it's processing
		if item, ok := m.list.SelectedItem().(TaskItem); ok {
			if m.executor.IsRunning(item.task.ID) {
				m.watchView = NewWatchModel(m.db, m.executor, item.task.ID, m.width, m.height)
				m.previousView = m.currentView
				m.currentView = ViewWatch
				return m, m.watchView.Init()
			}
		}

	case key.Matches(msg, m.keys.Interrupt):
		// Interrupt the selected task if it's processing
		if item, ok := m.list.SelectedItem().(TaskItem); ok {
			if m.executor.IsRunning(item.task.ID) {
				return m, m.interruptTask(item.task.ID)
			}
		}

	case key.Matches(msg, m.keys.Settings):
		m.settingsView = NewSettingsModel(m.db, m.width, m.height)
		m.previousView = m.currentView
		m.currentView = ViewSettings
		return m, m.settingsView.Init()

	case key.Matches(msg, m.keys.Refresh):
		m.loading = true
		return m, m.loadTasks()

	case key.Matches(msg, m.keys.ToggleClosed):
		m.showClosed = !m.showClosed
		if m.showClosed {
			m.list.Title = "Tasks (all)"
		} else {
			m.list.Title = "Tasks"
		}
		return m, m.loadTasks()

	case key.Matches(msg, m.keys.Back):
		return m, tea.Quit
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *AppModel) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) {
		m.currentView = ViewDashboard
		m.detailView = nil
		return m, nil
	}

	// Handle queue/close/retry from detail view
	if key.Matches(msg, m.keys.Queue) && m.selectedTask != nil {
		return m, m.queueTask(m.selectedTask.ID)
	}
	if key.Matches(msg, m.keys.Retry) && m.selectedTask != nil {
		task := m.selectedTask
		if task.Status == db.StatusBlocked || task.Status == db.StatusReady ||
			task.Status == db.StatusPending || task.Status == db.StatusProcessing {
			m.retryView = NewRetryModel(task, m.db, m.width, m.height)
			m.previousView = m.currentView
			m.currentView = ViewRetry
			return m, m.retryView.Init()
		}
	}
	if key.Matches(msg, m.keys.Close) && m.selectedTask != nil {
		m.currentView = ViewDashboard
		return m, m.closeTask(m.selectedTask.ID)
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
			// Capture task and nil out form immediately to prevent duplicate submissions
			task := form.GetDBTask()
			m.newTaskForm = nil
			m.currentView = ViewDashboard
			return m, m.createTask(task)
		}
		if form.cancelled {
			m.currentView = ViewDashboard
			m.newTaskForm = nil
			return m, nil
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
		taskID := m.retryView.task.ID
		m.currentView = ViewDashboard
		m.retryView = nil
		m.detailView = nil
		return m, m.retryTask(taskID, feedback)
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

type tickMsg time.Time

func (m *AppModel) loadTasks() tea.Cmd {
	showClosed := m.showClosed
	return func() tea.Msg {
		tasks, err := m.db.ListTasks(db.ListTasksOptions{Limit: 50, IncludeClosed: showClosed})
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
	return func() tea.Msg {
		err := m.db.CreateTask(t)
		return taskCreatedMsg{task: t, err: err}
	}
}

func (m *AppModel) queueTask(id int64) tea.Cmd {
	return func() tea.Msg {
		err := m.db.UpdateTaskStatus(id, db.StatusQueued)
		return taskQueuedMsg{err: err}
	}
}

func (m *AppModel) closeTask(id int64) tea.Cmd {
	return func() tea.Msg {
		err := m.db.UpdateTaskStatus(id, db.StatusClosed)
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

func (m *AppModel) interruptTask(id int64) tea.Cmd {
	return func() tea.Msg {
		m.executor.Interrupt(id)
		return taskInterruptedMsg{}
	}
}

func (m *AppModel) tick() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}
