package ui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

func newOnboardingTestModel(t *testing.T) *AppModel {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return &AppModel{db: database, width: 100, height: 40}
}

// TestOnboarding_PastedRepoURLOpensTheCloneView walks the first-run path a
// user takes: the folder picker, a pasted URL, enter.
func TestOnboarding_PastedRepoURLOpensTheCloneView(t *testing.T) {
	m := newOnboardingTestModel(t)
	m.currentView = ViewFolderPicker
	m.folderPicker = NewFolderPickerModel(m.width, m.height)

	for _, r := range "https://github.com/bborn/taskyou" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app := updated.(*AppModel)

	// The picker asks for a clone; the app opens the clone view for it.
	req, ok := cmd().(repoRequestedMsg)
	if !ok {
		t.Fatalf("want repoRequestedMsg from the picker, got %T", cmd())
	}
	updated, _ = app.Update(req)
	app = updated.(*AppModel)

	if app.currentView != ViewRepoClone || app.repoClone == nil {
		t.Fatalf("view = %v, repoClone = %v — want the clone view", app.currentView, app.repoClone)
	}
	if app.folderPicker != nil {
		t.Error("the folder picker should be closed while cloning")
	}
	if got := app.repoClone.ref.Slug(); got != "bborn/taskyou" {
		t.Errorf("clone view is pointed at %q", got)
	}
}

// TestOnboarding_ClonedRepoRejoinsTheFolderPath is the point of the whole
// feature: once the clone lands, it is an ordinary picked folder.
func TestOnboarding_ClonedRepoRejoinsTheFolderPath(t *testing.T) {
	m := newOnboardingTestModel(t)
	repo := t.TempDir()
	mkGitRepo(t, repo)
	m.currentView = ViewRepoClone
	m.repoClone = newRepoCloneModel(mustRef(t, "bborn/taskyou"), github.Cloner{Root: t.TempDir()}, m.width, m.height)

	updated, _ := m.Update(repoClonedMsg{path: repo})
	app := updated.(*AppModel)

	if app.currentView != ViewProjectDetectConfirm {
		t.Fatalf("view = %v, want the ordinary project-confirm card", app.currentView)
	}
	if app.repoClone != nil {
		t.Error("the clone view should be closed once the repo is on disk")
	}
	if app.detectedProject == nil || app.detectedProject.Path != repo {
		t.Errorf("detected project = %+v, want one pointed at %s", app.detectedProject, repo)
	}
}

// TestOnboarding_EscFromTheCloneViewGoesBackToThePicker keeps the back door
// open: a mistyped URL shouldn't strand anyone.
func TestOnboarding_EscFromTheCloneViewGoesBackToThePicker(t *testing.T) {
	m := newOnboardingTestModel(t)
	m.currentView = ViewRepoClone
	m.repoClone = newRepoCloneModel(mustRef(t, "bborn/taskyou"), github.Cloner{Root: t.TempDir()}, m.width, m.height)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	app := updated.(*AppModel)

	if app.currentView != ViewFolderPicker || app.folderPicker == nil {
		t.Fatalf("view = %v — esc should return to the folder picker", app.currentView)
	}
	if app.repoClone != nil {
		t.Error("the clone view should be discarded on the way back")
	}
}
