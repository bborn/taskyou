package ui

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// newNavDetailModel builds a minimal, tmux-free DetailModel suitable for
// exercising the pinned quick-nav logic in isolation.
func newNavDetailModel(t *testing.T) *DetailModel {
	t.Helper()
	t.Setenv("TMUX", "") // no tmux: NewDetailModel returns without pane setup

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	task := &db.Task{ID: 1, Title: "Current", Status: db.StatusProcessing, Body: "body"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	m, _ := NewDetailModel(task, database, nil, 160, 50, false)
	return m
}

func TestPinnedNavVisibility(t *testing.T) {
	m := newNavDetailModel(t)

	// No pins: bar hidden.
	if m.HasPinnedNav() {
		t.Error("expected no pinned nav with zero items")
	}

	// A single pin that is the current task: nothing to hop to, so hidden.
	m.SetPinnedNav([]PinnedNavItem{{ID: 1, Title: "Current"}}, 1)
	if m.HasPinnedNav() {
		t.Error("expected no pinned nav when the only pin is the current task")
	}

	// A single pin that is a *different* task: worth showing.
	m.SetPinnedNav([]PinnedNavItem{{ID: 2, Title: "Other"}}, 1)
	if !m.HasPinnedNav() {
		t.Error("expected pinned nav when there's a different pinned task to jump to")
	}

	// Multiple pins including the current one: shown.
	m.SetPinnedNav([]PinnedNavItem{{ID: 1, Title: "Current"}, {ID: 2, Title: "Other"}}, 1)
	if !m.HasPinnedNav() {
		t.Error("expected pinned nav with multiple pinned tasks")
	}
}

func TestPinnedNavNextPrevWrap(t *testing.T) {
	m := newNavDetailModel(t)
	items := []PinnedNavItem{{ID: 10, Title: "a"}, {ID: 20, Title: "b"}, {ID: 30, Title: "c"}}

	// Current is the middle item.
	m.SetPinnedNav(items, 20)
	if got := m.PinnedNavNextID(); got != 30 {
		t.Errorf("next from middle: got %d, want 30", got)
	}
	if got := m.PinnedNavPrevID(); got != 10 {
		t.Errorf("prev from middle: got %d, want 10", got)
	}

	// Wrap around from the ends.
	m.SetPinnedNav(items, 30)
	if got := m.PinnedNavNextID(); got != 10 {
		t.Errorf("next should wrap to first: got %d, want 10", got)
	}
	m.SetPinnedNav(items, 10)
	if got := m.PinnedNavPrevID(); got != 30 {
		t.Errorf("prev should wrap to last: got %d, want 30", got)
	}

	// Current task not in the pinned set (viewing an unpinned task): next/prev
	// still land on the ends of the list.
	m.SetPinnedNav(items, 999)
	if got := m.PinnedNavNextID(); got != 10 {
		t.Errorf("next with no current index: got %d, want 10", got)
	}
	if got := m.PinnedNavPrevID(); got != 30 {
		t.Errorf("prev with no current index: got %d, want 30", got)
	}
}

func TestPinnedNavIDAt(t *testing.T) {
	m := newNavDetailModel(t)
	items := []PinnedNavItem{{ID: 10}, {ID: 20}, {ID: 30}}
	m.SetPinnedNav(items, 10)

	if got := m.PinnedNavIDAt(1); got != 10 {
		t.Errorf("PinnedNavIDAt(1)=%d, want 10", got)
	}
	if got := m.PinnedNavIDAt(3); got != 30 {
		t.Errorf("PinnedNavIDAt(3)=%d, want 30", got)
	}
	// Out of range returns 0 (no-op).
	if got := m.PinnedNavIDAt(0); got != 0 {
		t.Errorf("PinnedNavIDAt(0)=%d, want 0", got)
	}
	if got := m.PinnedNavIDAt(4); got != 0 {
		t.Errorf("PinnedNavIDAt(4)=%d, want 0", got)
	}
}

func TestPinnedNavRendersInHeader(t *testing.T) {
	m := newNavDetailModel(t)
	m.SetPinnedNav([]PinnedNavItem{
		{ID: 1, Title: "Current"},
		{ID: 42, Title: "Refactor login"},
	}, 1)

	header := m.renderHeader()
	if !strings.Contains(header, "#42") {
		t.Errorf("expected pinned nav bar to reference task #42 in header, got:\n%s", header)
	}
	// The bar adds a line, so headerHeight must grow to keep the viewport clear.
	if m.headerHeight() != 7 {
		t.Errorf("headerHeight with nav = %d, want 7", m.headerHeight())
	}
}
