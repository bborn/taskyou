package ui

import (
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CommandPaletteModel represents the Command+P task switcher.
type CommandPaletteModel struct {
	db            *db.DB
	allTasks      []*db.Task
	filteredTasks []*db.Task
	projects      []*db.Project
	searchInput   textinput.Model
	selectedIndex int
	width         int
	height        int
	maxVisible    int

	// Result
	selectedTask *db.Task
	cancelled    bool
}

// NewCommandPaletteModel creates a new command palette model.
func NewCommandPaletteModel(database *db.DB, tasks []*db.Task, width, height int) *CommandPaletteModel {
	searchInput := textinput.New()
	searchInput.Placeholder = "Search tasks by title, ID, project, or PR URL/number..."
	searchInput.Focus()
	searchInput.CharLimit = 100
	searchInput.Width = min(60, width-10)

	// Load projects for project-based filtering
	projects, _ := database.ListProjects()

	m := &CommandPaletteModel{
		db:          database,
		allTasks:    tasks,
		projects:    projects,
		searchInput: searchInput,
		width:       width,
		height:      height,
		maxVisible:  10,
	}
	m.filterTasks()
	return m
}

// Init initializes the command palette.
func (m *CommandPaletteModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles messages.
func (m *CommandPaletteModel) Update(msg tea.Msg) (*CommandPaletteModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.cancelled = true
			return m, nil
		case "enter":
			if len(m.filteredTasks) > 0 && m.selectedIndex < len(m.filteredTasks) {
				m.selectedTask = m.filteredTasks[m.selectedIndex]
			}
			return m, nil
		case "up", "ctrl+p", "ctrl+k":
			if m.selectedIndex > 0 {
				m.selectedIndex--
			} else if len(m.filteredTasks) > 0 {
				// Wrap to bottom
				m.selectedIndex = len(m.filteredTasks) - 1
			}
			return m, nil
		case "down", "ctrl+n", "ctrl+j":
			if m.selectedIndex < len(m.filteredTasks)-1 {
				m.selectedIndex++
			} else {
				// Wrap to top
				m.selectedIndex = 0
			}
			return m, nil
		case "pgup":
			m.selectedIndex -= m.maxVisible
			if m.selectedIndex < 0 {
				m.selectedIndex = 0
			}
			return m, nil
		case "pgdown":
			m.selectedIndex += m.maxVisible
			if m.selectedIndex >= len(m.filteredTasks) {
				m.selectedIndex = len(m.filteredTasks) - 1
			}
			if m.selectedIndex < 0 {
				m.selectedIndex = 0
			}
			return m, nil
		}

		// Update search input
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		m.filterTasks()
		return m, cmd
	}

	return m, nil
}

// filterTasks filters tasks based on the search query.
func (m *CommandPaletteModel) filterTasks() {
	query := strings.ToLower(strings.TrimSpace(m.searchInput.Value()))

	if query == "" {
		m.filteredTasks = m.allTasks
	} else {
		m.filteredTasks = nil
		for _, task := range m.allTasks {
			if m.matchesQuery(task, query) {
				m.filteredTasks = append(m.filteredTasks, task)
			}
		}
	}

	// Clamp selected index
	if m.selectedIndex >= len(m.filteredTasks) {
		m.selectedIndex = max(0, len(m.filteredTasks)-1)
	}
}

// matchesQuery checks if a task matches the search query.
func (m *CommandPaletteModel) matchesQuery(task *db.Task, query string) bool {
	// Check task ID
	if strings.Contains(fmt.Sprintf("%d", task.ID), query) {
		return true
	}
	// Check task ID with # prefix
	if strings.HasPrefix(query, "#") {
		idQuery := strings.TrimPrefix(query, "#")
		if strings.Contains(fmt.Sprintf("%d", task.ID), idQuery) {
			return true
		}
	}
	// Check title
	if strings.Contains(strings.ToLower(task.Title), query) {
		return true
	}
	// Check project
	if strings.Contains(strings.ToLower(task.Project), query) {
		return true
	}
	// Check status
	if strings.Contains(strings.ToLower(task.Status), query) {
		return true
	}
	// Check PR URL (e.g., "https://github.com/offerlab/offerlab/pull/2382")
	if task.PRURL != "" && strings.Contains(strings.ToLower(task.PRURL), query) {
		return true
	}
	// Check PR number (e.g., "2382" or "#2382")
	if task.PRNumber > 0 {
		prNumStr := fmt.Sprintf("%d", task.PRNumber)
		if strings.Contains(prNumStr, query) {
			return true
		}
		// Also match with # prefix
		if strings.HasPrefix(query, "#") {
			prQuery := strings.TrimPrefix(query, "#")
			if strings.Contains(prNumStr, prQuery) {
				return true
			}
		}
	}
	// Fuzzy match: check if all characters in query appear in order in title
	if fuzzyMatch(strings.ToLower(task.Title), query) {
		return true
	}
	return false
}

// fuzzyMatch performs a simple fuzzy match - all characters in pattern appear in order in str.
func fuzzyMatch(str, pattern string) bool {
	if len(pattern) == 0 {
		return true
	}
	if len(str) == 0 {
		return false
	}

	patternIdx := 0
	for i := 0; i < len(str) && patternIdx < len(pattern); i++ {
		if str[i] == pattern[patternIdx] {
			patternIdx++
		}
	}
	return patternIdx == len(pattern)
}

// View renders the command palette.
func (m *CommandPaletteModel) View() string {
	// Modal dimensions
	modalWidth := min(80, m.width-4)

	// Header
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		MarginBottom(1).
		Render("Go to Task")

	// Search input
	inputStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorSecondary).
		Padding(0, 1).
		Width(modalWidth - 6)
	searchBox := inputStyle.Render(m.searchInput.View())

	// Task list
	var taskList strings.Builder
	if len(m.filteredTasks) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true).
			Padding(1, 0)
		taskList.WriteString(emptyStyle.Render("No tasks found"))
	} else {
		// Calculate visible range (for scrolling)
		start := 0
		end := len(m.filteredTasks)
		if end > m.maxVisible {
			// Center the selected item when possible
			halfVisible := m.maxVisible / 2
			start = m.selectedIndex - halfVisible
			if start < 0 {
				start = 0
			}
			end = start + m.maxVisible
			if end > len(m.filteredTasks) {
				end = len(m.filteredTasks)
				start = end - m.maxVisible
				if start < 0 {
					start = 0
				}
			}
		}

		// Show scroll indicator at top
		if start > 0 {
			scrollUp := lipgloss.NewStyle().
				Foreground(ColorMuted).
				Italic(true).
				Render(fmt.Sprintf("  ... %d more above", start))
			taskList.WriteString(scrollUp + "\n")
		}

		// Render visible tasks
		for i := start; i < end; i++ {
			task := m.filteredTasks[i]
			isSelected := i == m.selectedIndex

			taskList.WriteString(m.renderTaskItem(task, isSelected, modalWidth-6))
			if i < end-1 {
				taskList.WriteString("\n")
			}
		}

		// Show scroll indicator at bottom
		remaining := len(m.filteredTasks) - end
		if remaining > 0 {
			scrollDown := lipgloss.NewStyle().
				Foreground(ColorMuted).
				Italic(true).
				Render(fmt.Sprintf("\n  ... %d more below", remaining))
			taskList.WriteString(scrollDown)
		}
	}

	// Help text
	helpStyle := lipgloss.NewStyle().
		Foreground(ColorMuted).
		MarginTop(1)
	help := helpStyle.Render("Enter: select  Esc: cancel  ↑/↓: navigate")

	// Combine all parts
	content := lipgloss.JoinVertical(lipgloss.Left,
		header,
		searchBox,
		"",
		taskList.String(),
		help,
	)

	// Modal box
	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(modalWidth)

	modalContent := modalBox.Render(content)

	// Center on screen
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalContent)
}

// renderTaskItem renders a single task in the list.
func (m *CommandPaletteModel) renderTaskItem(task *db.Task, isSelected bool, width int) string {
	// Status icon
	statusIcon := StatusIcon(task.Status)
	statusColor := StatusColor(task.Status)

	// Build the line
	var line strings.Builder

	// Selection indicator
	if isSelected {
		line.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("> "))
	} else {
		line.WriteString("  ")
	}

	// Status
	line.WriteString(lipgloss.NewStyle().Foreground(statusColor).Render(statusIcon))
	line.WriteString(" ")

	// Task ID
	idStyle := lipgloss.NewStyle().Foreground(ColorMuted)
	line.WriteString(idStyle.Render(fmt.Sprintf("#%-4d", task.ID)))
	line.WriteString(" ")

	// Project tag
	if task.Project != "" {
		projectStyle := lipgloss.NewStyle().Foreground(ProjectColor(task.Project))
		shortProject := task.Project
		switch task.Project {
		case "offerlab":
			shortProject = "ol"
		case "influencekit":
			shortProject = "ik"
		}
		line.WriteString(projectStyle.Render("[" + shortProject + "]"))
		line.WriteString(" ")
	}

	// Title (truncate if needed)
	title := task.Title
	currentLen := lipgloss.Width(line.String())
	maxTitleLen := width - currentLen - 2
	if maxTitleLen < 10 {
		maxTitleLen = 10
	}
	if len(title) > maxTitleLen {
		title = title[:maxTitleLen-1] + "..."
	}

	titleStyle := lipgloss.NewStyle()
	if isSelected {
		titleStyle = titleStyle.Bold(true).Foreground(ColorPrimary)
	}
	line.WriteString(titleStyle.Render(title))

	return line.String()
}

// SelectedTask returns the selected task, or nil if cancelled.
func (m *CommandPaletteModel) SelectedTask() *db.Task {
	return m.selectedTask
}

// IsCancelled returns true if the user cancelled the palette.
func (m *CommandPaletteModel) IsCancelled() bool {
	return m.cancelled
}

// SetSize updates the command palette dimensions.
func (m *CommandPaletteModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.searchInput.Width = min(60, width-10)
}
