package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func newDetectTestModel(t *testing.T, workingDir string) (*AppModel, *db.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return &AppModel{db: database, workingDir: workingDir, width: 80, height: 24}, database
}

func TestMaybeOfferProjectCreation_OffersForNewGitRepo(t *testing.T) {
	repo := t.TempDir()
	mkGitRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("be good"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newDetectTestModel(t, repo)

	_, _, offered := m.maybeOfferProjectCreation()
	if !offered {
		t.Fatal("expected offer for new git repo")
	}
	if m.currentView != ViewProjectDetectConfirm {
		t.Fatalf("expected ViewProjectDetectConfirm, got %v", m.currentView)
	}
	if m.detectedProject == nil {
		t.Fatal("expected detectedProject to be set")
	}
	if m.detectedProject.Instructions != "be good" {
		t.Errorf("instructions = %q", m.detectedProject.Instructions)
	}
	if m.detectedInstructionSource != "AGENTS.md" {
		t.Errorf("source = %q", m.detectedInstructionSource)
	}

	// Should not offer a second time in the same session.
	if _, _, offered := m.maybeOfferProjectCreation(); offered {
		t.Fatal("should not offer twice in a session")
	}
}

func TestMaybeOfferProjectCreation_SkipsNonGit(t *testing.T) {
	m, _ := newDetectTestModel(t, t.TempDir())
	if _, _, offered := m.maybeOfferProjectCreation(); offered {
		t.Fatal("should not offer for a non-git directory")
	}
}

func TestMaybeOfferProjectCreation_SkipsWhenDismissed(t *testing.T) {
	repo := t.TempDir()
	mkGitRepo(t, repo)
	m, database := newDetectTestModel(t, repo)
	if err := database.SetSetting(projectSuggestionDismissedKey(repo), "1"); err != nil {
		t.Fatal(err)
	}
	if _, _, offered := m.maybeOfferProjectCreation(); offered {
		t.Fatal("should not offer when previously dismissed")
	}
}

func TestMaybeOfferProjectCreation_SkipsWhenProjectExists(t *testing.T) {
	repo := t.TempDir()
	mkGitRepo(t, repo)
	m, database := newDetectTestModel(t, repo)
	if err := database.CreateProject(&db.Project{Name: "existing", Path: repo}); err != nil {
		t.Fatal(err)
	}
	if _, _, offered := m.maybeOfferProjectCreation(); offered {
		t.Fatal("should not offer when a project already covers the directory")
	}
}

func TestDismissProjectSuggestionPersists(t *testing.T) {
	repo := t.TempDir()
	mkGitRepo(t, repo)
	m, database := newDetectTestModel(t, repo)

	m.dismissProjectSuggestion(m.workingDir)

	v, _ := database.GetSetting(projectSuggestionDismissedKey(repo))
	if v != "1" {
		t.Fatalf("expected dismissal to be persisted, got %q", v)
	}
	// And a fresh model should now skip the offer.
	m2, _ := newDetectTestModel(t, repo)
	m2.db = database
	if _, _, offered := m2.maybeOfferProjectCreation(); offered {
		t.Fatal("should not offer after dismissal persisted")
	}
}

func TestCreateDetectedProjectPersists(t *testing.T) {
	repo := t.TempDir()
	m, database := newDetectTestModel(t, repo)

	// UseWorktrees false avoids invoking git in the test; the persistence,
	// last-used-project, and notification logic is what we're verifying here.
	project := &db.Project{Name: "myrepo", Path: repo, Instructions: "x", UseWorktrees: false}

	m.createDetectedProject(project)

	got, err := database.GetProjectByName("myrepo")
	if err != nil || got == nil {
		t.Fatalf("expected project to be created, err=%v", err)
	}
	last, _ := database.GetLastUsedProject()
	if last != "myrepo" {
		t.Errorf("expected last-used project myrepo, got %q", last)
	}
	if m.notification == "" {
		t.Error("expected a success notification")
	}
}
