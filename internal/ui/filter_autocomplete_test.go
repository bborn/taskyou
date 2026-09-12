package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestFilterAutocomplete(t *testing.T) {
	projects := []*db.Project{
		{ID: 1, Name: "personal"},
		{ID: 2, Name: "offerlab"},
		{ID: 3, Name: "workflow"},
	}

	tests := []struct {
		name     string
		query    string
		expected int
	}{
		{"empty shows all", "", 3},
		{"prefix match", "off", 1},
		{"fuzzy match", "wfl", 1},
		{"no match", "xyz", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &FilterAutocompleteModel{projects: projects, maxShow: 5}
			// Manually set filtered for test (SetQuery requires db)
			if tt.query == "" {
				m.projects = projects
			} else {
				m.projects = nil
				for _, p := range projects {
					if fuzzyScore(p.Name, tt.query) > 0 {
						m.projects = append(m.projects, p)
					}
				}
			}
			if len(m.projects) != tt.expected {
				t.Errorf("got %d, want %d", len(m.projects), tt.expected)
			}
		})
	}
}

func TestFilterAutocompleteNavigation(t *testing.T) {
	m := &FilterAutocompleteModel{
		projects: []*db.Project{{Name: "a"}, {Name: "b"}, {Name: "c"}},
		maxShow:  5,
	}

	if m.selected != 0 {
		t.Errorf("initial = %d, want 0", m.selected)
	}

	m.MoveDown()
	if m.selected != 1 {
		t.Errorf("after down = %d, want 1", m.selected)
	}

	m.MoveDown()
	m.MoveDown() // wraps
	if m.selected != 0 {
		t.Errorf("after wrap = %d, want 0", m.selected)
	}

	m.MoveUp() // wraps to end
	if m.selected != 2 {
		t.Errorf("after up wrap = %d, want 2", m.selected)
	}
}

func TestFilterAutocompleteSelect(t *testing.T) {
	m := &FilterAutocompleteModel{
		projects: []*db.Project{{Name: "offerlab"}, {Name: "workflow"}},
		maxShow:  5,
	}

	if name := m.Select(); name != "offerlab" {
		t.Errorf("Select() = %q, want offerlab", name)
	}

	m.MoveDown()
	if name := m.Select(); name != "workflow" {
		t.Errorf("Select() = %q, want workflow", name)
	}
}

func TestFilterAutocompleteReset(t *testing.T) {
	m := &FilterAutocompleteModel{
		projects: []*db.Project{{Name: "test"}},
		selected: 1,
		maxShow:  5,
	}

	m.Reset()

	if m.projects != nil || m.selected != 0 {
		t.Error("Reset did not clear state")
	}
}

func TestFilterAutocompleteView(t *testing.T) {
	m := &FilterAutocompleteModel{
		projects: []*db.Project{{Name: "offerlab"}},
		maxShow:  5,
	}

	view := m.View()
	if view == "" {
		t.Error("expected non-empty view")
	}
	if !contains(view, "[offerlab]") {
		t.Error("view should contain [offerlab]")
	}
}

// The dropdown completes "@host" chips as well as "[project]" ones, so the
// machine names — which live nowhere but in the tasks themselves — are
// discoverable instead of having to be remembered.
func TestFilterAutocompleteHostSuggestions(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	for _, host := range []string{"mona", "mona", "bruce"} {
		task := &db.Task{Title: "placed", Project: "personal"}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("create task: %v", err)
		}
		if err := database.SetTaskPlacement(task.ID, host, "test"); err != nil {
			t.Fatalf("set placement: %v", err)
		}
	}

	m := NewFilterAutocompleteModel(database)
	m.SetHostQuery("")
	if !m.IsHostMode() {
		t.Fatal("SetHostQuery did not switch the dropdown to hosts")
	}
	// Busiest host first, with "local" offered for the tasks that ran here.
	if got := m.hosts; len(got) != 3 || got[0] != "mona" || got[2] != "local" {
		t.Fatalf("host suggestions = %v, want [mona bruce local]", got)
	}

	m.SetHostQuery("br")
	if got := m.Select(); got != "bruce" {
		t.Errorf("Select() after query %q = %q, want bruce", "br", got)
	}
	if view := m.View(); !contains(view, "@bruce") {
		t.Errorf("host dropdown does not render the chip syntax: %q", view)
	}

	// Switching back to projects must not leave stale hosts behind.
	m.SetQuery("")
	if m.IsHostMode() || len(m.hosts) != 0 {
		t.Errorf("project query left host state behind: mode=%v hosts=%v", m.IsHostMode(), m.hosts)
	}
}

// The filter bar has two chip syntaxes; whichever one the cursor is inside is
// the one that gets completed.
func TestFilterAutocompleteModeFollowsTheChipBeingTyped(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	task := &db.Task{Title: "placed", Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := database.SetTaskPlacement(task.ID, "mona", "test"); err != nil {
		t.Fatalf("set placement: %v", err)
	}
	if err := database.CreateProject(&db.Project{Name: "offerlab", Path: "/tmp/offerlab"}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	m := &AppModel{db: database, filterAutocomplete: NewFilterAutocompleteModel(database)}

	m.updateFilterAutocomplete("@mo")
	if !m.filterAutocomplete.IsHostMode() || !m.showFilterDropdown {
		t.Errorf("typing @mo did not open the host dropdown (host=%v shown=%v)",
			m.filterAutocomplete.IsHostMode(), m.showFilterDropdown)
	}

	m.updateFilterAutocomplete("@mona [off")
	if m.filterAutocomplete.IsHostMode() || !m.showFilterDropdown {
		t.Errorf("a project chip after a finished host chip should complete projects (host=%v shown=%v)",
			m.filterAutocomplete.IsHostMode(), m.showFilterDropdown)
	}

	m.updateFilterAutocomplete("[offerlab] flaky")
	if m.showFilterDropdown {
		t.Error("a closed chip should leave the dropdown shut")
	}
}
