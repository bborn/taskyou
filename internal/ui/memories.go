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

// MemoriesModel represents the project memories view.
type MemoriesModel struct {
	db     *db.DB
	width  int
	height int

	// List state
	memories []*db.ProjectMemory
	projects []*db.Project
	selected int

	// Filter
	filterProject string
	projectIdx    int // Index in projects list for cycling

	// Editing state
	editing       bool
	editMemory    *db.ProjectMemory
	contentInput  textarea.Model
	categoryInput textinput.Model
	formFocus     int // 0=content, 1=category

	err error
}

// NewMemoriesModel creates a new memories model.
func NewMemoriesModel(database *db.DB, width, height int) *MemoriesModel {
	contentInput := textarea.New()
	contentInput.Placeholder = "Memory content..."
	contentInput.SetWidth(width - 20)
	contentInput.SetHeight(4)

	categoryInput := textinput.New()
	categoryInput.Placeholder = "pattern, context, decision, gotcha, general"
	categoryInput.CharLimit = 50

	m := &MemoriesModel{
		db:            database,
		width:         width,
		height:        height,
		contentInput:  contentInput,
		categoryInput: categoryInput,
	}
	m.loadProjects()
	m.loadMemories()
	return m
}

func (m *MemoriesModel) loadProjects() {
	projects, err := m.db.ListProjects()
	if err != nil {
		m.err = err
		return
	}
	m.projects = projects
}

func (m *MemoriesModel) loadMemories() {
	memories, err := m.db.ListMemories(db.ListMemoriesOptions{
		Project: m.filterProject,
		Limit:   100,
	})
	if err != nil {
		m.err = err
		return
	}
	m.memories = memories
	if m.selected >= len(m.memories) {
		m.selected = max(0, len(m.memories)-1)
	}
}

// Init initializes the model.
func (m *MemoriesModel) Init() tea.Cmd {
	return nil
}

// Update handles messages.
func (m *MemoriesModel) Update(msg tea.Msg) (*MemoriesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.editing {
			return m.updateForm(msg)
		}

		switch msg.String() {
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected < len(m.memories)-1 {
				m.selected++
			}
		case "n":
			// New memory
			m.editing = true
			m.editMemory = &db.ProjectMemory{
				Category: db.MemoryCategoryGeneral,
			}
			// Pre-fill project if filtering
			if m.filterProject != "" {
				m.editMemory.Project = m.filterProject
			} else if len(m.projects) > 0 {
				m.editMemory.Project = m.projects[0].Name
			}
			m.contentInput.SetValue("")
			m.categoryInput.SetValue(db.MemoryCategoryGeneral)
			m.contentInput.Focus()
			m.formFocus = 0
			return m, textarea.Blink
		case "e":
			// Edit selected memory
			if len(m.memories) > 0 && m.selected < len(m.memories) {
				mem := m.memories[m.selected]
				m.editing = true
				m.editMemory = &db.ProjectMemory{
					ID:       mem.ID,
					Project:  mem.Project,
					Category: mem.Category,
					Content:  mem.Content,
				}
				m.contentInput.SetValue(mem.Content)
				m.categoryInput.SetValue(mem.Category)
				m.contentInput.Focus()
				m.formFocus = 0
				return m, textarea.Blink
			}
		case "d":
			// Delete selected memory
			if len(m.memories) > 0 && m.selected < len(m.memories) {
				m.db.DeleteMemory(m.memories[m.selected].ID)
				m.loadMemories()
			}
		case "p", "tab":
			// Cycle through projects filter
			m.cycleProject()
			m.loadMemories()
		case "P", "shift+tab":
			// Cycle backwards
			m.cycleProjectBack()
			m.loadMemories()
		}
	}

	return m, nil
}

func (m *MemoriesModel) cycleProject() {
	if len(m.projects) == 0 {
		return
	}
	m.projectIdx = (m.projectIdx + 1) % (len(m.projects) + 1)
	if m.projectIdx == 0 {
		m.filterProject = "" // All
	} else {
		m.filterProject = m.projects[m.projectIdx-1].Name
	}
}

func (m *MemoriesModel) cycleProjectBack() {
	if len(m.projects) == 0 {
		return
	}
	m.projectIdx--
	if m.projectIdx < 0 {
		m.projectIdx = len(m.projects)
	}
	if m.projectIdx == 0 {
		m.filterProject = ""
	} else {
		m.filterProject = m.projects[m.projectIdx-1].Name
	}
}

func (m *MemoriesModel) updateForm(msg tea.KeyMsg) (*MemoriesModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editing = false
		m.editMemory = nil
		return m, nil
	case "tab":
		m.formFocus = (m.formFocus + 1) % 2
		m.updateFormFocus()
		if m.formFocus == 0 {
			return m, textarea.Blink
		}
		return m, textinput.Blink
	case "shift+tab":
		m.formFocus = (m.formFocus + 1) % 2
		m.updateFormFocus()
		if m.formFocus == 0 {
			return m, textarea.Blink
		}
		return m, textinput.Blink
	case "ctrl+s":
		return m.saveMemory()
	}

	// Update focused input
	var cmd tea.Cmd
	if m.formFocus == 0 {
		m.contentInput, cmd = m.contentInput.Update(msg)
	} else {
		m.categoryInput, cmd = m.categoryInput.Update(msg)
	}
	return m, cmd
}

func (m *MemoriesModel) updateFormFocus() {
	m.contentInput.Blur()
	m.categoryInput.Blur()
	if m.formFocus == 0 {
		m.contentInput.Focus()
	} else {
		m.categoryInput.Focus()
	}
}

func (m *MemoriesModel) saveMemory() (*MemoriesModel, tea.Cmd) {
	content := strings.TrimSpace(m.contentInput.Value())
	category := strings.TrimSpace(m.categoryInput.Value())

	if content == "" {
		m.err = fmt.Errorf("content is required")
		return m, nil
	}
	if category == "" {
		category = db.MemoryCategoryGeneral
	}

	m.editMemory.Content = content
	m.editMemory.Category = category

	var err error
	if m.editMemory.ID == 0 {
		err = m.db.CreateMemory(m.editMemory)
	} else {
		err = m.db.UpdateMemory(m.editMemory)
	}

	if err != nil {
		m.err = err
		return m, nil
	}

	m.editing = false
	m.editMemory = nil
	m.err = nil
	m.loadMemories()
	return m, nil
}

// View renders the memories view.
func (m *MemoriesModel) View() string {
	var b strings.Builder

	// Header with filter
	header := Bold.Render("Project Memories")
	if m.filterProject != "" {
		header += Dim.Render(fmt.Sprintf(" (%s)", m.filterProject))
	} else {
		header += Dim.Render(" (all projects)")
	}
	b.WriteString(lipgloss.NewStyle().Padding(1, 2).Render(header))
	b.WriteString("\n")

	if m.editing {
		b.WriteString(m.renderForm())
	} else {
		if len(m.memories) == 0 {
			b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(Dim.Render("No memories yet. Press 'n' to add one.")))
		} else {
			for i, mem := range m.memories {
				prefix := "  "
				style := lipgloss.NewStyle()
				if i == m.selected {
					prefix = "> "
					style = style.Foreground(ColorPrimary)
				}

				// Truncate content for display
				content := mem.Content
				if len(content) > 60 {
					content = content[:57] + "..."
				}

				catStyle := m.categoryStyle(mem.Category)
				line := fmt.Sprintf("%s[%s] %s", prefix, catStyle.Render(mem.Category), content)
				if m.filterProject == "" {
					line += Dim.Render(fmt.Sprintf(" (%s)", mem.Project))
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

func (m *MemoriesModel) categoryStyle(cat string) lipgloss.Style {
	switch cat {
	case db.MemoryCategoryPattern:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("212")) // Blue
	case db.MemoryCategoryContext:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("35")) // Green
	case db.MemoryCategoryDecision:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // Orange
	case db.MemoryCategoryGotcha:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // Red
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("245")) // Gray
	}
}

func (m *MemoriesModel) renderForm() string {
	var b strings.Builder

	title := "New Memory"
	if m.editMemory.ID != 0 {
		title = "Edit Memory"
	}
	title += fmt.Sprintf(" (%s)", m.editMemory.Project)
	b.WriteString(lipgloss.NewStyle().Padding(1, 2).Render(Bold.Render(title)))
	b.WriteString("\n")

	// Content field
	contentLabel := Dim.Render("Content: ")
	if m.formFocus == 0 {
		contentLabel = Bold.Render("Content: ")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(contentLabel))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(m.contentInput.View()))
	b.WriteString("\n\n")

	// Category field
	categoryLabel := Dim.Render("Category: ")
	if m.formFocus == 1 {
		categoryLabel = Bold.Render("Category: ")
	}
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(categoryLabel + m.categoryInput.View()))
	b.WriteString("\n")

	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(
		Dim.Render("Categories: pattern, context, decision, gotcha, general")))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(Dim.Render("Tab: next field • Ctrl+S: save • Esc: cancel")))

	return b.String()
}

func (m *MemoriesModel) renderHelp() string {
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
			{"↑/↓", "navigate"},
			{"n", "new"},
			{"e", "edit"},
			{"d", "delete"},
			{"p/tab", "cycle project"},
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
func (m *MemoriesModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.contentInput.SetWidth(width - 20)
}
