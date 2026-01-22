package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// DueSeverity represents the urgency of an upcoming or missed deadline.
type DueSeverity int

const (
	// DueSeverityNone indicates no due date.
	DueSeverityNone DueSeverity = iota
	// DueSeverityUpcoming indicates a due date more than a day away.
	DueSeverityUpcoming
	// DueSeveritySoon indicates a due date within the next 24 hours.
	DueSeveritySoon
	// DueSeverityOverdue indicates the due date has already passed.
	DueSeverityOverdue
)

// DueInfo describes how to render a due date indicator.
type DueInfo struct {
	Text     string
	Icon     string
	Severity DueSeverity
}

// BuildDueInfo returns display metadata for a due date relative to now.
func BuildDueInfo(due time.Time, now time.Time) DueInfo {
	if due.IsZero() {
		return DueInfo{}
	}

	diff := due.Sub(now)
	info := DueInfo{}

	if diff <= 0 {
		info.Icon = "⚠"
		info.Severity = DueSeverityOverdue
		info.Text = fmt.Sprintf("%s late", formatDurationShort(-diff))
		return info
	}

	rel := formatRelativeTimeWithNow(due, now)
	info.Text = fmt.Sprintf("due %s", rel)

	if diff <= 24*time.Hour {
		info.Icon = "⌛"
		info.Severity = DueSeveritySoon
	} else {
		info.Icon = "📅"
		info.Severity = DueSeverityUpcoming
	}

	return info
}

// formatDurationShort renders a compact duration string (e.g. 2d, 4h, 15m).
func formatDurationShort(d time.Duration) string {
	if d < 0 {
		d = -d
	}

	if d >= 48*time.Hour {
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd", days)
	}
	if d >= time.Hour {
		hours := int(d.Hours())
		return fmt.Sprintf("%dh", hours)
	}
	mins := int(d.Minutes())
	if mins < 1 {
		mins = 1
	}
	return fmt.Sprintf("%dm", mins)
}

// DueSeverityColor maps a severity to a lipgloss color for display.
func DueSeverityColor(severity DueSeverity) lipgloss.Color {
	switch severity {
	case DueSeverityOverdue:
		return ColorError
	case DueSeveritySoon:
		return ColorWarning
	case DueSeverityUpcoming:
		return ColorPrimary
	default:
		return ColorMuted
	}
}
