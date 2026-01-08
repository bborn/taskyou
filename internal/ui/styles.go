// Package ui provides the terminal user interface.
package ui

import "github.com/charmbracelet/lipgloss"

// Colors
var (
	// Primary colors
	ColorPrimary   = lipgloss.Color("#7C3AED") // Purple
	ColorSecondary = lipgloss.Color("#06B6D4") // Cyan
	ColorSuccess   = lipgloss.Color("#10B981") // Green
	ColorWarning   = lipgloss.Color("#F59E0B") // Amber
	ColorError     = lipgloss.Color("#EF4444") // Red
	ColorMuted     = lipgloss.Color("#6B7280") // Gray

	// Status colors
	ColorInProgress = lipgloss.Color("#9A9463") // Subtler muted yellow
	ColorDone       = lipgloss.Color("#10B981") // Green
	ColorBlocked    = lipgloss.Color("#EF4444") // Red

	// Project colors
	ColorOfferlab     = lipgloss.Color("#0052CC") // Blue
	ColorInfluenceKit = lipgloss.Color("#0366D6") // Blue
	ColorPersonal     = lipgloss.Color("#1D76DB") // Blue

	// Type colors
	ColorCode     = lipgloss.Color("#5319E7") // Purple
	ColorWriting  = lipgloss.Color("#7057FF") // Purple
	ColorThinking = lipgloss.Color("#8B5CF6") // Purple
)

// Base styles
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
