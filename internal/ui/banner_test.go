package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// dashboardWithNotification builds the minimum AppModel viewDashboard needs:
// a sized kanban board and an unexpired notification.
func dashboardWithNotification(width int, notification string) *AppModel {
	return &AppModel{
		width:              width,
		height:             30,
		kanban:             NewKanbanBoard(width, 24),
		availableExecutors: []string{"claude"},
		notification:       notification,
		notifyUntil:        time.Now().Add(time.Minute),
		keys:               LoadKeyMap(),
	}
}

// A notification longer than the terminal used to render at its natural width,
// which made the whole dashboard wider than the terminal: the terminal re-wrapped
// every line and the kanban columns came apart.
func TestNotificationBannerDoesNotWidenDashboard(t *testing.T) {
	const width = 120
	long := "✓ Task #12 complete: " + strings.Repeat("a very long task title ", 10) + "(g to jump)"

	m := dashboardWithNotification(width, long)
	view := m.viewDashboard()

	if got := lipgloss.Width(view); got != width {
		t.Errorf("dashboard width = %d, want %d (banner overflowed the terminal)", got, width)
	}
}

// Notification text carries git/gh output, which is routinely multi-line. Each
// extra row pushes the board down, so newlines are folded into the single row.
func TestBannerFoldsNewlinesIntoOneRow(t *testing.T) {
	banner := renderBanner("failed to push branch:\nfatal: could not read Username", bannerWarnBg, bannerWarnFg, 120)

	if got := lipgloss.Height(banner); got != 1 {
		t.Errorf("banner height = %d, want 1", got)
	}
	plain := stripAnsiCodes(banner)
	if !strings.Contains(plain, "failed to push branch: fatal: could not read Username") {
		t.Errorf("newline was not folded to a space: %q", plain)
	}
}

func TestBannerFillsTerminalWidth(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		banner := renderBanner("✓ Task #12 complete", bannerWarnBg, bannerWarnFg, width)
		if got := lipgloss.Width(banner); got != width {
			t.Errorf("banner width at terminal %d = %d, want %d", width, got, width)
		}
	}
}

// Truncation keeps the banner on one row and marks that text was cut.
func TestBannerTruncatesWithEllipsis(t *testing.T) {
	banner := renderBanner(strings.Repeat("x", 200), bannerWarnBg, bannerWarnFg, 60)

	if got := lipgloss.Width(banner); got != 60 {
		t.Errorf("banner width = %d, want 60", got)
	}
	if got := lipgloss.Height(banner); got != 1 {
		t.Errorf("banner height = %d, want 1", got)
	}
	if plain := stripAnsiCodes(banner); !strings.Contains(plain, "…") {
		t.Errorf("truncated banner has no ellipsis: %q", plain)
	}
}

// Before the first WindowSizeMsg the model has no width; a banner sized to it
// would render as a stray fragment.
func TestBannerEmptyBeforeFirstResize(t *testing.T) {
	if got := renderBanner("anything", bannerWarnBg, bannerWarnFg, 0); got != "" {
		t.Errorf("banner at width 0 = %q, want empty", got)
	}
}
