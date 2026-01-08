// Package ui provides the terminal user interface.
package ui

import "github.com/charmbracelet/lipgloss"

// Colors - these are updated by refreshStyles() when theme changes
var (
	// Primary colors
	ColorPrimary   = lipgloss.Color("#61AFEF") // Soft blue (OneDark default)
	ColorSecondary = lipgloss.Color("#56B6C2") // Cyan
	ColorSuccess   = lipgloss.Color("#98C379") // Green
	ColorWarning   = lipgloss.Color("#E5C07B") // Yellow
	ColorError     = lipgloss.Color("#E06C75") // Red
	ColorMuted     = lipgloss.Color("#5C6370") // Gray

	// Status colors
	ColorInProgress = lipgloss.Color("#D19A66") // Orange
	ColorDone       = lipgloss.Color("#98C379") // Green
	ColorBlocked    = lipgloss.Color("#E06C75") // Red

	// Project colors (fixed, not theme-dependent)
	ColorOfferlab     = lipgloss.Color("#61AFEF") // Blue
	ColorInfluenceKit = lipgloss.Color("#56B6C2") // Cyan
	ColorPersonal     = lipgloss.Color("#C678DD") // Purple

	// Type colors (fixed, not theme-dependent)
	ColorCode     = lipgloss.Color("#C678DD") // Purple
	ColorWriting  = lipgloss.Color("#E5C07B") // Yellow
	ColorThinking = lipgloss.Color("#56B6C2") // Cyan
)

// Base styles - these are updated by refreshStyles() when theme changes
var (
	// Text styles
	Bold     = lipgloss.NewStyle().Bold(true)
	Dim      = lipgloss.NewStyle().Foreground(ColorMuted)
	Title    = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)
	Subtitle = lipgloss.NewStyle().Foreground(ColorSecondary)
	Success  = lipgloss.NewStyle().Foreground(ColorSuccess)
	Warning  = lipgloss.NewStyle().Foreground(ColorWarning)
	Error    = lipgloss.NewStyle().Foreground(ColorError)

	// Status badges
	StatusInProgress = lipgloss.NewStyle().
				Foreground(ColorInProgress).
				SetString("⋯")
	StatusDone = lipgloss.NewStyle().
			Foreground(ColorDone).
			SetString("✓")
	StatusBlocked = lipgloss.NewStyle().
			Foreground(ColorBlocked).
			SetString("!")

	// Priority
	PriorityHigh = lipgloss.NewStyle().
			Foreground(ColorError).
			SetString("↑")

	// Box styles
	Box = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorMuted).
		Padding(0, 1)

	FocusedBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorPrimary).
			Padding(0, 1)

	// List item styles
	ListItem = lipgloss.NewStyle().
			PaddingLeft(2)

	SelectedListItem = lipgloss.NewStyle().
				PaddingLeft(2).
				Foreground(ColorPrimary).
				Bold(true)

	// Help bar
	HelpBar = lipgloss.NewStyle().
		Foreground(ColorMuted).
		Padding(1, 0)

	HelpKey = lipgloss.NewStyle().
		Foreground(ColorSecondary).
		Bold(true)

	HelpDesc = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Header
	Header = lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		Padding(0, 1).
		MarginBottom(1)

	// Tag styles
	ProjectTag = lipgloss.NewStyle().
			Foreground(ColorSecondary).
			Bold(true)

	TypeTag = lipgloss.NewStyle().
		Foreground(ColorMuted).
		Italic(true)
)

// StatusStyle returns the style for a given status.
func StatusStyle(status string) lipgloss.Style {
	switch status {
	case "queued", "processing":
		return StatusInProgress
	case "done":
		return StatusDone
	case "blocked":
		return StatusBlocked
	default:
		return Dim
	}
}

// StatusIcon returns the icon for a given status.
func StatusIcon(status string) string {
	switch status {
	case "queued":
		return "◦"
	case "processing":
		return "⋯"
	case "done":
		return "✓"
	case "blocked":
		return "!"
	default:
		return "·"
	}
}

// ProjectColor returns the color for a project.
func ProjectColor(project string) lipgloss.Color {
	switch project {
	case "offerlab":
		return ColorOfferlab
	case "influencekit":
		return ColorInfluenceKit
	case "personal":
		return ColorPersonal
	default:
		return ColorMuted
	}
}

// StatusColor returns the background color for a status.
func StatusColor(status string) lipgloss.Color {
	switch status {
	case "queued", "processing":
		return ColorInProgress
	case "done":
		return ColorDone
	case "blocked":
		return ColorBlocked
	default:
		return ColorMuted
	}
}
