package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

// SortColumn identifies which task field the list view is sorted by.
type SortColumn int

const (
	SortByUpdated SortColumn = iota
	SortByCreated
	SortByID
	SortByStatus
	SortByTitle
	SortByProject
)

// sortColumns is the ordered set of columns the user can sort by (cycled with ←/→).
var sortColumns = []SortColumn{
	SortByUpdated,
	SortByCreated,
	SortByID,
	SortByStatus,
	SortByTitle,
	SortByProject,
}

// label returns the short header label for a sort column.
func (s SortColumn) label() string {
	switch s {
	case SortByUpdated:
		return "Updated"
	case SortByCreated:
		return "Created"
	case SortByID:
		return "#"
	case SortByStatus:
		return "Status"
	case SortByTitle:
		return "Title"
	case SortByProject:
		return "Project"
	default:
		return ""
	}
}

// defaultDesc returns the natural sort direction for a column when first selected.
// Time-based and ID columns default to descending (newest/highest first); text
// columns default to ascending (A→Z).
func (s SortColumn) defaultDesc() bool {
	switch s {
	case SortByUpdated, SortByCreated, SortByID:
		return true
	default:
		return false
	}
}

// statusRank orders statuses for the "Status" sort column so active work floats
// to the top and completed work sinks to the bottom.
func statusRank(status string) int {
	switch status {
	case db.StatusProcessing:
		return 0
	case db.StatusBlocked:
		return 1
	case db.StatusQueued:
		return 2
	case db.StatusBacklog:
		return 3
	case db.StatusDone:
		return 4
	case db.StatusArchived:
		return 5
	default:
		return 6
	}
}

// statusFilterOption represents a discrete status filter the user can cycle through.
type statusFilterOption struct {
	Label    string
	Statuses []string // empty == match all
}

var statusFilterOptions = []statusFilterOption{
	{Label: "All"},
	{Label: "Backlog", Statuses: []string{db.StatusBacklog}},
	{Label: "In Progress", Statuses: []string{db.StatusQueued, db.StatusProcessing}},
	{Label: "Blocked", Statuses: []string{db.StatusBlocked}},
	{Label: "Done", Statuses: []string{db.StatusDone}},
}

// dateFilterOption represents a discrete "updated within" filter.
type dateFilterOption struct {
	Label  string
	Within time.Duration // 0 == match all
}

var dateFilterOptions = []dateFilterOption{
	{Label: "Any time"},
	{Label: "Today", Within: 24 * time.Hour},
	{Label: "7 days", Within: 7 * 24 * time.Hour},
	{Label: "30 days", Within: 30 * 24 * time.Hour},
}

// ListView renders tasks as a sortable, filterable table — an alternative to the
// kanban board. It keeps its own selection, sort, and filter state but reads the
// same task data the board does.
type ListView struct {
	allTasks []*db.Task // tasks as provided (already passes the app-level "/" filter)
	rows     []*db.Task // filtered + sorted tasks actually displayed

	sortColIdx  int  // index into sortColumns
	sortDesc    bool // current sort direction
	statusIdx   int  // index into statusFilterOptions
	projectIdx  int  // index into the projects() slice (0 == All)
	dateIdx     int  // index into dateFilterOptions
	selectedRow int
	scrollOff   int

	width  int
	height int

	prInfo            map[int64]*github.PRInfo
	runningProcesses  map[int64]bool
	tasksNeedingInput map[int64]bool
	blockedByDeps     map[int64]int
}

// NewListView creates a new list view.
func NewListView(width, height int) *ListView {
	return &ListView{
		width:             width,
		height:            height,
		sortDesc:          sortColumns[0].defaultDesc(),
		prInfo:            make(map[int64]*github.PRInfo),
		runningProcesses:  make(map[int64]bool),
		tasksNeedingInput: make(map[int64]bool),
		blockedByDeps:     make(map[int64]int),
	}
}

// SetSize updates the list dimensions.
func (l *ListView) SetSize(width, height int) {
	if l == nil {
		return
	}
	l.width = width
	l.height = height
	l.ensureSelectedVisible()
}

// SetTasks updates the tasks shown in the list, preserving the selected task by ID.
func (l *ListView) SetTasks(tasks []*db.Task) {
	if l == nil {
		return
	}
	var selectedID int64
	if t := l.SelectedTask(); t != nil {
		selectedID = t.ID
	}
	l.allTasks = tasks
	l.rebuild()
	if selectedID != 0 {
		l.SelectTask(selectedID)
	}
}

// SetPRInfo updates the PR info for a task.
func (l *ListView) SetPRInfo(taskID int64, info *github.PRInfo) {
	if l == nil {
		return
	}
	if l.prInfo == nil {
		l.prInfo = make(map[int64]*github.PRInfo)
	}
	l.prInfo[taskID] = info
}

// SetRunningProcesses updates the map of tasks with running shell processes.
func (l *ListView) SetRunningProcesses(running map[int64]bool) {
	if l == nil {
		return
	}
	l.runningProcesses = running
}

// SetTasksNeedingInput updates the map of tasks waiting for user input.
func (l *ListView) SetTasksNeedingInput(needsInput map[int64]bool) {
	if l == nil {
		return
	}
	l.tasksNeedingInput = needsInput
}

// SetBlockedByDeps updates the map of tasks blocked by dependencies.
func (l *ListView) SetBlockedByDeps(blockedByDeps map[int64]int) {
	if l == nil {
		return
	}
	l.blockedByDeps = blockedByDeps
}

// projects returns the list of project filter labels: "All" followed by the
// unique project names present in the current task set (sorted).
func (l *ListView) projects() []string {
	seen := make(map[string]bool)
	var names []string
	for _, t := range l.allTasks {
		if t.Project != "" && !seen[t.Project] {
			seen[t.Project] = true
			names = append(names, t.Project)
		}
	}
	sort.Strings(names)
	return append([]string{"All"}, names...)
}

// rebuild applies the active filters and sort to produce the visible rows.
func (l *ListView) rebuild() {
	projectNames := l.projects()
	if l.projectIdx >= len(projectNames) {
		l.projectIdx = 0
	}
	statusOpt := statusFilterOptions[l.statusIdx]
	dateOpt := dateFilterOptions[l.dateIdx]
	now := time.Now()

	var rows []*db.Task
	for _, t := range l.allTasks {
		if !statusMatches(t, statusOpt) {
			continue
		}
		if l.projectIdx > 0 && t.Project != projectNames[l.projectIdx] {
			continue
		}
		if dateOpt.Within > 0 {
			if t.UpdatedAt.IsZero() || now.Sub(t.UpdatedAt.Time) > dateOpt.Within {
				continue
			}
		}
		rows = append(rows, t)
	}

	l.sortRows(rows)
	l.rows = rows
	l.clampSelection()
	l.ensureSelectedVisible()
}

// statusMatches reports whether a task matches a status filter option.
func statusMatches(t *db.Task, opt statusFilterOption) bool {
	if len(opt.Statuses) == 0 {
		return true
	}
	for _, s := range opt.Statuses {
		if t.Status == s {
			return true
		}
	}
	return false
}

// sortRows sorts the given rows by the active column and direction. Pinned tasks
// always float to the top regardless of sort, mirroring the board's behaviour.
func (l *ListView) sortRows(rows []*db.Task) {
	col := sortColumns[l.sortColIdx]
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Pinned != b.Pinned {
			return a.Pinned // pinned first
		}
		less := lessByColumn(a, b, col)
		if l.sortDesc {
			return !less
		}
		return less
	})
}

// lessByColumn reports whether task a sorts before task b for the given column
// in ascending order.
func lessByColumn(a, b *db.Task, col SortColumn) bool {
	switch col {
	case SortByID:
		return a.ID < b.ID
	case SortByStatus:
		ra, rb := statusRank(a.Status), statusRank(b.Status)
		if ra != rb {
			return ra < rb
		}
		return a.ID < b.ID
	case SortByTitle:
		ta, tb := strings.ToLower(a.Title), strings.ToLower(b.Title)
		if ta != tb {
			return ta < tb
		}
		return a.ID < b.ID
	case SortByProject:
		pa, pb := strings.ToLower(a.Project), strings.ToLower(b.Project)
		if pa != pb {
			return pa < pb
		}
		return a.ID < b.ID
	case SortByCreated:
		if !a.CreatedAt.Time.Equal(b.CreatedAt.Time) {
			return a.CreatedAt.Time.Before(b.CreatedAt.Time)
		}
		return a.ID < b.ID
	case SortByUpdated:
		fallthrough
	default:
		if !a.UpdatedAt.Time.Equal(b.UpdatedAt.Time) {
			return a.UpdatedAt.Time.Before(b.UpdatedAt.Time)
		}
		return a.ID < b.ID
	}
}

// --- Navigation -----------------------------------------------------------

// MoveUp moves the selection up one row, wrapping to the bottom.
func (l *ListView) MoveUp() {
	if len(l.rows) == 0 {
		return
	}
	if l.selectedRow > 0 {
		l.selectedRow--
	} else {
		l.selectedRow = len(l.rows) - 1
	}
	l.ensureSelectedVisible()
}

// MoveDown moves the selection down one row, wrapping to the top.
func (l *ListView) MoveDown() {
	if len(l.rows) == 0 {
		return
	}
	if l.selectedRow < len(l.rows)-1 {
		l.selectedRow++
	} else {
		l.selectedRow = 0
	}
	l.ensureSelectedVisible()
}

// NextSortColumn advances to the next sortable column (←/→), resetting the
// direction to that column's natural default.
func (l *ListView) NextSortColumn() {
	l.sortColIdx = (l.sortColIdx + 1) % len(sortColumns)
	l.sortDesc = sortColumns[l.sortColIdx].defaultDesc()
	l.rebuild()
}

// PrevSortColumn moves to the previous sortable column.
func (l *ListView) PrevSortColumn() {
	l.sortColIdx = (l.sortColIdx - 1 + len(sortColumns)) % len(sortColumns)
	l.sortDesc = sortColumns[l.sortColIdx].defaultDesc()
	l.rebuild()
}

// ToggleSortDirection flips between ascending and descending order.
func (l *ListView) ToggleSortDirection() {
	l.sortDesc = !l.sortDesc
	l.rebuild()
}

// CycleStatusFilter advances the status filter by dir (+1 / -1).
func (l *ListView) CycleStatusFilter(dir int) {
	n := len(statusFilterOptions)
	l.statusIdx = (l.statusIdx + dir + n) % n
	l.rebuild()
}

// CycleProjectFilter advances the project filter by dir (+1 / -1).
func (l *ListView) CycleProjectFilter(dir int) {
	n := len(l.projects())
	if n == 0 {
		return
	}
	l.projectIdx = (l.projectIdx + dir + n) % n
	l.rebuild()
}

// CycleDateFilter advances the "updated within" filter by dir (+1 / -1).
func (l *ListView) CycleDateFilter(dir int) {
	n := len(dateFilterOptions)
	l.dateIdx = (l.dateIdx + dir + n) % n
	l.rebuild()
}

// SelectedTask returns the currently selected task, or nil.
func (l *ListView) SelectedTask() *db.Task {
	if l.selectedRow < 0 || l.selectedRow >= len(l.rows) {
		return nil
	}
	return l.rows[l.selectedRow]
}

// SelectTask selects the row for the given task ID, returning true if found.
func (l *ListView) SelectTask(id int64) bool {
	for i, t := range l.rows {
		if t.ID == id {
			l.selectedRow = i
			l.ensureSelectedVisible()
			return true
		}
	}
	return false
}

// IsEmpty reports whether there are no visible rows.
func (l *ListView) IsEmpty() bool {
	return len(l.rows) == 0
}

// SelectVisibleRow selects the nth (1-indexed) currently visible row and returns
// the task, or nil if there is no such row.
func (l *ListView) SelectVisibleRow(n int) *db.Task {
	if n < 1 {
		return nil
	}
	idx := l.scrollOff + n - 1
	if idx < 0 || idx >= len(l.rows) {
		return nil
	}
	l.selectedRow = idx
	l.ensureSelectedVisible()
	return l.rows[idx]
}

// HasPrevTask reports whether a row exists above the current selection.
func (l *ListView) HasPrevTask() bool {
	return len(l.rows) > 1 && l.selectedRow > 0
}

// HasNextTask reports whether a row exists below the current selection.
func (l *ListView) HasNextTask() bool {
	return len(l.rows) > 1 && l.selectedRow < len(l.rows)-1
}

func (l *ListView) clampSelection() {
	if l.selectedRow >= len(l.rows) {
		l.selectedRow = len(l.rows) - 1
	}
	if l.selectedRow < 0 {
		l.selectedRow = 0
	}
}

// List layout constants. Each visible row gets a blank spacer line for breathing
// room (matching the board's card rhythm), so a row occupies listRowHeight lines.
const (
	listRowHeight   = 2 // one content line + one blank spacer
	listChromeLines = 6 // chips(2) + blank(1) + header content+rule(2) + blank(1)
	listFooterLines = 1 // scroll indicator
	listHPadding    = 2 // left/right padding inside the list
)

// visibleRowCount returns how many task rows fit in the current height.
func (l *ListView) visibleRowCount() int {
	avail := l.height - listChromeLines - listFooterLines
	if avail < listRowHeight {
		return 1
	}
	return avail / listRowHeight
}

func (l *ListView) ensureSelectedVisible() {
	capacity := l.visibleRowCount()
	if l.selectedRow < l.scrollOff {
		l.scrollOff = l.selectedRow
	}
	if l.selectedRow >= l.scrollOff+capacity {
		l.scrollOff = l.selectedRow - capacity + 1
	}
	maxOff := len(l.rows) - capacity
	if maxOff < 0 {
		maxOff = 0
	}
	if l.scrollOff > maxOff {
		l.scrollOff = maxOff
	}
	if l.scrollOff < 0 {
		l.scrollOff = 0
	}
}

// --- Rendering ------------------------------------------------------------

// listColumns describes the rendered table layout for a given width.
type listColumns struct {
	id      int
	status  int
	project int
	pr      int
	updated int
	created int
	title   int // remaining flexible space
}

// computeColumns returns column widths responsive to the available width.
func (l *ListView) computeColumns() listColumns {
	w := l.width - 2*listHPadding // horizontal padding on both sides
	c := listColumns{id: 5, status: 13, project: 12, pr: 4, updated: 9, created: 9}

	// Progressively drop optional columns on narrow terminals.
	fixed := func() int { return c.id + c.status + c.project + c.pr + c.updated + c.created }
	gaps := 7 // one space between each of the 7 columns
	if w-fixed()-gaps < 16 {
		c.created = 0 // drop Created
	}
	if w-c.id-c.status-c.project-c.pr-c.updated-c.created-6 < 16 {
		c.project = 0 // drop Project
	}
	if w-c.id-c.status-c.pr-c.updated-c.created-5 < 14 {
		c.pr = 0 // drop PR
	}
	// Title takes whatever remains.
	used := c.id + c.status + c.project + c.pr + c.updated + c.created
	visibleCols := 2 // id + title always present
	for _, x := range []int{c.status, c.project, c.pr, c.updated, c.created} {
		if x > 0 {
			visibleCols++
		}
	}
	c.title = w - used - (visibleCols - 1)
	if c.title < 10 {
		c.title = 10
	}
	return c
}

// View renders the list view.
func (l *ListView) View() string {
	if l.width < 30 || l.height < 8 {
		return lipgloss.Place(l.width, l.height, lipgloss.Center, lipgloss.Center, "Terminal too small")
	}

	cols := l.computeColumns()
	var lines []string
	lines = append(lines, l.renderFilterChips())
	lines = append(lines, "") // breathing room under the chips
	lines = append(lines, l.renderColumnHeader(cols))
	lines = append(lines, "") // breathing room under the header rule

	capacity := l.visibleRowCount()
	if len(l.rows) == 0 {
		empty := lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true).
			Width(l.width).
			Align(lipgloss.Center).
			MarginTop(1).
			Render("No tasks match the current filters")
		lines = append(lines, empty)
	} else {
		start := l.scrollOff
		end := start + capacity
		if end > len(l.rows) {
			end = len(l.rows)
		}
		for i := start; i < end; i++ {
			lines = append(lines, l.renderRow(l.rows[i], cols, i == l.selectedRow))
			lines = append(lines, "") // blank spacer between rows
		}
		// Scroll indicator
		hiddenAbove := start
		hiddenBelow := len(l.rows) - end
		if hiddenAbove > 0 || hiddenBelow > 0 {
			indicator := fmt.Sprintf("%s %d–%d of %d", IconArrowDown(), start+1, end, len(l.rows))
			lines = append(lines, lipgloss.NewStyle().
				Foreground(ColorMuted).
				Italic(true).
				Width(l.width).
				Align(lipgloss.Center).
				Render(indicator))
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(l.width).Render(content)
}

// renderFilterChips renders the sort + filter status bar at the top of the list.
func (l *ListView) renderFilterChips() string {
	chip := func(label, value string, active bool) string {
		labelStyle := lipgloss.NewStyle().Foreground(ColorMuted)
		valStyle := lipgloss.NewStyle().Bold(true)
		if active {
			valStyle = valStyle.Foreground(ColorPrimary)
		} else {
			valStyle = valStyle.Foreground(ColorSecondary)
		}
		return labelStyle.Render(label+": ") + valStyle.Render(value)
	}

	arrow := IconArrowUp()
	if l.sortDesc {
		arrow = IconArrowDown()
	}
	sortVal := sortColumns[l.sortColIdx].label() + " " + arrow

	projectName := "All"
	projects := l.projects()
	if l.projectIdx < len(projects) {
		projectName = projects[l.projectIdx]
	}

	chips := []string{
		chip("Sort", sortVal, true),
		chip("Status", statusFilterOptions[l.statusIdx].Label, l.statusIdx != 0),
		chip("Project", projectName, l.projectIdx != 0),
		chip("Updated", dateFilterOptions[l.dateIdx].Label, l.dateIdx != 0),
		lipgloss.NewStyle().Foreground(ColorMuted).Render(fmt.Sprintf("(%d)", len(l.rows))),
	}
	chipLine := strings.Join(chips, lipgloss.NewStyle().Foreground(ColorMuted).Render("  •  "))

	hint := lipgloss.NewStyle().Foreground(ColorMuted).Italic(true).Render(
		"←→ sort  ⎵ reverse  [ ] project  { } status  < > date  v board")

	barStyle := lipgloss.NewStyle().Width(l.width).Padding(0, listHPadding)
	return barStyle.Render(lipgloss.JoinVertical(lipgloss.Left, chipLine, hint))
}

// renderColumnHeader renders the underlined column header row, highlighting the
// active sort column.
func (l *ListView) renderColumnHeader(cols listColumns) string {
	activeCol := sortColumns[l.sortColIdx]
	arrow := IconArrowUp()
	if l.sortDesc {
		arrow = IconArrowDown()
	}

	headerCell := func(text string, width int, sortKey SortColumn, sortable bool, right bool) string {
		if width <= 0 {
			return ""
		}
		style := lipgloss.NewStyle().Width(width).Bold(true)
		label := text
		if sortable && sortKey == activeCol {
			style = style.Foreground(ColorPrimary)
			label = text + arrow
		} else {
			style = style.Foreground(ColorMuted)
		}
		if right {
			style = style.Align(lipgloss.Right)
		}
		return style.Render(truncate(label, width))
	}

	var cells []string
	cells = append(cells, headerCell("#", cols.id, SortByID, true, false))
	cells = append(cells, headerCell("Status", cols.status, SortByStatus, true, false))
	cells = append(cells, headerCell("Title", cols.title, SortByTitle, true, false))
	if cols.project > 0 {
		cells = append(cells, headerCell("Project", cols.project, SortByProject, true, false))
	}
	if cols.pr > 0 {
		cells = append(cells, headerCell("PR", cols.pr, 0, false, false))
	}
	if cols.updated > 0 {
		cells = append(cells, headerCell("Updated", cols.updated, SortByUpdated, true, true))
	}
	if cols.created > 0 {
		cells = append(cells, headerCell("Created", cols.created, SortByCreated, true, true))
	}

	row := joinCells(cells)
	return lipgloss.NewStyle().
		Width(l.width).
		Padding(0, listHPadding).
		BorderBottom(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(ColorMuted).
		Render(row)
}

// renderRow renders a single task row.
func (l *ListView) renderRow(task *db.Task, cols listColumns, selected bool) string {
	idStr := fmt.Sprintf("#%d", task.ID)

	statusIcon := StatusIcon(task.Status)
	statusText := statusIcon + " " + statusLabel(task.Status)

	title := task.Title
	if task.Pinned {
		title = IconPin() + " " + title
	}
	if l.blockedByDeps[task.ID] > 0 {
		title = "🔒 " + title
	}

	projectStr := task.Project

	prStr := ""
	if pr := l.prInfo[task.ID]; pr != nil {
		prStr = PRStatusBadge(pr)
	}

	// Activity indicator overrides the updated column for live tasks.
	updatedStr := relativeTime(task.UpdatedAt.Time)
	if l.runningProcesses[task.ID] {
		updatedStr = "● " + updatedStr
	} else if l.tasksNeedingInput[task.ID] {
		updatedStr = "! " + updatedStr
	}
	createdStr := relativeTime(task.CreatedAt.Time)

	cell := func(text string, width int, right bool) string {
		if width <= 0 {
			return ""
		}
		style := lipgloss.NewStyle().Width(width)
		if right {
			style = style.Align(lipgloss.Right)
		}
		return style.Render(truncate(text, width))
	}

	// Column-specific colouring (skipped for selected rows, which invert).
	idCell := cell(idStr, cols.id, false)
	statusCell := cell(statusText, cols.status, false)
	titleCell := cell(title, cols.title, false)
	projectCell := cell(projectStr, cols.project, false)
	prCell := cell(prStr, cols.pr, false)
	updatedCell := cell(updatedStr, cols.updated, true)
	createdCell := cell(createdStr, cols.created, true)

	if !selected {
		statusCell = lipgloss.NewStyle().Width(cols.status).Foreground(StatusColor(task.Status)).Render(truncate(statusText, cols.status))
		idCell = lipgloss.NewStyle().Width(cols.id).Foreground(ColorMuted).Render(truncate(idStr, cols.id))
		if cols.project > 0 && projectStr != "" {
			projectCell = lipgloss.NewStyle().Width(cols.project).Foreground(ProjectColor(task.Project)).Render(truncate(projectStr, cols.project))
		}
		if cols.updated > 0 {
			updatedCell = lipgloss.NewStyle().Width(cols.updated).Align(lipgloss.Right).Foreground(ColorMuted).Render(truncate(updatedStr, cols.updated))
		}
		if cols.created > 0 {
			createdCell = lipgloss.NewStyle().Width(cols.created).Align(lipgloss.Right).Foreground(ColorMuted).Render(truncate(createdStr, cols.created))
		}
	}

	var cells []string
	cells = append(cells, idCell, statusCell, titleCell)
	if cols.project > 0 {
		cells = append(cells, projectCell)
	}
	if cols.pr > 0 {
		cells = append(cells, prCell)
	}
	if cols.updated > 0 {
		cells = append(cells, updatedCell)
	}
	if cols.created > 0 {
		cells = append(cells, createdCell)
	}

	row := joinCells(cells)

	rowStyle := lipgloss.NewStyle().Width(l.width).Padding(0, listHPadding)
	if selected {
		cardBg, cardFg := GetThemeCardColors()
		rowStyle = rowStyle.Background(cardBg).Foreground(cardFg).Bold(true)
	} else if l.tasksNeedingInput[task.ID] {
		rowStyle = rowStyle.Foreground(ColorWarning)
	}
	return rowStyle.Render(row)
}

// HandleClick maps a click at (x, y) to a task row, updating the selection.
// Returns the clicked task, or nil if the click was not on a row.
func (l *ListView) HandleClick(x, y int) *db.Task {
	// The chrome above the rows occupies listChromeLines; each visible row then
	// occupies listRowHeight lines (content + spacer).
	rowY := y - listChromeLines
	if rowY < 0 {
		return nil
	}
	displayed := rowY / listRowHeight
	idx := l.scrollOff + displayed
	if idx < 0 || idx >= len(l.rows) {
		return nil
	}
	l.selectedRow = idx
	l.ensureSelectedVisible()
	return l.rows[idx]
}

// debugState returns a snapshot of the list view's sort/filter state and visible
// rows for the debug "Text DOM" used by the QA harness.
func (l *ListView) debugState() *DebugList {
	projectName := "All"
	projects := l.projects()
	if l.projectIdx < len(projects) {
		projectName = projects[l.projectIdx]
	}
	d := &DebugList{
		Sort:          sortColumns[l.sortColIdx].label(),
		SortDesc:      l.sortDesc,
		StatusFilter:  statusFilterOptions[l.statusIdx].Label,
		ProjectFilter: projectName,
		DateFilter:    dateFilterOptions[l.dateIdx].Label,
	}
	for i, t := range l.rows {
		d.Rows = append(d.Rows, DebugTask{
			ID:       t.ID,
			Title:    t.Title,
			Status:   t.Status,
			Project:  t.Project,
			Selected: i == l.selectedRow,
			Pinned:   t.Pinned,
		})
	}
	return d
}

// --- helpers --------------------------------------------------------------

// joinCells joins rendered cells with a single space separator, skipping empties.
func joinCells(cells []string) string {
	var nonEmpty []string
	for _, c := range cells {
		if c != "" {
			nonEmpty = append(nonEmpty, c)
		}
	}
	return strings.Join(nonEmpty, " ")
}

// truncate shortens s to fit width display columns, adding an ellipsis.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	// Trim rune by rune until it fits (accounts for wide runes).
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// statusLabel returns a human-readable label for a task status.
func statusLabel(status string) string {
	switch status {
	case db.StatusBacklog:
		return "Backlog"
	case db.StatusQueued:
		return "Queued"
	case db.StatusProcessing:
		return "Running"
	case db.StatusBlocked:
		return "Blocked"
	case db.StatusDone:
		return "Done"
	case db.StatusArchived:
		return "Archived"
	default:
		return status
	}
}

// relativeTime formats a timestamp as a short relative string (e.g. "3h", "2d").
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < 0:
		return t.Format("Jan 2")
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
}
