package ui

import (
	"github.com/bborn/workflow/internal/db"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// FormModel represents the new task form.
type FormModel struct {
	db        *db.DB
	form      *huh.Form
	width     int
	height    int
	submitted bool
	cancelled bool

	// Form values
	title    string
	body     string
	project  string
	taskType string
	priority string
	queue    bool
}

// NewFormModel creates a new form model.
func NewFormModel(database *db.DB, width, height int) *FormModel {
	m := &FormModel{
		db:     database,
		width:  width,
		height: height,
	}
	m.initForm()
	return m
}

func (m *FormModel) initForm() {
	m.form = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Key("title").
				Title("Title").
				Placeholder("What needs to be done?").
				Value(&m.title),

			huh.NewText().
				Key("body").
				Title("Details").
				Placeholder("Additional context (optional)").
				CharLimit(2000).
				Value(&m.body),
		),

		huh.NewGroup(
			huh.NewSelect[string]().
				Key("project").
				Title("Project").
				Options(m.projectOptions()...).
				Filtering(true).
				Value(&m.project),

			huh.NewSelect[string]().
				Key("type").
				Title("Type").
				Options(
					huh.NewOption("None", ""),
					huh.NewOption("Code", "code"),
					huh.NewOption("Writing", "writing"),
					huh.NewOption("Thinking", "thinking"),
				).
				Filtering(true).
				Value(&m.taskType),

			huh.NewSelect[string]().
				Key("priority").
				Title("Priority").
				Options(
					huh.NewOption("Normal", "normal"),
					huh.NewOption("High", "high"),
					huh.NewOption("Low", "low"),
				).
				Filtering(true).
				Value(&m.priority),
		),

		huh.NewGroup(
			huh.NewConfirm().
				Key("queue").
				Title("Queue for execution?").
				Description("Start processing immediately").
				Affirmative("Yes").
				Negative("No").
				Value(&m.queue),
		),
	).WithTheme(huh.ThemeDracula()).
		WithWidth(m.width - 4).
		WithShowHelp(true).
		WithShowErrors(true)
}

// projectOptions returns the project options from the database.
func (m *FormModel) projectOptions() []huh.Option[string] {
	options := []huh.Option[string]{
		huh.NewOption("None", ""),
	}

	if m.db == nil {
		return options
	}

	projects, err := m.db.ListProjects()
	if err != nil {
		return options
	}

	for _, p := range projects {
		options = append(options, huh.NewOption(p.Name, p.Name))
	}

	return options
}

// Init initializes the form.
func (m *FormModel) Init() tea.Cmd {
	return m.form.Init()
}

// Update handles messages.
func (m *FormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	form, cmd := m.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.form = f
	}

	if m.form.State == huh.StateCompleted {
		m.submitted = true
	}

	return m, cmd
}

// View renders the form.
func (m *FormModel) View() string {
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		MarginBottom(1).
		Render("New Task")

	formView := m.form.View()

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(m.width - 4)

	return box.Render(lipgloss.JoinVertical(lipgloss.Left, header, formView))
}

// GetDBTask returns a db.Task from the form values.
func (m *FormModel) GetDBTask() *db.Task {
	status := db.StatusBacklog
	if m.queue {
		status = db.StatusInProgress
	}

	return &db.Task{
		Title:    m.title,
		Body:     m.body,
		Status:   status,
		Type:     m.taskType,
		Project:  m.project,
		Priority: m.priority,
	}
}
