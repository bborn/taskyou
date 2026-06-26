package ui

import (
	"fmt"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

// cyc returns s[i mod len(s)], used to spread fixture variety across tasks.
func cyc(s []string, i int) string { return s[i%len(s)] } //nolint:gosec // i%len(s) is always in bounds

// benchBoard builds a realistic, busy kanban board for render benchmarking:
// a wide terminal with many tasks across all columns, a mix of PR info,
// running processes, permission modes, pins, blockers, and input flags.
// This is the worst case for per-frame allocations.
func benchBoard() *KanbanBoard {
	k := NewKanbanBoard(160, 50)

	statuses := []string{db.StatusBacklog, db.StatusQueued, db.StatusProcessing, db.StatusBlocked, db.StatusDone}
	projects := []string{"offerlab", "influencekit", "workflow", "taskyou"}
	modes := []string{"default", "auto", "accept-edits", "dangerous"}

	var tasks []*db.Task
	prInfo := make(map[int64]*github.PRInfo)
	running := make(map[int64]bool)
	needsInput := make(map[int64]bool)
	blockers := make(map[int64]int)

	for i := 0; i < 60; i++ {
		id := int64(i + 1)
		t := &db.Task{
			ID:             id,
			Title:          fmt.Sprintf("Implement feature %d with a reasonably long descriptive title", i),
			Status:         cyc(statuses, i),
			Project:        cyc(projects, i),
			PermissionMode: cyc(modes, i),
			Pinned:         i%9 == 0,
		}
		tasks = append(tasks, t)

		// Roughly every other task carries a PR with diff stats.
		if i%2 == 0 {
			prInfo[id] = &github.PRInfo{
				State:      github.PRStateOpen,
				CheckState: github.CheckStatePassing,
				Mergeable:  "MERGEABLE",
				Additions:  120 + i,
				Deletions:  30 + i,
			}
		}
		if i%3 == 0 {
			running[id] = true
		}
		if i%5 == 0 {
			needsInput[id] = true
		}
		if i%7 == 0 {
			blockers[id] = 1 + i%3
		}
	}

	k.SetTasks(tasks)
	k.prInfo = prInfo
	k.SetRunningProcesses(running)
	k.SetTasksNeedingInput(needsInput)
	k.SetBlockedByDeps(blockers)
	return k
}

// BenchmarkKanbanViewIdle measures a re-render where nothing changed — the
// overwhelmingly common case (periodic ticks, mouse motion, unrelated events).
// With the render cache this should be a cheap signature hash with no allocation.
func BenchmarkKanbanViewIdle(b *testing.B) {
	k := benchBoard()
	_ = k.View() // warm the cache
	b.ReportAllocs()
	b.ResetTimer()
	var sink string
	for i := 0; i < b.N; i++ {
		sink = k.View()
	}
	_ = sink
}

// BenchmarkKanbanViewNavigate measures moving the selection each frame: the
// board signature changes so it re-renders, but only the two cards whose
// selection state changed miss the per-card cache.
func BenchmarkKanbanViewNavigate(b *testing.B) {
	k := benchBoard()
	_ = k.View()
	b.ReportAllocs()
	b.ResetTimer()
	var sink string
	for i := 0; i < b.N; i++ {
		k.selectedRow = i & 1 // alternate between the first two rows
		sink = k.View()
	}
	_ = sink
}

// BenchmarkKanbanViewCold measures a full render with both caches cleared — the
// worst case (first paint, or the frame after a fresh task load).
func BenchmarkKanbanViewCold(b *testing.B) {
	k := benchBoard()
	b.ReportAllocs()
	b.ResetTimer()
	var sink string
	for i := 0; i < b.N; i++ {
		k.cardCache = nil
		k.cachedViewOK = false
		sink = k.View()
	}
	_ = sink
}

// BenchmarkRenderTaskCardCold measures a single uncached card render — the inner
// loop on a cold board.
func BenchmarkRenderTaskCardCold(b *testing.B) {
	k := benchBoard()
	task := k.columns[0].Tasks[0]
	b.ReportAllocs()
	b.ResetTimer()
	var sink string
	for i := 0; i < b.N; i++ {
		k.cardCache = nil
		sink = k.renderTaskCard(task, 36, false)
	}
	_ = sink
}
