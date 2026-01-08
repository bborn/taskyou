package ui

import (
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// RetryModel handles retrying a blocked/failed task with optional feedback.
type RetryModel struct {
	task     *db.Task
	question string // The question asked by the agent (if any)
	textarea textarea.Model
	width    int
	height   int

	submitted   bool
	cancelled   bool
	buttonFocus bool // true when submit button is focused
}

// NewRetryModel creates a new retry model.
func NewRetryModel(task *db.Task, database *db.DB, width, height int) *RetryModel {
	ta := textarea.New()
	ta.Placeholder = "Enter your response..."
	ta.SetWidth(width - 10)
	ta.SetHeight(5)
	ta.Focus()

	// Try to get the last question for this task
	question, _ := database.GetLastQuestion(task.ID)

	return &RetryModel{
		task:     task,
		question: question,
		textarea: ta,
		width:    width,
		height:   height,
	}
}

// Init initializes the model.
func (m *RetryModel) Init() tea.Cmd {
	return textarea.Blink
}

// Update handles messages.
func (m *RetryModel) Update(msg tea.Msg) (*RetryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.cancelled = true
			return m, nil
		case "ctrl+s":
			m.submitted = true
			return m, nil
		case "tab", "shift+tab":
			m.buttonFocus = !m.buttonFocus
			if m.buttonFocus {
				m.textarea.Blur()
			} else {
				m.textarea.Focus()
			}
			return m, nil
		case "enter":
			if m.buttonFocus {
				m.submitted = true
				return m, nil
			}
		}
	}

	if !m.buttonFocus {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd
	}
	return m, nil
}

// View renders the retry view.
func (m *RetryModel) View() string {
	var b strings.Builder

	header := Bold.Render(fmt.Sprintf("Retry Task #%d", m.task.ID))
	b.WriteString(lipgloss.NewStyle().Padding(1, 2).Render(header))
	b.WriteString("\n")

	title := m.task.Title
	if len(title) > 60 {
		title = title[:57] + "..."
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(Dim.Render(title)))
	b.WriteString("\n\n")

	// Show the question if there is one
	if m.question != "" {
		questionStyle := lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(ColorPrimary)
		b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(Bold.Render("❓ Question from agent:")))
		b.WriteString("\n")
		b.WriteString(questionStyle.Render(m.question))
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render("Your answer:"))
	} else {
		b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render("Feedback for retry:"))
	}
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(m.textarea.View()))
	b.WriteString("\n\n")

	// Submit button
	buttonStyle := lipgloss.NewStyle().
		Padding(0, 2).
		MarginLeft(2)
	if m.buttonFocus {
		buttonStyle = buttonStyle.
			Background(ColorPrimary).
			Foreground(lipgloss.Color("#000"))
	} else {
		buttonStyle = buttonStyle.
			Border(lipgloss.NormalBorder()).
			BorderForeground(ColorMuted)
	}
	b.WriteString(buttonStyle.Render("[ Submit ]"))
	b.WriteString("\n\n")

	help := HelpKey.Render("Ctrl+S") + " " + HelpDesc.Render("submit") + "  "
	help += HelpKey.Render("Tab") + " " + HelpDesc.Render("switch focus") + "  "
	help += HelpKey.Render("Esc") + " " + HelpDesc.Render("cancel")
	b.WriteString(HelpBar.Render(help))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(m.width - 4)

	return box.Render(b.String())
}

// GetFeedback returns the feedback text.
func (m *RetryModel) GetFeedback() string {
	return strings.TrimSpace(m.textarea.Value())
}

// SetSize updates the size.
func (m *RetryModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.textarea.SetWidth(width - 10)
}
