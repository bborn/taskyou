package ui

import (
	"fmt"
	"time"
)

// formatRelativeTimeWithNow formats a future time relative to the provided base time.
func formatRelativeTimeWithNow(t, now time.Time) string {
	diff := t.Sub(now)

	if diff < 0 {
		return "overdue"
	}

	if diff < time.Hour {
		mins := int(diff.Minutes())
		if mins <= 0 {
			return "now"
		}
		return fmt.Sprintf("%dm", mins)
	}

	if diff < 24*time.Hour {
		hours := int(diff.Hours())
		return fmt.Sprintf("%dh", hours)
	}

	if t.Day() == now.Day() && t.Month() == now.Month() && t.Year() == now.Year() {
		return t.Format("3:04pm")
	}

	tomorrow := now.AddDate(0, 0, 1)
	if t.Day() == tomorrow.Day() && t.Month() == tomorrow.Month() && t.Year() == tomorrow.Year() {
		return "tmrw " + t.Format("3pm")
	}

	if t.Year() == now.Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 '06")
}

// formatScheduleTime retains the old helper signature for schedule indicators.
func formatScheduleTime(t time.Time) string {
	return formatRelativeTimeWithNow(t, time.Now())
}

// FormatRelativeTime exposes the relative time formatter for other packages.
func FormatRelativeTime(t time.Time, now time.Time) string {
	return formatRelativeTimeWithNow(t, now)
}
