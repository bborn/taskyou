package ui

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// A task that ran on this machine looks exactly as it always has: no badge, no
// hint that placement exists.
func TestTaskCardHasNoHostBadgeForALocalTask(t *testing.T) {
	k := NewKanbanBoard(100, 50)
	task := &db.Task{ID: 1, Title: "local task", Status: db.StatusProcessing, Project: "taskyou"}

	if got := k.renderTaskCard(task, 40, false); strings.Contains(got, "@") {
		t.Errorf("card for a local task shows a host badge: %q", got)
	}
}

// Once tasks run on several machines, the card has to say which one produced
// the result, or a host-specific failure is indistinguishable from a real bug.
func TestTaskCardShowsTheHostATaskRanOn(t *testing.T) {
	k := NewKanbanBoard(100, 50)
	task := &db.Task{
		ID: 2, Title: "remote task", Status: db.StatusProcessing, Project: "taskyou",
		PlacementTarget: "ol-agents",
		PlacementReason: "most free memory of 2 hosts serving offerlab",
	}

	if got := k.renderTaskCard(task, 40, false); !strings.Contains(got, "@ol-agents") {
		t.Errorf("card does not name the host it ran on: %q", got)
	}
}

// The per-card render cache keys off everything that changes a card. A task
// that moves hosts between renders must not serve the stale badge.
func TestTaskCardCacheInvalidatesOnPlacementChange(t *testing.T) {
	k := NewKanbanBoard(100, 50)
	task := &db.Task{ID: 3, Title: "moving task", Status: db.StatusProcessing, Project: "taskyou"}

	local := k.renderTaskCard(task, 40, false)
	task.PlacementTarget = "mona"
	placed := k.renderTaskCard(task, 40, false)

	if local == placed {
		t.Error("card cache served the pre-placement render after the task moved hosts")
	}
	if !strings.Contains(placed, "@mona") {
		t.Errorf("re-rendered card does not name the new host: %q", placed)
	}
}
