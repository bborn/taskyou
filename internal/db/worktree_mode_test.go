package db

import (
	"path/filepath"
	"testing"
)

func TestNormalizeWorktreeMode(t *testing.T) {
	cases := map[string]string{
		"worktree":   WorktreeModeWorktree,
		"  Worktree": WorktreeModeWorktree, // trimmed + case-insensitive
		"in-place":   WorktreeModeInPlace,
		"in_place":   WorktreeModeInPlace,
		"inplace":    WorktreeModeInPlace,
		"In-Place":   WorktreeModeInPlace,
		"":           WorktreeModeInherit,
		"bogus":      WorktreeModeInherit, // unknown values inherit the project setting
	}
	for in, want := range cases {
		if got := NormalizeWorktreeMode(in); got != want {
			t.Errorf("NormalizeWorktreeMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShouldUseWorktree(t *testing.T) {
	worktreesOn := &Project{Name: "on", UseWorktrees: true}
	worktreesOff := &Project{Name: "off", UseWorktrees: false}

	cases := []struct {
		name    string
		project *Project
		task    *Task
		want    bool
	}{
		// Inherit (default): the project setting decides, exactly as before.
		{"inherit follows project on", worktreesOn, &Task{}, true},
		{"inherit follows project off", worktreesOff, &Task{}, false},
		{"inherit with nil project defaults on", nil, &Task{}, true},
		{"nil task follows project on", worktreesOn, nil, true},
		{"nil task follows project off", worktreesOff, nil, false},

		// Force worktree: wins even when the project default is off.
		{"force worktree in worktrees-off project", worktreesOff, &Task{WorktreeMode: WorktreeModeWorktree}, true},
		{"force worktree in worktrees-on project", worktreesOn, &Task{WorktreeMode: WorktreeModeWorktree}, true},

		// Force in-place: wins even when the project default is on.
		{"force in-place in worktrees-on project", worktreesOn, &Task{WorktreeMode: WorktreeModeInPlace}, false},
		{"force in-place in worktrees-off project", worktreesOff, &Task{WorktreeMode: WorktreeModeInPlace}, false},

		// Unknown values inherit (never crash, never surprise).
		{"unknown mode inherits project on", worktreesOn, &Task{WorktreeMode: "bogus"}, true},
		{"unknown mode inherits project off", worktreesOff, &Task{WorktreeMode: "bogus"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldUseWorktree(c.project, c.task); got != c.want {
				t.Errorf("ShouldUseWorktree(%v, %v) = %v, want %v", c.project, c.task, got, c.want)
			}
		})
	}
}

func TestResolveWorktreeBase(t *testing.T) {
	cases := []struct {
		name          string
		task          *Task
		defaultBranch string
		want          string
	}{
		{"empty base branch uses default", &Task{}, "main", "main"},
		{"explicit base branch wins", &Task{BaseBranch: "release/2.0"}, "main", "release/2.0"},
		{"whitespace-only base branch uses default", &Task{BaseBranch: "  "}, "main", "main"},
		{"base branch is trimmed", &Task{BaseBranch: " develop "}, "main", "develop"},
		{"nil task uses default", nil, "main", "main"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveWorktreeBase(c.task, c.defaultBranch); got != c.want {
				t.Errorf("ResolveWorktreeBase(%v, %q) = %q, want %q", c.task, c.defaultBranch, got, c.want)
			}
		})
	}
}

// TestTaskWorktreeModePersists verifies the worktree_mode and base_branch
// columns round-trip through CreateTask, GetTask, ListTasks, and UpdateTask
// (i.e. the migration ran and every scan site includes the new columns).
func TestTaskWorktreeModePersists(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	if err := database.CreateProject(&Project{Name: "p", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	task := &Task{
		Title:        "T",
		Status:       StatusBacklog,
		Type:         TypeCode,
		Project:      "p",
		WorktreeMode: WorktreeModeInPlace,
		BaseBranch:   "release/2.0",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.WorktreeMode != WorktreeModeInPlace {
		t.Errorf("WorktreeMode = %q, want %q", got.WorktreeMode, WorktreeModeInPlace)
	}
	if got.BaseBranch != "release/2.0" {
		t.Errorf("BaseBranch = %q, want %q", got.BaseBranch, "release/2.0")
	}

	tasks, err := database.ListTasks(ListTasksOptions{Project: "p"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].WorktreeMode != WorktreeModeInPlace || tasks[0].BaseBranch != "release/2.0" {
		t.Errorf("ListTasks did not round-trip worktree fields: %+v", tasks)
	}

	got.WorktreeMode = WorktreeModeWorktree
	got.BaseBranch = ""
	if err := database.UpdateTask(got); err != nil {
		t.Fatalf("update task: %v", err)
	}
	got, err = database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task after update: %v", err)
	}
	if got.WorktreeMode != WorktreeModeWorktree || got.BaseBranch != "" {
		t.Errorf("after update: WorktreeMode = %q, BaseBranch = %q; want %q, %q",
			got.WorktreeMode, got.BaseBranch, WorktreeModeWorktree, "")
	}
}

// TestCreateTaskNormalizesWorktreeMode verifies CreateTask stores a normalized
// worktree mode so sloppy spellings from API callers don't leak into the DB.
func TestCreateTaskNormalizesWorktreeMode(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	if err := database.CreateProject(&Project{Name: "p", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	task := &Task{Title: "T", Status: StatusBacklog, Type: TypeCode, Project: "p", WorktreeMode: "In_Place"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	got, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.WorktreeMode != WorktreeModeInPlace {
		t.Errorf("WorktreeMode = %q, want %q", got.WorktreeMode, WorktreeModeInPlace)
	}
}
