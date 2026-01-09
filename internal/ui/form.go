package ui

import (
	"os"
	"path/filepath"
	"strings"
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
	FieldPriority
	FieldAttachments
)

// FormModel represents the new task form.
type FormModel struct {
	db        *db.DB
	width     int
	height    int
	submitted bool
	cancelled bool

	// Current field
	focused FormField

	// Form inputs
	titleInput       textinput.Model
	bodyInput        textarea.Model
	attachmentsInput textinput.Model

	// Select values
	project      string
	projectIdx   int
	projects     []string
	taskType     string
	typeIdx      int
	types        []string
	priority     string
	priorityIdx  int
	priorities   []string
	queue        bool
	attachments  []string // Parsed file paths
}

// NewFormModel creates a new form model.
func NewFormModel(database *db.DB, width, height int, workingDir string) *FormModel {
	m := &FormModel{
		db:         database,
		width:      width,
		height:     height,
		focused:    FieldTitle,
		types:      []string{"", "code", "writing", "thinking"},
		priorities: []string{"normal", "high", "low"},
		priority:   "normal",
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
			// Not a file path - treat as regular paste into focused field
			pastedText := string(msg.Runes)
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
				return m, nil
			}
			if m.focused == FieldType {
				m.typeIdx = (m.typeIdx - 1 + len(m.types)) % len(m.types)
				m.taskType = m.types[m.typeIdx]
				return m, nil
			}
			if m.focused == FieldPriority {
				m.priorityIdx = (m.priorityIdx - 1 + len(m.priorities)) % len(m.priorities)
				m.priority = m.priorities[m.priorityIdx]
				return m, nil
			}

		case "right":
			if m.focused == FieldProject {
				m.projectIdx = (m.projectIdx + 1) % len(m.projects)
				m.project = m.projects[m.projectIdx]
				return m, nil
			}
			if m.focused == FieldType {
				m.typeIdx = (m.typeIdx + 1) % len(m.types)
				m.taskType = m.types[m.typeIdx]
				return m, nil
			}
			if m.focused == FieldPriority {
				m.priorityIdx = (m.priorityIdx + 1) % len(m.priorities)
				m.priority = m.priorities[m.priorityIdx]
				return m, nil
			}

		default:
			// Type-to-select for selector fields
			if m.focused == FieldProject || m.focused == FieldType || m.focused == FieldPriority {
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
	case FieldPriority:
		for i, p := range m.priorities {
			if strings.HasPrefix(strings.ToLower(p), prefix) {
				m.priorityIdx = i
				m.priority = p
				return
			}
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
	m.attachmentsInput.Blur()
}

func (m *FormModel) focusCurrent() {
	switch m.focused {
	case FieldTitle:
		m.titleInput.Focus()
	case FieldBody:
		m.bodyInput.Focus()
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
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		Render("New Task")
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
	typeLabels := []string{"none", "code", "writing", "thinking"}
	b.WriteString(cursor + " " + labelStyle.Render("Type") + m.renderSelector(typeLabels, m.typeIdx, m.focused == FieldType, selectedStyle, optionStyle, dimStyle))
	b.WriteString("\n\n")

	// Priority selector
	cursor = " "
	if m.focused == FieldPriority {
		cursor = cursorStyle.Render("▸")
	}
	b.WriteString(cursor + " " + labelStyle.Render("Priority") + m.renderSelector(m.priorities, m.priorityIdx, m.focused == FieldPriority, selectedStyle, optionStyle, dimStyle))
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
		attachmentLine = lipgloss.NewStyle().Foreground(ColorPrimary).Render("📎 "+strings.Join(fileNames, ", ")) + "  " + m.attachmentsInput.View()
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

	return &db.Task{
		Title:    m.titleInput.Value(),
		Body:     m.bodyInput.Value(),
		Status:   status,
		Type:     m.taskType,
		Project:  m.project,
		Priority: m.priority,
	}
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
