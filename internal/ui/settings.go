package ui

import (
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SettingsModel represents the settings view.
type SettingsModel struct {
	db     *db.DB
	width  int
	height int

	// Section focus: 0=theme, 1=projects
	section int

	// Theme selection
	themes        []string
	selectedTheme int

	// Projects
	projects []*db.Project
	selected int

	// Project form
	editing           bool
	editProject       *db.Project
	nameInput         textinput.Model
	aliasInput        textinput.Model
	instructionsInput textarea.Model
	formFocus         int // 0=name, 1=path (browser), 2=aliases, 3=instructions

	// File browser for path selection
	browsing    bool
	browsingFor string // "path" or "dir"
	fileBrowser *FileBrowserModel

	// Settings
	projectsDir string

	err error
}

// NewSettingsModel creates a new settings model.
func NewSettingsModel(database *db.DB, width, height int) *SettingsModel {
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
		db:                database,
		width:             width,
		height:            height,
		themes:            themes,
		selectedTheme:     selectedTheme,
		nameInput:         nameInput,
		aliasInput:        aliasInput,
		instructionsInput: instructionsInput,
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
	// Handle file browser mode
	if m.browsing && m.fileBrowser != nil {
		return m.updateBrowser(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle editing mode
		if m.editing {
			return m.updateForm(msg)
		}

		switch msg.String() {
		case "tab":
			// Switch between sections
			m.section = (m.section + 1) % 2
			return m, nil
		case "shift+tab":
			m.section = (m.section + 1) % 2
			return m, nil
		case "up", "k":
			if m.section == 0 {
				// Theme section
				if m.selectedTheme > 0 {
					m.selectedTheme--
					m.applyTheme()
				}
			} else {
				// Projects section
				if m.selected > 0 {
					m.selected--
				}
			}
		case "down", "j":
			if m.section == 0 {
				// Theme section
				if m.selectedTheme < len(m.themes)-1 {
					m.selectedTheme++
					m.applyTheme()
				}
			} else {
				// Projects section
				if m.selected < len(m.projects)-1 {
					m.selected++
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
			// New project (only in projects section)
			if m.section == 1 {
				m.editing = true
				m.editProject = &db.Project{}
				m.nameInput.SetValue("")
				m.aliasInput.SetValue("")
				m.instructionsInput.SetValue("")
				m.nameInput.Focus()
				m.formFocus = 0
				return m, textinput.Blink
			}
		case "e":
			// Edit selected project (only in projects section)
			if m.section == 1 && len(m.projects) > 0 && m.selected < len(m.projects) {
				m.editing = true
				m.editProject = m.projects[m.selected]
				m.nameInput.SetValue(m.editProject.Name)
				m.aliasInput.SetValue(m.editProject.Aliases)
				m.instructionsInput.SetValue(m.editProject.Instructions)
				m.nameInput.Focus()
				m.formFocus = 0
				return m, textinput.Blink
			}
		case "d":
			// Delete selected project (only in projects section)
			if m.section == 1 && len(m.projects) > 0 && m.selected < len(m.projects) {
				err := m.db.DeleteProject(m.projects[m.selected].ID)
				if err != nil {
					m.err = err
				} else {
					m.err = nil
					m.loadSettings()
					if m.selected >= len(m.projects) && m.selected > 0 {
						m.selected--
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
		case "esc", "q":
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

func (m *SettingsModel) updateForm(msg tea.KeyMsg) (*SettingsModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editing = false
		m.editProject = nil
		return m, nil
	case "tab":
		// Tab moves forward through fields (unless in textarea)
		if m.formFocus == 3 {
			// In instructions textarea, tab inserts tab character
			var cmd tea.Cmd
			m.instructionsInput, cmd = m.instructionsInput.Update(msg)
			return m, cmd
		}
		m.formFocus = (m.formFocus + 1) % 4
		m.updateFormFocus()
		if m.formFocus == 1 {
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
		if m.formFocus == 3 {
			return m, textarea.Blink
		}
		return m, nil
	case "shift+tab":
		m.formFocus = (m.formFocus + 3) % 4
		m.updateFormFocus()
		if m.formFocus == 1 {
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
		if m.formFocus == 3 {
			return m, textarea.Blink
		}
		return m, nil
	case "enter":
		if m.formFocus == 1 {
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
		if m.formFocus == 3 {
			// In instructions textarea, enter inserts newline
			var cmd tea.Cmd
			m.instructionsInput, cmd = m.instructionsInput.Update(msg)
			return m, cmd
		}
		m.formFocus = (m.formFocus + 1) % 4
		m.updateFormFocus()
		if m.formFocus == 1 {
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
		if m.formFocus == 3 {
			return m, textarea.Blink
		}
		return m, nil
	case "ctrl+s":
		return m.saveProject()
	}

	// Update focused input
	var cmd tea.Cmd
	switch m.formFocus {
	case 0:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case 2:
		m.aliasInput, cmd = m.aliasInput.Update(msg)
	case 3:
		m.instructionsInput, cmd = m.instructionsInput.Update(msg)
	}
	return m, cmd
}

func (m *SettingsModel) updateFormFocus() {
	m.nameInput.Blur()
	m.aliasInput.Blur()
	m.instructionsInput.Blur()
	switch m.formFocus {
	case 0:
		m.nameInput.Focus()
	case 2:
		m.aliasInput.Focus()
	case 3:
		m.instructionsInput.Focus()
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

	m.editing = false
	m.editProject = nil
	m.err = nil
	m.loadSettings()
	return m, nil
}

// View renders the settings view.
func (m *SettingsModel) View() string {
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
	themeHeader := "Theme"
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
	projectsHeader := "Projects"
	if m.section == 1 {
		projectsHeader = Bold.Foreground(ColorPrimary).Render("Projects")
	} else {
		projectsHeader = Bold.Render("Projects")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(projectsHeader))
	b.WriteString("\n")

	if m.editing {
		// Show form
		b.WriteString(m.renderForm())
	} else {
		// Show list
		if len(m.projects) == 0 {
			b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(Dim.Render("No projects configured. Press 'n' to add one.")))
		} else {
			for i, p := range m.projects {
				prefix := "  "
				style := lipgloss.NewStyle()
				if m.section == 1 && i == m.selected {
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

func (m *SettingsModel) renderForm() string {
	var b strings.Builder

	title := "New Project"
	if m.editProject.ID != 0 {
		title = "Edit Project"
	}
	b.WriteString(lipgloss.NewStyle().Padding(1, 2).Render(Bold.Render(title)))
	b.WriteString("\n")

	// Name field
	nameLabel := Dim.Render("Name:         ")
	if m.formFocus == 0 {
		nameLabel = Bold.Render("Name:         ")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(nameLabel + m.nameInput.View()))
	b.WriteString("\n")

	// Path field (shows current path or prompt to browse)
	pathLabel := Dim.Render("Path:         ")
	if m.formFocus == 1 {
		pathLabel = Bold.Render("Path:         ")
	}
	pathValue := Dim.Render("[press Enter to browse]")
	if m.editProject != nil && m.editProject.Path != "" {
		pathValue = m.editProject.Path
	}
	pathLine := pathLabel + pathValue
	if m.formFocus == 1 {
		pathLine = lipgloss.NewStyle().Foreground(ColorPrimary).Render(pathLine)
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(pathLine))
	b.WriteString("\n")

	// Aliases field
	aliasLabel := Dim.Render("Aliases:      ")
	if m.formFocus == 2 {
		aliasLabel = Bold.Render("Aliases:      ")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(aliasLabel + m.aliasInput.View()))
	b.WriteString("\n\n")

	// Instructions field
	instructionsLabel := Dim.Render("Instructions: ")
	if m.formFocus == 3 {
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

func (m *SettingsModel) renderHelp() string {
	var keys []struct {
		key  string
		desc string
	}

	if m.editing {
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
			{"q/esc", "back"},
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
}
