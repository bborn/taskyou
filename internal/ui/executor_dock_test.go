package ui

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// fakePaneController records calls and returns canned snapshots.
type fakePaneController struct {
	captures   []int64 // task IDs captured, in order
	snapshot   string
	joined     []int64
	broken     []int64
	focused    []string
	tuiFocused bool
}

func (f *fakePaneController) Capture(task *db.Task, lines int) string {
	f.captures = append(f.captures, task.ID)
	return f.snapshot
}

func (f *fakePaneController) JoinBelow(task *db.Task, tuiHeightPercent int) (string, error) {
	f.joined = append(f.joined, task.ID)
	return "%99", nil
}

func (f *fakePaneController) BreakBack(task *db.Task, paneID string) error {
	f.broken = append(f.broken, task.ID)
	return nil
}

func (f *fakePaneController) FocusPane(paneID string) error {
	f.focused = append(f.focused, paneID)
	return nil
}

func (f *fakePaneController) TUIPaneFocused() bool { return f.tuiFocused }
func (f *fakePaneController) ResizeTUIFull()       {}

func TestDock_ToggleOpensAndCloses(t *testing.T) {
	d := NewDockModel(&fakePaneController{})
	if d.IsOpen() {
		t.Fatal("dock should start closed")
	}
	d.Toggle()
	if !d.IsOpen() {
		t.Fatal("dock should be open after toggle")
	}
	d.Toggle()
	if d.IsOpen() {
		t.Fatal("dock should be closed after second toggle")
	}
}

func TestDock_ClosedIsZeroHeight(t *testing.T) {
	d := NewDockModel(&fakePaneController{})
	if d.Height(40) != 0 {
		t.Fatalf("closed dock height = %d, want 0", d.Height(40))
	}
	d.Toggle()
	if d.Height(40) == 0 {
		t.Fatal("open dock height should be > 0")
	}
}

func TestDock_RefreshCapturesSelectedTaskWhenOpenSnapshot(t *testing.T) {
	f := &fakePaneController{snapshot: "● Bash(ls)\nDo you want to proceed?"}
	d := NewDockModel(f)
	task := &db.Task{ID: 4281}

	// Closed: refresh must be a no-op (no capture).
	d.Refresh(task, 40)
	if len(f.captures) != 0 {
		t.Fatalf("closed dock captured %d times, want 0", len(f.captures))
	}

	// Open snapshot: refresh captures the selected task.
	d.Toggle()
	d.Refresh(task, 40)
	if len(f.captures) != 1 || f.captures[0] != 4281 {
		t.Fatalf("captures = %v, want [4281]", f.captures)
	}
	if d.Snapshot() != f.snapshot {
		t.Fatalf("snapshot = %q, want %q", d.Snapshot(), f.snapshot)
	}
}

func TestDock_RefreshBumpsVersionOnlyOnChange(t *testing.T) {
	f := &fakePaneController{snapshot: "same"}
	d := NewDockModel(f)
	d.Toggle()
	task := &db.Task{ID: 1}
	d.Refresh(task, 40)
	v1 := d.contentVersion
	d.Refresh(task, 40) // identical content
	if d.contentVersion != v1 {
		t.Fatal("version bumped despite unchanged snapshot")
	}
	f.snapshot = "different"
	d.Refresh(task, 40)
	if d.contentVersion == v1 {
		t.Fatal("version did not bump on changed snapshot")
	}
}

func TestDock_ViewShowsSnapshotAndHeader(t *testing.T) {
	f := &fakePaneController{snapshot: "● Bash(cat x)\nProceed? > 1. Yes"}
	d := NewDockModel(f)
	d.Toggle()
	task := &db.Task{ID: 4281}
	d.Refresh(task, 40)

	out := d.View(80, 40, task)
	if !strings.Contains(out, "4281") {
		t.Fatalf("view missing task id, got:\n%s", out)
	}
	if !strings.Contains(out, "Proceed?") {
		t.Fatalf("view missing snapshot content, got:\n%s", out)
	}
	if !strings.Contains(out, "shift") { // promotion hint
		t.Fatalf("view missing interact hint, got:\n%s", out)
	}
}

func TestDock_ViewEmptyWhenClosed(t *testing.T) {
	d := NewDockModel(&fakePaneController{})
	if d.View(80, 40, &db.Task{ID: 1}) != "" {
		t.Fatal("closed dock View should be empty")
	}
}
