package ui

import (
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/lipgloss"
)

// KanbanColumn represents a column in the kanban board.
type KanbanColumn struct {
	Title  string
	Status string // The status this column represents
	Tasks  []*db.Task
	Color  lipgloss.Color
}

// KanbanBoard manages the kanban board state.
type KanbanBoard struct {
	columns       []KanbanColumn
	selectedCol   int
	selectedRow   int
	width         int
	height        int
	showClosed    bool
	numberFilter  string
	allTasks      []*db.Task // All tasks for filtering
	filteredTasks []*db.Task // Tasks after applying text filter
	textFilter    string     // Current text filter
}

// NewKanbanBoard creates a new kanban board.
func NewKanbanBoard(width, height int) *KanbanBoard {
	return &KanbanBoard{
		columns: []KanbanColumn{
			{Title: "Backlog", Status: db.StatusBacklog, Color: ColorMuted},
			{Title: "In Progress", Status: db.StatusInProgress, Color: ColorInProgress},
			{Title: "Blocked", Status: db.StatusBlocked, Color: ColorBlocked},
			{Title: "Done", Status: db.StatusDone, Color: ColorDone},
		},
		width:  width,
		height: height,
	}
}

// SetTasks updates the tasks in the kanban board.
func (k *KanbanBoard) SetTasks(tasks []*db.Task) {
	k.allTasks = tasks
	k.applyFilter()
}

// applyFilter filters tasks and distributes them to columns.
func (k *KanbanBoard) applyFilter() {
	// Apply text filter
	if k.textFilter == "" {
		k.filteredTasks = k.allTasks
	} else {
		k.filteredTasks = nil
		filter := strings.ToLower(k.textFilter)
		for _, t := range k.allTasks {
			if strings.Contains(strings.ToLower(t.Title), filter) ||
				strings.Contains(fmt.Sprintf("%d", t.ID), filter) {
				k.filteredTasks = append(k.filteredTasks, t)
			}
		}
	}

	// Clear all columns
	for i := range k.columns {
		k.columns[i].Tasks = nil
	}

	// Distribute tasks to columns
	for _, task := range k.filteredTasks {
		for i := range k.columns {
			if k.columns[i].Status == task.Status {
				k.columns[i].Tasks = append(k.columns[i].Tasks, task)
				break
			}
		}
	}

	// Ensure selected position is valid
	k.clampSelection()
}

// SetFilter sets the text filter.
func (k *KanbanBoard) SetFilter(filter string) {
	k.textFilter = filter
	k.applyFilter()
}

// GetFilter returns the current text filter.
func (k *KanbanBoard) GetFilter() string {
	return k.textFilter
}

// SetShowClosed sets whether to show closed tasks.
func (k *KanbanBoard) SetShowClosed(show bool) {
	k.showClosed = show
	k.applyFilter()
}

// SetSize updates the board dimensions.
func (k *KanbanBoard) SetSize(width, height int) {
	k.width = width
	k.height = height
}

// MoveLeft moves selection to the left column.
func (k *KanbanBoard) MoveLeft() {
	if k.selectedCol > 0 {
		k.selectedCol--
		k.clampSelection()
	}
}

// MoveRight moves selection to the right column.
func (k *KanbanBoard) MoveRight() {
	if k.selectedCol < len(k.columns)-1 {
		k.selectedCol++
		k.clampSelection()
	}
}

// MoveUp moves selection up within the current column.
func (k *KanbanBoard) MoveUp() {
	if k.selectedRow > 0 {
		k.selectedRow--
	}
}

// MoveDown moves selection down within the current column.
func (k *KanbanBoard) MoveDown() {
	col := k.columns[k.selectedCol]
	if k.selectedRow < len(col.Tasks)-1 {
		k.selectedRow++
	}
}

// clampSelection ensures selection is within bounds.
func (k *KanbanBoard) clampSelection() {
	if k.selectedCol >= len(k.columns) {
		k.selectedCol = len(k.columns) - 1
	}
	if k.selectedCol < 0 {
		k.selectedCol = 0
	}

	col := k.columns[k.selectedCol]
	if k.selectedRow >= len(col.Tasks) {
		k.selectedRow = len(col.Tasks) - 1
	}
	if k.selectedRow < 0 {
		k.selectedRow = 0
	}
}

// SelectedTask returns the currently selected task.
func (k *KanbanBoard) SelectedTask() *db.Task {
	if k.selectedCol >= len(k.columns) {
		return nil
	}
	col := k.columns[k.selectedCol]
	if k.selectedRow >= len(col.Tasks) || k.selectedRow < 0 {
		return nil
	}
	return col.Tasks[k.selectedRow]
}

// SelectTask selects a task by ID.
func (k *KanbanBoard) SelectTask(id int64) bool {
	for colIdx, col := range k.columns {
		for rowIdx, task := range col.Tasks {
			if task.ID == id {
				k.selectedCol = colIdx
				k.selectedRow = rowIdx
				return true
			}
		}
	}
	return false
}

// View renders the kanban board.
func (k *KanbanBoard) View() string {
	if k.width < 40 || k.height < 10 {
		return lipgloss.Place(k.width, k.height, lipgloss.Center, lipgloss.Center, "Terminal too small")
	}

	// Calculate column width (subtract borders and gaps)
	numCols := len(k.columns)
	// Account for borders (2 chars per column) and gaps between columns
	availableWidth := k.width - (numCols * 2) - (numCols - 1)
	colWidth := availableWidth / numCols
	if colWidth < 20 {
		colWidth = 20
	}

	// Calculate available height for tasks (subtract borders)
	colHeight := k.height - 2

	// Build columns
	var columnViews []string
	for colIdx, col := range k.columns {
		isSelectedCol := colIdx == k.selectedCol

		// Column header
		headerStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(col.Color).
			Width(colWidth).
			Align(lipgloss.Center).
			MarginBottom(1)

		countStr := fmt.Sprintf(" (%d)", len(col.Tasks))
		header := headerStyle.Render(col.Title + countStr)

		// Task cards - calculate how many fit (each card is ~3 lines with margin)
		cardHeight := 3
		maxTasks := (colHeight - 4) / cardHeight // -4 for header and padding
		if maxTasks < 1 {
			maxTasks = 1
		}
		displayTasks := col.Tasks
		if len(displayTasks) > maxTasks {
			displayTasks = displayTasks[:maxTasks]
		}

		var taskViews []string
		for taskIdx, task := range displayTasks {
			isSelected := isSelectedCol && taskIdx == k.selectedRow
			taskView := k.renderTaskCard(task, colWidth-2, isSelected)
			taskViews = append(taskViews, taskView)
		}

		// Show overflow indicator
		if len(col.Tasks) > maxTasks {
			overflowStyle := lipgloss.NewStyle().
				Foreground(ColorMuted).
				Width(colWidth - 2).
				Align(lipgloss.Center)
			taskViews = append(taskViews, overflowStyle.Render(fmt.Sprintf("+%d more", len(col.Tasks)-maxTasks)))
		}

		// Combine header and tasks
		taskContent := lipgloss.JoinVertical(lipgloss.Left, taskViews...)
		colContent := lipgloss.JoinVertical(lipgloss.Left, header, taskContent)

		// Column container with border
		borderColor := ColorMuted
		if isSelectedCol {
			borderColor = ColorPrimary
		}

		colStyle := lipgloss.NewStyle().
			Width(colWidth).
			Height(colHeight).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor)

		columnViews = append(columnViews, colStyle.Render(colContent))
	}

	// Join columns horizontally with small gap
	board := lipgloss.JoinHorizontal(lipgloss.Top, columnViews...)

	return board
}

// renderTaskCard renders a single task card.
func (k *KanbanBoard) renderTaskCard(task *db.Task, width int, isSelected bool) string {
	if width < 10 {
		width = 10
	}

	var b strings.Builder

	// Task ID
	b.WriteString(Dim.Render(fmt.Sprintf("#%d", task.ID)))

	// Priority indicator
	if task.Priority == "high" {
		b.WriteString(" ")
		b.WriteString(PriorityHigh.Render())
	}

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
		b.WriteString(" ")
		b.WriteString(projectStyle.Render("[" + shortProject + "]"))
	}

	// Title (truncate if needed)
	title := task.Title
	maxTitleLen := width - 8
	if maxTitleLen < 10 {
		maxTitleLen = 10
	}
	if len(title) > maxTitleLen {
		title = title[:maxTitleLen-1] + "…"
	}

	idLine := b.String()
	titleLine := title

	// Card style
	cardStyle := lipgloss.NewStyle().
		Width(width).
		Padding(0, 1)

	if isSelected {
		cardStyle = cardStyle.
			Bold(true).
			Background(lipgloss.Color("#333333")).
			Foreground(lipgloss.Color("#FFFFFF"))
	}

	content := idLine + "\n" + titleLine
	return cardStyle.Render(content)
}

// ApplyNumberFilter filters by task ID prefix.
func (k *KanbanBoard) ApplyNumberFilter(filter string) {
	k.numberFilter = filter
	if filter == "" {
		k.applyFilter()
		return
	}

	// Try to find exact match first
	for colIdx, col := range k.columns {
		for rowIdx, task := range col.Tasks {
			if fmt.Sprintf("%d", task.ID) == filter {
				k.selectedCol = colIdx
				k.selectedRow = rowIdx
				return
			}
		}
	}

	// Otherwise just keep current selection
}

// GetNumberFilter returns the current number filter.
func (k *KanbanBoard) GetNumberFilter() string {
	return k.numberFilter
}
