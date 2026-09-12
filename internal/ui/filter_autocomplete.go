package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
)

// filterSuggestMode is what the dropdown is currently completing. The filter bar
// has two chip syntaxes — "[project]" and "@host" — and only one of them can be
// open at a time, so one dropdown serves both.
type filterSuggestMode int

const (
	suggestProjects filterSuggestMode = iota
	suggestHosts
)

// FilterAutocompleteModel provides project name and host name autocomplete for
// the filter.
type FilterAutocompleteModel struct {
	db       *db.DB
	projects []*db.Project // filtered results (suggestProjects)
	hosts    []string      // filtered results (suggestHosts)
	mode     filterSuggestMode
	selected int
	maxShow  int
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
	m.mode, m.hosts = suggestProjects, nil
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

// SetHostQuery filters host names for the "@host" chip and resets selection.
//
// The fleet has no central registry — a placement hook names whatever machines
// it likes — so the candidates are the hosts tasks have actually run on, plus
// "local" for the ones that ran here.
func (m *FilterAutocompleteModel) SetHostQuery(query string) {
	m.mode, m.projects = suggestHosts, nil
	if m.db == nil {
		m.hosts = nil
		return
	}
	all, _ := m.db.ListPlacementHosts()
	all = append(all, filterHostLocalName)

	query = strings.ToLower(query)
	if query == "" {
		m.hosts = all
	} else {
		m.hosts = nil
		type scored struct {
			name string
			s    int
		}
		var results []scored
		for _, h := range all {
			if s := fuzzyScore(h, query); s > 0 {
				results = append(results, scored{h, s})
			}
		}
		sort.SliceStable(results, func(i, j int) bool { return results[i].s > results[j].s })
		for _, r := range results {
			m.hosts = append(m.hosts, r.name)
		}
	}
	if len(m.hosts) > 10 {
		m.hosts = m.hosts[:10]
	}
	m.selected = 0
}

// count is how many suggestions the current mode is offering.
func (m *FilterAutocompleteModel) count() int {
	if m.mode == suggestHosts {
		return len(m.hosts)
	}
	return len(m.projects)
}

// IsHostMode reports whether the dropdown is completing a host rather than a
// project, so the caller knows which chip syntax to write back.
func (m *FilterAutocompleteModel) IsHostMode() bool { return m.mode == suggestHosts }

func (m *FilterAutocompleteModel) MoveUp() {
	if m.selected > 0 {
		m.selected--
	} else if n := m.count(); n > 0 {
		m.selected = n - 1
	}
}

func (m *FilterAutocompleteModel) MoveDown() {
	if m.selected < m.count()-1 {
		m.selected++
	} else {
		m.selected = 0
	}
}

// Select returns the selected project or host name.
func (m *FilterAutocompleteModel) Select() string {
	if m.selected >= m.count() {
		return ""
	}
	if m.mode == suggestHosts {
		return m.hosts[m.selected]
	}
	return m.projects[m.selected].Name
}

func (m *FilterAutocompleteModel) HasResults() bool { return m.count() > 0 }
func (m *FilterAutocompleteModel) Reset() {
	m.projects, m.hosts, m.mode, m.selected = nil, nil, suggestProjects, 0
}

// View renders the dropdown.
func (m *FilterAutocompleteModel) View() string {
	total := m.count()
	if total == 0 {
		return ""
	}

	// Calculate visible window around selection
	start, end := 0, total
	if end > m.maxShow {
		start = m.selected - m.maxShow/2
		if start < 0 {
			start = 0
		}
		end = start + m.maxShow
		if end > total {
			end = total
			start = end - m.maxShow
		}
	}

	var lines []string
	if start > 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(ColorMuted).Render("  ↑"))
	}
	for i := start; i < end; i++ {
		prefix, style := "  ", lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		if i == m.selected {
			prefix = "> "
			style = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)
		}
		var name string
		if m.mode == suggestHosts {
			// The same "@host" the task cards wear, so the two read as one idea.
			name = "@" + m.hosts[i]
		} else {
			p := m.projects[i]
			name = "[" + p.Name + "]"
			if p.Color != "" {
				name = lipgloss.NewStyle().Foreground(lipgloss.Color(p.Color)).Render("●") + " " + name
			}
		}
		lines = append(lines, prefix+style.Render(name))
	}
	if end < total {
		lines = append(lines, lipgloss.NewStyle().Foreground(ColorMuted).Render("  ↓"))
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}
