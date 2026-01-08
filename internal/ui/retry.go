package ui

import (
	"github.com/bborn/workflow/internal/db"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// RetryModel handles retrying a blocked/failed task with optional feedback.
type RetryModel struct {
	task       *db.Task
	db         *db.DB
	form       *huh.Form
	width      int
	height     int
	submitted  bool
	cancelled  bool

	// Form values
	feedback   string
	attachment string
}

// NewRetryModel creates a new retry model.
func NewRetryModel(task *db.Task, database *db.DB, width, height int) *RetryModel {
	m := &RetryModel{
		task:   task,
		db:     database,
		width:  width,
		height: height,
	}
	m.initForm()
	return m
}

func (m *RetryModel) initForm() {
	// Get the last question for context
	question, _ := m.db.GetLastQuestion(m.task.ID)

	var fields []huh.Field

	// Show question if there is one
	if question != "" {
		fields = append(fields,
			huh.NewNote().
				Title("Question from agent").
				Description(question),
		)
	}

	fields = append(fields,
		huh.NewText().
			Key("feedback").
			Title("Your response").
			Placeholder("Enter feedback or answer...").
			CharLimit(2000).
			Value(&m.feedback),

		huh.NewFilePicker().
			Key("attachment").
			Title("Attachment").
			Description("Optional file to attach").
			AllowedTypes([]string{".png", ".jpg", ".jpeg", ".gif", ".pdf", ".md", ".txt", ".json", ".go", ".py", ".rb", ".rs", ".js", ".ts"}).
			Value(&m.attachment),
	)

	m.form = huh.NewForm(
		huh.NewGroup(fields...),
	).WithTheme(huh.ThemeDracula()).
		WithWidth(m.width - 8).
		WithShowHelp(true).
		WithShowErrors(true)
}

// Init initializes the model.
func (m *RetryModel) Init() tea.Cmd {
	return m.form.Init()
}

// Update handles messages.
func (m *RetryModel) Update(msg tea.Msg) (*RetryModel, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.cancelled = true
			return m, nil
		}
	}

	form, cmd := m.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.form = f
	}

	if m.form.State == huh.StateCompleted {
		m.submitted = true
	}

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

	formView := m.form.View()

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(m.width - 4)

	return box.Render(lipgloss.JoinVertical(lipgloss.Left, header, subtitle, "", formView))
}

// GetFeedback returns the feedback text.
func (m *RetryModel) GetFeedback() string {
	return m.feedback
}

// GetAttachment returns the selected attachment path.
func (m *RetryModel) GetAttachment() string {
	return m.attachment
}

// SetSize updates the size.
func (m *RetryModel) SetSize(width, height int) {
	m.width = width
	m.height = height
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
