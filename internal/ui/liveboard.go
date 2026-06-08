package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
)

// Live mode turns the kanban board into a real-time window on what each agent
// is doing: a per-card activity line, elapsed time, and an animated spinner on
// running tasks. It is opt-in (toggled with the live-mode key) and never the
// default, so the compact board stays untouched for users who prefer it.

// SetLiveMode enables or disables live mode.
func (k *KanbanBoard) SetLiveMode(on bool) {
	if k.liveMode == on {
		return
	}
	k.liveMode = on
	// Card height changes with the mode, so re-derive scroll so the selected
	// task stays on screen.
	k.ensureSelectedVisible()
}

// ToggleLiveMode flips live mode and returns the new state.
func (k *KanbanBoard) ToggleLiveMode() bool {
	k.SetLiveMode(!k.liveMode)
	return k.liveMode
}

// LiveMode reports whether live mode is active.
func (k *KanbanBoard) LiveMode() bool {
	return k.liveMode
}

// SetLatestActivity updates the most-recent-log-per-task map used to render
// activity lines in live mode.
func (k *KanbanBoard) SetLatestActivity(activity map[int64]*db.TaskLog) {
	k.latestActivity = activity
}

// AdvanceSpinner moves the running-task spinner to its next animation frame.
func (k *KanbanBoard) AdvanceSpinner() {
	k.spinnerFrame++
}

// liveSpinner returns the current spinner glyph (reusing the detail view's
// braille frames for visual consistency).
func (k *KanbanBoard) liveSpinner() string {
	if len(spinnerFrames) == 0 {
		return IconProcessing()
	}
	return spinnerFrames[k.spinnerFrame%len(spinnerFrames)]
}

// RunningTaskCount returns how many tasks are currently processing.
func (k *KanbanBoard) RunningTaskCount() int {
	n := 0
	for _, col := range k.columns {
		for _, t := range col.Tasks {
			if t.Status == db.StatusProcessing {
				n++
			}
		}
	}
	return n
}

// NeedsInputCount returns how many tasks are waiting on the user.
func (k *KanbanBoard) NeedsInputCount() int {
	n := 0
	for _, col := range k.columns {
		for _, t := range col.Tasks {
			if k.NeedsInput(t.ID) {
				n++
			}
		}
	}
	return n
}

// cardSubLine renders the live-mode third line for a card: the agent's latest
// activity for running tasks, an attention prompt for tasks needing input, and
// a concise age hint otherwise. Returns the rendered (already styled) line.
func (k *KanbanBoard) cardSubLine(task *db.Task, width int, isSelected bool) string {
	innerWidth := width - 2 // account for horizontal padding
	if innerWidth < 6 {
		innerWidth = 6
	}

	text, color := k.subLineContent(task)
	text = truncateRunes(text, innerWidth)

	// Selected cards already invert colors via the card background, so render
	// the sub-line plain to stay legible.
	if isSelected {
		return text
	}
	return lipgloss.NewStyle().Foreground(color).Render(text)
}

// subLineContent returns the raw text and color for a card's live sub-line.
func (k *KanbanBoard) subLineContent(task *db.Task) (string, lipgloss.Color) {
	switch task.Status {
	case db.StatusProcessing:
		activity := ""
		if log := k.latestActivity[task.ID]; log != nil {
			activity = cleanActivityContent(log.Content)
		}
		elapsed := taskElapsedShort(task)
		switch {
		case activity != "" && elapsed != "":
			return elapsed + " · " + activity, ColorMuted
		case activity != "":
			return activity, ColorMuted
		default:
			return taskAgeHint(task), ColorMuted
		}
	case db.StatusBlocked:
		if k.NeedsInput(task.ID) {
			return IconBlocked() + " needs your input", ColorWarning
		}
		return taskAgeHint(task), ColorMuted
	default:
		return taskAgeHint(task), ColorMuted
	}
}

// taskReferenceTime picks the timestamp that best represents a task's current
// state, used to compute its age hint.
func taskReferenceTime(task *db.Task) time.Time {
	switch task.Status {
	case db.StatusProcessing:
		if task.StartedAt != nil && !task.StartedAt.IsZero() {
			return task.StartedAt.Time
		}
	case db.StatusDone:
		if task.CompletedAt != nil && !task.CompletedAt.IsZero() {
			return task.CompletedAt.Time
		}
	case db.StatusBlocked, db.StatusQueued:
		if !task.UpdatedAt.IsZero() {
			return task.UpdatedAt.Time
		}
	}
	return task.CreatedAt.Time
}

// taskAgeHint returns a concise, status-aware age string (e.g. "running 4m",
// "queued 2h", "created 3d").
func taskAgeHint(task *db.Task) string {
	ref := taskReferenceTime(task)
	if ref.IsZero() {
		return ""
	}
	dur := formatShortDuration(time.Since(ref))
	switch task.Status {
	case db.StatusProcessing:
		return "running " + dur
	case db.StatusBlocked:
		return "blocked " + dur
	case db.StatusQueued:
		return "queued " + dur
	case db.StatusDone:
		return "done " + dur
	default:
		return "created " + dur
	}
}

// taskElapsedMinutes returns the integer-minute bucket since the task's
// reference time. Used by the render-cache hash so age hints invalidate when
// they tick over a minute boundary.
func taskElapsedMinutes(task *db.Task) int {
	ref := taskReferenceTime(task)
	if ref.IsZero() {
		return 0
	}
	d := time.Since(ref)
	if d < 0 {
		d = -d
	}
	return int(d.Minutes())
}

// taskElapsedShort returns just the bare duration since a task's reference time
// (e.g. "4m"), or empty if unknown.
func taskElapsedShort(task *db.Task) string {
	ref := taskReferenceTime(task)
	if ref.IsZero() {
		return ""
	}
	return formatShortDuration(time.Since(ref))
}

// formatShortDuration renders a duration compactly: "12s", "4m", "3h", "2d".
func formatShortDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// cleanActivityContent collapses a raw log entry into a single, trimmed line
// suitable for inline display on a card.
func cleanActivityContent(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx != -1 {
		s = s[:idx]
	}
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.TrimSpace(s)
}

// truncateRunes shortens a string to at most maxLen display runes, appending an
// ellipsis when truncated. Rune-aware so multi-byte content isn't split.
func truncateRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	if maxLen == 1 {
		return "…"
	}
	return string(r[:maxLen-1]) + "…"
}
