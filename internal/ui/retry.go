package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// RetryModel handles retrying a blocked/failed task with optional feedback.
type RetryModel struct {
	task      *db.Task
	db        *db.DB
	width     int
	height    int
	submitted bool
	cancelled bool

	// Form inputs
	textarea    textarea.Model
	question    string
	attachments []string
}

// NewRetryModel creates a new retry model.
func NewRetryModel(task *db.Task, database *db.DB, width, height int) *RetryModel {
	// Get the last question for context
	question, _ := database.GetLastQuestion(task.ID)

	ta := textarea.New()
	ta.Placeholder = "Enter your response..."
	ta.Focus()
	ta.SetWidth(width - 12)
	ta.SetHeight(6)
	ta.CharLimit = 2000

	return &RetryModel{
		task:     task,
		db:       database,
		width:    width,
		height:   height,
		textarea: ta,
		question: question,
	}
}

// Init initializes the model.
func (m *RetryModel) Init() tea.Cmd {
	return textarea.Blink
}

// Update handles messages.
func (m *RetryModel) Update(msg tea.Msg) (*RetryModel, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle bracketed paste (file drag-drop)
		if msg.Paste && msg.Type == tea.KeyRunes {
			path := strings.TrimSpace(string(msg.Runes))
			path = strings.Trim(path, "\"'")
			path = strings.ReplaceAll(path, "\\", "")
			if absPath, err := filepath.Abs(path); err == nil {
				if _, statErr := os.Stat(absPath); statErr == nil {
					m.attachments = append(m.attachments, absPath)
					return m, nil
				}
			}
			// Not a file, insert as text
			m.textarea.InsertString(string(msg.Runes))
			return m, nil
		}

		switch msg.String() {
		case "esc":
			m.cancelled = true
			return m, nil
		case "ctrl+s":
			m.submitted = true
			return m, nil
		}
	}

	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// View renders the retry view.
func (m *RetryModel) View() string {
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		MarginBottom(1).
		Render("Retry Task #" + itoa(int(m.task.ID)))

	title := m.task.Title
	if len(title) > 60 {
		title = title[:57] + "..."
	}
	subtitle := Dim.Render(title)

	var content strings.Builder

	// Show question if there is one
	if m.question != "" {
		questionStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Bold(true)
		content.WriteString(questionStyle.Render("Question from agent:"))
		content.WriteString("\n")
		content.WriteString(Dim.Render(m.question))
		content.WriteString("\n\n")
	}

	// Response label
	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)
	content.WriteString(labelStyle.Render("Your response"))
	content.WriteString("\n")
	content.WriteString(m.textarea.View())
	content.WriteString("\n\n")

	// Attachments
	if len(m.attachments) > 0 {
		var fileNames []string
		for _, path := range m.attachments {
			fileNames = append(fileNames, filepath.Base(path))
		}
		content.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Render("📎 " + strings.Join(fileNames, ", ")))
		content.WriteString("\n\n")
	} else {
		content.WriteString(Dim.Render("Drag files here to attach"))
		content.WriteString("\n\n")
	}

	// Help
	helpText := "ctrl+s submit • esc cancel"
	content.WriteString(Dim.Render(helpText))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(m.width - 4)

	return box.Render(lipgloss.JoinVertical(lipgloss.Left, header, subtitle, "", content.String()))
}

// GetFeedback returns the feedback text.
func (m *RetryModel) GetFeedback() string {
	return strings.TrimSpace(m.textarea.Value())
}

// GetAttachment returns the first attachment path.
func (m *RetryModel) GetAttachment() string {
	if len(m.attachments) > 0 {
		return m.attachments[0]
	}
	return ""
}

// GetAttachments returns all attachment paths.
func (m *RetryModel) GetAttachments() []string {
	return m.attachments
}

// SetSize updates the size.
func (m *RetryModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.textarea.SetWidth(width - 12)
}

// itoa converts int to string without importing strconv
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
