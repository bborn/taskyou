package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
)

func setupRoutinesView(t *testing.T) *RoutinesModel {
	t.Helper()
	routinesDir := t.TempDir()
	t.Setenv("TY_ROUTINES_DIR", routinesDir)
	t.Setenv("TY_ROUTINES_STATE_DIR", filepath.Join(t.TempDir(), "state"))

	for _, name := range []string{"alpha-scout", "beta-scout"} {
		dir := filepath.Join(routinesDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte("prompt"), 0o644); err != nil {
			t.Fatalf("write prompt: %v", err)
		}
	}

	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	runID, err := database.CreateRoutineRun("alpha-scout")
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := database.FinishRoutineRun(runID, db.RoutineRunStatusFailed, 1, "boom"); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	return NewRoutinesModel(database, 100, 40)
}

func keyPress(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	}
	return tea.KeyMsg{}
}

func TestRoutinesViewRendersHealthTable(t *testing.T) {
	m := setupRoutinesView(t)

	view := m.View()
	for _, want := range []string{"Routines", "alpha-scout", "beta-scout", "failed", "never run"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestRoutinesViewCursorAndBack(t *testing.T) {
	m := setupRoutinesView(t)

	if m.cursor != 0 {
		t.Fatalf("expected cursor 0, got %d", m.cursor)
	}
	m, _ = m.Update(keyPress("down"))
	if m.cursor != 1 {
		t.Errorf("expected cursor 1 after down, got %d", m.cursor)
	}
	m, _ = m.Update(keyPress("down"))
	if m.cursor != 1 {
		t.Errorf("cursor should clamp at last row, got %d", m.cursor)
	}
	m, _ = m.Update(keyPress("esc"))
	if !m.done {
		t.Error("esc should mark the view done")
	}
}

func TestRoutinesViewToggleDisable(t *testing.T) {
	m := setupRoutinesView(t)

	m, _ = m.Update(keyPress("d"))
	if !m.routines[0].Disabled {
		t.Error("expected alpha-scout disabled after d")
	}
	if !strings.Contains(m.View(), "disabled") {
		t.Error("view should show disabled state")
	}
	m, _ = m.Update(keyPress("d"))
	if m.routines[0].Disabled {
		t.Error("expected alpha-scout re-enabled after second d")
	}
}

func TestRoutinesViewLogViewer(t *testing.T) {
	m := setupRoutinesView(t)

	// alpha-scout has a failed run with stored output; log file path is fake,
	// so the viewer falls back to the stored output tail.
	m, _ = m.Update(keyPress("enter"))
	if !m.viewingLog {
		t.Fatal("enter should open the log view")
	}
	if !strings.Contains(m.View(), "boom") {
		t.Errorf("log view missing run output:\n%s", m.View())
	}
	m, _ = m.Update(keyPress("esc"))
	if m.viewingLog {
		t.Error("esc should close the log view")
	}
	if m.done {
		t.Error("closing the log view should not exit the routines view")
	}

	// beta-scout has no runs — enter is a no-op.
	m, _ = m.Update(keyPress("down"))
	m, _ = m.Update(keyPress("enter"))
	if m.viewingLog {
		t.Error("enter on a never-run routine should not open a log view")
	}
}
