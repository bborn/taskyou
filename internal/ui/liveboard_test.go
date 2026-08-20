package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

func TestKanbanBoard_RendersActivity(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	board.SetTasks([]*db.Task{
		{ID: 7, Title: "Refactor auth", Status: db.StatusProcessing},
	})
	board.SetLatestActivity(map[int64]*db.TaskLog{
		7: {TaskID: 7, LineType: "system", Content: "Editing store.go"},
	})

	out := board.View()
	if !strings.Contains(out, "Editing store.go") {
		t.Errorf("board should show the agent activity line, got:\n%s", out)
	}
}

func TestKanbanBoard_ShowsAgeHint(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	board.SetTasks([]*db.Task{
		{ID: 9, Title: "Write docs", Status: db.StatusBacklog,
			CreatedAt: db.LocalTime{Time: time.Now().Add(-5 * time.Minute)}},
	})

	out := board.View()
	if !strings.Contains(out, "created 5m") {
		t.Errorf("board should show an age hint for backlog tasks, got:\n%s", out)
	}
}

func TestKanbanBoard_BlockedShowsStand(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	stand := "Merge email ingest PR"
	board.SetTasks([]*db.Task{
		{ID: 11, Title: "Email ingest", Status: db.StatusBlocked, Summary: stand},
	})
	board.SetTasksNeedingInput(map[int64]bool{11: true})
	board.SetLatestActivity(map[int64]*db.TaskLog{
		11: {TaskID: 11, LineType: "system", Content: "Reconnecting to claude session abc"},
	})

	out := board.View()
	if !strings.Contains(out, "Merge email ingest PR") {
		t.Errorf("blocked card should show stand, got:\n%s", out)
	}
	if strings.Contains(out, "needs your input") {
		t.Errorf("stand should replace the needs-input prompt, got:\n%s", out)
	}
	if strings.Contains(out, "Reconnecting") {
		t.Errorf("reconnect log must not appear on the card, got:\n%s", out)
	}
}

func TestKanbanBoard_BlockedIgnoresFossilRecap(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	board.SetTasks([]*db.Task{
		{ID: 12, Title: "Email ingest", Status: db.StatusBlocked,
			Summary: "- Built the IMAP poller\n- Opened a PR\n- Waiting on merge"},
	})
	board.SetTasksNeedingInput(map[int64]bool{12: true})

	out := board.View()
	if strings.Contains(out, "Built the IMAP poller") {
		t.Errorf("fossil recap must not render as the stand, got:\n%s", out)
	}
	if !strings.Contains(out, "needs your input") {
		t.Errorf("without a stand, blocked+needs-input should fall back to the prompt, got:\n%s", out)
	}
}

func TestKanbanBoard_BlockedFallsBackToQuestion(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	board.SetTasks([]*db.Task{
		{ID: 13, Title: "Webhooks", Status: db.StatusBlocked},
	})
	board.SetLatestActivity(map[int64]*db.TaskLog{
		13: {TaskID: 13, LineType: "question", Content: "Which auth scheme should the webhook use?"},
	})

	out := board.View()
	if !strings.Contains(out, "Which auth scheme") {
		t.Errorf("blocked card should fall back to the question, got:\n%s", out)
	}
}

func TestKanbanBoard_ProcessingIgnoresReconnect(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	board.SetTasks([]*db.Task{
		{ID: 14, Title: "Refactor auth", Status: db.StatusProcessing},
	})
	board.SetLatestActivity(map[int64]*db.TaskLog{
		14: {TaskID: 14, LineType: "system", Content: "Reconnecting to claude session abc"},
	})

	out := board.View()
	if strings.Contains(out, "Reconnecting") {
		t.Errorf("processing card must not show reconnect logs, got:\n%s", out)
	}
}

func TestKanbanBoard_NeedsInputPrompt(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	board.SetTasks([]*db.Task{
		{ID: 11, Title: "Add webhooks", Status: db.StatusBlocked},
	})
	board.SetTasksNeedingInput(map[int64]bool{11: true})

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

// Regression: the per-card cardCache key must include the spinner frame for
// processing tasks. Without it the board-level cache invalidates on spinner
// advance but the per-card cache serves the previous glyph, freezing the
// animation while the chain ticks happily underneath.
func TestKanbanBoard_SpinnerAdvanceChangesRenderedCard(t *testing.T) {
	board := NewKanbanBoard(120, 50)
	board.SetTasks([]*db.Task{
		{ID: 42, Title: "Refactor auth", Status: db.StatusProcessing},
	})

	first := board.View()
	board.AdvanceSpinner()
	second := board.View()
	if first == second {
		t.Fatal("View() should differ after AdvanceSpinner — cardCache may be serving stale glyph")
	}
	if !strings.Contains(second, spinnerFrames[1]) {
		t.Errorf("expected new spinner frame %q in view after advance, got:\n%s", spinnerFrames[1], second)
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
