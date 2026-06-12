package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// TestFormWorktreeModeDefaultsToInherit verifies a new form starts on
// "project default" so creating a task without touching the field preserves
// the project-level worktree behavior exactly.
func TestFormWorktreeModeDefaultsToInherit(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)

	if m.worktreeMode != db.WorktreeModeInherit {
		t.Errorf("worktreeMode = %q, want inherit (empty)", m.worktreeMode)
	}

	task := m.GetDBTask()
	if task.WorktreeMode != db.WorktreeModeInherit {
		t.Errorf("GetDBTask().WorktreeMode = %q, want inherit (empty)", task.WorktreeMode)
	}
	if task.BaseBranch != "" {
		t.Errorf("GetDBTask().BaseBranch = %q, want empty", task.BaseBranch)
	}
}

// TestFormApplyToCarriesWorktreeFields verifies the selected worktree mode and
// base branch are applied to the task.
func TestFormApplyToCarriesWorktreeFields(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	m.worktreeMode = db.WorktreeModeWorktree
	m.baseBranchInput.SetValue("  release/2.0  ")

	task := m.GetDBTask()
	if task.WorktreeMode != db.WorktreeModeWorktree {
		t.Errorf("WorktreeMode = %q, want %q", task.WorktreeMode, db.WorktreeModeWorktree)
	}
	if task.BaseBranch != "release/2.0" {
		t.Errorf("BaseBranch = %q, want trimmed %q", task.BaseBranch, "release/2.0")
	}
}

// TestFormInPlaceClearsBaseBranch verifies the base branch is not carried for
// in-place tasks, where no worktree is ever created.
func TestFormInPlaceClearsBaseBranch(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	m.baseBranchInput.SetValue("release/2.0")
	m.worktreeMode = db.WorktreeModeInPlace

	task := m.GetDBTask()
	if task.WorktreeMode != db.WorktreeModeInPlace {
		t.Errorf("WorktreeMode = %q, want %q", task.WorktreeMode, db.WorktreeModeInPlace)
	}
	if task.BaseBranch != "" {
		t.Errorf("BaseBranch = %q, want empty for in-place task", task.BaseBranch)
	}
}

// TestFormBaseBranchVisibility verifies the base-branch input only shows when
// a fresh worktree will be created.
func TestFormBaseBranchVisibility(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	m.showAdvanced = true

	cases := []struct {
		name                 string
		mode                 string
		projectUsesWorktrees bool
		want                 bool
	}{
		{"inherit with worktrees-on project", db.WorktreeModeInherit, true, true},
		{"inherit with worktrees-off project", db.WorktreeModeInherit, false, false},
		{"forced worktree in worktrees-off project", db.WorktreeModeWorktree, false, true},
		{"in-place in worktrees-on project", db.WorktreeModeInPlace, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m.worktreeMode = c.mode
			m.projectUsesWorktrees = c.projectUsesWorktrees
			if got := m.isFieldVisible(FieldBaseBranch); got != c.want {
				t.Errorf("isFieldVisible(FieldBaseBranch) = %v, want %v", got, c.want)
			}
		})
	}
}

// TestEditFormPrepopulatesWorktreeFields verifies editing keeps the task's
// existing worktree mode and base branch.
func TestEditFormPrepopulatesWorktreeFields(t *testing.T) {
	task := &db.Task{
		Title:        "T",
		WorktreeMode: db.WorktreeModeWorktree,
		BaseBranch:   "release/2.0",
	}
	m := NewEditFormModel(nil, task, 100, 50, nil)

	if m.worktreeMode != db.WorktreeModeWorktree {
		t.Errorf("worktreeMode = %q, want %q", m.worktreeMode, db.WorktreeModeWorktree)
	}
	if m.worktreeModeIdx != worktreeModeIndexFor(worktreeModeOptions(), db.WorktreeModeWorktree) {
		t.Errorf("worktreeModeIdx = %d not aligned with mode", m.worktreeModeIdx)
	}
	if got := m.baseBranchInput.Value(); got != "release/2.0" {
		t.Errorf("baseBranchInput = %q, want %q", got, "release/2.0")
	}
}
