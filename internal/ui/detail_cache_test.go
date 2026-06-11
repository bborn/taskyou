package ui

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

// assertNoStaleDetailView verifies the detail View() render cache returns fresh
// output after a mutation. It warms the cache with the pre-mutation state, applies
// the mutation, captures the (cached) render, then forces a cold re-render of the
// same final state and asserts the two are identical. If the signature failed to
// notice the mutation, the warm render returns stale bytes that differ from the
// cold render — failing the test. It also asserts the mutation actually changed
// the output, so the check can't pass vacuously.
func assertNoStaleDetailView(t *testing.T, name string, m *DetailModel, mutate func(*DetailModel)) {
	t.Helper()

	before := m.View() // warm the cache with the pre-mutation state

	mutate(m)
	cached := m.View() // cache hit iff the signature missed the mutation (stale)

	// Cold render of the identical post-mutation state.
	m.cachedViewOK = false
	fresh := m.View()

	if cached != fresh {
		t.Errorf("%s: detail View cache served stale output after mutation", name)
	}
	if before == fresh {
		t.Errorf("%s: mutation did not change rendered output; test is vacuous", name)
	}
}

func TestDetailViewCacheInvalidation(t *testing.T) {
	forceColor(t)

	cases := []struct {
		name   string
		mutate func(*DetailModel)
	}{
		{"focus", func(m *DetailModel) { m.focused = false }},
		{"status", func(m *DetailModel) { m.task.Status = db.StatusDone }},
		{"pane error", func(m *DetailModel) { m.paneError = "the executor exploded" }},
		{"pane loading", func(m *DetailModel) { m.paneLoading = true }},
		{"running shell process", func(m *DetailModel) { m.hasRunningShellProc = true }},
		{"pr info added", func(m *DetailModel) {
			m.prInfo = &github.PRInfo{Number: 7, State: github.PRStateOpen, CheckState: github.CheckStatePassing, Additions: 12, Deletions: 3}
		}},
		{"resize width", func(m *DetailModel) { m.SetSize(120, m.height) }},
		{"body content", func(m *DetailModel) {
			m.task.Body = "A completely different task body that should re-render the viewport."
			m.setViewportContent()
		}},
		{"summary content", func(m *DetailModel) {
			m.task.Summary = "A brand new activity summary line."
			m.setViewportContent()
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := benchDetail()
			m.setViewportContent()
			assertNoStaleDetailView(t, tc.name, m, tc.mutate)
		})
	}
}

// TestDetailViewCacheShellTab verifies the collapsed-shell indicator (which only
// appears inside tmux) is part of the View signature.
func TestDetailViewCacheShellTab(t *testing.T) {
	forceColor(t)
	t.Setenv("TMUX", "/tmp/tmux-test/default")
	m := benchDetail()
	m.setViewportContent()
	assertNoStaleDetailView(t, "shell pane hidden", m, func(m *DetailModel) {
		m.shellPaneHidden = true
	})
}

// TestDetailViewCacheScroll verifies the scroll offset is part of the View
// signature, using a short viewport so the content actually overflows.
func TestDetailViewCacheScroll(t *testing.T) {
	forceColor(t)
	m := benchDetail()
	m.SetSize(160, 14) // small enough that the body overflows the viewport
	m.setViewportContent()
	if m.viewport.TotalLineCount() <= m.viewport.VisibleLineCount() {
		t.Skip("content did not overflow viewport; cannot exercise scroll")
	}
	assertNoStaleDetailView(t, "scroll offset", m, func(m *DetailModel) {
		m.viewport.SetYOffset(2)
	})
}

// TestDetailViewCacheIdleStable verifies repeated renders with no change return
// the exact same bytes — the cache-hit path is correct, not just fast.
func TestDetailViewCacheIdleStable(t *testing.T) {
	forceColor(t)
	m := benchDetail()
	m.setViewportContent()
	first := m.View()
	for i := 0; i < 5; i++ {
		if got := m.View(); got != first {
			t.Fatalf("idle re-render %d differed from first render", i)
		}
	}
}

// TestPendingPaneAction verifies the open-path decision tree for a task with no
// existing tmux window: which status starts an executor, which skips, and which
// waits for the daemon.
func TestPendingPaneAction(t *testing.T) {
	cases := []struct {
		name     string
		task     *db.Task
		expected paneAction
	}{
		{"backlog skips", &db.Task{Status: db.StatusBacklog}, paneActionSkip},
		{"done skips", &db.Task{Status: db.StatusDone}, paneActionSkip},
		{"archived skips", &db.Task{Status: db.StatusArchived}, paneActionSkip},
		{"queued without worktree waits", &db.Task{Status: db.StatusQueued}, paneActionWaitForExecutor},
		{"queued with worktree starts", &db.Task{Status: db.StatusQueued, WorktreePath: "/tmp/wt"}, paneActionStartExecutor},
		{"processing starts", &db.Task{Status: db.StatusProcessing}, paneActionStartExecutor},
		{"blocked starts", &db.Task{Status: db.StatusBlocked}, paneActionStartExecutor},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pendingPaneAction(tc.task); got != tc.expected {
				t.Errorf("pendingPaneAction(%s) = %v, want %v", tc.task.Status, got, tc.expected)
			}
		})
	}
}

// TestNewDetailModelOpensAsync verifies that opening a task inside tmux returns
// immediately with a background command and a loading spinner, instead of
// blocking the UI thread on the tmux join. This is the core "instant open" fix.
func TestNewDetailModelOpensAsync(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-test/default")

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	task := &db.Task{ID: 1, Title: "Async open", Status: db.StatusProcessing, Body: "# Body"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	m, cmd := NewDetailModel(task, database, nil, 160, 50, false)

	if cmd == nil {
		t.Fatal("expected a background command for async pane setup, got nil (open path is blocking)")
	}
	if !m.paneLoading {
		t.Error("expected paneLoading=true so the loading spinner shows while panes set up")
	}
	// The view must paint immediately rather than waiting on tmux.
	if view := m.View(); strings.TrimSpace(view) == "" {
		t.Error("expected the detail view to render immediately on open")
	}
}
