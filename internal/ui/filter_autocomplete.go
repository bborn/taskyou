package ui

import (
	"sort"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/lipgloss"
)

// FilterAutocompleteModel provides project name autocomplete for the filter.
type FilterAutocompleteModel struct {
	db        *db.DB
	projects  []*db.Project // filtered results
	selected  int
	maxShow   int
}

// NewFilterAutocompleteModel creates a new filter autocomplete model.
func NewFilterAutocompleteModel(database *db.DB) *FilterAutocompleteModel {
	return &FilterAutocompleteModel{db: database, maxShow: 5}
}

// SetQuery filters projects based on query and resets selection.
func (m *FilterAutocompleteModel) SetQuery(query string) {
	if m.db == nil {
		return
	}
	all, _ := m.db.ListProjects()
	if query == "" {
		m.projects = all
	} else {
		m.projects = nil
		type scored struct {
			p *db.Project
			s int
		}
		var results []scored
		for _, p := range all {
			if s := fuzzyScore(p.Name, strings.ToLower(query)); s > 0 {
				results = append(results, scored{p, s})
			}
		}
		sort.Slice(results, func(i, j int) bool { return results[i].s > results[j].s })
		for _, r := range results {
			m.projects = append(m.projects, r.p)
		}
	}
	if len(m.projects) > 10 {
		m.projects = m.projects[:10]
	}
	m.selected = 0
}

func (m *FilterAutocompleteModel) MoveUp() {
	if m.selected > 0 {
		m.selected--
	} else if len(m.projects) > 0 {
		m.selected = len(m.projects) - 1
	}
}

func (m *FilterAutocompleteModel) MoveDown() {
	if m.selected < len(m.projects)-1 {
		m.selected++
	} else {
		m.selected = 0
	}
}

// Select returns the selected project name.
func (m *FilterAutocompleteModel) Select() string {
	if m.selected < len(m.projects) {
		return m.projects[m.selected].Name
	}
	return ""
}

func (m *FilterAutocompleteModel) HasResults() bool { return len(m.projects) > 0 }
func (m *FilterAutocompleteModel) Reset()           { m.projects = nil; m.selected = 0 }

// View renders the dropdown.
func (m *FilterAutocompleteModel) View() string {
	if len(m.projects) == 0 {
		return ""
	}

	// Calculate visible window around selection
	start, end := 0, len(m.projects)
	if end > m.maxShow {
		start = m.selected - m.maxShow/2
		if start < 0 {
			start = 0
		}
		end = start + m.maxShow
		if end > len(m.projects) {
			end = len(m.projects)
			start = end - m.maxShow
		}
	}

	var lines []string
	if start > 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(ColorMuted).Render("  ↑"))
	}
	for i := start; i < end; i++ {
		p := m.projects[i]
		prefix, style := "  ", lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		if i == m.selected {
			prefix = "> "
			style = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)
		}
		name := "[" + p.Name + "]"
		if p.Color != "" {
			name = lipgloss.NewStyle().Foreground(lipgloss.Color(p.Color)).Render("●") + " " + name
		}
		lines = append(lines, prefix+style.Render(name))
	}
	if end < len(m.projects) {
		lines = append(lines, lipgloss.NewStyle().Foreground(ColorMuted).Render("  ↓"))
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}
