package ui

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bborn/workflow/internal/db"
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

	// Project form modal (two-step: 1. browse path, 2. fill details)
	editingProject             bool
	editProject                *db.Project
	projectForm                *huh.Form
	projectFormName            string
	projectFormAliases         string
	projectFormInstructions    string
	projectFormClaudeConfigDir string

	// Task Types
	taskTypes        []*db.TaskType
	selectedTaskType int

	// Task Type form modal
	editingTaskType          bool
	editTaskType             *db.TaskType
	taskTypeForm             *huh.Form
	taskTypeFormName         string
	taskTypeFormLabel        string
	taskTypeFormInstructions string

	// File browser for path selection
	browsing    bool
	fileBrowser *FileBrowserModel

	// Delete project confirmation
	confirmingDeleteProject bool
	pendingDeleteProject    *db.Project
	deleteProjectConfirm    *huh.Form
	deleteProjectValue      bool

	err error
}

// NewSettingsModel creates a new settings model.
func NewSettingsModel(database *db.DB, width, height int) *SettingsModel {
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
		db:            database,
		width:         width,
		height:        height,
		themes:        themes,
		selectedTheme: selectedTheme,
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
}

// Init initializes the model.
func (m *SettingsModel) Init() tea.Cmd {
	return nil
}

// Update handles messages.
func (m *SettingsModel) Update(msg tea.Msg) (*SettingsModel, tea.Cmd) {
	// Handle modals first (they overlay everything)
	if m.confirmingDeleteProject && m.deleteProjectConfirm != nil {
		return m.updateDeleteProjectConfirm(msg)
	}
	if m.editingProject && m.projectForm != nil {
		return m.updateProjectFormModal(msg)
	}
	if m.editingTaskType && m.taskTypeForm != nil {
		return m.updateTaskTypeFormModal(msg)
	}

	// Handle file browser mode
	if m.browsing && m.fileBrowser != nil {
		return m.updateBrowser(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
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
				// For new project, first get name, then browse for path
				return m.showProjectForm(&db.Project{})
			} else if m.section == 2 {
				return m.showTaskTypeForm(nil)
			}
		case "e":
			// Edit selected item
			if m.section == 1 && len(m.projects) > 0 && m.selectedProject < len(m.projects) {
				return m.showProjectForm(m.projects[m.selectedProject])
			} else if m.section == 2 && len(m.taskTypes) > 0 && m.selectedTaskType < len(m.taskTypes) {
				return m.showTaskTypeForm(m.taskTypes[m.selectedTaskType])
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
			m.editProject = nil // Cancel new project creation
			return m, nil
		case " ":
			// Select current directory
			path := m.fileBrowser.CurrentDir()
			m.browsing = false
			m.fileBrowser = nil

			if m.editProject != nil {
				m.editProject.Path = path
				// If project already has name (form was filled first), save it
				// Otherwise show the form (for backwards compat if needed)
				if m.editProject.Name != "" {
					return m.saveProject()
				}
				return m.showProjectForm(m.editProject)
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.fileBrowser, cmd = m.fileBrowser.Update(msg)
	return m, cmd
}

// showProjectForm creates and shows the project form modal
func (m *SettingsModel) showProjectForm(project *db.Project) (*SettingsModel, tea.Cmd) {
	m.editingProject = true
	m.editProject = project

	// Initialize form values
	m.projectFormName = project.Name
	m.projectFormAliases = project.Aliases
	m.projectFormInstructions = project.Instructions
	m.projectFormClaudeConfigDir = project.ClaudeConfigDir

	title := "New Project"
	description := "You'll choose a directory next"
	if project.ID != 0 {
		title = "Edit Project"
		description = fmt.Sprintf("Path: %s", project.Path)
	} else if project.Path != "" {
		// New project but path already selected
		description = fmt.Sprintf("Path: %s", project.Path)
	}

	modalWidth := min(70, m.width-8)
	m.projectForm = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Key("name").
				Title("Name").
				Placeholder("Project name").
				Value(&m.projectFormName),
			huh.NewInput().
				Key("aliases").
				Title("Aliases").
				Description("Comma-separated shortcuts").
				Placeholder("alias1, alias2").
				Value(&m.projectFormAliases),
			huh.NewText().
				Key("instructions").
				Title("Instructions").
				Description("Project-specific instructions for AI").
				Placeholder("Instructions...").
				CharLimit(5000).
				Value(&m.projectFormInstructions),
			huh.NewInput().
				Key("claude_config_dir").
				Title("Claude Config Directory").
				Description("Overrides CLAUDE_CONFIG_DIR for this project").
				Placeholder("~/.claude-other-account").
				Value(&m.projectFormClaudeConfigDir),
		).Title(title).Description(description),
	).WithTheme(huh.ThemeDracula()).
		WithWidth(modalWidth - 6).
		WithShowHelp(true)

	return m, m.projectForm.Init()
}

// updateProjectFormModal handles updates to the project form modal
func (m *SettingsModel) updateProjectFormModal(msg tea.Msg) (*SettingsModel, tea.Cmd) {
	// Handle escape to cancel
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.editingProject = false
			m.projectForm = nil
			m.editProject = nil
			return m, nil
		}
	}

	// Update the huh form
	form, cmd := m.projectForm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.projectForm = f
	}

	// Check if form completed
	if m.projectForm.State == huh.StateCompleted {
		return m.saveProject()
	}

	return m, cmd
}

// showTaskTypeForm creates and shows the task type form modal
func (m *SettingsModel) showTaskTypeForm(taskType *db.TaskType) (*SettingsModel, tea.Cmd) {
	m.editingTaskType = true
	if taskType == nil {
		m.editTaskType = &db.TaskType{}
		m.taskTypeFormName = ""
		m.taskTypeFormLabel = ""
		m.taskTypeFormInstructions = ""
	} else {
		m.editTaskType = taskType
		m.taskTypeFormName = taskType.Name
		m.taskTypeFormLabel = taskType.Label
		m.taskTypeFormInstructions = taskType.Instructions
	}

	title := "New Task Type"
	if m.editTaskType.ID != 0 {
		title = "Edit Task Type"
	}

	modalWidth := min(80, m.width-8)
	m.taskTypeForm = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Key("name").
				Title("Name").
				Description("Lowercase, no spaces").
				Placeholder("task-type-name").
				Value(&m.taskTypeFormName),
			huh.NewInput().
				Key("label").
				Title("Label").
				Placeholder("Display Label").
				Value(&m.taskTypeFormLabel),
			huh.NewText().
				Key("instructions").
				Title("Prompt Template").
				Description("Use {{title}}, {{body}}, {{project}}, {{project_instructions}}, {{memories}}, {{attachments}}, {{history}}").
				Placeholder("Instructions...").
				CharLimit(10000).
				Value(&m.taskTypeFormInstructions),
		).Title(title),
	).WithTheme(huh.ThemeDracula()).
		WithWidth(modalWidth - 6).
		WithShowHelp(true)

	return m, m.taskTypeForm.Init()
}

// updateTaskTypeFormModal handles updates to the task type form modal
func (m *SettingsModel) updateTaskTypeFormModal(msg tea.Msg) (*SettingsModel, tea.Cmd) {
	// Handle escape to cancel
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "esc" {
			m.editingTaskType = false
			m.taskTypeForm = nil
			m.editTaskType = nil
			return m, nil
		}
	}

	// Update the huh form
	form, cmd := m.taskTypeForm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.taskTypeForm = f
	}

	// Check if form completed
	if m.taskTypeForm.State == huh.StateCompleted {
		return m.saveTaskType()
	}

	return m, cmd
}

// isGitRepo checks if a directory contains a .git folder
func isGitRepo(path string) bool {
	gitDir := filepath.Join(path, ".git")
	info, err := os.Stat(gitDir)
	return err == nil && info.IsDir()
}

// findNestedGitRepos searches for .git directories inside the given path (excluding root)
func findNestedGitRepos(rootPath string) []string {
	var nested []string
	filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		// Skip common dependency/build directories
		if d.Name() == "node_modules" || d.Name() == "vendor" || d.Name() == ".task-worktrees" {
			return filepath.SkipDir
		}
		// Check for nested .git (not the root one)
		if d.Name() == ".git" && filepath.Dir(path) != rootPath {
			nested = append(nested, filepath.Dir(path))
			return filepath.SkipDir
		}
		return nil
	})
	return nested
}

// initGitRepo initializes a git repo with an initial commit
func initGitRepo(path string) error {
	// Create directory if needed
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	cmd := exec.Command("git", "init")
	cmd.Dir = path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init: %v\n%s", err, output)
	}

	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "Initial commit")
	cmd.Dir = path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %v\n%s", err, output)
	}

	return nil
}

func (m *SettingsModel) saveProject() (*SettingsModel, tea.Cmd) {
	// Get values from form if available, otherwise use values already stored in editProject
	// (which happens when coming from browser after form was already filled)
	name := strings.TrimSpace(m.projectFormName)
	aliases := strings.TrimSpace(m.projectFormAliases)
	instructions := strings.TrimSpace(m.projectFormInstructions)
	configDir := strings.TrimSpace(m.projectFormClaudeConfigDir)

	// If form values are empty but editProject has values, use those
	if name == "" && m.editProject.Name != "" {
		name = m.editProject.Name
		aliases = m.editProject.Aliases
		instructions = m.editProject.Instructions
		configDir = m.editProject.ClaudeConfigDir
	}

	if name == "" {
		m.err = fmt.Errorf("name is required")
		return m, nil
	}

	// For new projects without a path, open file browser
	if m.editProject.ID == 0 && m.editProject.Path == "" {
		m.editProject.Name = name
		m.editProject.Aliases = aliases
		m.editProject.Instructions = instructions
		m.editProject.ClaudeConfigDir = configDir
		m.editingProject = false
		m.projectForm = nil
		m.browsing = true
		home, _ := os.UserHomeDir()
		m.fileBrowser = NewFileBrowserModel(home, m.width, m.height)
		return m, nil
	}

	path := m.editProject.Path

	// Check if path exists
	info, err := os.Stat(path)
	pathExists := err == nil

	if pathExists {
		if !info.IsDir() {
			m.err = fmt.Errorf("path is not a directory")
			return m, nil
		}

		if isGitRepo(path) {
			// Existing git repo - check for nested repos
			nested := findNestedGitRepos(path)
			if len(nested) > 0 {
				m.err = fmt.Errorf("directory contains %d nested git repo(s) - select a single repo instead", len(nested))
				return m, nil
			}
			// Valid existing git repo - proceed
		} else {
			// Not a git repo - initialize it
			if err := initGitRepo(path); err != nil {
				m.err = fmt.Errorf("failed to initialize git: %w", err)
				return m, nil
			}
		}
	} else {
		// Path doesn't exist - create and initialize
		if err := initGitRepo(path); err != nil {
			m.err = fmt.Errorf("failed to create project: %w", err)
			return m, nil
		}
	}

	m.editProject.Name = name
	m.editProject.Aliases = aliases
	m.editProject.Instructions = instructions
	m.editProject.ClaudeConfigDir = configDir

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
	m.projectForm = nil
	m.editProject = nil
	m.err = nil
	m.loadSettings()
	return m, nil
}

func (m *SettingsModel) saveTaskType() (*SettingsModel, tea.Cmd) {
	name := strings.TrimSpace(m.taskTypeFormName)
	label := strings.TrimSpace(m.taskTypeFormLabel)
	instructions := strings.TrimSpace(m.taskTypeFormInstructions)

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
	m.taskTypeForm = nil
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
		Render("Confirm Delete Project")

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

// viewProjectFormModal renders the project form as a centered modal.
func (m *SettingsModel) viewProjectFormModal() string {
	if m.projectForm == nil {
		return ""
	}

	formView := m.projectForm.View()

	// Modal box with border
	modalWidth := min(70, m.width-8)
	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(modalWidth)

	modalContent := modalBox.Render(formView)

	// Center the modal on screen
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalContent)
}

// viewTaskTypeFormModal renders the task type form as a centered modal.
func (m *SettingsModel) viewTaskTypeFormModal() string {
	if m.taskTypeForm == nil {
		return ""
	}

	formView := m.taskTypeForm.View()

	// Modal box with border
	modalWidth := min(80, m.width-8)
	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(modalWidth)

	modalContent := modalBox.Render(formView)

	// Center the modal on screen
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalContent)
}

// View renders the settings view.
func (m *SettingsModel) View() string {
	// Show modals if active (these overlay the settings view)
	if m.confirmingDeleteProject && m.deleteProjectConfirm != nil {
		return m.viewDeleteProjectConfirm()
	}
	if m.editingProject && m.projectForm != nil {
		return m.viewProjectFormModal()
	}
	if m.editingTaskType && m.taskTypeForm != nil {
		return m.viewTaskTypeFormModal()
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

	// Projects section
	var projectsHeader string
	if m.section == 1 {
		projectsHeader = Bold.Foreground(ColorPrimary).Render("Projects")
	} else {
		projectsHeader = Bold.Render("Projects")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(projectsHeader))
	b.WriteString("\n")

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
			if strings.TrimSpace(p.ClaudeConfigDir) != "" {
				line += Dim.Render(fmt.Sprintf(" [claude: %s]", p.ClaudeConfigDir))
			}
			b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(style.Render(line)))
			b.WriteString("\n")
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
			{"tab", "next field"},
			{"enter", "submit"},
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
}
