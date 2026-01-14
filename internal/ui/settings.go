package ui

import (
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// SettingsModel represents the settings view.
type SettingsModel struct {
	db     *db.DB
	width  int
	height int

	// Section focus: 0=theme, 1=projects, 2=task types
	section int

	// Theme selection
	themes        []string
	selectedTheme int

	// Projects
	projects        []*db.Project
	selectedProject int

	// Project form
	editingProject    bool
	editProject       *db.Project
	nameInput         textinput.Model
	aliasInput        textinput.Model
	instructionsInput textarea.Model
	projectFormFocus  int // 0=name, 1=path (browser), 2=aliases, 3=instructions

	// Task Types
	taskTypes        []*db.TaskType
	selectedTaskType int

	// Task Type form
	editingTaskType       bool
	editTaskType          *db.TaskType
	typeNameInput         textinput.Model
	typeLabelInput        textinput.Model
	typeInstructionsInput textarea.Model
	typeFormFocus         int // 0=name, 1=label, 2=instructions

	// File browser for path selection
	browsing    bool
	browsingFor string // "path" or "dir"
	fileBrowser *FileBrowserModel

	// Settings
	projectsDir string

	// Delete project confirmation
	confirmingDeleteProject bool
	pendingDeleteProject    *db.Project
	deleteProjectConfirm    *huh.Form
	deleteProjectValue      bool

	err error
}

// NewSettingsModel creates a new settings model.
func NewSettingsModel(database *db.DB, width, height int) *SettingsModel {
	// Project form inputs
	nameInput := textinput.New()
	nameInput.Placeholder = "Project name"
	nameInput.CharLimit = 50

	aliasInput := textinput.New()
	aliasInput.Placeholder = "alias1, alias2"
	aliasInput.CharLimit = 100

	instructionsInput := textarea.New()
	instructionsInput.Placeholder = "Project-specific instructions for AI..."
	instructionsInput.SetWidth(width - 20)
	instructionsInput.SetHeight(5)

	// Task type form inputs
	typeNameInput := textinput.New()
	typeNameInput.Placeholder = "type-name (lowercase, no spaces)"
	typeNameInput.CharLimit = 30

	typeLabelInput := textinput.New()
	typeLabelInput.Placeholder = "Display Label"
	typeLabelInput.CharLimit = 50

	typeInstructionsInput := textarea.New()
	typeInstructionsInput.Placeholder = "Prompt template for this task type...\nUse {{title}}, {{body}}, {{project}}, {{project_instructions}}, {{memories}}, {{attachments}}, {{history}}"
	typeInstructionsInput.SetWidth(width - 20)
	typeInstructionsInput.SetHeight(10)

	// Get available themes and current selection
	themes := ListThemes()
	currentThemeName := CurrentTheme().Name
	selectedTheme := 0
	for i, t := range themes {
		if t == currentThemeName {
			selectedTheme = i
			break
		}
	}

	m := &SettingsModel{
		db:                    database,
		width:                 width,
		height:                height,
		themes:                themes,
		selectedTheme:         selectedTheme,
		nameInput:             nameInput,
		aliasInput:            aliasInput,
		instructionsInput:     instructionsInput,
		typeNameInput:         typeNameInput,
		typeLabelInput:        typeLabelInput,
		typeInstructionsInput: typeInstructionsInput,
	}
	m.loadSettings()
	return m
}

func (m *SettingsModel) loadSettings() {
	projects, err := m.db.ListProjects()
	if err != nil {
		m.err = err
		return
	}
	m.projects = projects

	taskTypes, err := m.db.ListTaskTypes()
	if err != nil {
		m.err = err
		return
	}
	m.taskTypes = taskTypes

	dir, _ := m.db.GetSetting("projects_dir")
	m.projectsDir = dir
	if m.projectsDir == "" {
		m.projectsDir = "~/Projects"
	}
}

// Init initializes the model.
func (m *SettingsModel) Init() tea.Cmd {
	return nil
}

// Update handles messages.
func (m *SettingsModel) Update(msg tea.Msg) (*SettingsModel, tea.Cmd) {
	// Handle delete project confirmation
	if m.confirmingDeleteProject && m.deleteProjectConfirm != nil {
		return m.updateDeleteProjectConfirm(msg)
	}

	// Handle file browser mode
	if m.browsing && m.fileBrowser != nil {
		return m.updateBrowser(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle editing mode
		if m.editingProject {
			return m.updateProjectForm(msg)
		}
		if m.editingTaskType {
			return m.updateTaskTypeForm(msg)
		}

		switch msg.String() {
		case "tab":
			// Switch between sections (0=theme, 1=projects, 2=task types)
			m.section = (m.section + 1) % 3
			return m, nil
		case "shift+tab":
			m.section = (m.section + 2) % 3
			return m, nil
		case "up", "k":
			switch m.section {
			case 0: // Theme section
				if m.selectedTheme > 0 {
					m.selectedTheme--
					m.applyTheme()
				}
			case 1: // Projects section
				if m.selectedProject > 0 {
					m.selectedProject--
				}
			case 2: // Task types section
				if m.selectedTaskType > 0 {
					m.selectedTaskType--
				}
			}
		case "down", "j":
			switch m.section {
			case 0: // Theme section
				if m.selectedTheme < len(m.themes)-1 {
					m.selectedTheme++
					m.applyTheme()
				}
			case 1: // Projects section
				if m.selectedProject < len(m.projects)-1 {
					m.selectedProject++
				}
			case 2: // Task types section
				if m.selectedTaskType < len(m.taskTypes)-1 {
					m.selectedTaskType++
				}
			}
		case "left", "h":
			if m.section == 0 && m.selectedTheme > 0 {
				m.selectedTheme--
				m.applyTheme()
			}
		case "right", "l":
			if m.section == 0 && m.selectedTheme < len(m.themes)-1 {
				m.selectedTheme++
				m.applyTheme()
			}
		case "n":
			// New item (projects or task types section)
			if m.section == 1 {
				m.editingProject = true
				m.editProject = &db.Project{}
				m.nameInput.SetValue("")
				m.aliasInput.SetValue("")
				m.instructionsInput.SetValue("")
				m.nameInput.Focus()
				m.projectFormFocus = 0
				return m, textinput.Blink
			} else if m.section == 2 {
				m.editingTaskType = true
				m.editTaskType = &db.TaskType{}
				m.typeNameInput.SetValue("")
				m.typeLabelInput.SetValue("")
				m.typeInstructionsInput.SetValue("")
				m.typeNameInput.Focus()
				m.typeFormFocus = 0
				return m, textinput.Blink
			}
		case "e":
			// Edit selected item
			if m.section == 1 && len(m.projects) > 0 && m.selectedProject < len(m.projects) {
				m.editingProject = true
				m.editProject = m.projects[m.selectedProject]
				m.nameInput.SetValue(m.editProject.Name)
				m.aliasInput.SetValue(m.editProject.Aliases)
				m.instructionsInput.SetValue(m.editProject.Instructions)
				m.nameInput.Focus()
				m.projectFormFocus = 0
				return m, textinput.Blink
			} else if m.section == 2 && len(m.taskTypes) > 0 && m.selectedTaskType < len(m.taskTypes) {
				m.editingTaskType = true
				m.editTaskType = m.taskTypes[m.selectedTaskType]
				m.typeNameInput.SetValue(m.editTaskType.Name)
				m.typeLabelInput.SetValue(m.editTaskType.Label)
				m.typeInstructionsInput.SetValue(m.editTaskType.Instructions)
				m.typeNameInput.Focus()
				m.typeFormFocus = 0
				return m, textinput.Blink
			}
		case "d":
			// Delete selected item
			if m.section == 1 && len(m.projects) > 0 && m.selectedProject < len(m.projects) {
				return m.showDeleteProjectConfirm(m.projects[m.selectedProject])
			} else if m.section == 2 && len(m.taskTypes) > 0 && m.selectedTaskType < len(m.taskTypes) {
				err := m.db.DeleteTaskType(m.taskTypes[m.selectedTaskType].ID)
				if err != nil {
					m.err = err
				} else {
					m.err = nil
					m.loadSettings()
					if m.selectedTaskType >= len(m.taskTypes) && m.selectedTaskType > 0 {
						m.selectedTaskType--
					}
				}
			}
		case "p":
			// Browse for projects dir
			m.browsing = true
			m.browsingFor = "dir"
			m.fileBrowser = NewFileBrowserModel(m.projectsDir, m.width, m.height)
			return m, nil
		}
	}

	return m, nil
}

// applyTheme sets the selected theme and persists it.
func (m *SettingsModel) applyTheme() {
	if m.selectedTheme < len(m.themes) {
		themeName := m.themes[m.selectedTheme]
		if err := SetTheme(themeName); err == nil {
			m.db.SetSetting("theme", themeName)
		}
	}
}

func (m *SettingsModel) updateBrowser(msg tea.Msg) (*SettingsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.browsing = false
			m.fileBrowser = nil
			return m, nil
		case " ":
			// Select current directory
			path := m.fileBrowser.CurrentDir()
			m.browsing = false
			m.fileBrowser = nil

			if m.browsingFor == "dir" {
				m.db.SetSetting("projects_dir", path)
				m.projectsDir = path
			} else if m.browsingFor == "path" && m.editProject != nil {
				m.editProject.Path = path
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.fileBrowser, cmd = m.fileBrowser.Update(msg)
	return m, cmd
}

func (m *SettingsModel) updateProjectForm(msg tea.KeyMsg) (*SettingsModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editingProject = false
		m.editProject = nil
		return m, nil
	case "tab":
		// Tab moves forward through fields
		m.projectFormFocus = (m.projectFormFocus + 1) % 4
		m.updateProjectFormFocus()
		if m.projectFormFocus == 1 {
			// Open file browser for path
			startPath := m.projectsDir
			if m.editProject != nil && m.editProject.Path != "" {
				startPath = m.editProject.Path
			}
			m.browsing = true
			m.browsingFor = "path"
			m.fileBrowser = NewFileBrowserModel(startPath, m.width, m.height)
			return m, nil
		}
		if m.projectFormFocus == 3 {
			return m, textarea.Blink
		}
		return m, nil
	case "shift+tab":
		m.projectFormFocus = (m.projectFormFocus + 3) % 4
		m.updateProjectFormFocus()
		if m.projectFormFocus == 1 {
			// Open file browser for path
			startPath := m.projectsDir
			if m.editProject != nil && m.editProject.Path != "" {
				startPath = m.editProject.Path
			}
			m.browsing = true
			m.browsingFor = "path"
			m.fileBrowser = NewFileBrowserModel(startPath, m.width, m.height)
			return m, nil
		}
		if m.projectFormFocus == 3 {
			return m, textarea.Blink
		}
		return m, nil
	case "enter":
		if m.projectFormFocus == 1 {
			// Open file browser
			startPath := m.projectsDir
			if m.editProject != nil && m.editProject.Path != "" {
				startPath = m.editProject.Path
			}
			m.browsing = true
			m.browsingFor = "path"
			m.fileBrowser = NewFileBrowserModel(startPath, m.width, m.height)
			return m, nil
		}
		if m.projectFormFocus == 3 {
			// In instructions textarea, enter inserts newline
			var cmd tea.Cmd
			m.instructionsInput, cmd = m.instructionsInput.Update(msg)
			return m, cmd
		}
		m.projectFormFocus = (m.projectFormFocus + 1) % 4
		m.updateProjectFormFocus()
		if m.projectFormFocus == 1 {
			// Open file browser for path
			startPath := m.projectsDir
			if m.editProject != nil && m.editProject.Path != "" {
				startPath = m.editProject.Path
			}
			m.browsing = true
			m.browsingFor = "path"
			m.fileBrowser = NewFileBrowserModel(startPath, m.width, m.height)
			return m, nil
		}
		if m.projectFormFocus == 3 {
			return m, textarea.Blink
		}
		return m, nil
	case "ctrl+s":
		return m.saveProject()
	}

	// Update focused input
	var cmd tea.Cmd
	switch m.projectFormFocus {
	case 0:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case 2:
		m.aliasInput, cmd = m.aliasInput.Update(msg)
	case 3:
		m.instructionsInput, cmd = m.instructionsInput.Update(msg)
	}
	return m, cmd
}

func (m *SettingsModel) updateProjectFormFocus() {
	m.nameInput.Blur()
	m.aliasInput.Blur()
	m.instructionsInput.Blur()
	switch m.projectFormFocus {
	case 0:
		m.nameInput.Focus()
	case 2:
		m.aliasInput.Focus()
	case 3:
		m.instructionsInput.Focus()
	}
}

func (m *SettingsModel) updateTaskTypeForm(msg tea.KeyMsg) (*SettingsModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editingTaskType = false
		m.editTaskType = nil
		return m, nil
	case "tab":
		// Tab moves forward through fields
		m.typeFormFocus = (m.typeFormFocus + 1) % 3
		m.updateTaskTypeFormFocus()
		if m.typeFormFocus == 2 {
			return m, textarea.Blink
		}
		return m, nil
	case "shift+tab":
		m.typeFormFocus = (m.typeFormFocus + 2) % 3
		m.updateTaskTypeFormFocus()
		if m.typeFormFocus == 2 {
			return m, textarea.Blink
		}
		return m, nil
	case "enter":
		if m.typeFormFocus == 2 {
			// In instructions textarea, enter inserts newline
			var cmd tea.Cmd
			m.typeInstructionsInput, cmd = m.typeInstructionsInput.Update(msg)
			return m, cmd
		}
		m.typeFormFocus = (m.typeFormFocus + 1) % 3
		m.updateTaskTypeFormFocus()
		if m.typeFormFocus == 2 {
			return m, textarea.Blink
		}
		return m, nil
	case "ctrl+s":
		return m.saveTaskType()
	}

	// Update focused input
	var cmd tea.Cmd
	switch m.typeFormFocus {
	case 0:
		m.typeNameInput, cmd = m.typeNameInput.Update(msg)
	case 1:
		m.typeLabelInput, cmd = m.typeLabelInput.Update(msg)
	case 2:
		m.typeInstructionsInput, cmd = m.typeInstructionsInput.Update(msg)
	}
	return m, cmd
}

func (m *SettingsModel) updateTaskTypeFormFocus() {
	m.typeNameInput.Blur()
	m.typeLabelInput.Blur()
	m.typeInstructionsInput.Blur()
	switch m.typeFormFocus {
	case 0:
		m.typeNameInput.Focus()
	case 1:
		m.typeLabelInput.Focus()
	case 2:
		m.typeInstructionsInput.Focus()
	}
}

func (m *SettingsModel) saveProject() (*SettingsModel, tea.Cmd) {
	name := strings.TrimSpace(m.nameInput.Value())
	aliases := strings.TrimSpace(m.aliasInput.Value())
	instructions := strings.TrimSpace(m.instructionsInput.Value())

	if name == "" {
		m.err = fmt.Errorf("name is required")
		return m, nil
	}
	if m.editProject.Path == "" {
		m.err = fmt.Errorf("path is required - press Tab to browse")
		return m, nil
	}

	m.editProject.Name = name
	m.editProject.Aliases = aliases
	m.editProject.Instructions = instructions

	var err error
	if m.editProject.ID == 0 {
		err = m.db.CreateProject(m.editProject)
	} else {
		err = m.db.UpdateProject(m.editProject)
	}

	if err != nil {
		m.err = err
		return m, nil
	}

	// Update the project color cache
	if m.editProject.Color != "" {
		SetProjectColor(m.editProject.Name, m.editProject.Color)
	}

	m.editingProject = false
	m.editProject = nil
	m.err = nil
	m.loadSettings()
	return m, nil
}

func (m *SettingsModel) saveTaskType() (*SettingsModel, tea.Cmd) {
	name := strings.TrimSpace(m.typeNameInput.Value())
	label := strings.TrimSpace(m.typeLabelInput.Value())
	instructions := strings.TrimSpace(m.typeInstructionsInput.Value())

	if name == "" {
		m.err = fmt.Errorf("name is required")
		return m, nil
	}
	if label == "" {
		m.err = fmt.Errorf("label is required")
		return m, nil
	}

	m.editTaskType.Name = name
	m.editTaskType.Label = label
	m.editTaskType.Instructions = instructions

	var err error
	if m.editTaskType.ID == 0 {
		err = m.db.CreateTaskType(m.editTaskType)
	} else {
		err = m.db.UpdateTaskType(m.editTaskType)
	}

	if err != nil {
		m.err = err
		return m, nil
	}

	m.editingTaskType = false
	m.editTaskType = nil
	m.err = nil
	m.loadSettings()
	return m, nil
}

// showDeleteProjectConfirm shows the delete project confirmation dialog.
func (m *SettingsModel) showDeleteProjectConfirm(project *db.Project) (*SettingsModel, tea.Cmd) {
	// Don't allow deleting the personal project
	if project.Name == "personal" {
		m.err = fmt.Errorf("cannot delete the personal project")
		return m, nil
	}

	m.pendingDeleteProject = project
	m.deleteProjectValue = false
	m.confirmingDeleteProject = true

	// Count associated tasks and memories
	taskCount, _ := m.db.CountTasksByProject(project.Name)
	memoryCount, _ := m.db.CountMemoriesByProject(project.Name)

	// Build description with warning about what will happen
	var description strings.Builder
	description.WriteString("This will permanently delete the project configuration.\n")
	if taskCount > 0 || memoryCount > 0 {
		description.WriteString("\n")
		if taskCount > 0 {
			description.WriteString(fmt.Sprintf("• %d task(s) will become orphaned\n", taskCount))
		}
		if memoryCount > 0 {
			description.WriteString(fmt.Sprintf("• %d memory(ies) will become orphaned\n", memoryCount))
		}
		description.WriteString("\nOrphaned items will still exist but won't be associated with any project.")
	}

	modalWidth := min(60, m.width-8)
	m.deleteProjectConfirm = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Key("delete").
				Title(fmt.Sprintf("Delete project \"%s\"?", project.Name)).
				Description(description.String()).
				Affirmative("Delete").
				Negative("Cancel").
				Value(&m.deleteProjectValue),
		),
	).WithTheme(huh.ThemeDracula()).
		WithWidth(modalWidth - 6).
		WithShowHelp(true)

	return m, m.deleteProjectConfirm.Init()
}

// updateDeleteProjectConfirm handles the delete project confirmation dialog.
func (m *SettingsModel) updateDeleteProjectConfirm(msg tea.Msg) (*SettingsModel, tea.Cmd) {
	// Handle escape to cancel
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.confirmingDeleteProject = false
			m.deleteProjectConfirm = nil
			m.pendingDeleteProject = nil
			return m, nil
		}
	}

	// Update the huh form
	form, cmd := m.deleteProjectConfirm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.deleteProjectConfirm = f
	}

	// Check if form completed
	if m.deleteProjectConfirm.State == huh.StateCompleted {
		if m.pendingDeleteProject != nil && m.deleteProjectValue {
			// User confirmed - delete the project
			err := m.db.DeleteProject(m.pendingDeleteProject.ID)
			if err != nil {
				m.err = err
			} else {
				m.err = nil
				m.loadSettings()
				if m.selectedProject >= len(m.projects) && m.selectedProject > 0 {
					m.selectedProject--
				}
			}
		}
		// Clean up confirmation state
		m.confirmingDeleteProject = false
		m.deleteProjectConfirm = nil
		m.pendingDeleteProject = nil
		return m, nil
	}

	return m, cmd
}

// viewDeleteProjectConfirm renders the delete project confirmation dialog.
func (m *SettingsModel) viewDeleteProjectConfirm() string {
	if m.deleteProjectConfirm == nil {
		return ""
	}

	// Modal header with warning icon
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorError).
		MarginBottom(1).
		Render("⚠ Confirm Delete Project")

	formView := m.deleteProjectConfirm.View()

	// Modal box with border
	modalWidth := min(60, m.width-8)
	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorError).
		Padding(1, 2).
		Width(modalWidth)

	modalContent := modalBox.Render(lipgloss.JoinVertical(lipgloss.Center, header, formView))

	// Center the modal on screen
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalContent)
}

// View renders the settings view.
func (m *SettingsModel) View() string {
	// Show delete project confirmation if active
	if m.confirmingDeleteProject && m.deleteProjectConfirm != nil {
		return m.viewDeleteProjectConfirm()
	}

	// Show file browser if active
	if m.browsing && m.fileBrowser != nil {
		return m.fileBrowser.View()
	}

	var b strings.Builder

	// Header
	header := Bold.Render("Settings")
	b.WriteString(lipgloss.NewStyle().Padding(1, 2).Render(header))
	b.WriteString("\n")

	// Theme section
	var themeHeader string
	if m.section == 0 {
		themeHeader = Bold.Foreground(ColorPrimary).Render("Theme")
	} else {
		themeHeader = Bold.Render("Theme")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(themeHeader))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(m.renderThemeSelector()))
	b.WriteString("\n\n")

	// Projects directory
	dirLabel := Dim.Render("Projects Directory: ")
	dirValue := m.projectsDir + Dim.Render("  [p to browse]")
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(dirLabel + dirValue))
	b.WriteString("\n\n")

	// Projects section
	var projectsHeader string
	if m.section == 1 {
		projectsHeader = Bold.Foreground(ColorPrimary).Render("Projects")
	} else {
		projectsHeader = Bold.Render("Projects")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(projectsHeader))
	b.WriteString("\n")

	if m.editingProject {
		// Show project form
		b.WriteString(m.renderProjectForm())
	} else {
		// Show project list
		if len(m.projects) == 0 {
			b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(Dim.Render("No projects configured. Press 'n' to add one.")))
		} else {
			for i, p := range m.projects {
				prefix := "  "
				style := lipgloss.NewStyle()
				if m.section == 1 && i == m.selectedProject {
					prefix = "> "
					style = style.Foreground(ColorPrimary)
				}

				line := fmt.Sprintf("%s%s", prefix, p.Name)
				line += Dim.Render(fmt.Sprintf(" → %s", p.Path))
				if p.Aliases != "" {
					line += Dim.Render(fmt.Sprintf(" (%s)", p.Aliases))
				}
				b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(style.Render(line)))
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("\n")

	// Task Types section
	var taskTypesHeader string
	if m.section == 2 {
		taskTypesHeader = Bold.Foreground(ColorPrimary).Render("Task Types")
	} else {
		taskTypesHeader = Bold.Render("Task Types")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(taskTypesHeader))
	b.WriteString("\n")

	if m.editingTaskType {
		// Show task type form
		b.WriteString(m.renderTaskTypeForm())
	} else {
		// Show task type list
		if len(m.taskTypes) == 0 {
			b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(Dim.Render("No task types configured.")))
		} else {
			for i, t := range m.taskTypes {
				prefix := "  "
				style := lipgloss.NewStyle()
				if m.section == 2 && i == m.selectedTaskType {
					prefix = "> "
					style = style.Foreground(ColorPrimary)
				}

				line := fmt.Sprintf("%s%s", prefix, t.Label)
				line += Dim.Render(fmt.Sprintf(" (%s)", t.Name))
				if t.IsBuiltin {
					line += Dim.Render(" [builtin]")
				}
				b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(style.Render(line)))
				b.WriteString("\n")
			}
		}
	}

	// Error
	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(Error.Render(m.err.Error())))
	}

	// Help
	b.WriteString("\n")
	b.WriteString(m.renderHelp())

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Width(m.width - 2).
		Height(m.height - 2).
		Padding(0)

	return box.Render(b.String())
}

// renderThemeSelector renders the horizontal theme picker.
func (m *SettingsModel) renderThemeSelector() string {
	var parts []string
	for i, theme := range m.themes {
		style := lipgloss.NewStyle().Padding(0, 1)
		if i == m.selectedTheme {
			// Selected theme - show with theme's primary color as background
			t := BuiltinThemes[theme]
			style = style.
				Background(lipgloss.Color(t.Primary)).
				Foreground(lipgloss.Color("#000000")).
				Bold(true)
		} else {
			style = style.Foreground(ColorMuted)
		}
		parts = append(parts, style.Render(theme))
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, parts...)
}

func (m *SettingsModel) renderProjectForm() string {
	var b strings.Builder

	title := "New Project"
	if m.editProject.ID != 0 {
		title = "Edit Project"
	}
	b.WriteString(lipgloss.NewStyle().Padding(1, 2).Render(Bold.Render(title)))
	b.WriteString("\n")

	// Name field
	nameLabel := Dim.Render("Name:         ")
	if m.projectFormFocus == 0 {
		nameLabel = Bold.Render("Name:         ")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(nameLabel + m.nameInput.View()))
	b.WriteString("\n")

	// Path field (shows current path or prompt to browse)
	pathLabel := Dim.Render("Path:         ")
	if m.projectFormFocus == 1 {
		pathLabel = Bold.Render("Path:         ")
	}
	pathValue := Dim.Render(fmt.Sprintf("[optional - defaults to %s/<name>]", m.projectsDir))
	if m.editProject != nil && m.editProject.Path != "" {
		pathValue = m.editProject.Path
	}
	pathLine := pathLabel + pathValue
	if m.projectFormFocus == 1 {
		pathLine = lipgloss.NewStyle().Foreground(ColorPrimary).Render(pathLine)
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(pathLine))
	b.WriteString("\n")

	// Aliases field
	aliasLabel := Dim.Render("Aliases:      ")
	if m.projectFormFocus == 2 {
		aliasLabel = Bold.Render("Aliases:      ")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(aliasLabel + m.aliasInput.View()))
	b.WriteString("\n\n")

	// Instructions field
	instructionsLabel := Dim.Render("Instructions: ")
	if m.projectFormFocus == 3 {
		instructionsLabel = Bold.Render("Instructions: ")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(instructionsLabel))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(m.instructionsInput.View()))
	b.WriteString("\n")

	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(Dim.Render("Tab: next field • Ctrl+S: save • Esc: cancel")))

	return b.String()
}

func (m *SettingsModel) renderTaskTypeForm() string {
	var b strings.Builder

	title := "New Task Type"
	if m.editTaskType.ID != 0 {
		title = "Edit Task Type"
	}
	b.WriteString(lipgloss.NewStyle().Padding(1, 2).Render(Bold.Render(title)))
	b.WriteString("\n")

	// Name field
	nameLabel := Dim.Render("Name:         ")
	if m.typeFormFocus == 0 {
		nameLabel = Bold.Render("Name:         ")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(nameLabel + m.typeNameInput.View()))
	b.WriteString("\n")

	// Label field
	labelLabel := Dim.Render("Label:        ")
	if m.typeFormFocus == 1 {
		labelLabel = Bold.Render("Label:        ")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(labelLabel + m.typeLabelInput.View()))
	b.WriteString("\n\n")

	// Instructions field
	instructionsLabel := Dim.Render("Instructions: ")
	if m.typeFormFocus == 2 {
		instructionsLabel = Bold.Render("Instructions: ")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(instructionsLabel))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(m.typeInstructionsInput.View()))
	b.WriteString("\n")

	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(Dim.Render("Placeholders: {{title}}, {{body}}, {{project}}, {{project_instructions}}, {{memories}}, {{attachments}}, {{history}}")))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(Dim.Render("Tab: next field • Ctrl+S: save • Esc: cancel")))

	return b.String()
}

func (m *SettingsModel) renderHelp() string {
	var keys []struct {
		key  string
		desc string
	}

	if m.editingProject || m.editingTaskType {
		keys = []struct {
			key  string
			desc string
		}{
			{"tab", "next"},
			{"ctrl+s", "save"},
			{"esc", "cancel"},
		}
	} else {
		keys = []struct {
			key  string
			desc string
		}{
			{"tab", "section"},
			{"←/→", "theme"},
			{"↑/↓", "navigate"},
			{"n", "new"},
			{"e", "edit"},
			{"d", "delete"},
			{"p", "projects dir"},
			{"esc", "back"},
		}
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

// SetSize updates the view size.
func (m *SettingsModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.instructionsInput.SetWidth(width - 20)
	m.typeInstructionsInput.SetWidth(width - 20)
}
