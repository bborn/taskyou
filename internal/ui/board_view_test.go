package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

func viewTestApp(t *testing.T) (*AppModel, *db.DB) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	m := &AppModel{
		db:                 database,
		kanban:             NewKanbanBoard(120, 30),
		filterInput:        textinput.New(),
		filterAutocomplete: NewFilterAutocompleteModel(database),
		width:              120,
		height:             30,
	}
	return m, database
}

func seedFilterTasks(t *testing.T, m *AppModel, database *db.DB) {
	t.Helper()
	for _, spec := range []struct {
		title   string
		status  string
		project string
		pinned  bool
	}{
		{"Running task", db.StatusProcessing, "personal", false},
		{"Blocked task", db.StatusBlocked, "personal", false},
		{"Backlog task", db.StatusBacklog, "personal", true},
		{"Finished task", db.StatusDone, "personal", false},
	} {
		task := &db.Task{Title: spec.title, Status: spec.status, Project: spec.project, Pinned: spec.pinned}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("create task: %v", err)
		}
		m.tasks = append(m.tasks, task)
	}
}

// runFilter applies the current filter and drains the asynchronous pass when
// there is one. applyFilter resolves inline (returning nil) whenever the query
// is purely structural, and dispatches a command otherwise.
func runFilter(t *testing.T, m *AppModel) {
	t.Helper()
	cmd := m.applyFilter()
	if cmd == nil {
		return
	}
	msg, ok := cmd().(boardFilterMsg)
	if !ok {
		t.Fatalf("applyFilter produced %T, want boardFilterMsg", cmd())
	}
	m.finishBoardFilter(msg)
}

func boardTitles(m *AppModel) []string {
	var titles []string
	for _, task := range m.kanban.allTasks {
		titles = append(titles, task.Title)
	}
	return titles
}

// The headline case from the feature request: one query that shows exactly the
// things in flight, and nothing else.
func TestApplyFilterStatusTokens(t *testing.T) {
	m, database := viewTestApp(t)
	seedFilterTasks(t, m, database)

	m.filterText = "status:in-progress status:blocked"
	runFilter(t, m)

	got := boardTitles(m)
	if len(got) != 2 {
		t.Fatalf("expected 2 tasks, got %v", got)
	}
	for _, title := range got {
		if title == "Backlog task" || title == "Finished task" {
			t.Errorf("status filter leaked %q onto the board", title)
		}
	}
}

func TestApplyFilterPinnedToken(t *testing.T) {
	m, database := viewTestApp(t)
	seedFilterTasks(t, m, database)

	m.filterText = "is:pinned"
	runFilter(t, m)

	got := boardTitles(m)
	if len(got) != 1 || got[0] != "Backlog task" {
		t.Errorf("is:pinned should show only the pinned task, got %v", got)
	}
}

// Structured tokens and free text must compose: the token narrows, the keyword
// ranks within what is left.
func TestApplyFilterCombinesTokenAndKeyword(t *testing.T) {
	m, database := viewTestApp(t)
	seedFilterTasks(t, m, database)

	m.filterText = "status:blocked Blocked"
	runFilter(t, m)

	got := boardTitles(m)
	if len(got) != 1 || got[0] != "Blocked task" {
		t.Errorf("expected just the blocked task, got %v", got)
	}
}

// applyFilter supplements the in-memory board with a database search for
// keywords. Those extra rows must still obey the structured predicates, or a
// `status:blocked` view quietly fills up with done tasks again.
func TestApplyFilterStatusTokenConstrainsDBSearch(t *testing.T) {
	m, database := viewTestApp(t)

	// Only in the database, never loaded onto the board.
	hidden := &db.Task{Title: "Ancient shipped thing", Project: "personal", Status: db.StatusDone}
	if err := database.CreateTask(hidden); err != nil {
		t.Fatalf("create task: %v", err)
	}
	live := &db.Task{Title: "Ancient blocked thing", Project: "personal", Status: db.StatusBlocked}
	if err := database.CreateTask(live); err != nil {
		t.Fatalf("create task: %v", err)
	}
	m.tasks = []*db.Task{live}

	m.filterText = "status:blocked Ancient"
	runFilter(t, m)

	for _, title := range boardTitles(m) {
		if title == "Ancient shipped thing" {
			t.Fatal("a done task from the DB search survived a status:blocked filter")
		}
	}
	if !kanbanHasTask(m.kanban, live.ID) {
		t.Error("the matching blocked task should still be on the board")
	}
}

func TestApplySavedViewSetsFilterAndLabel(t *testing.T) {
	m, database := viewTestApp(t)
	seedFilterTasks(t, m, database)

	view, err := database.SaveView("Active", "status:in-progress status:blocked")
	if err != nil {
		t.Fatalf("save view: %v", err)
	}
	m.applySavedView(view)

	if m.filterText != view.Query {
		t.Errorf("filter = %q, want %q", m.filterText, view.Query)
	}
	if m.activeView != "Active" {
		t.Errorf("activeView = %q", m.activeView)
	}
	if m.boardFilterLabel() != "Active" {
		t.Errorf("label = %q, want the view name", m.boardFilterLabel())
	}
	if len(boardTitles(m)) != 2 {
		t.Errorf("applying the view should filter the board, got %v", boardTitles(m))
	}

	// The choice must survive a restart.
	if got, _ := database.GetSetting(config.SettingBoardFilter); got != view.Query {
		t.Errorf("persisted filter = %q", got)
	}
	if got, _ := database.GetSetting(config.SettingBoardView); got != "Active" {
		t.Errorf("persisted view name = %q", got)
	}
}

// Typing over an applied view turns it back into an ad-hoc filter; leaving the
// name attached would misreport what the board is showing.
func TestEditingFilterDetachesSavedView(t *testing.T) {
	m, database := viewTestApp(t)
	view, _ := database.SaveView("Active", "status:blocked")
	m.applySavedView(view)

	m.filterInput.SetValue("something else")
	m.handleFilterInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	if m.activeView != "" {
		t.Errorf("activeView should be cleared after a manual edit, got %q", m.activeView)
	}
}

func TestClearBoardFilterResetsEverything(t *testing.T) {
	m, database := viewTestApp(t)
	seedFilterTasks(t, m, database)
	view, _ := database.SaveView("Active", "status:blocked")
	m.applySavedView(view)

	m.clearBoardFilter()

	if m.filterText != "" || m.activeView != "" {
		t.Errorf("filter %q / view %q should both be empty", m.filterText, m.activeView)
	}
	if len(boardTitles(m)) != len(m.tasks) {
		t.Errorf("clearing the filter should restore every task, got %v", boardTitles(m))
	}
	if got, _ := database.GetSetting(config.SettingBoardFilter); got != "" {
		t.Errorf("cleared filter should be persisted, got %q", got)
	}
}

func TestListModePersistsAcrossRestart(t *testing.T) {
	m, database := viewTestApp(t)

	m.setListMode(true)
	if got, _ := database.GetSetting(config.SettingBoardDisplayMode); got != config.BoardDisplayList {
		t.Fatalf("display mode not persisted: %q", got)
	}

	// A fresh model over the same database restores the mode and the filter.
	if err := database.SetSetting(config.SettingBoardFilter, "is:pinned"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSetting(config.SettingBoardView, "Pinned"); err != nil {
		t.Fatal(err)
	}

	restored, _ := viewTestApp(t)
	restored.db = database
	restored.loadBoardViewState()

	if !restored.listMode || !restored.kanban.IsListMode() {
		t.Error("list mode should be restored on launch")
	}
	if restored.filterText != "is:pinned" {
		t.Errorf("filter = %q, want the persisted one", restored.filterText)
	}
	if restored.activeView != "Pinned" {
		t.Errorf("activeView = %q", restored.activeView)
	}
	if restored.filterInput.Value() != "is:pinned" {
		t.Error("the filter input should show the restored query when the user presses /")
	}
}

func TestViewPickerAppliesAndSaves(t *testing.T) {
	_, database := viewTestApp(t)
	if _, err := database.SaveView("Zeta", "is:pinned"); err != nil {
		t.Fatalf("save: %v", err)
	}

	picker := NewViewPickerModel(database, "status:blocked", 120, 30)
	if len(picker.views) == 0 {
		t.Fatal("picker should list the saved views")
	}

	// Save the current filter under a new name.
	picker, _ = picker.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	for _, r := range "Blocked" {
		picker, _ = picker.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	picker, _ = picker.Update(tea.KeyMsg{Type: tea.KeyEnter})

	saved, err := database.GetSavedView("Blocked")
	if err != nil || saved == nil {
		t.Fatalf("view was not saved: %v %+v", err, saved)
	}
	if saved.Query != "status:blocked" {
		t.Errorf("saved query = %q", saved.Query)
	}

	// The cursor lands on what was just saved, so Enter applies it.
	picker, _ = picker.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if applied := picker.Applied(); applied == nil || applied.Name != "Blocked" {
		t.Fatalf("enter should apply the highlighted view, got %+v", applied)
	}
}

func TestViewPickerRefusesToSaveAnEmptyFilter(t *testing.T) {
	_, database := viewTestApp(t)
	picker := NewViewPickerModel(database, "", 120, 30)

	picker, _ = picker.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if picker.mode == viewPickerNaming {
		t.Error("there is nothing to save when no filter is set")
	}
	if !strings.Contains(picker.err, "nothing to save") {
		t.Errorf("expected an explanation, got %q", picker.err)
	}
}

func TestViewPickerDeleteNeedsConfirmation(t *testing.T) {
	_, database := viewTestApp(t)
	picker := NewViewPickerModel(database, "", 120, 30)
	if len(picker.views) == 0 {
		t.Fatal("expected the seeded starter views")
	}
	name := picker.views[0].Name

	// A stray 'd' must not delete anything on its own.
	picker, _ = picker.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if v, _ := database.GetSavedView(name); v == nil {
		t.Fatal("'d' alone deleted a view")
	}
	// Declining keeps it.
	picker, _ = picker.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if v, _ := database.GetSavedView(name); v == nil {
		t.Fatal("declining the confirmation deleted the view anyway")
	}

	picker, _ = picker.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	picker, _ = picker.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if v, _ := database.GetSavedView(name); v != nil {
		t.Error("confirming should delete the view")
	}
	if picker.mode != viewPickerBrowse {
		t.Error("picker should return to browsing after a delete")
	}
}

func TestViewPickerClearRequest(t *testing.T) {
	_, database := viewTestApp(t)
	picker := NewViewPickerModel(database, "is:pinned", 120, 30)

	picker, _ = picker.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if !picker.ClearRequested() {
		t.Error("'c' should ask the board to drop its filter")
	}
}
