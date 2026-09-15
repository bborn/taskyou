package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/bborn/workflow/internal/db"
)

func TestSessionClosed_OffersResume(t *testing.T) {
	m := &DetailModel{task: &db.Task{ID: 1, Status: db.StatusBlocked}, focused: true}

	if m.SessionClosed() {
		t.Fatal("a view whose session never closed must not offer resume")
	}
	if help := ansi.Strip(m.renderHelp()); strings.Contains(help, "resume session") {
		t.Errorf("help offers resume with a live session: %s", help)
	}

	// What afterWindowClosed leaves behind once the idle sweep kills the window.
	m.afterWindowClosed()
	if !m.SessionClosed() {
		t.Fatal("a blocked task whose window closed should offer resume")
	}
	if help := ansi.Strip(m.renderHelp()); !strings.Contains(help, "enter resume session") {
		t.Errorf("help does not offer resume: %s", help)
	}

	// Only blocked tasks are left closed; anything else sets itself up again.
	m.task.Status = db.StatusDone
	if m.SessionClosed() {
		t.Error("resume offered for a done task")
	}
}
