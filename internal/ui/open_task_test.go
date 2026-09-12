package ui

import (
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestOpenPaletteOnLoadIsDroppedByReload(t *testing.T) {
	m := &AppModel{}
	m.OpenPaletteOnLoad("draft offers")
	if m.pendingPaletteQuery != "draft offers" {
		t.Fatalf("pendingPaletteQuery = %q", m.pendingPaletteQuery)
	}
	m.RestoreReloadState(ReloadState{TaskID: 7})
	if m.pendingPaletteQuery != "" {
		t.Errorf("a reload reopened `ty open`'s search: %q", m.pendingPaletteQuery)
	}
}

func TestOpenCommandPaletteWithQuery(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	m := &AppModel{
		db:          database,
		width:       100,
		height:      40,
		currentView: ViewDashboard,
		tasks: []*db.Task{
			{ID: 1, Title: "Catalog sync", Status: db.StatusBacklog},
			{ID: 2, Title: "Draft offers bundle", Status: db.StatusBacklog},
		},
	}
	m.openCommandPalette("draft")
	p := m.commandPaletteView
	if m.currentView != ViewCommandPalette || p == nil {
		t.Fatalf("palette not shown: view %v", m.currentView)
	}
	if p.searchInput.Value() != "draft" || p.resultsStale() {
		t.Fatalf("query %q typed, stale=%v", p.searchInput.Value(), p.resultsStale())
	}
	if len(p.filteredTasks) == 0 || p.filteredTasks[0].ID != 2 {
		t.Errorf("top result = %v, want task 2", p.filteredTasks)
	}
}

func TestOpenTaskOnLoadRestoresIntoDetail(t *testing.T) {
	m := &AppModel{}
	m.OpenTaskOnLoad(42)
	if m.pendingFocusTaskID != 42 {
		t.Errorf("pendingFocusTaskID = %d, want 42", m.pendingFocusTaskID)
	}
	if r := m.reloadRestoring; r == nil || r.TaskID != 42 || !r.Detail {
		t.Fatalf("reloadRestoring = %+v, want task 42 in detail", r)
	}

	// A TUI started with `ty open 42` re-runs that command line when it reloads;
	// the reload's own state must win so the user stays where they were.
	m.RestoreReloadState(ReloadState{TaskID: 7})
	if m.pendingFocusTaskID != 7 {
		t.Errorf("after reload restore, pendingFocusTaskID = %d, want 7", m.pendingFocusTaskID)
	}
	if r := m.reloadRestoring; r == nil || r.TaskID != 7 || r.Detail {
		t.Errorf("after reload restore, reloadRestoring = %+v, want task 7 on the board", r)
	}
}
