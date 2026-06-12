package ui

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/autocomplete"
	"github.com/bborn/workflow/internal/db"
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
	FieldEffort
	FieldPermission
	FieldWorktree
	FieldBaseBranch
	FieldCount
)

// FormModel represents the new task form.
type FormModel struct {
	db              *db.DB
	width           int
	height          int
	submitted       bool
	cancelled       bool
	isEdit          bool   // true when editing an existing task
	originalProject string // original project when editing (to detect project changes)

	// Current field
	focused FormField

	// Form inputs
	titleInput       textinput.Model
	bodyInput        textarea.Model
	attachmentsInput textinput.Model
	baseBranchInput  textinput.Model

	// Select values
	project              string
	projectIdx           int
	projects             []string
	projectSearchMode    bool     // true when typing to search/filter projects
	projectSearchQuery   string   // current search query
	projectFiltered      []string // filtered project list (fuzzy matched)
	projectFilteredIdx   int      // selected index in filtered list
	taskType             string
	typeIdx              int
	types                []string
	executor             string // "claude", "codex", "gemini"
	executorIdx          int
	executors            []string
	availableExecutors   []string // Original list of available executors (for rebuilding when project changes)
	effortLevel          string   // Per-task Claude effort override ("" = use global/Claude default)
	effortIdx            int
	effortLevels         []string // Selectable effort values; "" (first) means "default"
	effortTouched        bool     // user picked an effort explicitly; stop following project default
	permissionMode       string   // Per-task permission mode ("default"/"auto"/"dangerous")
	permissionIdx        int
	permissionModes      []string // Selectable permission modes
	permissionTouched    bool     // user picked a permission explicitly; stop following project default
	worktreeMode         string   // Per-task worktree override ("" = project default, "worktree", "in-place")
	worktreeModeIdx      int
	worktreeModes        []string // Selectable worktree modes; "" (first) means "project default"
	projectUsesWorktrees bool     // selected project's UseWorktrees setting (drives base-branch visibility)
	queue                bool
	attachments          []string // Parsed file paths
	attachmentCursor     int      // Index of the currently selected attachment chip

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

	// Progressive disclosure: hide advanced fields for simpler first experience
	showAdvanced bool

	// modal renders the form as a centered, content-sized modal overlay
	// (used when editing from the detail view) rather than a full-screen form.
	modal bool
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

// buildExecutorList creates the list of executors for the form.
// Only available (installed) executors are included in the list.
// Executors are sorted by usage count (most used first) for the current project.
func buildExecutorList(availableExecutors []string, usageCounts map[string]int) []string {
	if len(availableExecutors) == 0 {
		return []string{}
	}
	result := make([]string, len(availableExecutors))
	copy(result, availableExecutors)

	// Sort by usage count (descending), then alphabetically for ties
	if len(usageCounts) > 0 {
		sort.Slice(result, func(i, j int) bool {
			countI := usageCounts[result[i]]
			countJ := usageCounts[result[j]]
			if countI != countJ {
				return countI > countJ // Higher count first
			}
			return result[i] < result[j] // Alphabetical for ties
		})
	}

	return result
}

// effortLevelOptions returns the selectable effort values for the form. The first
// entry is the empty string, which represents "default" (no per-task override —
// uses Claude's global default).
func effortLevelOptions() []string {
	return append([]string{""}, db.EffortLevels()...)
}

// effortIndexFor returns the index of the given effort level in the options list,
// defaulting to 0 ("default") when not found.
func effortIndexFor(options []string, level string) int {
	for i, l := range options {
		if l == level {
			return i
		}
	}
	return 0
}

// permissionModeOptions returns the selectable permission modes for the form, in
// display order. "default" (prompt for each permission) comes first, then the
// less-restrictive modes.
func permissionModeOptions() []string {
	// Mirror the canonical UI order (default → accept-edits → auto → dangerous)
	// so the form stays in step with the rest of the app. Copy it since callers
	// store and index the slice.
	return append([]string(nil), db.PermissionModeCycle...)
}

// permissionIndexFor returns the index of the given permission mode in the
// options list, defaulting to 0 ("default") when not found.
func permissionIndexFor(options []string, mode string) int {
	for i, o := range options {
		if o == mode {
			return i
		}
	}
	return 0
}

// worktreeModeOptions returns the selectable worktree modes for the form. The
// first entry is the empty string, which represents "project default" (inherit
// the project's UseWorktrees setting — no per-task override).
func worktreeModeOptions() []string {
	return []string{db.WorktreeModeInherit, db.WorktreeModeWorktree, db.WorktreeModeInPlace}
}

// worktreeModeLabel returns the display label for a worktree mode option.
func worktreeModeLabel(mode string) string {
	switch mode {
	case db.WorktreeModeWorktree:
		return "worktree"
	case db.WorktreeModeInPlace:
		return "in place"
	default:
		return "project default"
	}
}

// worktreeModeIndexFor returns the index of the given worktree mode in the
// options list, defaulting to 0 ("project default") when not found.
func worktreeModeIndexFor(options []string, mode string) int {
	for i, o := range options {
		if o == mode {
			return i
		}
	}
	return 0
}

// NewEditFormModel creates a form model pre-populated with an existing task's data for editing.
func NewEditFormModel(database *db.DB, task *db.Task, width, height int, availableExecutors []string) *FormModel {
	// Set executor to default if not specified
	taskExecutor := task.Executor
	if taskExecutor == "" {
		taskExecutor = db.DefaultExecutor()
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

	// Get executor usage counts for sorting (scoped to project)
	var usageCounts map[string]int
	if database != nil {
		usageCounts, _ = database.GetExecutorUsageByProject(task.Project)
	}

	// Build executor list: only show available (installed) executors, sorted by usage
	executors := buildExecutorList(availableExecutors, usageCounts)

	// Find the executor index - if task's executor is not available, fall back to first available
	var executorIdx int
	var executorDisplay string
	found := false
	for i, e := range executors {
		if e == taskExecutor {
			executorIdx = i
			executorDisplay = e
			found = true
			break
		}
	}

	// If the task's executor is not in the available list, fall back to first available
	if !found && len(executors) > 0 {
		executorIdx = 0
		executorDisplay = executors[0]
	}

	m := &FormModel{
		db:                  database,
		width:               width,
		height:              height,
		focused:             FieldProject,
		taskType:            task.Type,
		project:             task.Project,
		originalProject:     task.Project, // Track original project for detecting changes
		executor:            executorDisplay,
		executorIdx:         executorIdx,
		executors:           executors,
		availableExecutors:  availableExecutors, // Store for rebuilding when project changes
		effortLevel:         task.EffortLevel,
		effortLevels:        effortLevelOptions(),
		effortIdx:           effortIndexFor(effortLevelOptions(), task.EffortLevel),
		effortTouched:       true, // editing: keep the task's existing effort unless changed
		permissionMode:      task.EffectivePermissionMode(),
		permissionModes:     permissionModeOptions(),
		permissionIdx:       permissionIndexFor(permissionModeOptions(), task.EffectivePermissionMode()),
		permissionTouched:   true, // editing: keep the task's existing mode unless changed
		worktreeMode:        db.NormalizeWorktreeMode(task.WorktreeMode),
		worktreeModes:       worktreeModeOptions(),
		worktreeModeIdx:     worktreeModeIndexFor(worktreeModeOptions(), db.NormalizeWorktreeMode(task.WorktreeMode)),
		isEdit:              true,
		prURL:               task.PRURL,
		prNumber:            task.PRNumber,
		autocompleteSvc:     autocompleteSvc,
		autocompleteEnabled: autocompleteEnabled,
		taskRefAutocomplete: NewTaskRefAutocompleteModel(database, width-24),
		attachmentCursor:    -1,
		showAdvanced:        true, // Always show all fields when editing
		modal:               true, // Edit form is shown as a centered modal
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
	m.titleInput.Placeholder = "What needs to be done? (optional if details provided)"
	m.titleInput.Prompt = ""
	m.titleInput.Cursor.SetMode(cursor.CursorStatic)
	m.titleInput.Width = width - 24
	m.titleInput.SetValue(task.Title)

	// Body textarea - pre-populate with existing body
	m.bodyInput = textarea.New()
	m.bodyInput.Placeholder = "Details (AI generates title if left empty above)"
	m.bodyInput.Prompt = ""
	m.bodyInput.ShowLineNumbers = false
	m.bodyInput.Cursor.SetMode(cursor.CursorStatic)
	m.bodyInput.SetWidth(width - 24)
	m.bodyInput.FocusedStyle.CursorLine = lipgloss.NewStyle()
	m.bodyInput.BlurredStyle.CursorLine = lipgloss.NewStyle()
	m.bodyInput.SetValue(task.Body)
	m.updateBodyHeight() // Autogrow based on content

	// Attachments input
	m.attachmentsInput = textinput.New()
	m.attachmentsInput.Placeholder = "Files (drag anywhere or type paths)"
	m.attachmentsInput.Prompt = ""
	m.attachmentsInput.Cursor.SetMode(cursor.CursorStatic)
	m.attachmentsInput.Width = width - 24

	// Base branch input - pre-populate with existing base branch
	m.baseBranchInput = textinput.New()
	m.baseBranchInput.Placeholder = "Base branch (default branch if empty)"
	m.baseBranchInput.Prompt = ""
	m.baseBranchInput.Cursor.SetMode(cursor.CursorStatic)
	m.baseBranchInput.Width = width - 24
	m.baseBranchInput.SetValue(task.BaseBranch)

	// Load the project's worktree setting for base-branch visibility
	m.refreshProjectWorktreeSetting()

	// Apply modal-aware input widths and body height now that modal is set.
	m.SetSize(width, height)

	return m
}

// NewFormModel creates a new form model.
func NewFormModel(database *db.DB, width, height int, workingDir string, availableExecutors []string) *FormModel {
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

	// Load show_advanced preference (default: true)
	showAdvanced := true
	if database != nil {
		if setting, _ := database.GetSetting("show_advanced"); setting == "false" {
			showAdvanced = false
		}
	}

	// Create form model first (executors will be set after project is determined)
	m := &FormModel{
		db:                  database,
		width:               width,
		height:              height,
		focused:             FieldProject, // Project first for immediate context
		autocompleteSvc:     autocompleteSvc,
		autocompleteEnabled: autocompleteEnabled,
		taskRefAutocomplete: NewTaskRefAutocompleteModel(database, width-24),
		attachmentCursor:    -1,
		effortLevels:        effortLevelOptions(), // Defaults to "" (Claude's global default)
		permissionModes:     permissionModeOptions(),
		worktreeModes:       worktreeModeOptions(), // Defaults to "" (project default)
		showAdvanced:        showAdvanced,          // Load from user preference
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

	// Default project priority:
	// 1. Last used project (always wins if available)
	// 2. Detect from working directory
	// 3. Fall back to 'personal'
	m.project = "personal"

	// Try detecting from working directory first
	if workingDir != "" && database != nil {
		if proj, err := database.GetProjectByPath(workingDir); err == nil && proj != nil {
			m.project = proj.Name
		}
	}

	// Last used project always takes priority
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

	// Store available executors for rebuilding when project changes
	m.availableExecutors = availableExecutors

	// Build executor list sorted by usage for this project
	m.rebuildExecutorListForProject()

	// Set default executor (first in the sorted list)
	if len(m.executors) > 0 {
		m.executorIdx = 0
		m.executor = m.executors[0]
	}

	// Load last used task type for the selected project
	m.loadLastTaskTypeForProject()

	// Load last used executor for the selected project (overrides default if available)
	m.loadLastExecutorForProject()

	// Seed the permission and effort selectors from the project's defaults
	// (last-used per project) so new tasks start with the same choices as the
	// previous task in that project.
	m.refreshPermissionDefaultForProject()
	m.refreshEffortDefaultForProject()
	m.refreshProjectWorktreeSetting()

	// Title input
	m.titleInput = textinput.New()
	m.titleInput.Placeholder = "What needs to be done?"
	m.titleInput.Prompt = ""
	m.titleInput.Cursor.SetMode(cursor.CursorStatic)
	m.titleInput.Width = width - 24
	// Focus title only if project field is not visible (showAdvanced is false)
	if !m.showAdvanced {
		m.focused = FieldTitle
		m.titleInput.Focus()
	}

	// Body textarea
	m.bodyInput = textarea.New()
	m.bodyInput.Placeholder = "Add more details, or leave empty (optional)"
	m.bodyInput.Prompt = ""
	m.bodyInput.ShowLineNumbers = false
	m.bodyInput.Cursor.SetMode(cursor.CursorStatic)
	m.bodyInput.SetWidth(width - 24)
	m.bodyInput.FocusedStyle.CursorLine = lipgloss.NewStyle()
	m.bodyInput.BlurredStyle.CursorLine = lipgloss.NewStyle()
	m.updateBodyHeight() // Start with minimum height, will autogrow as content is added

	// Attachments input
	m.attachmentsInput = textinput.New()
	m.attachmentsInput.Placeholder = "Files (drag anywhere or type paths)"
	m.attachmentsInput.Prompt = ""
	m.attachmentsInput.Cursor.SetMode(cursor.CursorStatic)
	m.attachmentsInput.Width = width - 24

	// Base branch input (shown only when the task will run in a worktree)
	m.baseBranchInput = textinput.New()
	m.baseBranchInput.Placeholder = "Base branch (default branch if empty)"
	m.baseBranchInput.Prompt = ""
	m.baseBranchInput.Cursor.SetMode(cursor.CursorStatic)
	m.baseBranchInput.Width = width - 24

	return m
}

// Init initializes the form.
func (m *FormModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles messages.
func (m *FormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Normalize modern CSI-u / modifyOtherKeys sequences (Shift+Enter,
	// Option+Delete, Cmd+Delete on macOS terminals like Ghostty, WezTerm,
	// Kitty, and iTerm2 with modifyOtherKeys) into standard tea.KeyMsg
	// values so the rest of the form sees the keys it already handles.
	msg = translateModernKey(msg)

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

		// Handle project search mode when active
		if m.projectSearchMode && m.focused == FieldProject {
			switch msg.String() {
			case "esc":
				m.exitProjectSearch()
				return m, nil
			case "enter", "tab":
				m.selectProjectFromSearch()
				if msg.String() == "tab" {
					m.focusNext()
				}
				return m, nil
			case "up", "ctrl+p":
				if m.projectFilteredIdx > 0 {
					m.projectFilteredIdx--
				}
				return m, nil
			case "down", "ctrl+n":
				if m.projectFilteredIdx < len(m.projectFiltered)-1 {
					m.projectFilteredIdx++
				}
				return m, nil
			case "backspace", "ctrl+h":
				if len(m.projectSearchQuery) > 0 {
					m.projectSearchQuery = m.projectSearchQuery[:len(m.projectSearchQuery)-1]
					m.filterProjects()
				} else {
					m.exitProjectSearch()
				}
				return m, nil
			case "ctrl+c":
				m.cancelled = true
				return m, nil
			case "ctrl+w":
				// Delete word backward
				m.projectSearchQuery = ""
				m.filterProjects()
				return m, nil
			case "ctrl+u":
				// Clear line
				m.projectSearchQuery = ""
				m.filterProjects()
				return m, nil
			default:
				key := msg.String()
				// Only accept printable single characters
				if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
					m.projectSearchQuery += key
					m.filterProjects()
					return m, nil
				}
			}
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
			if m.focused == FieldAttachments && m.attachmentSelectionActive() {
				m.clearAttachmentSelection()
				return m, nil
			}
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

		case "ctrl+e":
			// Toggle advanced fields (progressive disclosure)
			m.showAdvanced = !m.showAdvanced
			// Save preference
			if m.db != nil {
				if m.showAdvanced {
					m.db.SetSetting("show_advanced", "true")
				} else {
					m.db.SetSetting("show_advanced", "false")
				}
			}
			// If collapsing and currently on a hidden field, move to Title
			if !m.showAdvanced && !m.isFieldVisible(m.focused) {
				m.blurAll()
				m.focused = FieldTitle
				m.focusCurrent()
			}
			// Recalculate body height since available space changes with mode
			m.updateBodyHeight()
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
			// On project field, enter opens search mode
			if m.focused == FieldProject {
				m.enterProjectSearch()
				return m, nil
			}
			// On last visible field, submit
			if m.isLastVisibleField() {
				m.parseAttachments()
				m.submitted = true
				return m, nil
			}
			// Otherwise move to next field
			m.focusNext()
			return m, nil

		case "left":
			if m.handleAttachmentNavigation(-1) {
				return m, nil
			}
			if m.focused == FieldProject {
				m.projectIdx = (m.projectIdx - 1 + len(m.projects)) % len(m.projects)
				m.project = m.projects[m.projectIdx]
				m.loadLastTaskTypeForProject()
				m.rebuildExecutorListForProject() // Re-sort by usage for new project
				m.loadLastExecutorForProject()
				m.refreshPermissionDefaultForProject()
				m.refreshEffortDefaultForProject()
				m.refreshProjectWorktreeSetting()
				return m, nil
			}
			if m.focused == FieldType {
				m.typeIdx = (m.typeIdx - 1 + len(m.types)) % len(m.types)
				m.taskType = m.types[m.typeIdx]
				return m, nil
			}
			if m.focused == FieldExecutor && len(m.executors) > 0 {
				m.executorIdx = (m.executorIdx - 1 + len(m.executors)) % len(m.executors)
				m.executor = m.executors[m.executorIdx]
				return m, nil
			}
			if m.focused == FieldEffort && len(m.effortLevels) > 0 {
				m.effortIdx = (m.effortIdx - 1 + len(m.effortLevels)) % len(m.effortLevels)
				m.effortLevel = m.effortLevels[m.effortIdx]
				m.effortTouched = true
				return m, nil
			}
			if m.focused == FieldPermission && len(m.permissionModes) > 0 {
				m.permissionIdx = (m.permissionIdx - 1 + len(m.permissionModes)) % len(m.permissionModes)
				m.permissionMode = m.permissionModes[m.permissionIdx]
				m.permissionTouched = true
				return m, nil
			}
			if m.focused == FieldWorktree && len(m.worktreeModes) > 0 {
				m.worktreeModeIdx = (m.worktreeModeIdx - 1 + len(m.worktreeModes)) % len(m.worktreeModes)
				m.worktreeMode = m.worktreeModes[m.worktreeModeIdx]
				return m, nil
			}

		case "right":
			if m.handleAttachmentNavigation(1) {
				return m, nil
			}
			if m.focused == FieldProject {
				m.projectIdx = (m.projectIdx + 1) % len(m.projects)
				m.project = m.projects[m.projectIdx]
				m.loadLastTaskTypeForProject()
				m.rebuildExecutorListForProject() // Re-sort by usage for new project
				m.loadLastExecutorForProject()
				m.refreshPermissionDefaultForProject()
				m.refreshEffortDefaultForProject()
				m.refreshProjectWorktreeSetting()
				return m, nil
			}
			if m.focused == FieldType {
				m.typeIdx = (m.typeIdx + 1) % len(m.types)
				m.taskType = m.types[m.typeIdx]
				return m, nil
			}
			if m.focused == FieldExecutor && len(m.executors) > 0 {
				m.executorIdx = (m.executorIdx + 1) % len(m.executors)
				m.executor = m.executors[m.executorIdx]
				return m, nil
			}
			if m.focused == FieldEffort && len(m.effortLevels) > 0 {
				m.effortIdx = (m.effortIdx + 1) % len(m.effortLevels)
				m.effortLevel = m.effortLevels[m.effortIdx]
				m.effortTouched = true
				return m, nil
			}
			if m.focused == FieldPermission && len(m.permissionModes) > 0 {
				m.permissionIdx = (m.permissionIdx + 1) % len(m.permissionModes)
				m.permissionMode = m.permissionModes[m.permissionIdx]
				m.permissionTouched = true
				return m, nil
			}
			if m.focused == FieldWorktree && len(m.worktreeModes) > 0 {
				m.worktreeModeIdx = (m.worktreeModeIdx + 1) % len(m.worktreeModes)
				m.worktreeMode = m.worktreeModes[m.worktreeModeIdx]
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

		case "/":
			// Slash to enter project search mode when on project field
			if m.focused == FieldProject {
				m.enterProjectSearch()
				return m, nil
			}

		default:
			if m.handleAttachmentRemovalKey(msg) {
				return m, nil
			}
			// Project field: any letter enters search mode
			if m.focused == FieldProject {
				key := msg.String()
				if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
					m.enterProjectSearch()
					m.projectSearchQuery = key
					m.filterProjects()
					return m, nil
				}
			}
			// Type-to-select for other selector fields
			if m.focused == FieldType || m.focused == FieldExecutor || m.focused == FieldEffort || m.focused == FieldPermission || m.focused == FieldWorktree {
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
	case FieldAttachments:
		m.attachmentsInput, cmd = m.attachmentsInput.Update(msg)
		if m.attachmentsInput.Value() != "" && m.attachmentCursor != -1 {
			m.clearAttachmentSelection()
		}
	case FieldBaseBranch:
		m.baseBranchInput, cmd = m.baseBranchInput.Update(msg)
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

// enterProjectSearch activates the project search mode.
func (m *FormModel) enterProjectSearch() {
	m.projectSearchMode = true
	m.projectSearchQuery = ""
	m.projectFilteredIdx = 0
	m.filterProjects()
}

// exitProjectSearch deactivates project search mode without selecting.
func (m *FormModel) exitProjectSearch() {
	m.projectSearchMode = false
	m.projectSearchQuery = ""
	m.projectFiltered = nil
	m.projectFilteredIdx = 0
}

// selectProjectFromSearch selects the currently highlighted project in search results.
func (m *FormModel) selectProjectFromSearch() {
	if len(m.projectFiltered) == 0 {
		m.exitProjectSearch()
		return
	}
	if m.projectFilteredIdx >= len(m.projectFiltered) {
		m.projectFilteredIdx = 0
	}
	selected := m.projectFiltered[m.projectFilteredIdx]
	m.project = selected
	// Update projectIdx to match
	for i, p := range m.projects {
		if p == selected {
			m.projectIdx = i
			break
		}
	}
	m.exitProjectSearch()
	m.loadLastTaskTypeForProject()
	m.rebuildExecutorListForProject()
	m.loadLastExecutorForProject()
	m.refreshPermissionDefaultForProject()
	m.refreshEffortDefaultForProject()
	m.refreshProjectWorktreeSetting()
}

// filterProjects updates the filtered project list based on the search query.
func (m *FormModel) filterProjects() {
	query := m.projectSearchQuery
	if query == "" {
		// Show all projects, with current project first
		m.projectFiltered = make([]string, len(m.projects))
		copy(m.projectFiltered, m.projects)
		// Pre-select the current project
		m.projectFilteredIdx = 0
		for i, p := range m.projectFiltered {
			if p == m.project {
				m.projectFilteredIdx = i
				break
			}
		}
		return
	}

	type scored struct {
		name  string
		score int
	}
	var results []scored
	for _, p := range m.projects {
		s := fuzzyScore(p, query)
		if s > 0 {
			results = append(results, scored{p, s})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })
	m.projectFiltered = make([]string, len(results))
	for i, r := range results {
		m.projectFiltered[i] = r.name
	}
	m.projectFilteredIdx = 0
}

func (m *FormModel) selectByPrefix(prefix string) {
	switch m.focused {
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
	case FieldEffort:
		for i, l := range m.effortLevels {
			label := l
			if label == "" {
				label = "default"
			}
			if strings.HasPrefix(strings.ToLower(label), prefix) {
				m.effortIdx = i
				m.effortLevel = l
				m.effortTouched = true
				return
			}
		}
	case FieldPermission:
		for i, p := range m.permissionModes {
			if strings.HasPrefix(strings.ToLower(p), prefix) {
				m.permissionIdx = i
				m.permissionMode = p
				m.permissionTouched = true
				return
			}
		}
	case FieldWorktree:
		for i, mode := range m.worktreeModes {
			if strings.HasPrefix(strings.ToLower(worktreeModeLabel(mode)), prefix) {
				m.worktreeModeIdx = i
				m.worktreeMode = mode
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

// loadLastExecutorForProject loads and sets the last used executor for the current project.
func (m *FormModel) loadLastExecutorForProject() {
	if m.db == nil || m.project == "" {
		return
	}

	lastExecutor, err := m.db.GetLastExecutorForProject(m.project)
	if err != nil || lastExecutor == "" {
		return
	}

	// Find the executor in the list and set it
	// Only select if the executor is available (i.e., in the list)
	for i, e := range m.executors {
		if e == lastExecutor {
			m.executorIdx = i
			m.executor = e
			return
		}
	}
}

// defaultPermissionForProject resolves the permission mode a new task should
// start in for the given project: the project's configured default, falling back
// to the global default ("auto"). This is the value the form pre-selects so most
// tasks can be created and run without touching the permission field.
func (m *FormModel) defaultPermissionForProject(project string) string {
	if m.db != nil && project != "" {
		// Stickiness: the last mode used in this project wins, so a per-task
		// choice carries forward to the next task in the same project.
		if last, err := m.db.GetLastPermissionForProject(project); err == nil {
			if mode := db.NormalizePermissionMode(last); mode != "" {
				return mode
			}
		}
		if p, err := m.db.GetProjectByName(project); err == nil && p != nil {
			return p.EffectiveDefaultPermissionMode()
		}
	}
	return db.GlobalDefaultPermissionMode()
}

// resolvedPermissionMode returns the form's selected permission mode, normalized
// and defaulting to "default" when unset.
func (m *FormModel) resolvedPermissionMode() string {
	if mode := db.NormalizePermissionMode(m.permissionMode); mode != "" {
		return mode
	}
	return db.PermissionModeDefault
}

// refreshPermissionDefaultForProject re-seeds the permission selector from the
// current project's default, unless the user has explicitly chosen a mode. Called
// when the selected project changes so the default follows the project.
func (m *FormModel) refreshPermissionDefaultForProject() {
	if m.permissionTouched {
		return
	}
	m.permissionMode = m.defaultPermissionForProject(m.project)
	m.permissionIdx = permissionIndexFor(m.permissionModes, m.permissionMode)
}

// defaultEffortForProject returns the effort override a new task should start with
// for the given project: the last effort used in it (stickiness), or "" (the
// global/Claude default) when none was recorded.
func (m *FormModel) defaultEffortForProject(project string) string {
	if m.db != nil && project != "" {
		if last, err := m.db.GetLastEffortForProject(project); err == nil && db.IsValidEffortLevel(last) {
			return last
		}
	}
	return ""
}

// refreshEffortDefaultForProject re-seeds the effort selector from the current
// project's last-used effort, unless the user has explicitly chosen one. Called
// when the selected project changes so the default follows the project.
func (m *FormModel) refreshEffortDefaultForProject() {
	if m.effortTouched {
		return
	}
	m.effortLevel = m.defaultEffortForProject(m.project)
	m.effortIdx = effortIndexFor(m.effortLevels, m.effortLevel)
}

// refreshProjectWorktreeSetting reloads the selected project's UseWorktrees
// setting, which drives whether the base-branch input is relevant when the
// worktree mode is "project default". Defaults to true (the use_worktrees
// column default) when the project cannot be loaded.
func (m *FormModel) refreshProjectWorktreeSetting() {
	m.projectUsesWorktrees = true
	if m.db != nil && m.project != "" {
		if p, err := m.db.GetProjectByName(m.project); err == nil && p != nil {
			m.projectUsesWorktrees = p.UsesWorktrees()
		}
	}
}

// baseBranchRelevant reports whether the base-branch input applies: only when
// the task will run in a fresh worktree (forced per task, or inherited from a
// worktrees-on project).
func (m *FormModel) baseBranchRelevant() bool {
	switch m.worktreeMode {
	case db.WorktreeModeWorktree:
		return true
	case db.WorktreeModeInPlace:
		return false
	}
	return m.projectUsesWorktrees
}

// rebuildExecutorListForProject rebuilds the executor list sorted by usage for the current project.
// This should be called when the project changes to re-sort executors by usage count.
func (m *FormModel) rebuildExecutorListForProject() {
	if len(m.availableExecutors) == 0 {
		return
	}

	// Get executor usage counts for the current project
	var usageCounts map[string]int
	if m.db != nil && m.project != "" {
		usageCounts, _ = m.db.GetExecutorUsageByProject(m.project)
	}

	// Rebuild the list sorted by usage
	m.executors = buildExecutorList(m.availableExecutors, usageCounts)

	// Reset to first executor (most used) if current executor is still valid
	if len(m.executors) > 0 {
		// Try to keep the current executor selected if it's in the new list
		found := false
		for i, e := range m.executors {
			if e == m.executor {
				m.executorIdx = i
				found = true
				break
			}
		}
		// If current executor not found, default to first (most used)
		if !found {
			m.executorIdx = 0
			m.executor = m.executors[0]
		}
	}
}

// isFieldVisible returns whether a field should be shown in the current view.
// When showAdvanced is false, only Title and Body are visible.
func (m *FormModel) isFieldVisible(field FormField) bool {
	if field == FieldEffort {
		// Effort is a Claude-specific override (claude --effort), only shown in
		// advanced mode and only when the Claude executor is selected.
		return m.showAdvanced && m.executor == db.ExecutorClaude
	}
	if field == FieldBaseBranch {
		// Base branch only applies when a fresh worktree will be created.
		return m.showAdvanced && m.baseBranchRelevant()
	}
	if m.showAdvanced {
		return true
	}
	return field == FieldTitle || field == FieldBody
}

// isLastVisibleField returns whether the currently focused field is the last visible one.
func (m *FormModel) isLastVisibleField() bool {
	for i := m.focused + 1; i < FieldCount; i++ {
		if m.isFieldVisible(i) {
			return false
		}
	}
	return true
}

func (m *FormModel) focusNext() {
	m.blurAll()
	m.cancelAutocomplete()
	for i := 0; i < int(FieldCount); i++ {
		m.focused = (m.focused + 1) % FieldCount
		if m.isFieldVisible(m.focused) {
			break
		}
	}
	m.focusCurrent()
}

func (m *FormModel) focusPrev() {
	m.blurAll()
	m.cancelAutocomplete()
	for i := 0; i < int(FieldCount); i++ {
		m.focused = (m.focused - 1 + FieldCount) % FieldCount
		if m.isFieldVisible(m.focused) {
			break
		}
	}
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
	m.attachmentsInput.Blur()
	m.baseBranchInput.Blur()
	m.clearAttachmentSelection()
	if m.projectSearchMode {
		m.exitProjectSearch()
	}
}

func (m *FormModel) focusCurrent() {
	switch m.focused {
	case FieldTitle:
		m.titleInput.Focus()
	case FieldBody:
		m.bodyInput.Focus()
	case FieldAttachments:
		m.attachmentsInput.Focus()
	case FieldBaseBranch:
		m.baseBranchInput.Focus()
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

func (m *FormModel) attachmentSelectionEnabled() bool {
	return m.focused == FieldAttachments && len(m.attachments) > 0 && m.attachmentsInput.Value() == ""
}

func (m *FormModel) attachmentSelectionActive() bool {
	return m.attachmentSelectionEnabled() && m.attachmentCursor >= 0 && m.attachmentCursor < len(m.attachments)
}

func (m *FormModel) clearAttachmentSelection() {
	m.attachmentCursor = -1
}

func (m *FormModel) handleAttachmentNavigation(direction int) bool {
	if !m.attachmentSelectionEnabled() {
		return false
	}
	if len(m.attachments) == 0 {
		return false
	}
	if m.attachmentCursor == -1 {
		if direction < 0 {
			m.attachmentCursor = len(m.attachments) - 1
		} else {
			m.attachmentCursor = 0
		}
		return true
	}
	newIdx := m.attachmentCursor + direction
	if newIdx < 0 {
		newIdx = 0
	}
	if newIdx >= len(m.attachments) {
		newIdx = len(m.attachments) - 1
	}
	m.attachmentCursor = newIdx
	return true
}

func (m *FormModel) handleAttachmentRemovalKey(msg tea.KeyMsg) bool {
	if !m.attachmentSelectionEnabled() {
		return false
	}
	switch msg.Type {
	case tea.KeyBackspace, tea.KeyDelete:
		// supported key types
	default:
		key := msg.String()
		if key != "backspace" && key != "delete" && key != "ctrl+h" {
			return false
		}
	}
	if m.attachmentCursor == -1 {
		m.attachmentCursor = len(m.attachments) - 1
		return true
	}
	m.removeAttachmentAt(m.attachmentCursor)
	return true
}

func (m *FormModel) removeAttachmentAt(idx int) {
	if idx < 0 || idx >= len(m.attachments) {
		return
	}
	copy(m.attachments[idx:], m.attachments[idx+1:])
	m.attachments = m.attachments[:len(m.attachments)-1]
	if len(m.attachments) == 0 {
		m.attachmentCursor = -1
		return
	}
	if idx >= len(m.attachments) {
		m.attachmentCursor = len(m.attachments) - 1
		return
	}
	m.attachmentCursor = idx
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
	// Show project context in header when in simple mode
	if !m.showAdvanced && !m.isEdit && m.project != "" {
		headerText = "New Task · " + m.project
	}
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		Render(headerText)
	b.WriteString(header)
	b.WriteString("\n\n")

	// Discard confirmation warning. In modal mode it appears at the top of the
	// modal (right under the header) so it's immediately visible.
	confirmStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("214")) // Orange/warning color
	if m.modal && m.showCancelConfirm {
		b.WriteString("  " + confirmStyle.Render("Discard changes? (y/n)"))
		b.WriteString("\n\n")
	}

	// Ghost text style for autocomplete suggestions
	ghostStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)

	// Cursor indicator for focused field
	var cursor string

	// Project selector (only shown in advanced mode)
	if m.showAdvanced {
		cursor = " "
		if m.focused == FieldProject {
			cursor = cursorStyle.Render("▸")
		}
		if m.projectSearchMode && m.focused == FieldProject {
			// Search mode: show search input and filtered dropdown
			searchInputStyle := lipgloss.NewStyle().Foreground(ColorPrimary)
			queryDisplay := m.projectSearchQuery
			cursorChar := lipgloss.NewStyle().Background(ColorPrimary).Foreground(lipgloss.Color("0")).Render(" ")
			b.WriteString(cursor + " " + labelStyle.Render("Project") + searchInputStyle.Render(queryDisplay) + cursorChar)
			b.WriteString("\n")

			// Show filtered results as a vertical dropdown
			maxShow := 8
			if len(m.projectFiltered) < maxShow {
				maxShow = len(m.projectFiltered)
			}

			// Calculate scroll window
			scrollStart := 0
			if m.projectFilteredIdx >= maxShow {
				scrollStart = m.projectFilteredIdx - maxShow + 1
			}
			scrollEnd := scrollStart + maxShow
			if scrollEnd > len(m.projectFiltered) {
				scrollEnd = len(m.projectFiltered)
				scrollStart = scrollEnd - maxShow
				if scrollStart < 0 {
					scrollStart = 0
				}
			}

			// Scroll-up indicator
			if scrollStart > 0 {
				b.WriteString("                 " + dimStyle.Render("↑ more") + "\n")
			}

			for i := scrollStart; i < scrollEnd; i++ {
				p := m.projectFiltered[i]
				if i == m.projectFilteredIdx {
					b.WriteString("                 " + selectedStyle.Render(" "+p+" ") + "\n")
				} else {
					b.WriteString("                 " + optionStyle.Render(" "+p) + "\n")
				}
			}

			// Scroll-down indicator
			if scrollEnd < len(m.projectFiltered) {
				b.WriteString("                 " + dimStyle.Render("↓ more") + "\n")
			}

			if len(m.projectFiltered) == 0 {
				b.WriteString("                 " + dimStyle.Render("no matches") + "\n")
			}
			b.WriteString("\n")
		} else {
			// Normal mode: show current project with hint
			projectDisplay := m.project
			if projectDisplay == "" {
				projectDisplay = "none"
			}
			if m.focused == FieldProject {
				b.WriteString(cursor + " " + labelStyle.Render("Project") + selectedStyle.Render(" "+projectDisplay+" ") + "  " + dimStyle.Render("type to search · "+IconArrowLeft()+"/"+IconArrowRight()+" cycle"))
			} else {
				b.WriteString(cursor + " " + labelStyle.Render("Project") + optionStyle.Bold(true).Render(projectDisplay))
			}
			b.WriteString("\n\n")
		}
	}

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
	// Show indicator when title will be auto-generated
	if strings.TrimSpace(m.titleInput.Value()) == "" && strings.TrimSpace(m.bodyInput.Value()) != "" {
		autoGenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Italic(true)
		b.WriteString("  " + autoGenStyle.Render("✨ AI will generate title"))
	}
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

	// Advanced fields (Attachments, Type, Executor) - hidden by default
	if m.showAdvanced {
		// Attachments (positioned after body - drag files from anywhere in the form)
		cursor = " "
		if m.focused == FieldAttachments {
			cursor = cursorStyle.Render("▸")
		}
		attachmentInputView := m.attachmentsInput.View()
		b.WriteString("\n")
		b.WriteString(cursor + " " + labelStyle.Render("Attachments") + "\n")
		if len(m.attachments) > 0 {
			for i, path := range m.attachments {
				style := AttachmentChip
				if m.focused == FieldAttachments && m.attachmentSelectionActive() && m.attachmentCursor == i {
					style = AttachmentChipSelected
				}
				chip := style.Render(filepath.Base(path))
				for _, line := range strings.Split(chip, "\n") {
					if line != "" {
						b.WriteString("   " + line + "\n")
					}
				}
			}
		}
		b.WriteString("   " + attachmentInputView + "\n")
		if len(m.attachments) > 0 {
			help := Dim.Render(IconArrowLeft() + "/" + IconArrowRight() + " select • backspace/delete remove")
			b.WriteString("   " + help + "\n")
		}
		b.WriteString("\n")

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

		// Effort selector (Claude-specific; only shown for the Claude executor)
		if m.isFieldVisible(FieldEffort) {
			cursor = " "
			if m.focused == FieldEffort {
				cursor = cursorStyle.Render("▸")
			}
			// Build effort labels (replace "" with "default")
			effortLabels := make([]string, len(m.effortLevels))
			for i, l := range m.effortLevels {
				if l == "" {
					effortLabels[i] = "default"
				} else {
					effortLabels[i] = l
				}
			}
			b.WriteString(cursor + " " + labelStyle.Render("Effort") + m.renderSelector(effortLabels, m.effortIdx, m.focused == FieldEffort, selectedStyle, optionStyle, dimStyle))
			b.WriteString("\n\n")
		}

		// Permission selector
		if m.isFieldVisible(FieldPermission) {
			cursor = " "
			if m.focused == FieldPermission {
				cursor = cursorStyle.Render("▸")
			}
			b.WriteString(cursor + " " + labelStyle.Render("Permission") + m.renderSelector(m.permissionModes, m.permissionIdx, m.focused == FieldPermission, selectedStyle, optionStyle, dimStyle))
			b.WriteString("\n\n")
		}

		// Worktree selector (per-task override of the project's worktree setting)
		if m.isFieldVisible(FieldWorktree) {
			cursor = " "
			if m.focused == FieldWorktree {
				cursor = cursorStyle.Render("▸")
			}
			worktreeLabels := make([]string, len(m.worktreeModes))
			for i, mode := range m.worktreeModes {
				worktreeLabels[i] = worktreeModeLabel(mode)
			}
			b.WriteString(cursor + " " + labelStyle.Render("Worktree") + m.renderSelector(worktreeLabels, m.worktreeModeIdx, m.focused == FieldWorktree, selectedStyle, optionStyle, dimStyle))
			b.WriteString("\n\n")
		}

		// Base branch input (only when a fresh worktree will be created)
		if m.isFieldVisible(FieldBaseBranch) {
			cursor = " "
			if m.focused == FieldBaseBranch {
				cursor = cursorStyle.Render("▸")
			}
			b.WriteString(cursor + " " + labelStyle.Render("Base branch") + m.baseBranchInput.View())
			b.WriteString("\n\n")
		}
	} else {
		// Show compact summary of defaults and toggle hint
		b.WriteString("\n")
		summaryParts := []string{}
		if m.project != "" {
			summaryParts = append(summaryParts, m.project)
		}
		if m.executor != "" {
			summaryParts = append(summaryParts, m.executor)
		}
		if m.taskType != "" {
			summaryParts = append(summaryParts, m.taskType)
		}
		if m.permissionMode != "" {
			summaryParts = append(summaryParts, m.permissionMode)
		}
		defaultsLine := strings.Join(summaryParts, " · ")
		if defaultsLine != "" {
			defaultsLine = dimStyle.Render(defaultsLine+" ") + dimStyle.Foreground(ColorSecondary).Render("ctrl+e more options")
		} else {
			defaultsLine = dimStyle.Foreground(ColorSecondary).Render("ctrl+e more options")
		}
		b.WriteString("  " + defaultsLine)
		b.WriteString("\n\n")
	}

	// Cancel confirmation message. In modal mode this is rendered at the top
	// (see above); in full-screen mode it appears here near the help text.
	if m.showCancelConfirm && !m.modal {
		b.WriteString("  " + confirmStyle.Render("Discard changes? (y/n)") + "\n")
	}

	// Help
	var helpText string
	if m.showAdvanced {
		helpText = "tab accept/navigate • # ref task • ctrl+space suggest • " + IconArrowLeft() + IconArrowRight() + " select • ctrl+e fewer options • ctrl+s submit • esc cancel"
	} else {
		helpText = "tab accept/navigate • ctrl+space suggest • ctrl+e more options • ctrl+s submit • esc cancel"
	}
	b.WriteString("  " + dimStyle.Render(helpText))

	// Modal mode: wrap in a content-sized box and center it on screen so the
	// whole form (all fields) is visible at once, floating like a modal.
	if m.modal {
		modalBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorPrimary).
			Padding(1, 2).
			Width(m.modalBoxWidth())

		return lipgloss.NewStyle().
			Width(m.width).
			Height(m.height).
			Align(lipgloss.Center, lipgloss.Center).
			Render(modalBox.Render(b.String()))
	}

	// Full-screen mode: wrap in box using full height (subtract 2 for border)
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
		strings.TrimSpace(m.attachmentsInput.Value()) != "" ||
		strings.TrimSpace(m.baseBranchInput.Value()) != "" ||
		len(m.attachments) > 0
}

// GetDBTask returns a db.Task from the form values.
func (m *FormModel) GetDBTask() *db.Task {
	status := db.StatusBacklog
	if m.queue {
		status = db.StatusQueued
	}

	task := &db.Task{Status: status}
	m.ApplyTo(task)
	return task
}

// ApplyTo overlays the form's editable fields onto task, leaving every other
// persisted column untouched. Editing a task in place uses this so that saving
// never resets fields the form doesn't expose (session IDs, pin state, tags,
// source branch, PR info, ...). See issue #560. Permission mode IS exposed by
// the form, so it is applied here (and kept in sync with DangerousMode).
func (m *FormModel) ApplyTo(task *db.Task) {
	task.Title = m.titleInput.Value()
	task.Body = m.bodyInput.Value()
	task.Type = m.taskType
	task.Project = m.project
	task.Executor = m.executor
	task.PRURL = m.prURL
	task.PRNumber = m.prNumber

	// Effort is a Claude-specific override; only carry it for the Claude executor.
	if m.executor == db.ExecutorClaude {
		task.EffortLevel = m.effortLevel
	} else {
		task.EffortLevel = ""
	}

	// Permission mode is now an editable field on the form, so carry it onto the
	// task, keeping the legacy DangerousMode boolean consistent with it.
	task.PermissionMode = m.resolvedPermissionMode()
	task.DangerousMode = task.PermissionMode == db.PermissionModeDangerous

	// Worktree override; the base branch only applies when a worktree will be
	// created, so don't carry it for in-place tasks.
	task.WorktreeMode = db.NormalizeWorktreeMode(m.worktreeMode)
	if m.baseBranchRelevant() {
		task.BaseBranch = strings.TrimSpace(m.baseBranchInput.Value())
	} else {
		task.BaseBranch = ""
	}
}

// SetQueue sets whether to queue the task.
func (m *FormModel) SetQueue(queue bool) {
	m.queue = queue
}

// SetSize updates the form dimensions for window resize handling.
func (m *FormModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	// Update input widths. In modal mode the form floats in a narrower
	// centered box, so inputs are sized to the modal width instead of the
	// full screen width.
	inputWidth := width - 24
	if m.modal {
		// Modal content area = box width - padding(4) - label/cursor prefix(~18)
		inputWidth = m.modalBoxWidth() - 22
	}
	if inputWidth < 10 {
		inputWidth = 10
	}
	m.titleInput.Width = inputWidth
	m.bodyInput.SetWidth(inputWidth)
	m.attachmentsInput.Width = inputWidth
	m.baseBranchInput.Width = inputWidth
	// Recalculate body height based on new dimensions
	m.updateBodyHeight()
}

// modalBoxWidth returns the outer width of the floating edit modal.
// It is capped so the modal stays readable on very wide terminals and
// shrinks gracefully on narrow ones.
func (m *FormModel) modalBoxWidth() int {
	w := m.width - 8
	if w > 90 {
		w = 90
	}
	if w < 40 {
		w = 40
	}
	return w
}

// calculateBodyHeight calculates the appropriate height for the body textarea.
// The body expands to fill all available screen space after accounting for other form elements.
func (m *FormModel) calculateBodyHeight() int {
	minHeight := 8

	// Box chrome: border(2) + padding(2) + Height offset(2) = 6
	boxChrome := 6

	// Non-body content lines common to both modes:
	// header(1) + blank(1) + title(1) + blank(1) + details label(1) + help(1) = 6
	commonOverhead := 6

	var modeOverhead int
	if m.showAdvanced {
		// Advanced adds: project(1+blank) + attachments(blank+label+input+blank=4) +
		// type(1+blank) + executor(1+blank) = 10
		modeOverhead = 10
		// Attachment chips render one line each plus a help line.
		if n := len(m.attachments); n > 0 {
			modeOverhead += n + 1
		}
		// Conditional rows (Effort/Permission/Worktree/Base branch) each render
		// as a selector line plus a blank when visible.
		for _, f := range []FormField{FieldEffort, FieldPermission, FieldWorktree, FieldBaseBranch} {
			if m.isFieldVisible(f) {
				modeOverhead += 2
			}
		}
	} else {
		// Simple adds: blank + summary + blank(2) = 3
		modeOverhead = 3
	}

	totalOverhead := boxChrome + commonOverhead + modeOverhead
	availableHeight := m.height - totalOverhead

	if m.modal {
		// In modal mode the form floats centered with margin around it, so
		// size the body to its content within sensible bounds rather than
		// stretching to fill the whole screen. This keeps every field visible
		// at once while still giving long bodies a scrollable editing area.
		const modalMinBody = 6
		const modalMaxBody = 14
		lines := m.bodyInput.LineCount()
		switch {
		case lines < modalMinBody:
			availableHeight = modalMinBody
		case lines > modalMaxBody:
			availableHeight = modalMaxBody
		default:
			availableHeight = lines
		}
		// Never let the modal grow past what fits on screen. On large screens
		// the content-sized body is well under this cap, which naturally
		// leaves margin around the floating modal.
		maxFit := m.height - totalOverhead
		if maxFit < 3 {
			maxFit = 3
		}
		if availableHeight > maxFit {
			availableHeight = maxFit
		}
		return availableHeight
	}

	if availableHeight < minHeight {
		availableHeight = minHeight
	}

	return availableHeight
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
