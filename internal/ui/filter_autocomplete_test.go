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
