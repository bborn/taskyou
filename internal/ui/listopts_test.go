package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

func at(mins int) db.LocalTime {
	return db.LocalTime{Time: time.Now().Add(-time.Duration(mins) * time.Minute)}
}

func optsTasks() []*db.Task {
	return []*db.Task{
		{ID: 1, Title: "Zebra", Status: db.StatusBacklog, Project: "alpha", UpdatedAt: at(50), CreatedAt: at(90)},
		{ID: 2, Title: "Apple", Status: db.StatusBlocked, Project: "beta", UpdatedAt: at(10), CreatedAt: at(80)},
		{ID: 3, Title: "Mango", Status: db.StatusProcessing, Project: "alpha", UpdatedAt: at(30), CreatedAt: at(70)},
		{ID: 4, Title: "Cherry", Status: db.StatusDone, Project: "", UpdatedAt: at(5), CreatedAt: at(60)},
	}
}

func arrangedIDs(opts ListOptions, tasks []*db.Task) []int64 {
	var ids []int64
	for _, t := range opts.Arrange(tasks) {
		ids = append(ids, t.ID)
	}
	return ids
}

func eqIDs(got []int64, want ...int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestArrangeGroupByStatus(t *testing.T) {
	opts := ListOptions{GroupBy: GroupByStatus, Sort: SortUrgency, Density: DensityCompact}
	// Sections in urgency order: blocked, In progress (processing + queued),
	// backlog, done.
	if got := arrangedIDs(opts, optsTasks()); !eqIDs(got, 2, 3, 1, 4) {
		t.Errorf("group by status = %v, want [2 3 1 4]", got)
	}
}

func TestArrangeGroupByProject(t *testing.T) {
	opts := ListOptions{GroupBy: GroupByProject, Sort: SortUrgency, Density: DensityCompact}
	// alpha, then beta, then the projectless task last.
	if got := arrangedIDs(opts, optsTasks()); !eqIDs(got, 3, 1, 2, 4) {
		t.Errorf("group by project = %v, want [3 1 2 4] (alpha, beta, no project)", got)
	}
}

func TestArrangeSorts(t *testing.T) {
	cases := []struct {
		sort ListSort
		want []int64
	}{
		// No grouping, so the sort is the whole order.
		{SortUpdated, []int64{4, 2, 3, 1}},
		{SortCreated, []int64{4, 3, 2, 1}},
		{SortTitle, []int64{2, 4, 3, 1}}, // Apple, Cherry, Mango, Zebra
		{SortUrgency, []int64{3, 2, 1, 4}},
	}
	for _, c := range cases {
		opts := ListOptions{GroupBy: GroupByNone, Sort: c.sort, Density: DensityCompact}
		if got := arrangedIDs(opts, optsTasks()); !eqIDs(got, c.want...) {
			t.Errorf("sort %s = %v, want %v", c.sort, got, c.want)
		}
	}
}

// Pinning means "keep this in sight", so a pinned task leads the list under
// every grouping — otherwise grouping by project would scatter the very tasks
// pinning exists to gather.
func TestPinnedLeadsUnderEveryGrouping(t *testing.T) {
	for _, group := range []ListGroupBy{GroupByStatus, GroupByProject, GroupByNone} {
		tasks := optsTasks()
		tasks[3].Pinned = true // the done task, which would otherwise sort last
		opts := ListOptions{GroupBy: group, Sort: SortUrgency, Density: DensityCompact}
		if got := arrangedIDs(opts, tasks); got[0] != 4 {
			t.Errorf("group by %s: pinned task should lead, got %v", group, got)
		}
	}
}

func TestNormalizeRepairsUnknownValues(t *testing.T) {
	got := ListOptions{GroupBy: "nonsense", Sort: "nope", Density: "huge"}.Normalize()
	if got != DefaultListOptions() {
		t.Errorf("Normalize() = %+v, want the defaults", got)
	}
}

func TestListOptionsRoundTripThroughSettings(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	want := ListOptions{GroupBy: GroupByProject, Sort: SortTitle, Density: DensityRelaxed}
	want.Save(database.SetSetting)

	if got := LoadListOptions(database.GetSetting); got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
	// And the keys are the documented ones, so a user can set them by hand.
	if v, _ := database.GetSetting(config.SettingListDensity); v != "relaxed" {
		t.Errorf("density stored as %q", v)
	}
}

func TestListOptionsWidgetCycles(t *testing.T) {
	m := NewListOptionsModel(DefaultListOptions(), 100, 30)

	// Row 0 is Group by; right cycles status -> project.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.Options().GroupBy != GroupByProject {
		t.Errorf("group by = %s, want project", m.Options().GroupBy)
	}
	// Left wraps back.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.Options().GroupBy != GroupByStatus {
		t.Errorf("group by = %s, want status", m.Options().GroupBy)
	}

	// Down to Density, then right.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.Options().Density != DensityRelaxed {
		t.Errorf("density = %s, want relaxed", m.Options().Density)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.IsDone() {
		t.Error("enter should commit")
	}
}

// Esc must put back what was there, because the board is re-arranged live while
// the user cycles — without this, backing out would silently keep the preview.
func TestListOptionsWidgetCancelRestores(t *testing.T) {
	start := ListOptions{GroupBy: GroupByStatus, Sort: SortUrgency, Density: DensityCompact}
	m := NewListOptionsModel(start, 100, 30)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.Options() == start {
		t.Fatal("setup: expected the preview to differ")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if !m.IsCancelled() {
		t.Error("esc should cancel")
	}
	if m.Options() != start {
		t.Errorf("cancel left %+v, want the original %+v", m.Options(), start)
	}
}

func TestListOptionsWidgetShowsEveryChoice(t *testing.T) {
	out := NewListOptionsModel(DefaultListOptions(), 100, 30).View()
	for _, want := range []string{"Group by", "Sort", "Density", "status", "project", "none", "urgency", "compact", "relaxed"} {
		if !strings.Contains(out, want) {
			t.Errorf("widget does not offer %q:\n%s", want, out)
		}
	}
}

// The board must re-sort when the arrangement changes, and keep the same task
// selected across the rearrangement.
func TestSetListOptionsKeepsSelection(t *testing.T) {
	board := NewKanbanBoard(120, 30)
	board.SetListMode(true)
	board.SetTasks(optsTasks())

	board.MoveDown()
	selected := board.SelectedTask().ID

	board.SetListOptions(ListOptions{GroupBy: GroupByProject, Sort: SortTitle, Density: DensityRelaxed})

	if got := board.SelectedTask(); got == nil || got.ID != selected {
		t.Errorf("rearranging lost the selection: %v, want #%d", got, selected)
	}
	if board.ListOptions().Density != DensityRelaxed {
		t.Error("options not applied")
	}
}

func TestRelaxedDensityRendersThreeLinesPerTask(t *testing.T) {
	board := NewKanbanBoard(120, 40)
	board.SetListMode(true)
	board.SetTasks(optsTasks())
	board.SetListOptions(ListOptions{GroupBy: GroupByNone, Sort: SortUrgency, Density: DensityRelaxed})

	out := board.View()
	// Every task shows its id and its title on separate lines.
	for _, task := range optsTasks() {
		if !strings.Contains(out, task.Title) {
			t.Errorf("relaxed row missing title %q", task.Title)
		}
	}
	if board.listRowLines() != 4 {
		t.Errorf("relaxed row spans %d lines, want 4", board.listRowLines())
	}

	board.SetListOptions(ListOptions{GroupBy: GroupByNone, Sort: SortUrgency, Density: DensityCompact})
	if board.listRowLines() != 1 {
		t.Errorf("compact row spans %d lines, want 1", board.listRowLines())
	}
}

func TestListShowsArrangementWidget(t *testing.T) {
	board := NewKanbanBoard(120, 30)
	board.SetListMode(true)
	board.SetTasks(optsTasks())

	out := board.View()
	for _, want := range []string{"group:", "sort:", "density:", "O: arrange"} {
		if !strings.Contains(out, want) {
			t.Errorf("list header does not advertise %q:\n%s", want, out)
		}
	}
}

// Grouping by project makes the per-row [project] tag pure repetition of the
// section header, so it comes off and the width goes to titles.
func TestProjectTagDroppedWhenGroupingByProject(t *testing.T) {
	board := NewKanbanBoard(120, 40)
	board.SetListMode(true)
	board.SetTasks(optsTasks())

	board.SetListOptions(ListOptions{GroupBy: GroupByStatus, Sort: SortUrgency, Density: DensityCompact})
	if !strings.Contains(board.View(), "[alpha]") {
		t.Error("grouping by status should keep the project tag on rows")
	}

	board.SetListOptions(ListOptions{GroupBy: GroupByProject, Sort: SortUrgency, Density: DensityCompact})
	out := board.View()
	if strings.Contains(out, "[alpha]") {
		t.Errorf("grouping by project should drop the repeated row tag:\n%s", out)
	}
	if !strings.Contains(out, "ALPHA") {
		t.Errorf("the project should still name its section:\n%s", out)
	}
}
