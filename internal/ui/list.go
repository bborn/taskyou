package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
)

// List mode is the board's second face: instead of four status columns, one
// flat, scannable line per task. It exists for the question the kanban answers
// badly — "what am I actually working on right now?" — where the interesting
// tasks are spread across In Progress and Blocked and each card costs four
// lines. Paired with a saved view ("status:in-progress status:blocked"), the
// list is the whole answer on one screen.
//
// The two modes share one selection: list mode keeps its own row index, but
// SetListMode carries the selected task across, so toggling never loses your
// place and every action in app.go keeps working through SelectedTask().

// listRowLines is how many lines one task occupies at each density: one for a
// compact row, and four for a relaxed one (id, title, sub-line, spacer). The
// scroll maths needs it to know how many tasks fit on screen.
func (k *KanbanBoard) listRowLines() int {
	if k.listOpts.Density == DensityRelaxed {
		return 4
	}
	return 1
}

// listStatusRank orders the flat list by "how much this needs me now".
// Anything unlisted sorts last.
var listStatusRank = map[string]int{
	db.StatusProcessing: 0,
	db.StatusBlocked:    1,
	db.StatusQueued:     2,
	db.StatusBacklog:    3,
	db.StatusDone:       4,
	db.StatusArchived:   5,
}

// IsListMode reports whether the board renders as a flat list.
func (k *KanbanBoard) IsListMode() bool { return k.listMode }

// SetListMode switches between the kanban and the flat list, keeping the
// selected task selected across the switch.
func (k *KanbanBoard) SetListMode(on bool) {
	if k.listMode == on {
		return
	}
	selected := k.SelectedTask()
	k.listMode = on
	k.invalidateView()
	if selected != nil {
		k.SelectTask(selected.ID)
		return
	}
	k.clampListSelection()
}

// SetListTitle sets the label shown in the list header — the active saved view
// or filter, so the list always says what it is a list of.
func (k *KanbanBoard) SetListTitle(title string) {
	if k.listTitle == title {
		return
	}
	k.listTitle = title
	k.invalidateView()
}

// invalidateView drops the memoized render so the next View() rebuilds. Used
// by state that the render signature covers but that changes outside SetTasks.
func (k *KanbanBoard) invalidateView() { k.cachedViewOK = false }

// rebuildListTasks flattens the board's tasks into display order. What that
// order is — the sections, and the sort inside them — is ListOptions' business,
// not the board's.
func (k *KanbanBoard) rebuildListTasks() {
	k.listTasks = k.listOpts.Arrange(k.allTasks)
	k.clampListSelection()
}

// SetListOptions changes how the list is arranged and re-sorts it, keeping the
// selected task selected across the rearrangement.
func (k *KanbanBoard) SetListOptions(opts ListOptions) {
	opts = opts.Normalize()
	if k.listOpts == opts {
		return
	}
	var selectedID int64
	if t := k.selectedListTask(); t != nil {
		selectedID = t.ID
	}
	k.listOpts = opts
	k.rebuildListTasks()
	if selectedID != 0 {
		k.selectListTask(selectedID)
	}
	k.invalidateView()
}

// ListOptions returns the current arrangement.
func (k *KanbanBoard) ListOptions() ListOptions { return k.listOpts }

func listRank(status string) int {
	if r, ok := listStatusRank[status]; ok {
		return r
	}
	return len(listStatusRank)
}

// ListTasks returns the tasks in list order. Exported for tests and for the
// header counts.
func (k *KanbanBoard) ListTasks() []*db.Task { return k.listTasks }

func (k *KanbanBoard) clampListSelection() {
	if k.listRow >= len(k.listTasks) {
		k.listRow = len(k.listTasks) - 1
	}
	if k.listRow < 0 {
		k.listRow = 0
	}
	k.ensureListRowVisible()
}

// listCapacity is how many TASKS fit in the viewport at the current height and
// density. Section headers also consume lines, so this is a floor rather than
// an exact count — it only has to be conservative enough that the selected row
// is always scrolled into view.
func (k *KanbanBoard) listCapacity() int {
	// -2 container border, -1 header bar, -1 arrangement widget.
	capacity := (k.height - 4) / k.listRowLines()
	if capacity < 1 {
		capacity = 1
	}
	return capacity
}

// ensureListRowVisible scrolls the list so the selected row is on screen.
func (k *KanbanBoard) ensureListRowVisible() {
	capacity := k.listCapacity()
	if k.listRow < k.listScroll {
		k.listScroll = k.listRow
	} else if k.listRow >= k.listScroll+capacity {
		k.listScroll = k.listRow - capacity + 1
	}
	maxScroll := len(k.listTasks) - capacity
	if maxScroll < 0 {
		maxScroll = 0
	}
	if k.listScroll > maxScroll {
		k.listScroll = maxScroll
	}
	if k.listScroll < 0 {
		k.listScroll = 0
	}
}

// selectedListTask returns the task under the list cursor.
func (k *KanbanBoard) selectedListTask() *db.Task {
	if k.listRow < 0 || k.listRow >= len(k.listTasks) {
		return nil
	}
	return k.listTasks[k.listRow]
}

// selectListTask moves the list cursor to a task by ID.
func (k *KanbanBoard) selectListTask(id int64) bool {
	for i, t := range k.listTasks {
		if t.ID == id {
			k.listRow = i
			k.ensureListRowVisible()
			return true
		}
	}
	return false
}

// moveListUp moves the list cursor up one row, wrapping at the top.
func (k *KanbanBoard) moveListUp() {
	if len(k.listTasks) == 0 {
		return
	}
	if k.listRow > 0 {
		k.listRow--
	} else {
		k.listRow = len(k.listTasks) - 1
	}
	k.ensureListRowVisible()
}

// moveListDown moves the list cursor down one row, wrapping at the bottom.
func (k *KanbanBoard) moveListDown() {
	if len(k.listTasks) == 0 {
		return
	}
	if k.listRow < len(k.listTasks)-1 {
		k.listRow++
	} else {
		k.listRow = 0
	}
	k.ensureListRowVisible()
}

// jumpListToStatus moves the cursor to the first task with the given status,
// which is what the column-focus keys (B/P/L/D) mean in a flat list. It scans
// display order rather than assuming the list is grouped by status, because
// under "group by project" it is not.
func (k *KanbanBoard) jumpListToStatus(status string) {
	wanted := []string{status}
	if status == db.StatusQueued {
		// The In Progress column covers both queued and processing.
		wanted = []string{db.StatusProcessing, db.StatusQueued}
	}
	for i, t := range k.listTasks {
		for _, s := range wanted {
			if t.Status == s {
				k.listRow = i
				k.ensureListRowVisible()
				return
			}
		}
	}
}

// listEmptyMessage explains an empty list in terms of why it is empty: an
// unfiltered empty board is a new install, a filtered one is a filter that
// matched nothing.
func listEmptyMessage(title string) string {
	if title == "" {
		return "No tasks — press 'n' to create one"
	}
	return fmt.Sprintf("No tasks match %s — press '/' to change the filter", title)
}

// renderListHeader names the list and counts what is in it by status, so the
// header answers "how much is in flight" without counting rows.
func (k *KanbanBoard) renderListHeader(width int) string {
	title := k.listTitle
	if title == "" {
		title = "All tasks"
	}

	counts := map[string]int{}
	for _, t := range k.listTasks {
		counts[t.Status]++
	}
	var segments []string
	for _, c := range []struct {
		label  string
		status string
	}{
		{"running", db.StatusProcessing},
		{"blocked", db.StatusBlocked},
		{"queued", db.StatusQueued},
		{"backlog", db.StatusBacklog},
		{"done", db.StatusDone},
	} {
		if n := counts[c.status]; n > 0 {
			segments = append(segments, fmt.Sprintf("%s %d %s", StatusIcon(c.status), n, c.label))
		}
	}

	left := fmt.Sprintf("%s  %d tasks", title, len(k.listTasks))
	right := strings.Join(segments, "  ")

	headerStyle := lipgloss.NewStyle().
		Width(width).
		Background(ColorPrimary).
		Foreground(lipgloss.Color("#000000")).
		Bold(true).
		Padding(0, 1)

	gap := width - 2 - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// Too narrow for the breakdown — the name and total matter more.
		return headerStyle.Render(truncateRunes(left, width-2))
	}
	return headerStyle.Render(left + strings.Repeat(" ", gap) + right)
}

// listRowIndicators returns the badges for a row: the same set the kanban card
// carries, minus the ones that only make sense with three lines to spend.
func (k *KanbanBoard) listRowIndicators(task *db.Task, isSelected bool) []string {
	var out []string

	if pr := k.prInfo[task.ID]; pr != nil {
		if isSelected {
			if stats := PRDiffStatsPlain(pr); stats != "" {
				out = append(out, stats)
			}
			out = append(out, PRStatusIcon(pr))
		} else {
			if stats := PRDiffStats(pr); stats != "" {
				out = append(out, stats)
			}
			out = append(out, PRStatusBadge(pr))
		}
	}
	if k.HasRunningProcess(task.ID) {
		out = append(out, dotIndicator(lipgloss.Color("46"), isSelected))
	}
	if task.IsDangerous() && isActiveStatus(task.Status) && !IsGlobalDangerousMode() {
		out = append(out, dotIndicator(ColorDangerous, isSelected))
	}
	if task.IsAutoPermission() && isActiveStatus(task.Status) {
		out = append(out, dotIndicator(ColorWarning, isSelected))
	}
	if task.IsAcceptEdits() && isActiveStatus(task.Status) {
		out = append(out, dotIndicator(ColorCode, isSelected))
	}
	if host := task.PlacementTarget; host != "" {
		badge := "@" + host
		if isSelected {
			out = append(out, badge)
		} else {
			out = append(out, FgStyle(ColorCode).Render(badge))
		}
	}
	if n := k.GetOpenBlockerCount(task.ID); n > 0 {
		badge := "🔒"
		if n > 1 {
			badge = fmt.Sprintf("🔒%d", n)
		}
		if isSelected {
			out = append(out, badge)
		} else {
			out = append(out, FgStyle(lipgloss.Color("#F59E0B")).Render(badge))
		}
	}
	if task.Pinned {
		if isSelected {
			out = append(out, IconPin())
		} else {
			out = append(out, FgStyle(ColorWarning).Render(IconPin()))
		}
	}
	return out
}

func dotIndicator(color lipgloss.Color, isSelected bool) string {
	if isSelected {
		return "●"
	}
	return FgStyle(color).Render("●")
}

func isActiveStatus(status string) bool {
	return status == db.StatusProcessing || status == db.StatusBlocked
}

// shortProjectName abbreviates the long project names that would otherwise eat
// a third of a narrow row. Mirrors the kanban card's abbreviations.
func shortProjectName(project string) string {
	switch project {
	case "offerlab":
		return "ol"
	case "influencekit":
		return "ik"
	}
	return project
}

// handleClickList maps a click to the row under it, selecting that task.
//
// It replays the same block layout the renderer builds rather than dividing y
// by a row height: with section headers and two densities in play, any
// shortcut here would drift from what is actually on screen, and a click that
// selects the wrong task is worse than one that selects nothing.
func (k *KanbanBoard) handleClickList(x, y int) *db.Task {
	_ = x
	// y=0 container border, y=1 header bar, y=2 arrangement widget.
	const firstContentLine = 3
	target := y - firstContentLine
	if target < 0 {
		return nil
	}

	innerWidth := k.width - 2
	if innerWidth < 20 {
		innerWidth = 20
	}
	blocks := k.buildListBlocks(innerWidth)

	line := 0
	for i := k.listScroll; i < len(blocks); i++ {
		b := blocks[i]
		if i != k.listScroll || k.listScroll == 0 {
			line += len(b.header)
		}
		if target < line {
			return nil // landed on a section header, not a task
		}
		if target < line+len(b.body) {
			k.listRow = i
			k.ensureListRowVisible()
			return k.listTasks[i]
		}
		line += len(b.body)
	}
	return nil
}
