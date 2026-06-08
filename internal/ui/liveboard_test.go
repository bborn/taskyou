package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

func TestKanbanBoard_ToggleLiveMode(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	if board.LiveMode() {
		t.Fatal("live mode should be off by default")
	}
	if got := board.cardHeight(); got != 3 {
		t.Errorf("compact cardHeight = %d, want 3", got)
	}

	if on := board.ToggleLiveMode(); !on {
		t.Fatal("ToggleLiveMode should return true after enabling")
	}
	if !board.LiveMode() {
		t.Fatal("live mode should be on after toggle")
	}
	if got := board.cardHeight(); got != 4 {
		t.Errorf("live cardHeight = %d, want 4", got)
	}

	if on := board.ToggleLiveMode(); on {
		t.Fatal("ToggleLiveMode should return false after disabling")
	}
	if board.LiveMode() {
		t.Fatal("live mode should be off after second toggle")
	}
}

func TestKanbanBoard_LiveModeRendersActivity(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	board.SetTasks([]*db.Task{
		{ID: 7, Title: "Refactor auth", Status: db.StatusProcessing},
	})
	board.SetLiveMode(true)
	board.SetLatestActivity(map[int64]*db.TaskLog{
		7: {TaskID: 7, LineType: "system", Content: "Editing store.go"},
	})

	out := board.View()
	if !strings.Contains(out, "Editing store.go") {
		t.Errorf("live view should show the agent activity line, got:\n%s", out)
	}
}

func TestKanbanBoard_CompactModeHidesActivity(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	board.SetTasks([]*db.Task{
		{ID: 7, Title: "Refactor auth", Status: db.StatusProcessing},
	})
	// Live mode OFF (default). Activity is provided but must not render.
	board.SetLatestActivity(map[int64]*db.TaskLog{
		7: {TaskID: 7, LineType: "system", Content: "Editing store.go"},
	})

	out := board.View()
	if strings.Contains(out, "Editing store.go") {
		t.Errorf("compact view must not show activity lines, got:\n%s", out)
	}
}

func TestKanbanBoard_LiveModeShowsAgeHint(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	board.SetTasks([]*db.Task{
		{ID: 9, Title: "Write docs", Status: db.StatusBacklog,
			CreatedAt: db.LocalTime{Time: time.Now().Add(-5 * time.Minute)}},
	})
	board.SetLiveMode(true)

	out := board.View()
	if !strings.Contains(out, "created 5m") {
		t.Errorf("live view should show an age hint for backlog tasks, got:\n%s", out)
	}
}

func TestKanbanBoard_LiveModeNeedsInputPrompt(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	board.SetTasks([]*db.Task{
		{ID: 11, Title: "Add webhooks", Status: db.StatusBlocked},
	})
	board.SetTasksNeedingInput(map[int64]bool{11: true})
	board.SetLiveMode(true)

	out := board.View()
	if !strings.Contains(out, "needs your input") {
		t.Errorf("blocked task needing input should show prompt, got:\n%s", out)
	}
	if board.NeedsInputCount() != 1 {
		t.Errorf("NeedsInputCount = %d, want 1", board.NeedsInputCount())
	}
}

func TestKanbanBoard_RunningTaskCount(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	board.SetTasks([]*db.Task{
		{ID: 1, Title: "a", Status: db.StatusProcessing},
		{ID: 2, Title: "b", Status: db.StatusProcessing},
		{ID: 3, Title: "c", Status: db.StatusBacklog},
	})
	if got := board.RunningTaskCount(); got != 2 {
		t.Errorf("RunningTaskCount = %d, want 2", got)
	}
}

func TestKanbanBoard_SpinnerAdvances(t *testing.T) {
	board := NewKanbanBoard(100, 50)
	first := board.liveSpinner()
	board.AdvanceSpinner()
	second := board.liveSpinner()
	if first == second {
		t.Errorf("spinner frame should change after AdvanceSpinner (%q == %q)", first, second)
	}
}

func TestFormatShortDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
		{-2 * time.Minute, "2m"}, // negative clamps to absolute
	}
	for _, c := range cases {
		if got := formatShortDuration(c.in); got != c.want {
			t.Errorf("formatShortDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hell…"},
		{"héllo wörld", 5, "héll…"}, // rune-safe, no broken bytes
		{"x", 0, ""},
		{"abc", 1, "…"},
	}
	for _, c := range cases {
		if got := truncateRunes(c.in, c.maxLen); got != c.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", c.in, c.maxLen, got, c.want)
		}
	}
}

func TestCleanActivityContent(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"  Editing file  ", "Editing file"},
		{"first line\nsecond line", "first line"},
		{"tab\tseparated", "tab separated"},
	}
	for _, c := range cases {
		if got := cleanActivityContent(c.in); got != c.want {
			t.Errorf("cleanActivityContent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTaskAgeHint(t *testing.T) {
	now := time.Now()
	processing := &db.Task{
		Status:    db.StatusProcessing,
		StartedAt: &db.LocalTime{Time: now.Add(-2 * time.Minute)},
	}
	if got := taskAgeHint(processing); !strings.HasPrefix(got, "running ") {
		t.Errorf("processing age hint = %q, want 'running ...'", got)
	}

	backlog := &db.Task{
		Status:    db.StatusBacklog,
		CreatedAt: db.LocalTime{Time: now.Add(-3 * time.Hour)},
	}
	if got := taskAgeHint(backlog); got != "created 3h" {
		t.Errorf("backlog age hint = %q, want 'created 3h'", got)
	}
}
