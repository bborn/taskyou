package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/hooks"
)

func testItems() []PluginActionItem {
	return []PluginActionItem{
		{Plugin: hooks.Plugin{Name: "p1"}, Action: hooks.Action{ID: "a", Label: "Alpha"}},
		{Plugin: hooks.Plugin{Name: "p2"}, Action: hooks.Action{ID: "b", Label: "Beta"}},
	}
}

func keyFor(s string) tea.KeyMsg {
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestActionPicker_SelectDefaultIsFirst(t *testing.T) {
	m := NewActionPickerModel("task", testItems(), 80, 24)
	m, _ = m.Update(keyFor("enter"))
	sel := m.Selected()
	if sel == nil || sel.Action.ID != "a" {
		t.Fatalf("selected = %v, want action a", sel)
	}
}

func TestActionPicker_NavigateThenSelect(t *testing.T) {
	m := NewActionPickerModel("task", testItems(), 80, 24)
	m, _ = m.Update(keyFor("down"))
	m, _ = m.Update(keyFor("enter"))
	if sel := m.Selected(); sel == nil || sel.Action.ID != "b" {
		t.Fatalf("selected = %v, want action b", sel)
	}
}

func TestActionPicker_WrapAround(t *testing.T) {
	m := NewActionPickerModel("task", testItems(), 80, 24)
	m, _ = m.Update(keyFor("up")) // wraps from 0 to last
	m, _ = m.Update(keyFor("enter"))
	if sel := m.Selected(); sel == nil || sel.Action.ID != "b" {
		t.Fatalf("selected = %v, want last item after wrap", sel)
	}
}

func TestActionPicker_Cancel(t *testing.T) {
	m := NewActionPickerModel("task", testItems(), 80, 24)
	m, _ = m.Update(keyFor("esc"))
	if !m.IsCancelled() {
		t.Error("expected cancelled after esc")
	}
	if m.Selected() != nil {
		t.Error("expected no selection after cancel")
	}
}

func TestActionPicker_EmptyListEnterIsNoop(t *testing.T) {
	m := NewActionPickerModel("task", nil, 80, 24)
	m, _ = m.Update(keyFor("enter"))
	if m.Selected() != nil {
		t.Error("enter on empty list should not select")
	}
	// View must not panic on an empty list.
	_ = m.View()
}
