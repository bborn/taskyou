package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestResolveProjectPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"tilde only", "~", home},
		{"tilde slash", "~/Projects/app", filepath.Join(home, "Projects/app")},
		{"absolute", "/tmp/foo", "/tmp/foo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveProjectPath(tt.input)
			if err != nil {
				t.Fatalf("resolveProjectPath(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("resolveProjectPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestSaveProjectUpdatesPath verifies that editing an existing project's
// directory in the settings form persists the new path.
func TestSaveProjectUpdatesPath(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	oldDir := t.TempDir()
	newDir := t.TempDir()

	proj := &db.Project{Name: "myapp", Path: oldDir, UseWorktrees: false}
	if err := database.CreateProject(proj); err != nil {
		t.Fatalf("create project: %v", err)
	}

	m := &SettingsModel{db: database, width: 100, height: 40}
	m.loadSettings()

	// Simulate opening the edit form for the project and changing the directory.
	m.showProjectForm(proj)
	m.projectFormPath = newDir
	m.saveProject()

	if m.err != nil {
		t.Fatalf("saveProject returned error: %v", m.err)
	}

	updated, err := database.GetProjectByName("myapp")
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if updated.Path != newDir {
		t.Errorf("project path = %q, want %q", updated.Path, newDir)
	}
}

// TestSaveProjectRejectsMissingPath verifies that editing a project to point at
// a non-existent directory surfaces an error rather than creating it.
func TestSaveProjectRejectsMissingPath(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	oldDir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	proj := &db.Project{Name: "myapp", Path: oldDir, UseWorktrees: false}
	if err := database.CreateProject(proj); err != nil {
		t.Fatalf("create project: %v", err)
	}

	m := &SettingsModel{db: database, width: 100, height: 40}
	m.loadSettings()

	m.showProjectForm(proj)
	m.projectFormPath = missing
	m.saveProject()

	if m.err == nil {
		t.Fatal("expected error for missing path, got nil")
	}

	// Path should remain unchanged in the database.
	updated, err := database.GetProjectByName("myapp")
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if updated.Path != oldDir {
		t.Errorf("project path = %q, want unchanged %q", updated.Path, oldDir)
	}

	// The bad directory must not have been created.
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Errorf("expected %q to not exist", missing)
	}
}

// TestShowProjectFormDefaultsNameToFolder verifies that for a new project (folder
// chosen first), the form pre-fills the name from the folder name.
func TestShowProjectFormDefaultsNameToFolder(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	dir := t.TempDir()
	m := &SettingsModel{db: database, width: 100, height: 40}
	m.showProjectForm(&db.Project{Path: dir}) // new project: ID == 0, path already picked

	if m.projectFormName != filepath.Base(dir) {
		t.Errorf("projectFormName = %q, want folder name %q", m.projectFormName, filepath.Base(dir))
	}
	// A fresh (non-git) folder auto-disables worktrees.
	if m.projectFormUseWorktrees {
		t.Error("expected worktrees auto-disabled for a non-git folder")
	}
}

// TestSaveProjectDefaultsNameToFolder verifies that saving a new project with a
// blank name falls back to the folder name, and that the simplified form no
// longer sets a per-project permission default.
func TestSaveProjectDefaultsNameToFolder(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	dir := t.TempDir()
	wantName := filepath.Base(dir)

	m := &SettingsModel{db: database, width: 100, height: 40}
	m.loadSettings()

	// Simulate folder-first creation: path picked, name left blank.
	m.editProject = &db.Project{Path: dir}
	m.showProjectForm(m.editProject)
	m.projectFormName = "" // user cleared the pre-filled name
	m.saveProject()

	if m.err != nil {
		t.Fatalf("saveProject returned error: %v", m.err)
	}

	got, err := database.GetProjectByName(wantName)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if got == nil {
		t.Fatalf("expected project named %q to be created", wantName)
	}
	// Permission mode is no longer a project setting; it must stay empty.
	if got.DefaultPermissionMode != "" {
		t.Errorf("DefaultPermissionMode = %q, want empty (moved to task level)", got.DefaultPermissionMode)
	}
}

// TestShowProjectFormIncludesPathForExisting verifies the edit form pre-fills
// the editable directory field for an existing project.
func TestShowProjectFormIncludesPathForExisting(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	dir := t.TempDir()
	proj := &db.Project{Name: "myapp", Path: dir, UseWorktrees: false}
	if err := database.CreateProject(proj); err != nil {
		t.Fatalf("create project: %v", err)
	}

	m := &SettingsModel{db: database, width: 100, height: 40}
	m.showProjectForm(proj)

	if m.projectFormPath != dir {
		t.Errorf("projectFormPath = %q, want %q", m.projectFormPath, dir)
	}
	if m.projectForm == nil {
		t.Fatal("expected project form to be created")
	}
}

// TestSaveProjectRejectsRenamingPersonal verifies that the settings Edit form
// refuses to rename the seeded "personal" project away from "personal",
// mirroring the existing deletion guard in showDeleteProjectConfirm.
//
// The personal project is the implicit default for every new task (CreateTask
// falls back to t.Project = "personal") and is re-seeded by
// ensurePersonalProject on every Open, so a rename would break default task
// creation, defeat the name-based DeleteProject guard, and — across a reopen —
// orphan the user's customized row behind a fresh default. The UI guard
// surfaces the error inline (via reshowProjectFormWithError), preserving the
// user's input; the DB-layer guard in UpdateProject catches retries and other
// surfaces (CLI, HTTP).
func TestSaveProjectRejectsRenamingPersonal(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	personal, err := database.GetProjectByName("personal")
	if err != nil {
		t.Fatalf("get personal project: %v", err)
	}
	if personal == nil {
		t.Fatal("personal project not seeded by migrations")
	}
	personalID := personal.ID

	m := &SettingsModel{db: database, width: 100, height: 40}
	m.loadSettings()

	// Simulate opening the Edit form for the personal project and typing a
	// new name. UseWorktrees is disabled to keep the test hermetic — the
	// seeded personal dir does have a git repo (initGitRepo), but skipping
	// the git logic here means the test only depends on the rename guard.
	m.showProjectForm(personal)
	m.projectFormName = "mywork"
	m.projectFormUseWorktrees = false

	m.saveProject()

	if m.err == nil {
		t.Fatal("expected error when renaming personal project via saveProject, got nil")
	}
	if m.err.Error() != "cannot rename the personal project" {
		t.Errorf("expected 'cannot rename the personal project', got %q", m.err.Error())
	}

	// The form must stay open with the input preserved so the user can fix
	// the name (reshowProjectFormWithError rather than dropping back to the
	// settings list).
	if !m.editingProject {
		t.Error("expected the project form to stay open after the rename was rejected")
	}
	if m.projectForm == nil {
		t.Error("expected m.projectForm to be re-created after the rename was rejected")
	}
	if m.projectFormName != "mywork" {
		t.Errorf("expected the rejected name %q to be preserved in the form, got %q", "mywork", m.projectFormName)
	}

	// The DB row must be intact: same ID, still named "personal", no "mywork".
	stillThere, err := database.GetProjectByName("personal")
	if err != nil {
		t.Fatalf("get personal after rejected rename: %v", err)
	}
	if stillThere == nil {
		t.Fatal("personal project disappeared after rejected rename")
	}
	if stillThere.ID != personalID {
		t.Errorf("personal ID = %d, want %d", stillThere.ID, personalID)
	}
	if stillThere.Name != "personal" {
		t.Errorf("personal Name = %q, want %q", stillThere.Name, "personal")
	}
	if other, _ := database.GetProjectByName("mywork"); other != nil {
		t.Errorf("a 'mywork' project exists after rejected rename: %+v", other)
	}

	// Default task creation must still route to "personal" — i.e. the bug
	// (CreateTask falls back to "personal" → GetProjectByName returns nil →
	// ErrProjectNotFound) does not reproduce after the rejected rename.
	task := &db.Task{Title: "default-path task", Status: db.StatusBacklog}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("expected default task creation to still work, got %v", err)
	}
	if task.Project != "personal" {
		t.Errorf("task.Project = %q, want %q", task.Project, "personal")
	}

	// The name-based delete guard must still protect the row (no regression in
	// the existing deletion guard).
	if err := database.DeleteProject(personalID); err == nil {
		t.Error("expected deleting the personal project to still be blocked")
	}

	// Retry: simulate the user pressing Enter again without fixing the name.
	// reshowProjectFormWithError overwrote m.editProject.Name with the
	// rejected "mywork", so the UI guard (which checks m.editProject.Name)
	// no longer fires — but the DB-layer guard in UpdateProject reads the
	// row's current name from the DB and rejects the retry there.
	m.saveProject()
	if m.err == nil {
		t.Fatal("expected retry of personal rename to be rejected, got nil")
	}
	if m.err.Error() != "cannot rename the personal project" {
		t.Errorf("expected retry to surface 'cannot rename the personal project', got %q", m.err.Error())
	}
	// The form should still be open after a DB-guard rejection (we now route
	// DB save errors through reshowProjectFormWithError too, so the retry UX
	// matches the first attempt).
	if !m.editingProject {
		t.Error("expected the project form to stay open after a rejected retry")
	}
	stillThere2, err := database.GetProjectByName("personal")
	if err != nil || stillThere2 == nil || stillThere2.Name != "personal" || stillThere2.ID != personalID {
		t.Errorf("personal row not intact after rejected retry: err=%v row=%+v", err, stillThere2)
	}

	// Non-rename edits of the personal project through the UI must still
	// succeed: keeping the name "personal" and changing only instructions is
	// an ordinary "save my edits" flow that the guard must not block.
	m2 := &SettingsModel{db: database, width: 100, height: 40}
	m2.loadSettings()
	fresh, err := database.GetProjectByName("personal")
	if err != nil {
		t.Fatalf("get fresh personal: %v", err)
	}
	if fresh == nil {
		t.Fatal("personal project disappeared before non-rename edit")
	}
	m2.showProjectForm(fresh)
	m2.projectFormName = "personal" // unchanged
	m2.projectFormInstructions = "edited instructions via UI"
	m2.projectFormUseWorktrees = false

	m2.saveProject()

	if m2.err != nil {
		t.Fatalf("non-rename update of personal should succeed, got %v", m2.err)
	}
	edited, err := database.GetProjectByName("personal")
	if err != nil {
		t.Fatalf("get edited personal: %v", err)
	}
	if edited.Instructions != "edited instructions via UI" {
		t.Errorf("instructions = %q, want %q", edited.Instructions, "edited instructions via UI")
	}
}
