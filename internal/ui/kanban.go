package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
	"github.com/charmbracelet/lipgloss"
)

// KanbanColumn represents a column in the kanban board.
type KanbanColumn struct {
	Title  string
	Status string // The status this column represents
	Tasks  []*db.Task
	Color  lipgloss.Color
	Icon   string // Visual icon for the column
}

// MobileWidthThreshold is the minimum width for showing all columns.
// Below this, only the selected column is shown.
const MobileWidthThreshold = 80

// KanbanBoard manages the kanban board state.
type KanbanBoard struct {
	columns          []KanbanColumn
	selectedCol      int
	selectedRow      int
	scrollOffsets    []int // Scroll offset per column
	width            int
	height           int
	allTasks         []*db.Task               // All tasks
	prInfo           map[int64]*github.PRInfo // PR info by task ID
	runningProcesses map[int64]bool           // Tasks with running shell processes
	hiddenDoneCount  int                      // Number of done tasks not shown (older ones)
}

// IsMobileMode returns true if the board should show single-column mode.
func (k *KanbanBoard) IsMobileMode() bool {
	return k.width < MobileWidthThreshold
}

// NewKanbanBoard creates a new kanban board.
func NewKanbanBoard(width, height int) *KanbanBoard {
	columns := makeKanbanColumns()
	return &KanbanBoard{
		columns:          columns,
		scrollOffsets:    make([]int, len(columns)),
		width:            width,
		height:           height,
		prInfo:           make(map[int64]*github.PRInfo),
		runningProcesses: make(map[int64]bool),
	}
}

// makeKanbanColumns creates columns with current theme colors.
func makeKanbanColumns() []KanbanColumn {
	return []KanbanColumn{
		{Title: "Backlog", Status: db.StatusBacklog, Color: ColorMuted, Icon: "◦"},
		{Title: "In Progress", Status: db.StatusQueued, Color: ColorInProgress, Icon: "▶"}, // Also shows processing
		{Title: "Blocked", Status: db.StatusBlocked, Color: ColorBlocked, Icon: "⚠"},
		{Title: "Done", Status: db.StatusDone, Color: ColorDone, Icon: "✓"},
	}
}

// RefreshTheme updates column colors after a theme change.
func (k *KanbanBoard) RefreshTheme() {
	newCols := makeKanbanColumns()
	for i := range k.columns {
		if i < len(newCols) {
			k.columns[i].Color = newCols[i].Color
			k.columns[i].Icon = newCols[i].Icon
		}
	}
}

// SetTasks updates the tasks in the kanban board.
func (k *KanbanBoard) SetTasks(tasks []*db.Task) {
	k.allTasks = tasks
	k.distributeTasksToColumns()
}

// SetHiddenDoneCount sets the number of done tasks not shown in the kanban.
func (k *KanbanBoard) SetHiddenDoneCount(count int) {
	k.hiddenDoneCount = count
}

// SetPRInfo updates the PR info for a task.
func (k *KanbanBoard) SetPRInfo(taskID int64, info *github.PRInfo) {
	if k.prInfo == nil {
		k.prInfo = make(map[int64]*github.PRInfo)
	}
	k.prInfo[taskID] = info
}

// GetPRInfo returns the PR info for a task.
func (k *KanbanBoard) GetPRInfo(taskID int64) *github.PRInfo {
	if k.prInfo == nil {
		return nil
	}
	return k.prInfo[taskID]
}

// SetRunningProcesses updates the map of tasks with running shell processes.
func (k *KanbanBoard) SetRunningProcesses(running map[int64]bool) {
	k.runningProcesses = running
}

// HasRunningProcess returns true if the task has a running shell process.
func (k *KanbanBoard) HasRunningProcess(taskID int64) bool {
	if k.runningProcesses == nil {
		return false
	}
	return k.runningProcesses[taskID]
}

// distributeTasksToColumns distributes tasks to their respective columns.
func (k *KanbanBoard) distributeTasksToColumns() {
	// Clear all columns
	for i := range k.columns {
		k.columns[i].Tasks = nil
	}

	// Distribute tasks to columns
	for _, task := range k.allTasks {
		placed := false
		for i := range k.columns {
			if k.columns[i].Status == task.Status {
				k.columns[i].Tasks = append(k.columns[i].Tasks, task)
				placed = true
				break
			}
		}
		// Map processing tasks to In Progress column (which uses StatusQueued)
		if !placed && task.Status == db.StatusProcessing {
			for i := range k.columns {
				if k.columns[i].Status == db.StatusQueued {
					k.columns[i].Tasks = append(k.columns[i].Tasks, task)
					break
				}
			}
		}
	}

	// Sort each column to put recurring tasks at the bottom
	for i := range k.columns {
		k.sortColumnTasks(i)
	}

	// Ensure selected position is valid
	k.clampSelection()
}

// sortColumnTasks sorts tasks within a column, putting recurring tasks at the bottom.
// Non-recurring tasks maintain their original order (by creation date from DB query).
func (k *KanbanBoard) sortColumnTasks(colIdx int) {
	if colIdx < 0 || colIdx >= len(k.columns) {
		return
	}
	tasks := k.columns[colIdx].Tasks
	if len(tasks) <= 1 {
		return
	}

	// Stable sort: recurring tasks go to bottom, preserving relative order within groups
	var nonRecurring, recurring []*db.Task
	for _, task := range tasks {
		if task.IsRecurring() {
			recurring = append(recurring, task)
		} else {
			nonRecurring = append(nonRecurring, task)
		}
	}

	// Reconstruct the slice with non-recurring first, then recurring
	k.columns[colIdx].Tasks = append(nonRecurring, recurring...)
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
		k.ensureSelectedVisible()
	}
}

// MoveRight moves selection to the right column.
func (k *KanbanBoard) MoveRight() {
	if k.selectedCol < len(k.columns)-1 {
		k.selectedCol++
		k.clampSelection()
		k.ensureSelectedVisible()
	}
}

// MoveUp moves selection up within the current column.
// If at the top, wraps around to the bottom.
func (k *KanbanBoard) MoveUp() {
	col := k.columns[k.selectedCol]
	if len(col.Tasks) == 0 {
		return
	}
	if k.selectedRow > 0 {
		k.selectedRow--
	} else {
		// Wrap around to bottom
		k.selectedRow = len(col.Tasks) - 1
	}
	k.ensureSelectedVisible()
}

// MoveDown moves selection down within the current column.
// If at the bottom, wraps around to the top.
func (k *KanbanBoard) MoveDown() {
	col := k.columns[k.selectedCol]
	if len(col.Tasks) == 0 {
		return
	}
	if k.selectedRow < len(col.Tasks)-1 {
		k.selectedRow++
	} else {
		// Wrap around to top
		k.selectedRow = 0
	}
	k.ensureSelectedVisible()
}

// ensureSelectedVisible adjusts scroll offset so the selected task is visible.
func (k *KanbanBoard) ensureSelectedVisible() {
	if k.selectedCol < 0 || k.selectedCol >= len(k.columns) {
		return
	}

	// Ensure scrollOffsets slice is properly sized
	for len(k.scrollOffsets) < len(k.columns) {
		k.scrollOffsets = append(k.scrollOffsets, 0)
	}

	// Calculate how many tasks fit in the visible area
	// Must match viewDesktop()/viewMobile() calculation
	colHeight := k.height - 2 // -2 for column borders
	if k.IsMobileMode() {
		colHeight = k.height - 4 // -2 for tab bar, -2 for column borders
	}
	cardHeight := 3                            // Most cards are 3 lines (2 content + 1 border)
	maxVisible := (colHeight - 3) / cardHeight // -3 for header bar and scroll indicators
	if maxVisible < 1 {
		maxVisible = 1
	}

	offset := k.scrollOffsets[k.selectedCol]

	// If selected row is above visible area, scroll up
	if k.selectedRow < offset {
		k.scrollOffsets[k.selectedCol] = k.selectedRow
	}

	// If selected row is below visible area, scroll down
	if k.selectedRow >= offset+maxVisible {
		k.scrollOffsets[k.selectedCol] = k.selectedRow - maxVisible + 1
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
				k.ensureSelectedVisible()
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

	// Use mobile view for narrow terminals
	if k.IsMobileMode() {
		return k.viewMobile()
	}

	return k.viewDesktop()
}

// viewDesktop renders the full kanban board with all columns side by side.
func (k *KanbanBoard) viewDesktop() string {
	// Calculate column width (subtract borders and gaps)
	numCols := len(k.columns)
	// Account for borders (2 chars per column) and gaps between columns (1 char each)
	availableWidth := k.width - (numCols * 2) - (numCols - 1)
	colWidth := availableWidth / numCols
	if colWidth < 20 {
		colWidth = 20
	}

	// Calculate available height for tasks
	// Subtract 2: 1 for header bar + 1 for bottom border of column
	colHeight := k.height - 2

	// Build columns
	var columnViews []string
	for colIdx, col := range k.columns {
		isSelectedCol := colIdx == k.selectedCol

		// Colored header bar at top of column
		// Width matches the column content width (will be inside the border)
		headerBarStyle := lipgloss.NewStyle().
			Width(colWidth).
			Background(col.Color).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Align(lipgloss.Center)

		headerText := fmt.Sprintf("%s %s (%d)", col.Icon, col.Title, len(col.Tasks))
		headerBar := headerBarStyle.Render(headerText)

		// Task cards - calculate how many fit
		// Non-selected cards: 2 lines content + 1 line border = 3 lines
		cardHeight := 3
		maxTasks := (colHeight - 3) / cardHeight // -3 for scroll indicators and padding
		if maxTasks < 1 {
			maxTasks = 1
		}

		// Get scroll offset for this column
		scrollOffset := 0
		if colIdx < len(k.scrollOffsets) {
			scrollOffset = k.scrollOffsets[colIdx]
		}

		// Clamp scroll offset to valid range
		maxOffset := len(col.Tasks) - maxTasks
		if maxOffset < 0 {
			maxOffset = 0
		}
		if scrollOffset > maxOffset {
			scrollOffset = maxOffset
		}
		if scrollOffset < 0 {
			scrollOffset = 0
		}

		// Calculate visible task range
		startIdx := scrollOffset
		endIdx := scrollOffset + maxTasks
		if endIdx > len(col.Tasks) {
			endIdx = len(col.Tasks)
		}

		var taskViews []string

		// Show "more above" indicator
		if scrollOffset > 0 {
			scrollIndicatorStyle := lipgloss.NewStyle().
				Foreground(ColorMuted).
				Width(colWidth - 2).
				Align(lipgloss.Center).
				Italic(true)
			taskViews = append(taskViews, scrollIndicatorStyle.Render(fmt.Sprintf("↑ %d more", scrollOffset)))
		}

		// Render visible tasks
		for i := startIdx; i < endIdx; i++ {
			task := col.Tasks[i]
			isSelected := isSelectedCol && i == k.selectedRow
			taskView := k.renderTaskCard(task, colWidth-2, isSelected)
			taskViews = append(taskViews, taskView)
		}

		// Show "more below" indicator (combined with hidden done count for Done column)
		remainingBelow := len(col.Tasks) - endIdx
		isDoneCol := col.Status == db.StatusDone
		hasHiddenDone := isDoneCol && k.hiddenDoneCount > 0

		if remainingBelow > 0 || hasHiddenDone {
			scrollIndicatorStyle := lipgloss.NewStyle().
				Foreground(ColorMuted).
				Width(colWidth - 2).
				Align(lipgloss.Center).
				Italic(true)

			var indicatorText string
			if remainingBelow > 0 && hasHiddenDone {
				indicatorText = fmt.Sprintf("↓ %d more (+%d older)", remainingBelow, k.hiddenDoneCount)
			} else if remainingBelow > 0 {
				indicatorText = fmt.Sprintf("↓ %d more", remainingBelow)
			} else {
				indicatorText = fmt.Sprintf("+%d older (Ctrl+P)", k.hiddenDoneCount)
			}
			taskViews = append(taskViews, scrollIndicatorStyle.Render(indicatorText))
		}

		// Empty column placeholder
		if len(col.Tasks) == 0 {
			emptyStyle := lipgloss.NewStyle().
				Foreground(ColorMuted).
				Width(colWidth - 2).
				Align(lipgloss.Center).
				Italic(true).
				MarginTop(1)
			taskViews = append(taskViews, emptyStyle.Render("No tasks"))
		}

		// Combine tasks with spacing
		taskContent := lipgloss.JoinVertical(lipgloss.Left, taskViews...)

		// Column container with border (rounded to match active task card style)
		_, highlightBorder := GetThemeBorderColors()
		borderColor := col.Color // Use column color for border
		borderStyle := lipgloss.RoundedBorder()
		if isSelectedCol {
			borderColor = highlightBorder
		}

		// Combine header and tasks, then wrap with border
		// Header is inside the border so they align perfectly
		fullContent := lipgloss.JoinVertical(lipgloss.Left,
			headerBar,
			taskContent,
		)

		colStyle := lipgloss.NewStyle().
			Width(colWidth).
			Height(colHeight). // Full height including header
			Border(borderStyle).
			BorderForeground(borderColor)

		columnView := colStyle.Render(fullContent)

		columnViews = append(columnViews, columnView)
	}

	// Join columns horizontally with gap
	gapStyle := lipgloss.NewStyle().Width(1)
	var parts []string
	for i, cv := range columnViews {
		parts = append(parts, cv)
		if i < len(columnViews)-1 {
			parts = append(parts, gapStyle.Render(" "))
		}
	}
	board := lipgloss.JoinHorizontal(lipgloss.Top, parts...)

	return board
}

// viewMobile renders a single-column view with tab navigation for narrow terminals.
func (k *KanbanBoard) viewMobile() string {
	// Render tab bar for column navigation
	tabBar := k.renderColumnTabs()

	// Use full width for single column (minus borders)
	colWidth := k.width - 2
	if colWidth < 20 {
		colWidth = 20
	}

	// Calculate available height for tasks (subtract tab bar height and column border)
	tabBarHeight := 2 // Tab bar takes 2 lines (content + margin)
	colHeight := k.height - tabBarHeight - 2

	col := k.columns[k.selectedCol]

	// Colored header bar at top of column
	headerBarStyle := lipgloss.NewStyle().
		Width(colWidth).
		Background(col.Color).
		Foreground(lipgloss.Color("#000000")).
		Bold(true).
		Align(lipgloss.Center)

	headerText := fmt.Sprintf("%s %s (%d)", col.Icon, col.Title, len(col.Tasks))
	headerBar := headerBarStyle.Render(headerText)

	// Task cards - calculate how many fit
	cardHeight := 3
	maxTasks := (colHeight - 3) / cardHeight
	if maxTasks < 1 {
		maxTasks = 1
	}

	// Get scroll offset for this column
	scrollOffset := 0
	if k.selectedCol < len(k.scrollOffsets) {
		scrollOffset = k.scrollOffsets[k.selectedCol]
	}

	// Clamp scroll offset to valid range
	maxOffset := len(col.Tasks) - maxTasks
	if maxOffset < 0 {
		maxOffset = 0
	}
	if scrollOffset > maxOffset {
		scrollOffset = maxOffset
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}

	// Calculate visible task range
	startIdx := scrollOffset
	endIdx := scrollOffset + maxTasks
	if endIdx > len(col.Tasks) {
		endIdx = len(col.Tasks)
	}

	var taskViews []string

	// Show "more above" indicator
	if scrollOffset > 0 {
		scrollIndicatorStyle := lipgloss.NewStyle().
			Foreground(ColorMuted).
			Width(colWidth - 2).
			Align(lipgloss.Center).
			Italic(true)
		taskViews = append(taskViews, scrollIndicatorStyle.Render(fmt.Sprintf("↑ %d more", scrollOffset)))
	}

	// Render visible tasks
	for i := startIdx; i < endIdx; i++ {
		task := col.Tasks[i]
		isSelected := i == k.selectedRow
		taskView := k.renderTaskCard(task, colWidth-2, isSelected)
		taskViews = append(taskViews, taskView)
	}

	// Show "more below" indicator (combined with hidden done count for Done column)
	remainingBelow := len(col.Tasks) - endIdx
	isDoneCol := col.Status == db.StatusDone
	hasHiddenDone := isDoneCol && k.hiddenDoneCount > 0

	if remainingBelow > 0 || hasHiddenDone {
		scrollIndicatorStyle := lipgloss.NewStyle().
			Foreground(ColorMuted).
			Width(colWidth - 2).
			Align(lipgloss.Center).
			Italic(true)

		var indicatorText string
		if remainingBelow > 0 && hasHiddenDone {
			indicatorText = fmt.Sprintf("↓ %d more (+%d older)", remainingBelow, k.hiddenDoneCount)
		} else if remainingBelow > 0 {
			indicatorText = fmt.Sprintf("↓ %d more", remainingBelow)
		} else {
			indicatorText = fmt.Sprintf("+%d older (Ctrl+P)", k.hiddenDoneCount)
		}
		taskViews = append(taskViews, scrollIndicatorStyle.Render(indicatorText))
	}

	// Empty column placeholder
	if len(col.Tasks) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(ColorMuted).
			Width(colWidth - 2).
			Align(lipgloss.Center).
			Italic(true).
			MarginTop(1)
		taskViews = append(taskViews, emptyStyle.Render("No tasks"))
	}

	// Combine tasks with spacing
	taskContent := lipgloss.JoinVertical(lipgloss.Left, taskViews...)

	// Column container with border
	_, highlightBorder := GetThemeBorderColors()
	borderStyle := lipgloss.RoundedBorder()

	fullContent := lipgloss.JoinVertical(lipgloss.Left,
		headerBar,
		taskContent,
	)

	colStyle := lipgloss.NewStyle().
		Width(colWidth).
		Height(colHeight).
		Border(borderStyle).
		BorderForeground(highlightBorder)

	columnView := colStyle.Render(fullContent)

	// Combine tab bar and column
	return lipgloss.JoinVertical(lipgloss.Left, tabBar, columnView)
}

// renderColumnTabs renders the tab bar for mobile column navigation.
func (k *KanbanBoard) renderColumnTabs() string {
	var tabs []string

	for i, col := range k.columns {
		isSelected := i == k.selectedCol

		// Calculate tab width to fit all tabs
		tabWidth := (k.width - len(k.columns) - 1) / len(k.columns)
		if tabWidth < 8 {
			tabWidth = 8
		}

		// Short column names for mobile
		name := col.Icon
		switch col.Title {
		case "Backlog":
			name += " Back"
		case "In Progress":
			name += " Prog"
		case "Blocked":
			name += " Block"
		case "Done":
			name += " Done"
		default:
			name += " " + col.Title
		}

		// Add task count
		name += fmt.Sprintf(" %d", len(col.Tasks))

		tabStyle := lipgloss.NewStyle().
			Width(tabWidth).
			Align(lipgloss.Center).
			Padding(0, 0)

		if isSelected {
			// Selected tab has background color matching column
			tabStyle = tabStyle.
				Background(col.Color).
				Foreground(lipgloss.Color("#000000")).
				Bold(true)
		} else {
			// Unselected tabs are dimmed
			tabStyle = tabStyle.
				Foreground(ColorMuted)
		}

		tabs = append(tabs, tabStyle.Render(name))
	}

	// Join tabs with separator
	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)

	// Add a subtle bottom border
	tabBarStyle := lipgloss.NewStyle().
		Width(k.width).
		MarginBottom(1)

	return tabBarStyle.Render(tabBar)
}

// renderTaskCard renders a single task card.
func (k *KanbanBoard) renderTaskCard(task *db.Task, width int, isSelected bool) string {
	if width < 10 {
		width = 10
	}

	var b strings.Builder

	// Task ID with status indicator
	statusIcon := StatusIcon(task.Status)
	statusColor := StatusColor(task.Status)
	statusStyle := lipgloss.NewStyle().Foreground(statusColor)
	b.WriteString(statusStyle.Render(statusIcon))
	b.WriteString(" ")
	b.WriteString(Dim.Render(fmt.Sprintf("#%d", task.ID)))

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

	// PR status indicator
	if prInfo := k.prInfo[task.ID]; prInfo != nil {
		b.WriteString(" ")
		b.WriteString(PRStatusBadge(prInfo))
	}

	// Running process indicator
	if k.HasRunningProcess(task.ID) {
		processStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("46")) // Bright green
		b.WriteString(" ")
		b.WriteString(processStyle.Render("●")) // Green dot for running process
	}

	// Schedule indicator - show if scheduled OR recurring
	if task.IsScheduled() || task.IsRecurring() {
		scheduleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // Orange for schedule
		var scheduleText string
		if task.IsScheduled() {
			scheduleText = formatScheduleTime(task.ScheduledAt.Time)
		}
		icon := "⏰"
		if task.IsRecurring() {
			icon = "🔁" // Use repeat icon to indicate recurring task
		}
		b.WriteString(" ")
		b.WriteString(scheduleStyle.Render(icon + scheduleText))
	}

	// Title (truncate if needed)
	title := task.Title
	maxTitleLen := width - 4
	if maxTitleLen < 10 {
		maxTitleLen = 10
	}
	if len(title) > maxTitleLen {
		title = title[:maxTitleLen-1] + "…"
	}

	idLine := b.String()
	titleLine := title

	// Card style with bottom margin for separation
	cardStyle := lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		MarginBottom(1)

	// Recurring tasks are de-emphasized visually (dimmed) when not selected
	isRecurring := task.IsRecurring()

	if isSelected {
		cardBg, cardFg := GetThemeCardColors()
		// Selected card has border and background
		cardStyle = cardStyle.
			Bold(true).
			Background(cardBg).
			Foreground(cardFg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(currentTheme.CardBorderHi)).
			MarginBottom(0) // Border adds visual separation
	} else if isRecurring {
		// Recurring tasks are dimmed to de-emphasize them
		cardStyle = cardStyle.
			Foreground(ColorMuted).
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(ColorMuted).
			MarginBottom(0)
	} else {
		// Non-selected cards have a subtle bottom border for separation
		cardStyle = cardStyle.
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(ColorMuted).
			MarginBottom(0)
	}

	content := idLine + "\n" + titleLine
	return cardStyle.Render(content)
}

// FocusColumn moves selection to a specific column by index.
func (k *KanbanBoard) FocusColumn(colIdx int) {
	if colIdx >= 0 && colIdx < len(k.columns) {
		k.selectedCol = colIdx
		k.clampSelection()
		k.ensureSelectedVisible()
	}
}

// ColumnCount returns the number of columns.
func (k *KanbanBoard) ColumnCount() int {
	return len(k.columns)
}

// GetTaskPosition returns the position of the currently selected task in its column.
// Returns (position, total) where position is 1-indexed, or (0, 0) if no task is selected.
func (k *KanbanBoard) GetTaskPosition() (int, int) {
	if k.selectedCol < 0 || k.selectedCol >= len(k.columns) {
		return 0, 0
	}
	col := k.columns[k.selectedCol]
	if len(col.Tasks) == 0 || k.selectedRow < 0 || k.selectedRow >= len(col.Tasks) {
		return 0, 0
	}
	return k.selectedRow + 1, len(col.Tasks) // 1-indexed position
}

// HandleClick handles a mouse click at the given coordinates.
// Returns the clicked task if a task card was clicked, nil otherwise.
// Also updates the selection to the clicked task.
func (k *KanbanBoard) HandleClick(x, y int) *db.Task {
	if k.width < 40 || k.height < 10 {
		return nil
	}

	// Use mobile click handling for narrow terminals
	if k.IsMobileMode() {
		return k.handleClickMobile(x, y)
	}

	return k.handleClickDesktop(x, y)
}

// handleClickDesktop handles clicks in desktop (multi-column) mode.
func (k *KanbanBoard) handleClickDesktop(x, y int) *db.Task {
	// Calculate column layout (same as viewDesktop())
	numCols := len(k.columns)
	availableWidth := k.width - (numCols * 2) - (numCols - 1)
	colWidth := availableWidth / numCols
	if colWidth < 20 {
		colWidth = 20
	}

	// Each column has: 1 border + colWidth content + 1 border = colWidth + 2
	// Columns are joined with no gap between them in lipgloss.JoinHorizontal
	colTotalWidth := colWidth + 2

	// Determine which column was clicked
	colIdx := x / colTotalWidth
	if colIdx >= numCols {
		colIdx = numCols - 1
	}
	if colIdx < 0 {
		return nil
	}

	// Check if click is within column bounds (not on border)
	colStartX := colIdx * colTotalWidth
	relX := x - colStartX
	if relX < 1 || relX > colWidth {
		// Clicked on border
		return nil
	}

	// Calculate Y position within column
	// Column structure: 1 border line at top, then header (1 line), then task cards
	headerLines := 1 // Header bar is 1 line with no margin
	taskCardHeight := 3

	// relY is position within the column content (after top border)
	relY := y - 1 // -1 for top border

	// Skip header area
	taskAreaY := relY - headerLines
	if taskAreaY < 0 {
		// Clicked on header
		return nil
	}

	// Calculate which task was clicked
	col := k.columns[colIdx]
	colHeight := k.height
	maxTasks := (colHeight - 3) / taskCardHeight // -3 for header bar and minimal padding
	if maxTasks < 1 {
		maxTasks = 1
	}

	// Get scroll offset for this column
	scrollOffset := 0
	if colIdx < len(k.scrollOffsets) {
		scrollOffset = k.scrollOffsets[colIdx]
	}

	// Account for scroll indicator line when scrolled
	if scrollOffset > 0 {
		taskAreaY -= 1 // Subtract 1 for the "↑ N more" indicator line
		if taskAreaY < 0 {
			// Clicked on scroll indicator
			return nil
		}
	}

	visibleTaskIdx := taskAreaY / taskCardHeight
	if visibleTaskIdx >= maxTasks {
		return nil
	}

	// Convert visible index to actual task index
	taskIdx := scrollOffset + visibleTaskIdx
	if taskIdx >= len(col.Tasks) {
		return nil
	}

	// Update selection
	k.selectedCol = colIdx
	k.selectedRow = taskIdx

	return col.Tasks[taskIdx]
}

// handleClickMobile handles clicks in mobile (single-column) mode.
func (k *KanbanBoard) handleClickMobile(x, y int) *db.Task {
	// Check if click is on the tab bar (first 2 lines)
	tabBarHeight := 2
	if y < tabBarHeight {
		// Clicked on tab bar - determine which tab
		numCols := len(k.columns)
		tabWidth := (k.width - numCols - 1) / numCols
		if tabWidth < 8 {
			tabWidth = 8
		}

		colIdx := x / tabWidth
		if colIdx >= numCols {
			colIdx = numCols - 1
		}
		if colIdx >= 0 && colIdx < numCols {
			k.selectedCol = colIdx
			k.clampSelection()
			k.ensureSelectedVisible()
		}
		return nil
	}

	// Click is in the column content area
	// Column layout: tab bar (2 lines), then border (1 line), header (1 line), task cards
	colHeight := k.height - tabBarHeight - 2
	headerLines := 1 // Header bar is 1 line with no margin
	taskCardHeight := 3

	// relY is position within the column content (after tab bar and top border)
	relY := y - tabBarHeight - 1 // -1 for top border

	// Skip header area
	taskAreaY := relY - headerLines
	if taskAreaY < 0 {
		// Clicked on header
		return nil
	}

	// Calculate which task was clicked
	col := k.columns[k.selectedCol]
	maxTasks := (colHeight - 3) / taskCardHeight
	if maxTasks < 1 {
		maxTasks = 1
	}

	// Get scroll offset for this column
	scrollOffset := 0
	if k.selectedCol < len(k.scrollOffsets) {
		scrollOffset = k.scrollOffsets[k.selectedCol]
	}

	// Account for scroll indicator line when scrolled
	if scrollOffset > 0 {
		taskAreaY -= 1 // Subtract 1 for the "↑ N more" indicator line
		if taskAreaY < 0 {
			// Clicked on scroll indicator
			return nil
		}
	}

	visibleTaskIdx := taskAreaY / taskCardHeight
	if visibleTaskIdx >= maxTasks {
		return nil
	}

	// Convert visible index to actual task index
	taskIdx := scrollOffset + visibleTaskIdx
	if taskIdx >= len(col.Tasks) {
		return nil
	}

	// Update selection
	k.selectedRow = taskIdx

	return col.Tasks[taskIdx]
}

// formatScheduleTime formats a scheduled time for display.
func formatScheduleTime(t time.Time) string {
	now := time.Now()
	diff := t.Sub(now)

	// If in the past
	if diff < 0 {
		return "overdue"
	}

	// If less than an hour away
	if diff < time.Hour {
		mins := int(diff.Minutes())
		if mins <= 0 {
			return "now"
		}
		return fmt.Sprintf("%dm", mins)
	}

	// If less than 24 hours away
	if diff < 24*time.Hour {
		hours := int(diff.Hours())
		return fmt.Sprintf("%dh", hours)
	}

	// If today or tomorrow
	if t.Day() == now.Day() && t.Month() == now.Month() && t.Year() == now.Year() {
		return t.Format("3:04pm")
	}
	tomorrow := now.AddDate(0, 0, 1)
	if t.Day() == tomorrow.Day() && t.Month() == tomorrow.Month() && t.Year() == tomorrow.Year() {
		return "tmrw " + t.Format("3pm")
	}

	// Otherwise show date
	if t.Year() == now.Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 '06")
}
