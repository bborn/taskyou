package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/lipgloss"
)

// TaskRefAutocompleteModel provides inline autocomplete for task references (#123).
// It appears as a floating dropdown when the user types '#' in a text field.
type TaskRefAutocompleteModel struct {
	db            *db.DB
	allTasks      []*db.Task // Preloaded tasks for fast filtering
	filteredTasks []*db.Task
	selectedIndex int
	query         string // The text after '#' (e.g., "30" for "#30")
	cursorPos     int    // Position in the text where '#' was typed
	maxVisible    int
	width         int

	// Result
	selectedTask *db.Task
	dismissed    bool
}

// NewTaskRefAutocompleteModel creates a new task reference autocomplete model.
func NewTaskRefAutocompleteModel(database *db.DB, width int) *TaskRefAutocompleteModel {
	m := &TaskRefAutocompleteModel{
		db:         database,
		maxVisible: 5,
		width:      width,
	}
	m.loadTasks()
	return m
}

// loadTasks loads tasks from the database for filtering.
func (m *TaskRefAutocompleteModel) loadTasks() {
	if m.db == nil {
		return
	}

	// Load recent tasks (excluding archived)
	tasks, err := m.db.ListTasks(db.ListTasksOptions{
		Limit:         100,
		IncludeClosed: true, // Include done tasks for referencing
	})
	if err != nil {
		return
	}
	m.allTasks = tasks
}

// SetQuery sets the search query and filters tasks.
func (m *TaskRefAutocompleteModel) SetQuery(query string, cursorPos int) {
	m.query = query
	m.cursorPos = cursorPos
	m.filterTasks()
}

// filterTasks filters tasks based on the current query.
func (m *TaskRefAutocompleteModel) filterTasks() {
	if m.query == "" {
		// Show all tasks when just '#' is typed
		m.filteredTasks = m.allTasks
		if len(m.filteredTasks) > 20 {
			m.filteredTasks = m.filteredTasks[:20]
		}
	} else {
		m.filteredTasks = nil
		queryLower := strings.ToLower(m.query)

		// First, try to match by ID if query is numeric
		if id, err := strconv.ParseInt(m.query, 10, 64); err == nil {
			// Prioritize exact or prefix ID matches
			for _, task := range m.allTasks {
				if task.ID == id {
					m.filteredTasks = append([]*db.Task{task}, m.filteredTasks...)
				} else if strings.HasPrefix(fmt.Sprintf("%d", task.ID), m.query) {
					m.filteredTasks = append(m.filteredTasks, task)
				}
			}
		}

		// Also search by title
		for _, task := range m.allTasks {
			// Skip if already added by ID match
			alreadyAdded := false
			for _, added := range m.filteredTasks {
				if added.ID == task.ID {
					alreadyAdded = true
					break
				}
			}
			if alreadyAdded {
				continue
			}

			if strings.Contains(strings.ToLower(task.Title), queryLower) {
				m.filteredTasks = append(m.filteredTasks, task)
			}
		}

		// Limit results
		if len(m.filteredTasks) > 20 {
			m.filteredTasks = m.filteredTasks[:20]
		}
	}

	// Reset selection if out of bounds
	if m.selectedIndex >= len(m.filteredTasks) {
		m.selectedIndex = 0
	}
}

// MoveUp moves selection up.
func (m *TaskRefAutocompleteModel) MoveUp() {
	if m.selectedIndex > 0 {
		m.selectedIndex--
	} else if len(m.filteredTasks) > 0 {
		m.selectedIndex = len(m.filteredTasks) - 1
	}
}

// MoveDown moves selection down.
func (m *TaskRefAutocompleteModel) MoveDown() {
	if m.selectedIndex < len(m.filteredTasks)-1 {
		m.selectedIndex++
	} else {
		m.selectedIndex = 0
	}
}

// Select confirms the current selection.
func (m *TaskRefAutocompleteModel) Select() {
	if len(m.filteredTasks) > 0 && m.selectedIndex < len(m.filteredTasks) {
		m.selectedTask = m.filteredTasks[m.selectedIndex]
	}
}

// Dismiss closes the autocomplete without selecting.
func (m *TaskRefAutocompleteModel) Dismiss() {
	m.dismissed = true
}

// SelectedTask returns the selected task, or nil if none selected.
func (m *TaskRefAutocompleteModel) SelectedTask() *db.Task {
	return m.selectedTask
}

// IsDismissed returns true if the autocomplete was dismissed.
func (m *TaskRefAutocompleteModel) IsDismissed() bool {
	return m.dismissed
}

// HasResults returns true if there are matching tasks.
func (m *TaskRefAutocompleteModel) HasResults() bool {
	return len(m.filteredTasks) > 0
}

// GetReference returns the task reference string (e.g., "#123") for the selected task.
func (m *TaskRefAutocompleteModel) GetReference() string {
	if m.selectedTask == nil {
		return ""
	}
	return fmt.Sprintf("#%d", m.selectedTask.ID)
}

// GetCursorPos returns the position where '#' was typed.
func (m *TaskRefAutocompleteModel) GetCursorPos() int {
	return m.cursorPos
}

// GetQueryLength returns the length of the query (including #).
func (m *TaskRefAutocompleteModel) GetQueryLength() int {
	return len(m.query) + 1 // +1 for the '#'
}

// Reset resets the autocomplete state.
func (m *TaskRefAutocompleteModel) Reset() {
	m.query = ""
	m.cursorPos = 0
	m.selectedIndex = 0
	m.selectedTask = nil
	m.dismissed = false
	m.filteredTasks = nil
}

// View renders the autocomplete dropdown.
func (m *TaskRefAutocompleteModel) View() string {
	if len(m.filteredTasks) == 0 {
		return ""
	}

	// Calculate visible range
	start := 0
	end := len(m.filteredTasks)
	if end > m.maxVisible {
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

	var lines []string

	// Show scroll indicator at top
	if start > 0 {
		scrollUp := lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true).
			Render(fmt.Sprintf("  ... %d more", start))
		lines = append(lines, scrollUp)
	}

	// Render visible tasks
	for i := start; i < end; i++ {
		task := m.filteredTasks[i]
		isSelected := i == m.selectedIndex
		lines = append(lines, m.renderTaskItem(task, isSelected))
	}

	// Show scroll indicator at bottom
	remaining := len(m.filteredTasks) - end
	if remaining > 0 {
		scrollDown := lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true).
			Render(fmt.Sprintf("  ... %d more", remaining))
		lines = append(lines, scrollDown)
	}

	content := strings.Join(lines, "\n")

	// Style the dropdown
	dropdownWidth := m.width
	if dropdownWidth > 60 {
		dropdownWidth = 60
	}
	if dropdownWidth < 30 {
		dropdownWidth = 30
	}

	dropdown := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Background(lipgloss.Color("236")).
		Padding(0, 1).
		Width(dropdownWidth)

	return dropdown.Render(content)
}

// renderTaskItem renders a single task in the dropdown.
func (m *TaskRefAutocompleteModel) renderTaskItem(task *db.Task, isSelected bool) string {
	var line strings.Builder

	// Selection indicator
	if isSelected {
		line.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("> "))
	} else {
		line.WriteString("  ")
	}

	// Task ID
	idStyle := lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true)
	line.WriteString(idStyle.Render(fmt.Sprintf("#%-4d", task.ID)))
	line.WriteString(" ")

	// Status icon
	statusIcon := StatusIcon(task.Status)
	statusColor := StatusColor(task.Status)
	line.WriteString(lipgloss.NewStyle().Foreground(statusColor).Render(statusIcon))
	line.WriteString(" ")

	// Title (truncate if needed)
	title := task.Title
	maxTitleLen := 40
	if len(title) > maxTitleLen {
		title = title[:maxTitleLen-3] + "..."
	}

	titleStyle := lipgloss.NewStyle()
	if isSelected {
		titleStyle = titleStyle.Bold(true).Foreground(ColorPrimary)
	} else {
		titleStyle = titleStyle.Foreground(lipgloss.Color("252"))
	}
	line.WriteString(titleStyle.Render(title))

	return line.String()
}
