package ui

import (
	"fmt"
	"sort"
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

// listRowHeight is the vertical cost of one list row. Unlike a kanban card
// (cardHeight), a row is a single line — that density is the point.
const listRowHeight = 1

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

// rebuildListTasks flattens the board's tasks into list order: pinned first
// (they are pinned precisely so they stay in sight), then by status urgency,
// then keeping the order they arrived in — which is the freshness order the
// loader already established.
func (k *KanbanBoard) rebuildListTasks() {
	tasks := make([]*db.Task, 0, len(k.allTasks))
	tasks = append(tasks, k.allTasks...)

	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		return listRank(a.Status) < listRank(b.Status)
	})
	k.listTasks = tasks
	k.clampListSelection()
}

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

// listCapacity is how many rows fit in the viewport at the current height.
func (k *KanbanBoard) listCapacity() int {
	// -2 for the container border, -1 for the header bar.
	capacity := (k.height - 3) / listRowHeight
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
// which is what the column-focus keys (B/P/L/D) mean in a flat list.
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

// viewList renders the flat list: a header bar naming the view and its counts,
// then one line per task.
func (k *KanbanBoard) viewList() string {
	innerWidth := k.width - 2 // container border
	if innerWidth < 20 {
		innerWidth = 20
	}
	capacity := k.listCapacity()

	start := k.listScroll
	end := start + capacity
	if end > len(k.listTasks) {
		end = len(k.listTasks)
	}

	lines := []string{k.renderListHeader(innerWidth)}

	switch {
	case len(k.listTasks) == 0:
		empty := lipgloss.NewStyle().
			Foreground(ColorMuted).
			Width(innerWidth).
			Align(lipgloss.Center).
			Italic(true).
			MarginTop(1)
		lines = append(lines, empty.Render(listEmptyMessage(k.listTitle)))
	default:
		for i := start; i < end; i++ {
			lines = append(lines, k.renderListRow(k.listTasks[i], innerWidth, i == k.listRow))
		}
	}

	// One shared indicator line: how many rows sit outside the viewport.
	if hidden := k.listHiddenSummary(start, end); hidden != "" {
		style := lipgloss.NewStyle().
			Foreground(ColorMuted).
			Width(innerWidth).
			Align(lipgloss.Center).
			Italic(true)
		lines = append(lines, style.Render(hidden))
	}

	_, highlightBorder := GetThemeBorderColors()
	container := lipgloss.NewStyle().
		Width(innerWidth).
		Height(k.height - 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlightBorder)

	return container.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
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

// listHiddenSummary describes rows scrolled out of view, above and below.
func (k *KanbanBoard) listHiddenSummary(start, end int) string {
	above, below := start, len(k.listTasks)-end
	if below < 0 {
		below = 0
	}
	switch {
	case above > 0 && below > 0:
		return fmt.Sprintf("%s %d   %s %d", IconArrowUp(), above, IconArrowDown(), below)
	case above > 0:
		return fmt.Sprintf("%s %d more above", IconArrowUp(), above)
	case below > 0:
		return fmt.Sprintf("%s %d more below", IconArrowDown(), below)
	}
	return ""
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

// listCursor marks the selected row in the gutter. The selection also gets a
// background highlight, but a bare colour is not enough on its own: in a
// no-colour terminal (or a piped render) the highlight vanishes and every row
// looks the same, so the cursor glyph carries the selection too.
func listCursor(selected bool) string {
	if selected {
		return Icon("▌", ">")
	}
	return " "
}

// renderListRow renders one task as a single line:
//
//	▌ ● #1234 [ol] Fix the retry banner            +12/-3 ✓ 📌  running 4m
//
// The left half identifies the task, the right half carries the same badges
// the kanban card shows plus an age hint, so nothing is lost by switching modes.
func (k *KanbanBoard) renderListRow(task *db.Task, width int, isSelected bool) string {
	if width < 20 {
		width = 20
	}
	cursor := listCursor(isSelected)
	inner := width - 2 - lipgloss.Width(cursor) - 1 // padding + cursor gutter

	icon := StatusIcon(task.Status)
	id := fmt.Sprintf("#%d", task.ID)
	project := ""
	if task.Project != "" {
		project = "[" + shortProjectName(task.Project) + "]"
	}

	right := strings.Join(k.listRowIndicators(task, isSelected), " ")
	if hint := taskAgeHint(task); hint != "" {
		if right != "" {
			right += "  "
		}
		right += hint
	}

	// Budget the title with what is left after the fixed columns and the
	// right-hand badges, so a long title never pushes the badges off-screen.
	title := task.Title
	if wf := k.workflowGroup(task.ID); wf != nil {
		title = "⇄ " + wf.Goal()
	}
	fixed := lipgloss.Width(icon) + 1 + len(id) + 1
	if project != "" {
		fixed += lipgloss.Width(project) + 1
	}
	titleWidth := inner - fixed - lipgloss.Width(right) - 2
	if titleWidth < 8 {
		titleWidth = 8
	}
	title = truncateRunes(title, titleWidth)

	// A selected row is a solid highlight bar, so per-token colours would fight
	// the background; render it plain and let the bar carry the emphasis.
	var left string
	if isSelected {
		parts := []string{icon, id}
		if project != "" {
			parts = append(parts, project)
		}
		parts = append(parts, title)
		left = strings.Join(parts, " ")
	} else {
		parts := []string{FgStyle(StatusColor(task.Status)).Render(icon), Dim.Render(id)}
		if project != "" {
			parts = append(parts, FgStyle(ProjectColor(task.Project)).Render(project))
		}
		titleStyle := lipgloss.NewStyle()
		if k.NeedsInput(task.ID) {
			titleStyle = titleStyle.Foreground(ColorWarning)
		}
		parts = append(parts, titleStyle.Render(title))
		left = strings.Join(parts, " ")
	}

	gap := inner - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	line := cursor + " " + left + strings.Repeat(" ", gap) + right

	rowStyle := lipgloss.NewStyle().Width(width).Padding(0, 1).MaxHeight(1)
	if isSelected {
		bg, fg := GetThemeCardColors()
		rowStyle = rowStyle.Bold(true).Background(bg).Foreground(fg)
	}
	return rowStyle.Render(line)
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
func (k *KanbanBoard) handleClickList(x, y int) *db.Task {
	_ = x
	// y=0 is the container's top border, y=1 the header bar; rows start at 2.
	row := y - 2
	if row < 0 {
		return nil
	}
	idx := k.listScroll + row
	if idx < 0 || idx >= len(k.listTasks) {
		return nil
	}
	k.listRow = idx
	k.ensureListRowVisible()
	return k.listTasks[idx]
}
