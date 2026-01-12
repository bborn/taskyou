package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// FormField represents the currently focused field.
type FormField int

const (
	FieldTitle FormField = iota
	FieldBody
	FieldProject
	FieldType
	FieldSchedule
	FieldRecurrence
	FieldAttachments
)

// FormModel represents the new task form.
type FormModel struct {
	db        *db.DB
	width     int
	height    int
	submitted bool
	cancelled bool
	isEdit    bool // true when editing an existing task

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
	queue         bool
	attachments   []string // Parsed file paths
	recurrence    string   // "", "hourly", "daily", "weekly", "monthly"
	recurrenceIdx int
	recurrences   []string

	// Magic paste fields (populated when pasting URLs)
	prURL    string // GitHub PR URL if pasted
	prNumber int    // GitHub PR number if pasted
}

// NewEditFormModel creates a form model pre-populated with an existing task's data for editing.
func NewEditFormModel(database *db.DB, task *db.Task, width, height int) *FormModel {
	m := &FormModel{
		db:          database,
		width:       width,
		height:      height,
		focused:     FieldTitle,
		taskType:    task.Type,
		project:     task.Project,
		isEdit:      true,
		recurrence:  task.Recurrence,
		recurrences: []string{"", db.RecurrenceHourly, db.RecurrenceDaily, db.RecurrenceWeekly, db.RecurrenceMonthly},
		prURL:       task.PRURL,
		prNumber:    task.PRNumber,
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
	m.titleInput.Focus()
	m.titleInput.Width = width - 24
	m.titleInput.SetValue(task.Title)

	// Body textarea - pre-populate with existing body
	m.bodyInput = textarea.New()
	m.bodyInput.Placeholder = "Additional context (optional)"
	m.bodyInput.Prompt = ""
	m.bodyInput.ShowLineNumbers = false
	m.bodyInput.SetWidth(width - 24)
	m.bodyInput.SetHeight(4)
	m.bodyInput.FocusedStyle.CursorLine = lipgloss.NewStyle()
	m.bodyInput.BlurredStyle.CursorLine = lipgloss.NewStyle()
	m.bodyInput.SetValue(task.Body)

	// Schedule input - pre-populate if task has a scheduled time
	m.scheduleInput = textinput.New()
	m.scheduleInput.Placeholder = "e.g., 1h, 2h30m, tomorrow 9am"
	m.scheduleInput.Prompt = ""
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
	m.attachmentsInput.Placeholder = "Drag files here"
	m.attachmentsInput.Prompt = ""
	m.attachmentsInput.Width = width - 24

	return m
}

// NewFormModel creates a new form model.
func NewFormModel(database *db.DB, width, height int, workingDir string) *FormModel {
	m := &FormModel{
		db:          database,
		width:       width,
		height:      height,
		focused:     FieldTitle,
		recurrences: []string{"", db.RecurrenceHourly, db.RecurrenceDaily, db.RecurrenceWeekly, db.RecurrenceMonthly},
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

	// Default to 'personal' project
	m.project = "personal"
	for i, p := range m.projects {
		if p == "personal" {
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
	m.titleInput.Focus()
	m.titleInput.Width = width - 24

	// Body textarea
	m.bodyInput = textarea.New()
	m.bodyInput.Placeholder = "Additional context (optional)"
	m.bodyInput.Prompt = ""
	m.bodyInput.ShowLineNumbers = false
	m.bodyInput.SetWidth(width - 24)
	m.bodyInput.SetHeight(4)
	m.bodyInput.FocusedStyle.CursorLine = lipgloss.NewStyle()
	m.bodyInput.BlurredStyle.CursorLine = lipgloss.NewStyle()

	// Schedule input
	m.scheduleInput = textinput.New()
	m.scheduleInput.Placeholder = "e.g., 1h, 2h30m, tomorrow 9am"
	m.scheduleInput.Prompt = ""
	m.scheduleInput.Width = width - 24

	// Attachments input
	m.attachmentsInput = textinput.New()
	m.attachmentsInput.Placeholder = "Drag files here"
	m.attachmentsInput.Prompt = ""
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
			// Not a file path - check for magic paste URLs
			pastedText := string(msg.Runes)

			// Magic paste: parse URLs (GitHub PR, GitHub Issues, Linear) when pasting into title
			if m.focused == FieldTitle {
				if parsedURL := ParseURL(pastedText); parsedURL != nil {
					// Use the formatted title from the parsed URL
					m.titleInput.SetValue(parsedURL.Title)

					// Store PR info if it's a GitHub PR
					if parsedURL.Type == "github_pr" {
						m.prURL = parsedURL.PRURL
						m.prNumber = parsedURL.PRNumber
					}

					return m, nil
				}
			}

			// Not a recognized URL - treat as regular paste into focused field
			switch m.focused {
			case FieldTitle:
				m.titleInput.SetValue(m.titleInput.Value() + pastedText)
			case FieldBody:
				m.bodyInput.SetValue(m.bodyInput.Value() + pastedText)
			case FieldAttachments:
				m.attachmentsInput.SetValue(m.attachmentsInput.Value() + pastedText)
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, nil

		case "ctrl+s":
			// Submit from anywhere
			m.parseAttachments()
			m.submitted = true
			return m, nil

		case "tab":
			m.focusNext()
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
			if m.focused == FieldAttachments {
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
			if m.focused == FieldRecurrence {
				m.recurrenceIdx = (m.recurrenceIdx + 1) % len(m.recurrences)
				m.recurrence = m.recurrences[m.recurrenceIdx]
				return m, nil
			}

		default:
			// Type-to-select for selector fields
			if m.focused == FieldProject || m.focused == FieldType || m.focused == FieldRecurrence {
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
	case FieldBody:
		m.bodyInput, cmd = m.bodyInput.Update(msg)
	case FieldSchedule:
		m.scheduleInput, cmd = m.scheduleInput.Update(msg)
	case FieldAttachments:
		m.attachmentsInput, cmd = m.attachmentsInput.Update(msg)
	}

	return m, cmd
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
	m.focused = (m.focused + 1) % (FieldAttachments + 1)
	m.focusCurrent()
}

func (m *FormModel) focusPrev() {
	m.blurAll()
	m.focused = (m.focused - 1 + FieldAttachments + 1) % (FieldAttachments + 1)
	m.focusCurrent()
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

	// Title
	cursor := " "
	if m.focused == FieldTitle {
		cursor = cursorStyle.Render("▸")
	}
	b.WriteString(cursor + " " + labelStyle.Render("Title") + m.titleInput.View())
	b.WriteString("\n\n")

	// Body (textarea)
	cursor = " "
	if m.focused == FieldBody {
		cursor = cursorStyle.Render("▸")
	}
	b.WriteString(cursor + " " + labelStyle.Render("Details"))
	b.WriteString("\n")
	// Indent the textarea
	bodyLines := strings.Split(m.bodyInput.View(), "\n")
	for _, line := range bodyLines {
		b.WriteString("   " + line + "\n")
	}
	b.WriteString("\n")

	// Project selector
	cursor = " "
	if m.focused == FieldProject {
		cursor = cursorStyle.Render("▸")
	}
	b.WriteString(cursor + " " + labelStyle.Render("Project") + m.renderSelector(m.projects, m.projectIdx, m.focused == FieldProject, selectedStyle, optionStyle, dimStyle))
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

	// Attachments
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
	b.WriteString(cursor + " " + labelStyle.Render("Attachments") + attachmentLine)
	b.WriteString("\n\n")

	// Help
	helpText := "tab navigate • ←→ or type to select • ctrl+s submit • esc cancel"
	b.WriteString("  " + dimStyle.Render(helpText))

	// Wrap in box
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(m.width - 4)

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
