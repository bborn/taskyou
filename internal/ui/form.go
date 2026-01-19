package ui

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bborn/workflow/internal/autocomplete"
	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// FormField represents the currently focused field.
type FormField int

const (
	FieldProject FormField = iota // Project first for immediate context
	FieldTitle
	FieldBody
	FieldAttachments // Moved after body for proximity - drag works from any field
	FieldType
	FieldExecutor
	FieldSchedule
	FieldRecurrence
)

// FormModel represents the new task form.
type FormModel struct {
	db        *db.DB
	width     int
	height    int
	submitted bool
	cancelled bool
	isEdit          bool   // true when editing an existing task
	originalProject string // original project when editing (to detect project changes)

	// Current field
	focused FormField

	// Form inputs
	titleInput       textinput.Model
	bodyInput        textarea.Model
	attachmentsInput textinput.Model
	scheduleInput    textinput.Model // For entering schedule time (e.g., "1h", "2h30m", "tomorrow 9am")

	// Select values
	project       string
	projectIdx    int
	projects      []string
	taskType      string
	typeIdx       int
	types         []string
	executor      string // "claude", "codex"
	executorIdx   int
	executors     []string
	queue         bool
	attachments   []string // Parsed file paths
	recurrence    string   // "", "hourly", "daily", "weekly", "monthly"
	recurrenceIdx int
	recurrences   []string

	// Magic paste fields (populated when pasting URLs)
	prURL    string // GitHub PR URL if pasted
	prNumber int    // GitHub PR number if pasted

	// Ghost text autocompletion (LLM-powered)
	ghostText           string                // The suggested completion text (suffix only)
	ghostFullText       string                // The full text that would result from accepting
	lastTitleValue      string                // Track title changes for suggestion refresh
	lastBodyValue       string                // Track body changes for suggestion refresh
	autocompleteSvc     *autocomplete.Service // Autocomplete service
	autocompleteCancel  context.CancelFunc    // Cancel function for current request
	debounceID          int                   // ID to track debounce requests
	pendingDebounce     bool                  // Whether we're waiting on a debounce timer
	loadingAutocomplete bool                  // Whether we're waiting for LLM response
	autocompleteEnabled bool                  // Whether autocomplete is enabled (from settings)

	// Task reference autocomplete (for #123 references)
	taskRefAutocomplete     *TaskRefAutocompleteModel
	showTaskRefAutocomplete bool

	// Cancel confirmation state
	showCancelConfirm bool
}

// Autocomplete message types for async LLM suggestions
type autocompleteTickMsg struct {
	debounceID int
	fieldType  string
	input      string
	project    string
	context    string
}

type autocompleteSuggestionMsg struct {
	suggestion *autocomplete.Suggestion
	fieldType  string
	debounceID int // To verify response is still relevant
}

// NewEditFormModel creates a form model pre-populated with an existing task's data for editing.
func NewEditFormModel(database *db.DB, task *db.Task, width, height int) *FormModel {
	// Set executor to default if not specified
	executor := task.Executor
	if executor == "" {
		executor = db.DefaultExecutor()
	}

	// Check if autocomplete is enabled (default: true) and API key is available
	autocompleteEnabled := true
	var apiKey string
	if database != nil {
		if setting, _ := database.GetSetting("autocomplete_enabled"); setting == "false" {
			autocompleteEnabled = false
		}
		apiKey, _ = database.GetSetting("anthropic_api_key")
	}

	autocompleteSvc := autocomplete.NewService(apiKey)
	// Only enable if we have an API key
	if !autocompleteSvc.IsAvailable() {
		autocompleteEnabled = false
	}

	m := &FormModel{
		db:                  database,
		width:               width,
		height:              height,
		focused:             FieldProject,
		taskType:            task.Type,
		project:             task.Project,
		originalProject:     task.Project, // Track original project for detecting changes
		executor:            executor,
		executors:           []string{db.ExecutorClaude, db.ExecutorCodex},
		isEdit:              true,
		recurrence:          task.Recurrence,
		recurrences:         []string{"", db.RecurrenceHourly, db.RecurrenceDaily, db.RecurrenceWeekly, db.RecurrenceMonthly},
		prURL:               task.PRURL,
		prNumber:            task.PRNumber,
		autocompleteSvc:     autocompleteSvc,
		autocompleteEnabled: autocompleteEnabled,
		taskRefAutocomplete: NewTaskRefAutocompleteModel(database, width-24),
	}

	// Set executor index
	for i, e := range m.executors {
		if e == executor {
			m.executorIdx = i
			break
		}
	}

	// Load task types from database
	m.types = []string{""}
	if database != nil {
		if types, err := database.ListTaskTypes(); err == nil {
			for _, t := range types {
				m.types = append(m.types, t.Name)
			}
		}
	}
	// Fallback if no types loaded
	if len(m.types) == 1 {
		m.types = []string{"", "code", "writing", "thinking"}
	}

	// Set type index
	for i, t := range m.types {
		if t == task.Type {
			m.typeIdx = i
			break
		}
	}

	// Load projects
	m.projects = []string{""}
	if database != nil {
		if projs, err := database.ListProjects(); err == nil {
			for _, p := range projs {
				m.projects = append(m.projects, p.Name)
			}
		}
	}

	// Set project index
	for i, p := range m.projects {
		if p == task.Project {
			m.projectIdx = i
			break
		}
	}

	// Title input - pre-populate with existing title
	m.titleInput = textinput.New()
	m.titleInput.Placeholder = "What needs to be done?"
	m.titleInput.Prompt = ""
	m.titleInput.Cursor.SetMode(cursor.CursorStatic)
	m.titleInput.Width = width - 24
	m.titleInput.SetValue(task.Title)

	// Body textarea - pre-populate with existing body
	m.bodyInput = textarea.New()
	m.bodyInput.Placeholder = "Additional context (optional)"
	m.bodyInput.Prompt = ""
	m.bodyInput.ShowLineNumbers = false
	m.bodyInput.Cursor.SetMode(cursor.CursorStatic)
	m.bodyInput.SetWidth(width - 24)
	m.bodyInput.FocusedStyle.CursorLine = lipgloss.NewStyle()
	m.bodyInput.BlurredStyle.CursorLine = lipgloss.NewStyle()
	m.bodyInput.SetValue(task.Body)
	m.updateBodyHeight() // Autogrow based on content

	// Schedule input - pre-populate if task has a scheduled time
	m.scheduleInput = textinput.New()
	m.scheduleInput.Placeholder = "e.g., 1h, 2h30m, tomorrow 9am"
	m.scheduleInput.Prompt = ""
	m.scheduleInput.Cursor.SetMode(cursor.CursorStatic)
	m.scheduleInput.Width = width - 24
	if task.ScheduledAt != nil && !task.ScheduledAt.IsZero() {
		// Show relative time or formatted time
		m.scheduleInput.SetValue(task.ScheduledAt.Format("2006-01-02 15:04"))
	}

	// Set recurrence index
	for i, r := range m.recurrences {
		if r == task.Recurrence {
			m.recurrenceIdx = i
			break
		}
	}

	// Attachments input
	m.attachmentsInput = textinput.New()
	m.attachmentsInput.Placeholder = "Files (drag anywhere or type paths)"
	m.attachmentsInput.Prompt = ""
	m.attachmentsInput.Cursor.SetMode(cursor.CursorStatic)
	m.attachmentsInput.Width = width - 24

	return m
}

// NewFormModel creates a new form model.
func NewFormModel(database *db.DB, width, height int, workingDir string) *FormModel {
	// Check if autocomplete is enabled (default: true) and API key is available
	autocompleteEnabled := true
	var apiKey string
	if database != nil {
		if setting, _ := database.GetSetting("autocomplete_enabled"); setting == "false" {
			autocompleteEnabled = false
		}
		apiKey, _ = database.GetSetting("anthropic_api_key")
	}

	autocompleteSvc := autocomplete.NewService(apiKey)
	// Only enable if we have an API key
	if !autocompleteSvc.IsAvailable() {
		autocompleteEnabled = false
	}

	m := &FormModel{
		db:                  database,
		width:               width,
		height:              height,
		focused:             FieldProject,
		executor:            db.DefaultExecutor(),
		executors:           []string{db.ExecutorClaude, db.ExecutorCodex},
		recurrences:         []string{"", db.RecurrenceHourly, db.RecurrenceDaily, db.RecurrenceWeekly, db.RecurrenceMonthly},
		autocompleteSvc:     autocompleteSvc,
		autocompleteEnabled: autocompleteEnabled,
		taskRefAutocomplete: NewTaskRefAutocompleteModel(database, width-24),
	}

	// Load task types from database
	m.types = []string{""}
	if database != nil {
		if types, err := database.ListTaskTypes(); err == nil {
			for _, t := range types {
				m.types = append(m.types, t.Name)
			}
		}
	}
	// Fallback if no types loaded
	if len(m.types) == 1 {
		m.types = []string{"", "code", "writing", "thinking"}
	}

	// Load projects
	m.projects = []string{}
	if database != nil {
		if projs, err := database.ListProjects(); err == nil {
			for _, p := range projs {
				m.projects = append(m.projects, p.Name)
			}
		}
	}

	// Default to last used project, or fall back to 'personal'
	m.project = "personal"
	if database != nil {
		if lastProject, err := database.GetLastUsedProject(); err == nil && lastProject != "" {
			m.project = lastProject
		}
	}
	for i, p := range m.projects {
		if p == m.project {
			m.projectIdx = i
			break
		}
	}

	// Detect project from working directory (overrides default)
	if workingDir != "" && database != nil {
		if proj, err := database.GetProjectByPath(workingDir); err == nil && proj != nil {
			m.project = proj.Name
			for i, p := range m.projects {
				if p == proj.Name {
					m.projectIdx = i
					break
				}
			}
		}
	}

	// Load last used task type for the selected project
	m.loadLastTaskTypeForProject()

	// Title input
	m.titleInput = textinput.New()
	m.titleInput.Placeholder = "What needs to be done?"
	m.titleInput.Prompt = ""
	m.titleInput.Cursor.SetMode(cursor.CursorStatic)
	m.titleInput.Width = width - 24

	// Body textarea
	m.bodyInput = textarea.New()
	m.bodyInput.Placeholder = "Additional context (optional)"
	m.bodyInput.Prompt = ""
	m.bodyInput.ShowLineNumbers = false
	m.bodyInput.Cursor.SetMode(cursor.CursorStatic)
	m.bodyInput.SetWidth(width - 24)
	m.bodyInput.FocusedStyle.CursorLine = lipgloss.NewStyle()
	m.bodyInput.BlurredStyle.CursorLine = lipgloss.NewStyle()
	m.updateBodyHeight() // Start with minimum height, will autogrow as content is added

	// Schedule input
	m.scheduleInput = textinput.New()
	m.scheduleInput.Placeholder = "e.g., 1h, 2h30m, tomorrow 9am"
	m.scheduleInput.Prompt = ""
	m.scheduleInput.Cursor.SetMode(cursor.CursorStatic)
	m.scheduleInput.Width = width - 24

	// Attachments input
	m.attachmentsInput = textinput.New()
	m.attachmentsInput.Placeholder = "Files (drag anywhere or type paths)"
	m.attachmentsInput.Prompt = ""
	m.attachmentsInput.Cursor.SetMode(cursor.CursorStatic)
	m.attachmentsInput.Width = width - 24

	return m
}

// Init initializes the form.
func (m *FormModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles messages.
func (m *FormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	// Handle autocomplete debounce tick - fire the LLM request
	case autocompleteTickMsg:
		// Only process if this is still the current debounce request
		if msg.debounceID == m.debounceID && m.pendingDebounce {
			m.pendingDebounce = false
			m.loadingAutocomplete = true
			// Cancel any previous in-flight request
			if m.autocompleteCancel != nil {
				m.autocompleteCancel()
			}
			return m, m.fetchAutocompleteSuggestion(msg.fieldType, msg.input, msg.project, msg.context)
		}
		return m, nil

	// Handle autocomplete suggestion result
	case autocompleteSuggestionMsg:
		m.loadingAutocomplete = false
		if msg.suggestion != nil {
			// Get current input value to check if suggestion is still relevant
			var currentInput string
			switch msg.fieldType {
			case "title":
				currentInput = m.titleInput.Value()
			case "body":
				currentInput = m.bodyInput.Value()
			}

			// Check if current input is a prefix of the suggestion (user typed ahead but in same direction)
			fullText := msg.suggestion.FullText
			if strings.HasPrefix(strings.ToLower(fullText), strings.ToLower(currentInput)) && len(currentInput) < len(fullText) {
				// Current input is a valid prefix - show remaining text as ghost
				suffix := fullText[len(currentInput):]
				m.ghostText = suffix
				m.ghostFullText = currentInput + suffix // Preserve user's casing
			} else if msg.debounceID == m.debounceID {
				// Exact debounce match - use suggestion as-is
				m.ghostText = msg.suggestion.Text
				m.ghostFullText = msg.suggestion.FullText
			}
			// Otherwise: user typed something different, discard suggestion
		}
		return m, nil

	case tea.KeyMsg:
		// Handle bracketed paste (file drag-drop)
		if msg.Paste && msg.Type == tea.KeyRunes {
			path := strings.TrimSpace(string(msg.Runes))
			// Remove quotes and escape chars that terminals may add
			path = strings.Trim(path, "\"'")
			path = strings.ReplaceAll(path, "\\", "")
			// Check if it's a valid file path
			if absPath, err := filepath.Abs(path); err == nil {
				if _, statErr := os.Stat(absPath); statErr == nil {
					// It's a real file - add as attachment
					m.attachments = append(m.attachments, absPath)
					return m, nil
				}
			}
			// Not a file path - treat as regular paste into focused field
			pastedText := string(msg.Runes)
			switch m.focused {
			case FieldTitle:
				m.titleInput.SetValue(m.titleInput.Value() + pastedText)
			case FieldBody:
				m.bodyInput.SetValue(m.bodyInput.Value() + pastedText)
				m.updateBodyHeight() // Autogrow after paste
			case FieldAttachments:
				m.attachmentsInput.SetValue(m.attachmentsInput.Value() + pastedText)
			}
			return m, nil
		}

		// Handle cancel confirmation state
		if m.showCancelConfirm {
			switch msg.String() {
			case "y", "Y", "enter":
				m.cancelled = true
				return m, nil
			case "n", "N", "esc":
				m.showCancelConfirm = false
				return m, nil
			}
			// Ignore other keys while confirmation is showing
			return m, nil
		}

		// Handle task reference autocomplete when active
		if m.showTaskRefAutocomplete && m.taskRefAutocomplete != nil && m.taskRefAutocomplete.HasResults() {
			switch msg.String() {
			case "up", "ctrl+p", "ctrl+k":
				m.taskRefAutocomplete.MoveUp()
				return m, nil
			case "down", "ctrl+n", "ctrl+j":
				m.taskRefAutocomplete.MoveDown()
				return m, nil
			case "enter", "tab":
				m.taskRefAutocomplete.Select()
				m.insertTaskRef()
				return m, nil
			case "esc":
				m.showTaskRefAutocomplete = false
				m.taskRefAutocomplete.Reset()
				return m, nil
			}
			// Let other keys fall through to update the body input and re-trigger autocomplete
		}

		switch msg.String() {
		case "ctrl+c":
			m.cancelled = true
			return m, nil

		case "esc":
			// If task ref autocomplete is showing WITH visible results, dismiss it first
			if m.showTaskRefAutocomplete && m.taskRefAutocomplete != nil && m.taskRefAutocomplete.HasResults() {
				m.showTaskRefAutocomplete = false
				m.taskRefAutocomplete.Reset()
				return m, nil
			}
			// Clean up invisible autocomplete state (no return - continue processing)
			if m.showTaskRefAutocomplete {
				m.showTaskRefAutocomplete = false
				if m.taskRefAutocomplete != nil {
					m.taskRefAutocomplete.Reset()
				}
			}
			// If there's a ghost suggestion or loading, dismiss it first
			if m.ghostText != "" || m.loadingAutocomplete || m.pendingDebounce {
				m.clearGhostText()
				return m, nil
			}
			// If form has data, show confirmation before cancelling
			if m.hasFormData() {
				m.showCancelConfirm = true
				return m, nil
			}
			// Otherwise cancel the form
			m.cancelled = true
			return m, nil

		case "ctrl+ ", "ctrl+space":
			// Manually trigger autocomplete (if enabled)
			if m.autocompleteEnabled && (m.focused == FieldTitle || m.focused == FieldBody) {
				m.clearGhostText()
				var input, extraContext string
				var fieldType string
				if m.focused == FieldTitle {
					input = m.titleInput.Value()
					fieldType = "title"
				} else {
					input = m.bodyInput.Value()
					fieldType = "body"
					extraContext = m.titleInput.Value()
				}
				if len(input) >= 2 {
					m.loadingAutocomplete = true
					return m, m.fetchAutocompleteSuggestion(fieldType, input, m.project, extraContext)
				}
			}
			return m, nil

		case "ctrl+s":
			// Submit from anywhere
			m.parseAttachments()
			m.submitted = true
			return m, nil

		case "tab":
			// If there's ghost text, accept it instead of moving to next field
			if m.ghostText != "" && (m.focused == FieldTitle || m.focused == FieldBody) {
				m.acceptGhostText()
				return m, nil
			}
			// Check if moving from title to body - auto-suggest description
			wasOnTitle := m.focused == FieldTitle
			m.focusNext()
			if wasOnTitle && m.focused == FieldBody && m.bodyInput.Value() == "" && m.titleInput.Value() != "" {
				// Trigger autocomplete for empty body based on title
				return m, m.triggerBodySuggestion()
			}
			return m, nil

		case "shift+tab":
			m.focusPrev()
			return m, nil

		case "enter":
			// In body field, enter adds newline (handled by textarea)
			if m.focused == FieldBody {
				break
			}
			// On last field, submit
			if m.focused == FieldRecurrence {
				m.parseAttachments()
				m.submitted = true
				return m, nil
			}
			// Otherwise move to next field
			m.focusNext()
			return m, nil

		case "left":
			if m.focused == FieldProject {
				m.projectIdx = (m.projectIdx - 1 + len(m.projects)) % len(m.projects)
				m.project = m.projects[m.projectIdx]
				m.loadLastTaskTypeForProject()
				return m, nil
			}
			if m.focused == FieldType {
				m.typeIdx = (m.typeIdx - 1 + len(m.types)) % len(m.types)
				m.taskType = m.types[m.typeIdx]
				return m, nil
			}
			if m.focused == FieldExecutor {
				m.executorIdx = (m.executorIdx - 1 + len(m.executors)) % len(m.executors)
				m.executor = m.executors[m.executorIdx]
				return m, nil
			}
			if m.focused == FieldRecurrence {
				m.recurrenceIdx = (m.recurrenceIdx - 1 + len(m.recurrences)) % len(m.recurrences)
				m.recurrence = m.recurrences[m.recurrenceIdx]
				return m, nil
			}

		case "right":
			if m.focused == FieldProject {
				m.projectIdx = (m.projectIdx + 1) % len(m.projects)
				m.project = m.projects[m.projectIdx]
				m.loadLastTaskTypeForProject()
				return m, nil
			}
			if m.focused == FieldType {
				m.typeIdx = (m.typeIdx + 1) % len(m.types)
				m.taskType = m.types[m.typeIdx]
				return m, nil
			}
			if m.focused == FieldExecutor {
				m.executorIdx = (m.executorIdx + 1) % len(m.executors)
				m.executor = m.executors[m.executorIdx]
				return m, nil
			}
			if m.focused == FieldRecurrence {
				m.recurrenceIdx = (m.recurrenceIdx + 1) % len(m.recurrences)
				m.recurrence = m.recurrences[m.recurrenceIdx]
				return m, nil
			}

		// Alt+Arrow keys for word/paragraph movement in text fields
		case "alt+left":
			if m.focused == FieldBody {
				m.moveCursorWordBackward()
				return m, nil
			}
			if m.focused == FieldTitle {
				m.moveTitleCursorWordBackward()
				return m, nil
			}
		case "alt+right":
			if m.focused == FieldBody {
				m.moveCursorWordForward()
				return m, nil
			}
			if m.focused == FieldTitle {
				m.moveTitleCursorWordForward()
				return m, nil
			}
		case "alt+up":
			if m.focused == FieldBody {
				m.moveCursorParagraphBackward()
				return m, nil
			}
		case "alt+down":
			if m.focused == FieldBody {
				m.moveCursorParagraphForward()
				return m, nil
			}

		default:
			// Type-to-select for selector fields
			if m.focused == FieldProject || m.focused == FieldType || m.focused == FieldExecutor || m.focused == FieldRecurrence {
				key := msg.String()
				if len(key) == 1 && unicode.IsLetter(rune(key[0])) {
					m.selectByPrefix(strings.ToLower(key))
					return m, nil
				}
			}
		}
	}

	// Update the focused input
	var cmd tea.Cmd
	switch m.focused {
	case FieldTitle:
		m.titleInput, cmd = m.titleInput.Update(msg)
		// Trigger debounced autocomplete if title changed
		if m.titleInput.Value() != m.lastTitleValue {
			m.lastTitleValue = m.titleInput.Value()
			m.cancelAutocomplete() // Cancel any in-flight requests (including body suggestions)
			debounceCmd := m.scheduleAutocomplete("title", m.titleInput.Value(), m.project, "")
			return m, tea.Batch(cmd, debounceCmd)
		}
	case FieldBody:
		m.bodyInput, cmd = m.bodyInput.Update(msg)
		m.updateBodyHeight() // Autogrow as content changes
		// Check for task reference autocomplete trigger (#)
		m.updateTaskRefAutocomplete()
		// Trigger debounced autocomplete if body changed (but not if task ref autocomplete is active)
		if m.bodyInput.Value() != m.lastBodyValue {
			m.lastBodyValue = m.bodyInput.Value()
			if !m.showTaskRefAutocomplete {
				m.clearGhostText() // Clear old suggestion immediately
				debounceCmd := m.scheduleAutocomplete("body", m.bodyInput.Value(), m.project, m.titleInput.Value())
				return m, tea.Batch(cmd, debounceCmd)
			}
		}
	case FieldSchedule:
		m.scheduleInput, cmd = m.scheduleInput.Update(msg)
	case FieldAttachments:
		m.attachmentsInput, cmd = m.attachmentsInput.Update(msg)
	}

	return m, cmd
}

// scheduleAutocomplete schedules a debounced autocomplete request.
func (m *FormModel) scheduleAutocomplete(fieldType, input, project, extraContext string) tea.Cmd {
	// Skip if autocomplete is disabled
	if !m.autocompleteEnabled {
		return nil
	}

	// Cancel any in-flight request immediately when user types more
	if m.autocompleteCancel != nil {
		m.autocompleteCancel()
		m.autocompleteCancel = nil
	}

	// Increment debounce ID to invalidate old requests
	m.debounceID++
	m.pendingDebounce = true
	debounceID := m.debounceID

	// Return a tick command that fires after the debounce delay (500ms pause in typing)
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return autocompleteTickMsg{
			debounceID: debounceID,
			fieldType:  fieldType,
			input:      input,
			project:    project,
			context:    extraContext,
		}
	})
}

// fetchAutocompleteSuggestion fetches an autocomplete suggestion from the LLM.
func (m *FormModel) fetchAutocompleteSuggestion(fieldType, input, project, extraContext string) tea.Cmd {
	if m.autocompleteSvc == nil {
		return nil
	}

	// Create a cancellable context - store cancel func so we can abort if user types more
	ctx, cancel := context.WithCancel(context.Background())
	m.autocompleteCancel = cancel

	svc := m.autocompleteSvc
	debounceID := m.debounceID // Capture current ID to verify response is still relevant

	// Fetch recent tasks for the current project (context-aware suggestions)
	var recentTasks []string
	if m.db != nil {
		if tasks, err := m.db.ListTasks(db.ListTasksOptions{
			Project:       project,
			Limit:         15,
			IncludeClosed: true,
		}); err == nil {
			for _, t := range tasks {
				recentTasks = append(recentTasks, t.Title)
			}
		}
	}

	return func() tea.Msg {
		suggestion := svc.GetSuggestion(ctx, input, fieldType, project, extraContext, recentTasks)
		return autocompleteSuggestionMsg{
			suggestion: suggestion,
			fieldType:  fieldType,
			debounceID: debounceID,
		}
	}
}

// triggerBodySuggestion triggers an autocomplete suggestion for an empty body based on title.
func (m *FormModel) triggerBodySuggestion() tea.Cmd {
	if !m.autocompleteEnabled || m.autocompleteSvc == nil {
		return nil
	}

	title := m.titleInput.Value()
	if title == "" {
		return nil
	}

	// Cancel any in-flight request
	if m.autocompleteCancel != nil {
		m.autocompleteCancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.autocompleteCancel = cancel

	svc := m.autocompleteSvc
	m.debounceID++
	debounceID := m.debounceID
	project := m.project

	return func() tea.Msg {
		suggestion := svc.GetSuggestion(ctx, "", "body_suggest", project, title, nil)
		return autocompleteSuggestionMsg{
			suggestion: suggestion,
			fieldType:  "body",
			debounceID: debounceID,
		}
	}
}

// acceptGhostText accepts the current ghost text suggestion.
func (m *FormModel) acceptGhostText() {
	if m.ghostText == "" || m.ghostFullText == "" {
		return
	}

	switch m.focused {
	case FieldTitle:
		m.titleInput.SetValue(m.ghostFullText)
		m.titleInput.CursorEnd()
		m.lastTitleValue = m.ghostFullText
	case FieldBody:
		m.bodyInput.SetValue(m.ghostFullText)
		m.bodyInput.CursorEnd()
		m.lastBodyValue = m.ghostFullText
	}

	m.ghostText = ""
	m.ghostFullText = ""
}

func (m *FormModel) selectByPrefix(prefix string) {
	switch m.focused {
	case FieldProject:
		for i, p := range m.projects {
			if strings.HasPrefix(strings.ToLower(p), prefix) {
				m.projectIdx = i
				m.project = p
				m.loadLastTaskTypeForProject()
				return
			}
		}
	case FieldType:
		for i, t := range m.types {
			label := t
			if label == "" {
				label = "none"
			}
			if strings.HasPrefix(strings.ToLower(label), prefix) {
				m.typeIdx = i
				m.taskType = t
				return
			}
		}
	case FieldExecutor:
		for i, e := range m.executors {
			if strings.HasPrefix(strings.ToLower(e), prefix) {
				m.executorIdx = i
				m.executor = e
				return
			}
		}
	case FieldRecurrence:
		for i, r := range m.recurrences {
			label := r
			if label == "" {
				label = "none"
			}
			if strings.HasPrefix(strings.ToLower(label), prefix) {
				m.recurrenceIdx = i
				m.recurrence = r
				return
			}
		}
	}
}

// loadLastTaskTypeForProject loads and sets the last used task type for the current project.
func (m *FormModel) loadLastTaskTypeForProject() {
	if m.db == nil || m.project == "" {
		return
	}

	lastType, err := m.db.GetLastTaskTypeForProject(m.project)
	if err != nil || lastType == "" {
		return
	}

	// Find the type in the list and set it
	for i, t := range m.types {
		if t == lastType {
			m.typeIdx = i
			m.taskType = t
			return
		}
	}
}

func (m *FormModel) focusNext() {
	m.blurAll()
	m.cancelAutocomplete()
	m.focused = (m.focused + 1) % (FieldRecurrence + 1)
	m.focusCurrent()
}

func (m *FormModel) focusPrev() {
	m.blurAll()
	m.cancelAutocomplete()
	m.focused = (m.focused - 1 + FieldRecurrence + 1) % (FieldRecurrence + 1)
	m.focusCurrent()
}

func (m *FormModel) clearGhostText() {
	m.ghostText = ""
	m.ghostFullText = ""
	m.loadingAutocomplete = false
	m.pendingDebounce = false
}

func (m *FormModel) cancelAutocomplete() {
	m.clearGhostText()
	m.debounceID++ // Invalidate any in-flight requests
	if m.autocompleteCancel != nil {
		m.autocompleteCancel()
		m.autocompleteCancel = nil
	}
}

func (m *FormModel) blurAll() {
	m.titleInput.Blur()
	m.bodyInput.Blur()
	m.scheduleInput.Blur()
	m.attachmentsInput.Blur()
}

func (m *FormModel) focusCurrent() {
	switch m.focused {
	case FieldTitle:
		m.titleInput.Focus()
	case FieldBody:
		m.bodyInput.Focus()
	case FieldSchedule:
		m.scheduleInput.Focus()
	case FieldAttachments:
		m.attachmentsInput.Focus()
	}
}

func (m *FormModel) parseAttachments() {
	input := strings.TrimSpace(m.attachmentsInput.Value())
	if input == "" {
		return
	}

	// Split by comma or newline
	parts := strings.FieldsFunc(input, func(r rune) bool {
		return r == ',' || r == '\n'
	})

	for _, part := range parts {
		path := strings.TrimSpace(part)
		// Remove quotes that some terminals add
		path = strings.Trim(path, "\"'")
		// Expand ~ to home directory
		if strings.HasPrefix(path, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[1:])
			}
		}
		if path != "" {
			m.attachments = append(m.attachments, path)
		}
	}
}

// checkTaskRefTrigger checks if the user has typed '#' for task reference autocomplete.
// Returns the query (text after #) and position of #, or empty string and -1 if not active.
func (m *FormModel) checkTaskRefTrigger() (query string, hashPos int) {
	if m.focused != FieldBody {
		return "", -1
	}

	text := m.bodyInput.Value()

	// Calculate absolute cursor position from line and column
	lines := strings.Split(text, "\n")
	currentLine := m.bodyInput.Line()
	lineInfo := m.bodyInput.LineInfo()
	charOffset := lineInfo.CharOffset

	// Calculate absolute position: sum of previous lines + current column
	cursorPos := 0
	for i := 0; i < currentLine && i < len(lines); i++ {
		cursorPos += len(lines[i]) + 1 // +1 for newline
	}
	cursorPos += charOffset

	// Make sure cursorPos is within bounds
	if cursorPos > len(text) {
		cursorPos = len(text)
	}
	if cursorPos < 0 {
		cursorPos = 0
	}

	if cursorPos == 0 {
		return "", -1
	}

	// Find the # before cursor position
	// We need to find the last # that is either at the start or preceded by whitespace
	lastHashPos := -1
	for i := cursorPos - 1; i >= 0; i-- {
		if text[i] == '#' {
			// Check if this # is at start or preceded by whitespace
			if i == 0 || unicode.IsSpace(rune(text[i-1])) {
				lastHashPos = i
				break
			}
		}
		// Stop if we hit a space before finding #
		if unicode.IsSpace(rune(text[i])) {
			break
		}
	}

	if lastHashPos == -1 {
		return "", -1
	}

	// Extract the query (everything after # up to cursor or next whitespace)
	queryStart := lastHashPos + 1
	queryEnd := cursorPos
	for i := queryStart; i < cursorPos; i++ {
		if unicode.IsSpace(rune(text[i])) {
			return "", -1 // Space in the middle means it's not a task ref
		}
	}

	if queryEnd > len(text) {
		queryEnd = len(text)
	}

	return text[queryStart:queryEnd], lastHashPos
}

// updateTaskRefAutocomplete updates the task reference autocomplete state.
func (m *FormModel) updateTaskRefAutocomplete() {
	if m.taskRefAutocomplete == nil {
		return
	}

	query, hashPos := m.checkTaskRefTrigger()
	if hashPos >= 0 {
		// Show autocomplete
		m.showTaskRefAutocomplete = true
		m.taskRefAutocomplete.SetQuery(query, hashPos)
	} else {
		// Hide autocomplete
		m.showTaskRefAutocomplete = false
		m.taskRefAutocomplete.Reset()
	}
}

// insertTaskRef inserts the selected task reference into the body.
func (m *FormModel) insertTaskRef() {
	if m.taskRefAutocomplete == nil || m.taskRefAutocomplete.SelectedTask() == nil {
		return
	}

	text := m.bodyInput.Value()
	hashPos := m.taskRefAutocomplete.GetCursorPos()
	queryLen := m.taskRefAutocomplete.GetQueryLength()
	ref := m.taskRefAutocomplete.GetReference()

	// Replace #query with #123
	newText := text[:hashPos] + ref
	afterQuery := hashPos + queryLen
	if afterQuery < len(text) {
		newText += text[afterQuery:]
	}

	m.bodyInput.SetValue(newText)
	// Move cursor to after the inserted reference
	newCursorPos := hashPos + len(ref)
	m.bodyInput.SetCursor(newCursorPos)

	// Reset autocomplete
	m.showTaskRefAutocomplete = false
	m.taskRefAutocomplete.Reset()
}

// View renders the form.
func (m *FormModel) View() string {
	var b strings.Builder

	// Styles
	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary).Width(14)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	selectedStyle := lipgloss.NewStyle().Background(ColorPrimary).Foreground(lipgloss.Color("0"))
	optionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	cursorStyle := lipgloss.NewStyle().Foreground(ColorPrimary)

	// Header
	headerText := "New Task"
	if m.isEdit {
		headerText = "Edit Task"
	}
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		Render(headerText)
	b.WriteString(header)
	b.WriteString("\n\n")

	// Ghost text style for autocomplete suggestions
	ghostStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)

	// Project selector (first for immediate context)
	cursor := " "
	if m.focused == FieldProject {
		cursor = cursorStyle.Render("▸")
	}
	b.WriteString(cursor + " " + labelStyle.Render("Project") + m.renderSelector(m.projects, m.projectIdx, m.focused == FieldProject, selectedStyle, optionStyle, dimStyle))
	b.WriteString("\n\n")

	// Title
	cursor = " "
	if m.focused == FieldTitle {
		cursor = cursorStyle.Render("▸")
	}
	titleView := m.titleInput.View()
	// Add ghost text after title if field is focused and we have a suggestion
	if m.focused == FieldTitle && m.ghostText != "" {
		// Strip trailing whitespace from the fixed-width view before adding ghost text
		titleView = strings.TrimRight(titleView, " ") + ghostStyle.Render(m.ghostText)
	}
	b.WriteString(cursor + " " + labelStyle.Render("Title") + titleView)
	b.WriteString("\n\n")

	// Body (textarea)
	cursor = " "
	if m.focused == FieldBody {
		cursor = cursorStyle.Render("▸")
	}
	b.WriteString(cursor + " " + labelStyle.Render("Details"))
	b.WriteString("\n")
	// Hide placeholder when ghost text is showing
	savedPlaceholder := m.bodyInput.Placeholder
	if m.focused == FieldBody && m.ghostText != "" && m.bodyInput.Value() == "" {
		m.bodyInput.Placeholder = ""
	}
	// Indent the textarea and add scrollbar if content overflows
	bodyLines := strings.Split(m.bodyInput.View(), "\n")
	m.bodyInput.Placeholder = savedPlaceholder // Restore placeholder
	scrollbar := m.renderBodyScrollbar(len(bodyLines))
	cursorLine := m.bodyInput.Line() // Get the line where cursor is
	for i, line := range bodyLines {
		// Add ghost text at cursor line when focused (but not when task ref autocomplete is active)
		if m.focused == FieldBody && i == cursorLine && m.ghostText != "" && !m.showTaskRefAutocomplete {
			// Strip trailing whitespace from the fixed-width line before adding ghost text
			line = strings.TrimRight(line, " ") + ghostStyle.Render(m.ghostText)
		}
		scrollChar := ""
		if i < len(scrollbar) {
			scrollChar = scrollbar[i]
		}
		b.WriteString("   " + line + scrollChar + "\n")
	}

	// Show task reference autocomplete dropdown if active
	if m.showTaskRefAutocomplete && m.taskRefAutocomplete != nil && m.taskRefAutocomplete.HasResults() {
		dropdownView := m.taskRefAutocomplete.View()
		// Calculate horizontal offset based on cursor position
		// The dropdown should appear below where the '#' was typed
		lineInfo := m.bodyInput.LineInfo()
		queryLen := m.taskRefAutocomplete.GetQueryLength() // includes the '#'
		// Calculate visual column of '#': current column minus query length
		// Add 3 for base indent (the "   " before body lines)
		visualCol := lineInfo.ColumnOffset - queryLen + 3
		if visualCol < 3 {
			visualCol = 3 // Minimum indent to align with body content
		}
		indent := strings.Repeat(" ", visualCol)
		for _, line := range strings.Split(dropdownView, "\n") {
			b.WriteString(indent + line + "\n")
		}
	}

	// Attachments (positioned after body - drag files from anywhere in the form)
	cursor = " "
	if m.focused == FieldAttachments {
		cursor = cursorStyle.Render("▸")
	}
	attachmentLine := m.attachmentsInput.View()
	// Show attached files
	if len(m.attachments) > 0 {
		var fileNames []string
		for _, path := range m.attachments {
			fileNames = append(fileNames, filepath.Base(path))
		}
		attachmentLine = lipgloss.NewStyle().Foreground(ColorPrimary).Render(strings.Join(fileNames, ", ")) + "  " + m.attachmentsInput.View()
	}
	b.WriteString("\n")
	b.WriteString(cursor + " " + labelStyle.Render("Attachments") + attachmentLine)
	b.WriteString("\n\n")

	// Type selector
	cursor = " "
	if m.focused == FieldType {
		cursor = cursorStyle.Render("▸")
	}
	// Build type labels from m.types (replace empty string with "none")
	typeLabels := make([]string, len(m.types))
	for i, t := range m.types {
		if t == "" {
			typeLabels[i] = "none"
		} else {
			typeLabels[i] = t
		}
	}
	b.WriteString(cursor + " " + labelStyle.Render("Type") + m.renderSelector(typeLabels, m.typeIdx, m.focused == FieldType, selectedStyle, optionStyle, dimStyle))
	b.WriteString("\n\n")

	// Executor selector
	cursor = " "
	if m.focused == FieldExecutor {
		cursor = cursorStyle.Render("▸")
	}
	b.WriteString(cursor + " " + labelStyle.Render("Executor") + m.renderSelector(m.executors, m.executorIdx, m.focused == FieldExecutor, selectedStyle, optionStyle, dimStyle))
	b.WriteString("\n\n")

	// Schedule input
	cursor = " "
	if m.focused == FieldSchedule {
		cursor = cursorStyle.Render("▸")
	}
	b.WriteString(cursor + " " + labelStyle.Render("Schedule") + m.scheduleInput.View())
	b.WriteString("\n\n")

	// Recurrence selector
	cursor = " "
	if m.focused == FieldRecurrence {
		cursor = cursorStyle.Render("▸")
	}
	// Build recurrence labels from m.recurrences (replace empty string with "none")
	recurrenceLabels := make([]string, len(m.recurrences))
	for i, r := range m.recurrences {
		if r == "" {
			recurrenceLabels[i] = "none"
		} else {
			recurrenceLabels[i] = r
		}
	}
	b.WriteString(cursor + " " + labelStyle.Render("Recurrence") + m.renderSelector(recurrenceLabels, m.recurrenceIdx, m.focused == FieldRecurrence, selectedStyle, optionStyle, dimStyle))
	b.WriteString("\n\n")

	// Cancel confirmation message
	if m.showCancelConfirm {
		confirmStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("214")) // Orange/warning color
		b.WriteString("  " + confirmStyle.Render("Discard changes? (y/n)") + "\n")
	}

	// Help
	helpText := "tab accept/navigate • # ref task • ctrl+space suggest • ←→ select • ctrl+s submit • esc dismiss/cancel"
	b.WriteString("  " + dimStyle.Render(helpText))

	// Wrap in box - use full height (subtract 2 for border)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(m.width - 4).
		Height(m.height - 2)

	return box.Render(b.String())
}

func (m *FormModel) renderSelector(options []string, selected int, focused bool, selectedStyle, optionStyle, dimStyle lipgloss.Style) string {
	var parts []string
	for i, opt := range options {
		label := opt
		if label == "" {
			label = "none"
		}
		if i == selected {
			if focused {
				parts = append(parts, selectedStyle.Render(" "+label+" "))
			} else {
				parts = append(parts, optionStyle.Bold(true).Render(label))
			}
		} else {
			parts = append(parts, dimStyle.Render(label))
		}
	}
	return strings.Join(parts, "  ")
}

// hasFormData returns true if the form has any user-entered data.
// This is used to determine if we should show a confirmation before cancelling.
func (m *FormModel) hasFormData() bool {
	return strings.TrimSpace(m.titleInput.Value()) != "" ||
		strings.TrimSpace(m.bodyInput.Value()) != "" ||
		strings.TrimSpace(m.scheduleInput.Value()) != "" ||
		strings.TrimSpace(m.attachmentsInput.Value()) != "" ||
		len(m.attachments) > 0
}

// GetDBTask returns a db.Task from the form values.
func (m *FormModel) GetDBTask() *db.Task {
	status := db.StatusBacklog
	if m.queue {
		status = db.StatusQueued
	}

	task := &db.Task{
		Title:      m.titleInput.Value(),
		Body:       m.bodyInput.Value(),
		Status:     status,
		Type:       m.taskType,
		Project:    m.project,
		Executor:   m.executor,
		Recurrence: m.recurrence,
		PRURL:      m.prURL,
		PRNumber:   m.prNumber,
	}

	// Parse schedule time
	if scheduledAt := m.parseScheduleTime(); scheduledAt != nil {
		task.ScheduledAt = scheduledAt
	}

	return task
}

// parseScheduleTime parses the schedule input and returns a LocalTime.
// Supports formats like: "1h", "2h30m", "30m", "tomorrow 9am", "2024-01-15 14:00"
func (m *FormModel) parseScheduleTime() *db.LocalTime {
	input := strings.TrimSpace(m.scheduleInput.Value())
	if input == "" {
		return nil
	}

	now := time.Now()

	// Try to parse duration format (e.g., "1h", "2h30m", "30m")
	if duration := parseDuration(input); duration > 0 {
		scheduledTime := now.Add(duration)
		return &db.LocalTime{Time: scheduledTime}
	}

	// Try "tomorrow" with optional time
	if strings.HasPrefix(strings.ToLower(input), "tomorrow") {
		tomorrow := now.AddDate(0, 0, 1)
		timeStr := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(input), "tomorrow"))
		if timeStr == "" {
			// Default to 9am
			scheduled := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 9, 0, 0, 0, now.Location())
			return &db.LocalTime{Time: scheduled}
		}
		// Parse time like "9am", "2pm", "14:00"
		if t := parseTimeOfDay(timeStr, tomorrow); t != nil {
			return t
		}
	}

	// Try "today" with time
	if strings.HasPrefix(strings.ToLower(input), "today") {
		timeStr := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(input), "today"))
		if t := parseTimeOfDay(timeStr, now); t != nil {
			return t
		}
	}

	// Try full datetime format (e.g., "2024-01-15 14:00")
	if t, err := time.ParseInLocation("2006-01-02 15:04", input, now.Location()); err == nil {
		return &db.LocalTime{Time: t}
	}

	// Try date only (e.g., "2024-01-15") - defaults to 9am
	if t, err := time.ParseInLocation("2006-01-02", input, now.Location()); err == nil {
		scheduled := time.Date(t.Year(), t.Month(), t.Day(), 9, 0, 0, 0, now.Location())
		return &db.LocalTime{Time: scheduled}
	}

	return nil
}

// parseDuration parses duration strings like "1h", "30m", "2h30m"
func parseDuration(s string) time.Duration {
	// Try standard duration parsing first
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}

	// Handle shorthand like "1h30m" or "2h"
	re := regexp.MustCompile(`(?i)^(\d+)h(?:(\d+)m)?$`)
	if matches := re.FindStringSubmatch(s); matches != nil {
		hours, _ := strconv.Atoi(matches[1])
		minutes := 0
		if matches[2] != "" {
			minutes, _ = strconv.Atoi(matches[2])
		}
		return time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute
	}

	// Handle just minutes like "30m"
	re = regexp.MustCompile(`(?i)^(\d+)m$`)
	if matches := re.FindStringSubmatch(s); matches != nil {
		minutes, _ := strconv.Atoi(matches[1])
		return time.Duration(minutes) * time.Minute
	}

	return 0
}

// parseTimeOfDay parses time strings like "9am", "2pm", "14:00"
func parseTimeOfDay(s string, date time.Time) *db.LocalTime {
	s = strings.ToLower(strings.TrimSpace(s))

	// Parse 12-hour format like "9am", "2pm", "11:30am"
	re := regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?\s*(am|pm)$`)
	if matches := re.FindStringSubmatch(s); matches != nil {
		hour, _ := strconv.Atoi(matches[1])
		minutes := 0
		if matches[2] != "" {
			minutes, _ = strconv.Atoi(matches[2])
		}
		if matches[3] == "pm" && hour != 12 {
			hour += 12
		} else if matches[3] == "am" && hour == 12 {
			hour = 0
		}
		scheduled := time.Date(date.Year(), date.Month(), date.Day(), hour, minutes, 0, 0, date.Location())
		return &db.LocalTime{Time: scheduled}
	}

	// Parse 24-hour format like "14:00", "9:30"
	re = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	if matches := re.FindStringSubmatch(s); matches != nil {
		hour, _ := strconv.Atoi(matches[1])
		minutes, _ := strconv.Atoi(matches[2])
		scheduled := time.Date(date.Year(), date.Month(), date.Day(), hour, minutes, 0, 0, date.Location())
		return &db.LocalTime{Time: scheduled}
	}

	return nil
}

// SetQueue sets whether to queue the task.
func (m *FormModel) SetQueue(queue bool) {
	m.queue = queue
}

// SetSize updates the form dimensions for window resize handling.
func (m *FormModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	// Update input widths
	inputWidth := width - 24
	m.titleInput.Width = inputWidth
	m.bodyInput.SetWidth(inputWidth)
	m.scheduleInput.Width = inputWidth
	m.attachmentsInput.Width = inputWidth
	// Recalculate body height based on new dimensions
	m.updateBodyHeight()
}

// calculateBodyHeight calculates the appropriate height for the body textarea based on content.
// Returns a height between minHeight (8) and maxHeight (50% of available screen height).
func (m *FormModel) calculateBodyHeight() int {
	content := m.bodyInput.Value()

	// Minimum height - increased from 4 to 8 to display more content by default
	minHeight := 8

	// Maximum height is 50% of screen height
	// Account for other form elements: header(2) + title(2) + body label(1) + project(2) +
	// type(2) + schedule(2) + recurrence(2) + attachments(2) + help(1) + padding/borders(~6) = ~22 lines
	formOverhead := 22
	maxHeight := (m.height - formOverhead) / 2
	if maxHeight < minHeight {
		maxHeight = minHeight
	}

	// Count actual lines needed
	lines := 1
	if content != "" {
		lines = strings.Count(content, "\n") + 1
	}

	// Account for line wrapping
	textWidth := m.width - 24 // Same width as used in SetWidth
	if textWidth > 0 {
		for _, line := range strings.Split(content, "\n") {
			// Each line might wrap based on character count
			lineLen := len(line)
			if lineLen > textWidth {
				lines += lineLen / textWidth
			}
		}
	}

	// Apply min/max bounds
	height := lines
	if height < minHeight {
		height = minHeight
	}
	if height > maxHeight {
		height = maxHeight
	}

	return height
}

// updateBodyHeight updates the body textarea height based on content.
func (m *FormModel) updateBodyHeight() {
	height := m.calculateBodyHeight()
	m.bodyInput.SetHeight(height)
}

// renderBodyScrollbar renders a scrollbar for the body textarea if content overflows.
// Returns a slice of strings, one per visible line, containing the scrollbar character.
func (m *FormModel) renderBodyScrollbar(visibleLines int) []string {
	content := m.bodyInput.Value()
	if content == "" {
		return nil
	}

	// Get total content lines from the textarea
	totalLines := m.bodyInput.LineCount()
	viewportHeight := visibleLines

	// No scrollbar needed if all content fits
	if totalLines <= viewportHeight {
		return nil
	}

	// Get cursor line to estimate scroll offset
	// The textarea scrolls to keep the cursor visible
	cursorLine := m.bodyInput.Line()

	// Estimate the scroll offset: the viewport is positioned to keep cursor visible
	// The cursor should be somewhere within the visible viewport
	scrollOffset := 0
	if cursorLine >= viewportHeight {
		// Cursor is below initial viewport, so we've scrolled
		scrollOffset = cursorLine - viewportHeight + 1
	}
	// Clamp scroll offset to valid range
	maxOffset := totalLines - viewportHeight
	if scrollOffset > maxOffset {
		scrollOffset = maxOffset
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}

	// Calculate scrollbar dimensions
	// Thumb size is proportional to visible content / total content
	thumbSize := (viewportHeight * viewportHeight) / totalLines
	if thumbSize < 1 {
		thumbSize = 1
	}
	if thumbSize > viewportHeight {
		thumbSize = viewportHeight
	}

	// Thumb position is proportional to scroll offset
	scrollRange := totalLines - viewportHeight
	trackRange := viewportHeight - thumbSize
	thumbPos := 0
	if scrollRange > 0 && trackRange > 0 {
		thumbPos = (scrollOffset * trackRange) / scrollRange
	}

	// Style the scrollbar
	trackStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	thumbStyle := lipgloss.NewStyle().Foreground(ColorPrimary)

	// Build the scrollbar
	scrollbar := make([]string, viewportHeight)
	for i := 0; i < viewportHeight; i++ {
		if i >= thumbPos && i < thumbPos+thumbSize {
			scrollbar[i] = thumbStyle.Render("┃")
		} else {
			scrollbar[i] = trackStyle.Render("│")
		}
	}

	return scrollbar
}

// getCursorPos calculates the absolute cursor position in the body textarea.
func (m *FormModel) getCursorPos() int {
	text := m.bodyInput.Value()
	lines := strings.Split(text, "\n")
	currentLine := m.bodyInput.Line()
	lineInfo := m.bodyInput.LineInfo()
	charOffset := lineInfo.CharOffset

	// Calculate absolute position: sum of previous lines + current column
	cursorPos := 0
	for i := 0; i < currentLine && i < len(lines); i++ {
		cursorPos += len(lines[i]) + 1 // +1 for newline
	}
	cursorPos += charOffset

	// Clamp to valid range
	if cursorPos > len(text) {
		cursorPos = len(text)
	}
	if cursorPos < 0 {
		cursorPos = 0
	}

	return cursorPos
}

// isWordChar returns true if the rune is part of a word (alphanumeric or underscore).
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// moveCursorWordBackward moves the cursor to the start of the previous word in the body.
func (m *FormModel) moveCursorWordBackward() {
	text := m.bodyInput.Value()
	pos := m.getCursorPos()

	if pos == 0 {
		return
	}

	// Move past any whitespace/non-word chars before cursor
	for pos > 0 && !isWordChar(rune(text[pos-1])) {
		pos--
	}

	// Move to start of the word
	for pos > 0 && isWordChar(rune(text[pos-1])) {
		pos--
	}

	m.bodyInput.SetCursor(pos)
}

// moveCursorWordForward moves the cursor to the end of the next word in the body.
func (m *FormModel) moveCursorWordForward() {
	text := m.bodyInput.Value()
	pos := m.getCursorPos()
	length := len(text)

	if pos >= length {
		return
	}

	// Skip current word chars
	for pos < length && isWordChar(rune(text[pos])) {
		pos++
	}

	// Skip whitespace/non-word chars
	for pos < length && !isWordChar(rune(text[pos])) {
		pos++
	}

	m.bodyInput.SetCursor(pos)
}

// moveCursorParagraphBackward moves the cursor to the start of the previous paragraph.
func (m *FormModel) moveCursorParagraphBackward() {
	text := m.bodyInput.Value()
	pos := m.getCursorPos()

	if pos == 0 {
		return
	}

	// Move back one character to avoid staying on current position
	pos--

	// Skip any trailing newlines at current position
	for pos > 0 && text[pos] == '\n' {
		pos--
	}

	// Find the previous newline (start of current paragraph)
	for pos > 0 && text[pos-1] != '\n' {
		pos--
	}

	m.bodyInput.SetCursor(pos)
}

// moveCursorParagraphForward moves the cursor to the end of the current/next paragraph.
func (m *FormModel) moveCursorParagraphForward() {
	text := m.bodyInput.Value()
	pos := m.getCursorPos()
	length := len(text)

	if pos >= length {
		return
	}

	// Skip any newlines at current position
	for pos < length && text[pos] == '\n' {
		pos++
	}

	// Find the next newline (end of current paragraph)
	for pos < length && text[pos] != '\n' {
		pos++
	}

	m.bodyInput.SetCursor(pos)
}

// moveTitleCursorWordBackward moves the cursor to the start of the previous word in the title.
func (m *FormModel) moveTitleCursorWordBackward() {
	text := m.titleInput.Value()
	pos := m.titleInput.Position()

	if pos == 0 {
		return
	}

	// Move past any whitespace/non-word chars before cursor
	for pos > 0 && !isWordChar(rune(text[pos-1])) {
		pos--
	}

	// Move to start of the word
	for pos > 0 && isWordChar(rune(text[pos-1])) {
		pos--
	}

	m.titleInput.SetCursor(pos)
}

// moveTitleCursorWordForward moves the cursor to the end of the next word in the title.
func (m *FormModel) moveTitleCursorWordForward() {
	text := m.titleInput.Value()
	pos := m.titleInput.Position()
	length := len(text)

	if pos >= length {
		return
	}

	// Skip current word chars
	for pos < length && isWordChar(rune(text[pos])) {
		pos++
	}

	// Skip whitespace/non-word chars
	for pos < length && !isWordChar(rune(text[pos])) {
		pos++
	}

	m.titleInput.SetCursor(pos)
}

// GetAttachments returns the parsed attachment file paths.
func (m *FormModel) GetAttachments() []string {
	return m.attachments
}

// GetAttachment returns the first attachment for backwards compatibility.
func (m *FormModel) GetAttachment() string {
	if len(m.attachments) > 0 {
		return m.attachments[0]
	}
	return ""
}

// ProjectChanged returns true if the project was changed during editing.
// Only relevant when isEdit is true.
func (m *FormModel) ProjectChanged() bool {
	return m.isEdit && m.originalProject != "" && m.project != m.originalProject
}

// OriginalProject returns the original project when editing.
func (m *FormModel) OriginalProject() string {
	return m.originalProject
}
