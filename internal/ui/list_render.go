package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
)

// Rendering the list.
//
// The first cut was one undifferentiated line per task with the status repeated
// on every row: a data dump, not a view. This replaces it with sections, shared
// column widths, and two densities — compact (one aligned line) and relaxed
// (the kanban card at full width). Which of those you get, and what the
// sections are, comes from ListOptions rather than being baked in.
//
// Everything renders through listBlock so a task can occupy a variable number
// of lines without the scroll maths going out of step with what is drawn.

// listBlock is one task's lines plus any section header immediately above it.
type listBlock struct {
	header []string
	body   []string
}

// listColumns are column widths shared by every row, so ids and project tags
// line up instead of each row starting at its own offset. Ragged left edges
// were the single biggest reason the first cut read as a dump.
type listColumns struct {
	id      int
	project int
}

// showProjectColumn reports whether rows should carry a [project] tag. When the
// list is already grouped by project the tag repeats the section header on
// every row, so it is dropped and the width goes to titles instead — the same
// reason the status word lives in the header rather than the row.
func (k *KanbanBoard) showProjectColumn() bool {
	return k.listOpts.GroupBy != GroupByProject
}

func measureListColumns(tasks []*db.Task) listColumns {
	c := listColumns{}
	for _, t := range tasks {
		if n := len(fmt.Sprintf("#%d", t.ID)); n > c.id {
			c.id = n
		}
		if t.Project != "" {
			if n := len(shortProjectName(t.Project)) + 2; n > c.project {
				c.project = n
			}
		}
	}
	if c.project > 20 {
		c.project = 20
	}
	return c
}

func pad(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func padLeft(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

// viewList renders the flat list: header bar, the arrangement widget, then the
// tasks as sections of blocks.
func (k *KanbanBoard) viewList() string {
	innerWidth := k.width - 2 // container border
	if innerWidth < 20 {
		innerWidth = 20
	}

	lines := []string{k.renderListHeader(innerWidth), k.renderListOptionsBar(innerWidth)}
	budget := k.height - 2 - len(lines)

	if len(k.listTasks) == 0 {
		empty := lipgloss.NewStyle().
			Foreground(ColorMuted).Width(innerWidth).
			Align(lipgloss.Center).Italic(true).MarginTop(1)
		lines = append(lines, empty.Render(listEmptyMessage(k.listTitle)))
	} else {
		blocks := k.buildListBlocks(innerWidth)
		lines = append(lines, windowListBlocks(blocks, k.listScroll, budget)...)
	}

	_, highlightBorder := GetThemeBorderColors()
	container := lipgloss.NewStyle().
		Width(innerWidth).
		Height(k.height - 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlightBorder)
	return container.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// renderListOptionsBar is the arrangement widget: what the list is grouped and
// sorted by, and how much room each task gets, with the key that changes it.
// It is a visible line rather than a hidden binding because the arrangement is
// the thing you reach for, and an invisible setting is a forgotten one.
func (k *KanbanBoard) renderListOptionsBar(width int) string {
	left := Dim.Render(k.listOpts.Summary())
	right := Dim.Render("O: arrange")
	gap := width - 4 - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return lipgloss.NewStyle().Width(width).Padding(0, 2).MaxHeight(1).Render(left)
	}
	return lipgloss.NewStyle().Width(width).Padding(0, 2).MaxHeight(1).
		Render(left + strings.Repeat(" ", gap) + right)
}

// windowListBlocks emits whole blocks from start until the budget runs out.
func windowListBlocks(blocks []listBlock, start, budget int) []string {
	if start < 0 {
		start = 0
	}
	var out []string
	used := 0
	for i := start; i < len(blocks); i++ {
		b := blocks[i]
		// Suppress a section header only when we scrolled INTO the section; at
		// the top of the list the first header is not a partial one.
		h := b.header
		if i == start && start > 0 {
			h = nil
		}
		if used+len(h)+len(b.body) > budget {
			break
		}
		out = append(out, h...)
		out = append(out, b.body...)
		used += len(h) + len(b.body)
	}
	return out
}

func (k *KanbanBoard) buildListBlocks(width int) []listBlock {
	cols := measureListColumns(k.listTasks)
	if !k.showProjectColumn() {
		cols.project = 0
	}
	blocks := make([]listBlock, 0, len(k.listTasks))

	prevGroup := "\x00unset"
	for i, t := range k.listTasks {
		var b listBlock
		selected := i == k.listRow

		if group := k.listOpts.groupKeyFor(t); group != prevGroup {
			if title := k.listOpts.sectionTitle(group); title != "" {
				// Relaxed rows already end in a blank line, so only compact
				// sections need one added ahead of the header.
				spaced := i > 0 && k.listOpts.Density != DensityRelaxed
				b.header = k.renderSectionHeader(group, title, width, spaced)
			}
			prevGroup = group
		}

		if k.listOpts.Density == DensityRelaxed {
			b.body = k.renderRelaxedRow(t, width, cols, selected)
		} else {
			b.body = []string{k.renderCompactRow(t, width, cols, selected)}
		}
		blocks = append(blocks, b)
	}
	return blocks
}

// renderSectionHeader labels a run of tasks, with a rule running to a count on
// the right so the eye has a line to break on.
func (k *KanbanBoard) renderSectionHeader(group, title string, width int, spaced bool) []string {
	count := 0
	for _, t := range k.listTasks {
		if k.listOpts.groupKeyFor(t) == group {
			count++
		}
	}

	color, icon := ColorMuted, "•"
	switch {
	case group == pinnedGroupKey:
		color, icon = ColorWarning, IconPin()
	case k.listOpts.GroupBy == GroupByStatus:
		color, icon = StatusColor(group), StatusIcon(group)
	case k.listOpts.GroupBy == GroupByProject:
		color, icon = ProjectColor(group), "•"
	}

	left := icon + " " + strings.ToUpper(title)
	countStr := strconv.Itoa(count)
	rule := width - 4 - lipgloss.Width(left) - lipgloss.Width(countStr) - 2
	if rule < 1 {
		rule = 1
	}
	line := lipgloss.NewStyle().Bold(true).Foreground(color).Render(left) +
		" " + Dim.Render(strings.Repeat("─", rule)) + " " +
		lipgloss.NewStyle().Foreground(color).Render(countStr)

	out := []string{lipgloss.NewStyle().Width(width).Padding(0, 2).MaxHeight(1).Render(line)}
	if spaced {
		out = append([]string{""}, out...)
	}
	return out
}

// listRowTitle is the text a row shows: the task title, or the workflow's goal
// for a collapsed workflow lead.
func (k *KanbanBoard) listRowTitle(t *db.Task) string {
	if wf := k.workflowGroup(t.ID); wf != nil {
		return "⇄ " + wf.Goal()
	}
	return t.Title
}

// listRowTrailer is the right-hand side of a row: badges, then an age hint.
func (k *KanbanBoard) listRowTrailer(t *db.Task, selected bool) string {
	right := strings.Join(k.listRowIndicators(t, selected), " ")
	if age := taskElapsedShort(t); age != "" {
		if right != "" {
			right += "  "
		}
		right += age
	}
	return right
}

// renderCompactRow is one aligned line per task.
//
// The status word lives in the section header rather than on every row, but the
// glyph stays: the Pinned section mixes statuses, and grouping by project mixes
// them everywhere, so a row that said nothing about state would be unreadable
// in exactly the arrangements people reach for.
func (k *KanbanBoard) renderCompactRow(t *db.Task, width int, cols listColumns, selected bool) string {
	cursor := " "
	if selected {
		cursor = Icon("▌", ">")
	}
	glyph := StatusIcon(t.Status)

	id := padLeft(fmt.Sprintf("#%d", t.ID), cols.id)
	project := ""
	if t.Project != "" && k.showProjectColumn() {
		project = "[" + shortProjectName(t.Project) + "]"
	}
	project = pad(project, cols.project)

	right := k.listRowTrailer(t, selected)

	inner := width - 4
	titleWidth := inner - lipgloss.Width(cursor) - 1 - lipgloss.Width(glyph) - 1 -
		cols.id - 1 - cols.project - lipgloss.Width(right) - 2
	if titleWidth < 10 {
		titleWidth = 10
	}
	title := truncateRunes(k.listRowTitle(t), titleWidth)

	var left string
	if selected {
		left = cursor + " " + glyph + " " + id + " " + project + " " + title
	} else {
		titleStyle := lipgloss.NewStyle()
		if k.NeedsInput(t.ID) {
			titleStyle = titleStyle.Foreground(ColorWarning)
		}
		left = cursor + " " + FgStyle(StatusColor(t.Status)).Render(glyph) + " " +
			Dim.Render(id) + " " +
			FgStyle(ProjectColor(t.Project)).Render(project) + " " +
			titleStyle.Render(title)
	}

	gap := inner - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return k.listRowStyle(width, selected).Render(left + strings.Repeat(" ", gap) + right)
}

// renderRelaxedRow is the kanban card at full width: the same three lines a
// card carries (id and badges, title, live sub-line), given the whole row
// instead of a quarter of it. This is the density for watching work happen —
// the sub-line is where a running agent's current activity shows up.
func (k *KanbanBoard) renderRelaxedRow(t *db.Task, width int, cols listColumns, selected bool) []string {
	inner := width - 4
	style := k.listRowStyle(width, selected)

	cursor := " "
	if selected {
		cursor = Icon("▌", ">")
	}
	glyph := StatusIcon(t.Status)
	id := padLeft(fmt.Sprintf("#%d", t.ID), cols.id)

	// Line 1 — identity on the left, badges and age on the right.
	meta := cursor + " "
	if selected {
		meta += glyph + " " + id
	} else {
		meta += FgStyle(StatusColor(t.Status)).Render(glyph) + " " + Dim.Render(id)
	}
	if t.Project != "" && k.showProjectColumn() {
		tag := "[" + shortProjectName(t.Project) + "]"
		if selected {
			meta += " " + tag
		} else {
			meta += " " + FgStyle(ProjectColor(t.Project)).Render(tag)
		}
	}
	right := k.listRowTrailer(t, selected)
	gap := inner - lipgloss.Width(meta) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	metaLine := style.Render(meta + strings.Repeat(" ", gap) + right)

	// Line 2 — the title, with the whole row to itself. It indents to where the
	// id starts, so the three lines of a row read as one block rather than a
	// ragged stack.
	indent := strings.Repeat(" ", lipgloss.Width(cursor)+1+lipgloss.Width(glyph)+1)
	titleStyle := lipgloss.NewStyle()
	if selected {
		titleStyle = titleStyle.Bold(true)
	} else if k.NeedsInput(t.ID) {
		titleStyle = titleStyle.Foreground(ColorWarning)
	}
	titleLine := style.Render(indent + titleStyle.Render(truncateRunes(k.listRowTitle(t), inner-lipgloss.Width(indent))))

	// Line 3 — what the agent is doing right now, or why it is waiting. Shared
	// with the kanban card so both faces of the board say the same thing.
	sub := k.cardSubLine(t, inner-lipgloss.Width(indent)+2, selected)
	subLine := style.Render(indent + sub)

	return []string{metaLine, titleLine, subLine, ""}
}

// listRowStyle is the shared row chrome: full width, padded, one line tall, and
// highlighted when selected.
func (k *KanbanBoard) listRowStyle(width int, selected bool) lipgloss.Style {
	s := lipgloss.NewStyle().Width(width).Padding(0, 2).MaxHeight(1)
	if selected {
		bg, fg := GetThemeCardColors()
		s = s.Bold(true).Background(bg).Foreground(fg)
	}
	return s
}
