package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/bborn/workflow/internal/db"
)

func TestRenderHelp_FollowsPaneFocus(t *testing.T) {
	m := &DetailModel{task: &db.Task{ID: 1}, totalInColumn: 3, focused: true}

	focused := ansi.Strip(m.renderHelp())
	for _, want := range []string{"prev/next task", "edit", "esc"} {
		if !strings.Contains(focused, want) {
			t.Errorf("focused help missing %q: %s", want, focused)
		}
	}
	if strings.Contains(focused, "alt+shift") {
		t.Errorf("focused help should not show pane-level keys: %s", focused)
	}

	// With the executor or shell focused, TUI keys would type into that pane.
	m.focused = false
	unfocused := ansi.Strip(m.renderHelp())
	for _, want := range []string{"alt+shift+", "prev/next task", "shift+", "switch pane"} {
		if !strings.Contains(unfocused, want) {
			t.Errorf("unfocused help missing %q: %s", want, unfocused)
		}
	}
	for _, gone := range []string{"edit", "status", "esc", "more"} {
		if strings.Contains(unfocused, gone) {
			t.Errorf("unfocused help should not list %q: %s", gone, unfocused)
		}
	}
}
