package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// PinnedNavItem is a single entry in the detail view's pinned quick-nav bar.
// It carries only what the bar needs to render and to load the task on jump.
type PinnedNavItem struct {
	ID      int64
	Title   string
	Status  string
	Project string
}

// SetPinnedNav updates the pinned quick-nav bar for this detail view. items is
// the ordered set of pinned tasks (already filtered to match the board's active
// filter); currentID is the task currently being viewed, used to highlight its
// pill and anchor "next/prev" navigation. Toggling the bar's visibility changes
// the header height, so the viewport is reflowed to match.
func (m *DetailModel) SetPinnedNav(items []PinnedNavItem, currentID int64) {
	m.pinnedNav = items
	m.pinnedNavIndex = -1
	for i, it := range items {
		if it.ID == currentID {
			m.pinnedNavIndex = i
			break
		}
	}
	m.reflowViewport()
	// The header changed; drop the render cache so the bar paints immediately.
	m.cachedViewOK = false
}

// HasPinnedNav reports whether the pinned quick-nav bar is currently shown.
func (m *DetailModel) HasPinnedNav() bool {
	return m.showPinnedNav()
}

// showPinnedNav decides whether the bar is worth showing. We hide it when there
// is nowhere to hop to: no pinned tasks, or the only pinned task is the one
// already open.
func (m *DetailModel) showPinnedNav() bool {
	n := len(m.pinnedNav)
	if n == 0 {
		return false
	}
	if n == 1 && m.pinnedNavIndex == 0 {
		return false
	}
	return true
}

// PinnedNavNextID returns the ID of the pinned task after the current one
// (wrapping around), or 0 if navigation isn't possible.
func (m *DetailModel) PinnedNavNextID() int64 {
	n := len(m.pinnedNav)
	if n == 0 {
		return 0
	}
	idx := m.pinnedNavIndex
	if idx < 0 {
		return m.pinnedNav[0].ID
	}
	return m.pinnedNav[(idx+1)%n].ID
}

// PinnedNavPrevID returns the ID of the pinned task before the current one
// (wrapping around), or 0 if navigation isn't possible.
func (m *DetailModel) PinnedNavPrevID() int64 {
	n := len(m.pinnedNav)
	if n == 0 {
		return 0
	}
	idx := m.pinnedNavIndex
	if idx < 0 {
		return m.pinnedNav[n-1].ID
	}
	return m.pinnedNav[(idx-1+n)%n].ID
}

// PinnedNavIDAt returns the ID of the nth pinned task (1-indexed), or 0 if there
// is no such task. Backs the 1-9 quick-jump keys.
func (m *DetailModel) PinnedNavIDAt(n int) int64 {
	if n < 1 || n > len(m.pinnedNav) {
		return 0
	}
	return m.pinnedNav[n-1].ID
}

// renderPinnedNav builds the single-line pill bar. It windows the pills around
// the current one so the active task is always visible even with many pins, and
// never exceeds the available width (which would wrap and break header sizing).
func (m *DetailModel) renderPinnedNav() string {
	if !m.showPinnedNav() {
		return ""
	}

	label := lipgloss.NewStyle().Foreground(ColorMuted).Render(IconPin() + " ")

	currentStyle := lipgloss.NewStyle().
		Background(ColorPrimary).
		Foreground(lipgloss.Color("#FFFFFF")).
		Bold(true)
	restStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#374151")).
		Foreground(lipgloss.Color("#D1D5DB"))
	dimStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#374151")).
		Foreground(lipgloss.Color("#6B7280"))
	moreStyle := lipgloss.NewStyle().Foreground(ColorMuted)

	n := len(m.pinnedNav)
	pills := make([]string, n)
	widths := make([]int, n)
	for i, it := range m.pinnedNav {
		text := fmt.Sprintf(" #%d %s ", it.ID, truncateRunes(it.Title, 18))
		if i < 9 {
			// Prefix with the 1-9 quick-jump number.
			text = fmt.Sprintf(" %d·#%d %s ", i+1, it.ID, truncateRunes(it.Title, 18))
		}
		var style lipgloss.Style
		switch {
		case i == m.pinnedNavIndex:
			style = currentStyle
		case m.focused:
			style = restStyle
		default:
			style = dimStyle
		}
		pills[i] = style.Render(text)
		widths[i] = lipgloss.Width(pills[i])
	}

	// Budget for pills after the label, leaving room for edge "…" markers.
	avail := m.width - lipgloss.Width(label) - 6
	if avail < 10 {
		avail = 10
	}

	// Window outward from the current pill (or the first pill when the current
	// task isn't pinned) until adding another would overflow.
	anchor := m.pinnedNavIndex
	if anchor < 0 {
		anchor = 0
	}
	start, end := anchor, anchor+1
	used := widths[anchor]
	for {
		grew := false
		if end < n && used+1+widths[end] <= avail {
			used += 1 + widths[end]
			end++
			grew = true
		}
		if start > 0 && used+1+widths[start-1] <= avail {
			used += 1 + widths[start-1]
			start--
			grew = true
		}
		if !grew {
			break
		}
	}

	var b strings.Builder
	b.WriteString(label)
	if start > 0 {
		b.WriteString(moreStyle.Render(fmt.Sprintf("‹%d ", start)))
	}
	b.WriteString(strings.Join(pills[start:end], " "))
	if end < n {
		b.WriteString(moreStyle.Render(fmt.Sprintf(" %d›", n-end)))
	}
	return b.String()
}
