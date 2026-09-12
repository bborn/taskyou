package ui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
)

// helpRowTask is a task in the state that produces the widest detail help row:
// nothing is running, so execute, retry and the rest are all offered.
func helpRowModel(width int, expanded bool) *DetailModel {
	return &DetailModel{
		task:          &db.Task{ID: 4873, Title: "Apple Pay express checkout"},
		width:         width,
		helpExpanded:  expanded,
		focused:       true,
		totalInColumn: 4,
	}
}

func TestDetailHelpRowFitsTerminalWidth(t *testing.T) {
	// 152 is a common full-screen iTerm width; 80 is the classic terminal floor.
	for _, width := range []int{200, 152, 100, 80} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			got := lipgloss.Width(helpRowModel(width, true).renderHelp())
			if got > width {
				t.Errorf("expanded help row is %d columns wide, want <= %d", got, width)
			}
		})
	}
}

func TestDetailHelpRowAlwaysKeepsTheWayOut(t *testing.T) {
	// Whatever gets dropped, the keys that leave the view must survive: 'esc'
	// closes the detail view and '?' collapses the row again.
	for _, width := range []int{200, 152, 100, 80, 40} { //nolint:gocritic // 40 is below the floor on purpose
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			help := helpRowModel(width, true).renderHelp()
			for _, want := range []string{"back", "less"} {
				if !strings.Contains(help, want) {
					t.Errorf("width %d: help row lost %q: %s", width, want, help)
				}
			}
		})
	}
}

func TestDetailHelpRowShowsNewShortcutsAtNormalWidth(t *testing.T) {
	help := helpRowModel(152, true).renderHelp()
	for _, want := range []string{"copy id", "terminal"} {
		if !strings.Contains(help, want) {
			t.Errorf("help row is missing %q: %s", want, help)
		}
	}
}

func TestDetailHelpRowCollapsedIsShort(t *testing.T) {
	help := helpRowModel(152, false).renderHelp()
	if strings.Contains(help, "copy id") {
		t.Error("collapsed help row should not list secondary keys")
	}
	if !strings.Contains(help, "more") {
		t.Error("collapsed help row should offer '? more'")
	}
}

func TestDetailHelpRowFloorIsThePrimaryKeys(t *testing.T) {
	// Primary keys are never dropped, so they set a floor the row cannot go
	// under. Narrower than that and the terminal clips, as it always has; the
	// detail view's bordered box is unusable at that size anyway. This test
	// pins the floor so a future primary key is a deliberate choice.
	help := helpRowModel(40, true).renderHelp()

	if strings.Contains(help, "copy id") || strings.Contains(help, "archive") {
		t.Errorf("secondary keys should be dropped well before 40 columns: %s", help)
	}
	for _, want := range []string{"execute", "edit", "status", "back", "less"} {
		if !strings.Contains(help, want) {
			t.Errorf("primary key %q should survive any width: %s", want, help)
		}
	}
}
