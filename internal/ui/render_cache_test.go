package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

// forceColor makes lipgloss emit ANSI styling during the test. Without a TTY,
// lipgloss defaults to the Ascii profile and strips all colours/bold/background,
// which would make colour-only mutations (theme, project colour, selection
// highlight) invisible in the rendered string — and therefore untestable.
func forceColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// assertNoStaleRender verifies the render cache returns fresh output after a
// mutation. It warms the cache with the pre-mutation state, applies the
// mutation, captures the (cached) render, then forces a cold re-render of the
// same final state and asserts the two are identical. If the cache's signature
// failed to notice the mutation, the warm render would return stale bytes that
// differ from the cold render — failing the test.
//
// It also asserts the mutation actually changed the output, so the check can't
// pass vacuously.
func assertNoStaleRender(t *testing.T, name string, k *KanbanBoard, mutate func(*KanbanBoard)) {
	t.Helper()

	before := k.View() // warm the cache with the pre-mutation state

	mutate(k)
	cached := k.View() // cache hit iff the signature missed the mutation (stale)

	// Cold render of the identical post-mutation state.
	k.cachedViewOK = false
	k.cardCache = nil
	fresh := k.View()

	if cached != fresh {
		t.Errorf("%s: render cache served stale output after mutation", name)
	}
	if before == fresh {
		t.Errorf("%s: mutation did not change rendered output; test is vacuous", name)
	}
}

func TestRenderCacheInvalidation(t *testing.T) {
	forceColor(t)

	cases := []struct {
		name   string
		mutate func(*KanbanBoard)
	}{
		{"select row", func(k *KanbanBoard) { k.selectedRow = 1 }},
		{"select column", func(k *KanbanBoard) { k.selectedCol = 2 }},
		{"collapse column", func(k *KanbanBoard) { k.ToggleColumnCollapse(3) }},
		{"hidden done count", func(k *KanbanBoard) { k.SetHiddenDoneCount(7) }},
		{"task title", func(k *KanbanBoard) { k.columns[0].Tasks[0].Title = "Brand new title that is different" }},
		{"task status", func(k *KanbanBoard) { k.columns[0].Tasks[0].Status = db.StatusDone }},
		{"task pinned", func(k *KanbanBoard) { k.columns[0].Tasks[0].Pinned = !k.columns[0].Tasks[0].Pinned }},
		{"task permission mode", func(k *KanbanBoard) {
			// An unselected, active task so the permission-mode dot is drawn.
			tk := k.columns[0].Tasks[1]
			tk.Status = db.StatusProcessing
			tk.PermissionMode = "dangerous"
		}},
		{"running process", func(k *KanbanBoard) {
			id := k.columns[0].Tasks[1].ID // unselected card
			k.runningProcesses[id] = !k.runningProcesses[id]
		}},
		{"needs input", func(k *KanbanBoard) {
			// col0/row1 is an unselected backlog task; clearing its input flag
			// changes its warning tint.
			id := k.columns[0].Tasks[1].ID
			k.tasksNeedingInput[id] = false
		}},
		{"blocker count", func(k *KanbanBoard) {
			k.blockedByDeps[k.columns[0].Tasks[0].ID] = 5
		}},
		{"pr info added", func(k *KanbanBoard) {
			id := k.columns[1].Tasks[0].ID
			k.SetPRInfo(id, &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStateFailing, Mergeable: "CONFLICTING"})
		}},
		{"pr diff stats", func(k *KanbanBoard) {
			id := k.columns[0].Tasks[0].ID
			k.SetPRInfo(id, &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStatePassing, Mergeable: "MERGEABLE", Additions: 999, Deletions: 1})
		}},
		{"resize width", func(k *KanbanBoard) { k.SetSize(120, 50) }},
		{"resize height", func(k *KanbanBoard) { k.SetSize(160, 40) }},
		{"new task list", func(k *KanbanBoard) {
			tasks := append(k.allTasks, &db.Task{ID: 9999, Title: "An extra task appears", Status: db.StatusBacklog, Project: "workflow"})
			k.SetTasks(tasks)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := benchBoard()
			assertNoStaleRender(t, tc.name, k, tc.mutate)
		})
	}
}

// TestRenderCacheScroll verifies the scroll offset is part of the render
// signature, using a short board so the column actually overflows.
func TestRenderCacheScroll(t *testing.T) {
	forceColor(t)
	k := benchBoard()
	k.SetSize(160, 18) // small enough that the backlog column overflows
	assertNoStaleRender(t, "scroll offset", k, func(k *KanbanBoard) {
		k.scrollOffsets[0] = 1
	})
}

// TestRenderCacheProjectColorInvalidation verifies that changing a project's
// color (which bumps the style generation) invalidates cached renders.
func TestRenderCacheProjectColorInvalidation(t *testing.T) {
	forceColor(t)

	// Save and restore the global project color cache so we don't leak state.
	projectColorMu.RLock()
	saved := make(map[string]string, len(projectColorCache))
	for k, v := range projectColorCache {
		saved[k] = v
	}
	projectColorMu.RUnlock()
	t.Cleanup(func() { SetProjectColors(saved) })

	k := benchBoard()
	assertNoStaleRender(t, "project color", k, func(k *KanbanBoard) {
		SetProjectColor("workflow", "#123456")
	})
}

// TestRenderCacheThemeInvalidation verifies a theme switch (which changes colors
// globally) invalidates cached renders via the style generation counter.
func TestRenderCacheThemeInvalidation(t *testing.T) {
	forceColor(t)

	orig := CurrentTheme().Name
	t.Cleanup(func() { _ = SetTheme(orig) })

	k := benchBoard()
	assertNoStaleRender(t, "theme switch", k, func(k *KanbanBoard) {
		// Switch to a theme different from the current one.
		target := "dracula"
		if orig == target {
			target = "nord"
		}
		if err := SetTheme(target); err != nil {
			t.Fatalf("SetTheme: %v", err)
		}
		k.RefreshTheme()
	})
}

// TestRenderCacheIdleStable verifies repeated renders with no change return the
// exact same bytes (the cache-hit path is correct, not just fast).
func TestRenderCacheIdleStable(t *testing.T) {
	forceColor(t)
	k := benchBoard()
	first := k.View()
	for i := 0; i < 5; i++ {
		if got := k.View(); got != first {
			t.Fatalf("idle re-render %d differed from first render", i)
		}
	}
}

// TestCardCacheRespectsWidthAndSelection ensures the per-card cache key
// distinguishes renders that must differ.
func TestCardCacheRespectsWidthAndSelection(t *testing.T) {
	forceColor(t)
	k := benchBoard()
	task := k.columns[0].Tasks[0]

	narrow := k.renderTaskCard(task, 24, false)
	wide := k.renderTaskCard(task, 48, false)
	if narrow == wide {
		t.Error("card cache returned identical output for different widths")
	}

	unselected := k.renderTaskCard(task, 36, false)
	selected := k.renderTaskCard(task, 36, true)
	if unselected == selected {
		t.Error("card cache returned identical output for selected vs unselected")
	}

	// Same inputs must hit the cache and return identical bytes.
	again := k.renderTaskCard(task, 36, false)
	if again != unselected {
		t.Error("card cache returned different output for identical inputs")
	}
}
