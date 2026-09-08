package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
)

// stalePaletteModel is a palette showing results for one query while the user
// has already typed a different one — the state the palette sits in for the
// whole duration of an in-flight search.
func stalePaletteModel(t *testing.T, shown []*db.Task, typed string) *CommandPaletteModel {
	t.Helper()
	in := textinput.New()
	in.SetValue(typed)
	return &CommandPaletteModel{
		allTasks:      shown,
		filteredTasks: shown,
		resultsQuery:  "", // the recency list: produced by the empty query
		searchInput:   in,
		searchPending: true,
		width:         100,
		height:        40,
		maxVisible:    10,
	}
}

func staleTasks() []*db.Task {
	return []*db.Task{
		{ID: 4778, Title: "Build a generalized SOCIAL-INTERACTION framework", Status: db.StatusBlocked},
		{ID: 5119, Title: "Creator Referral v2", Status: db.StatusBlocked},
	}
}

// A list produced by a different query must not be drawn as though it were the
// results for what is currently typed. Every row in it is arbitrary relative to
// the query, so presenting it with a live selection invites the user to act on
// a task that does not match at all.
func TestPaletteDoesNotRenderStaleResultsAsMatches(t *testing.T) {
	m := stalePaletteModel(t, staleTasks(), "task/5174-add-stripe-connect-credit-incentive-feat")
	view := m.View()
	if strings.Contains(view, "SOCIAL-INTERACTION") || strings.Contains(view, "Creator Referral") {
		t.Fatal("stale non-matching rows rendered as results for the typed query")
	}
	if !strings.Contains(view, "Searching") {
		t.Fatal("no indication that results are still being computed")
	}
}

// Results arriving for a new query replace an unrelated list, so a selection
// index chosen against the old rows means nothing. It must land on the new
// top-ranked match, not carry over into a different set of tasks.
func TestPaletteResetsSelectionWhenResultsReplaceStaleList(t *testing.T) {
	m := stalePaletteModel(t, staleTasks(), "stripe")
	m.selectedIndex = 1 // user had moved down the stale list

	fresh := []*db.Task{
		{ID: 5174, Title: "Add Stripe Connect credit incentive feature", Status: db.StatusBlocked},
		{ID: 4001, Title: "Stripe webhook retries", Status: db.StatusBlocked},
	}
	m.Update(paletteSearchMsg{owner: m, query: "stripe", tasks: fresh})

	if m.selectedIndex != 0 {
		t.Fatalf("selection carried over from the stale list: index %d", m.selectedIndex)
	}
	if m.filteredTasks[m.selectedIndex].ID != 5174 {
		t.Fatalf("selection landed on #%d, not the top match", m.filteredTasks[m.selectedIndex].ID)
	}
}

// Once results match what is typed, the list renders normally.
func TestPaletteRendersFreshResults(t *testing.T) {
	m := stalePaletteModel(t, staleTasks(), "stripe")
	fresh := []*db.Task{{ID: 5174, Title: "Add Stripe Connect credit incentive feature", Status: db.StatusBlocked}}
	m.Update(paletteSearchMsg{owner: m, query: "stripe", tasks: fresh})
	if m.searchPending {
		t.Fatal("still pending after results for the current query")
	}
	if !strings.Contains(m.View(), "Stripe Connect") {
		t.Fatal("fresh matching results not rendered")
	}
}

// Enter pressed while a slow search is still running must select from the real
// results, never from whatever happened to be on screen. The gate makes the
// search genuinely outlast the keypress; an instant fake would pass either way.
func TestPaletteEnterDuringSlowSearchUsesRealResults(t *testing.T) {
	m := stalePaletteModel(t, staleTasks(), "stripe")
	m.searchInFlight = true

	gate := make(chan struct{})
	fresh := []*db.Task{{ID: 5174, Title: "Add Stripe Connect credit incentive feature", Status: db.StatusBlocked}}
	search := func() tea.Msg {
		<-gate
		return paletteSearchMsg{owner: m, query: "stripe", tasks: fresh}
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.selectedTask != nil {
		t.Fatalf("Enter selected #%d from the on-screen list before results arrived", m.selectedTask.ID)
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- search() }()
	close(gate)
	m.Update(<-done)

	if m.selectedTask == nil || m.selectedTask.ID != 5174 {
		t.Fatalf("deferred Enter did not select the real top match: %+v", m.selectedTask)
	}
}
