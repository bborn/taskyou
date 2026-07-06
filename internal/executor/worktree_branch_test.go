package executor

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestNewWorktreeBranchName(t *testing.T) {
	// No pinned branch: derive the usual task/<id>-<slug> name.
	got := newWorktreeBranchName(&db.Task{ID: 42}, "add-dark-mode")
	if want := "task/42-add-dark-mode"; got != want {
		t.Errorf("derived branch = %q, want %q", got, want)
	}

	// A pinned BranchName (as a pipeline sets) is honored verbatim so several
	// phase tasks can share one branch.
	pinned := "pipeline/7-add-rate-limiting"
	got = newWorktreeBranchName(&db.Task{ID: 42, BranchName: pinned}, "add-dark-mode")
	if got != pinned {
		t.Errorf("pinned branch = %q, want %q", got, pinned)
	}

	// Whitespace-only pins are ignored.
	got = newWorktreeBranchName(&db.Task{ID: 9, BranchName: "   "}, "x")
	if want := "task/9-x"; got != want {
		t.Errorf("blank pin branch = %q, want %q", got, want)
	}
}
