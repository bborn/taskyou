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
