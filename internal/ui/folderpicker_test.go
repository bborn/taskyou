package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCollectCandidateFolders(t *testing.T) {
	root := t.TempDir()
	mk := func(parts ...string) {
		if err := os.MkdirAll(filepath.Join(append([]string{root}, parts...)...), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("zeta")
	mk("alpha")
	mk("repo", ".git") // a git repo: must sort first
	mk(".hidden")      // dot-dirs are skipped
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Duplicate + missing roots: de-duplicated and ignored respectively.
	got := collectCandidateFolders([]string{root, root, filepath.Join(root, "missing")})

	want := []string{"repo", "alpha", "zeta"} // git first, then alphabetical
	if len(got) != len(want) {
		t.Fatalf("want %d entries, got %d (%+v)", len(want), len(got), got)
	}
	for i, name := range want {
		if filepath.Base(got[i].path) != name {
			t.Errorf("entry %d: want %q, got %q", i, name, filepath.Base(got[i].path))
		}
	}
	if !got[0].isGit {
		t.Errorf("repo should be detected as a git repo")
	}
	if got[1].isGit || got[2].isGit {
		t.Errorf("plain folders should not be flagged as git repos")
	}
}

func TestSortFolderEntries(t *testing.T) {
	entries := []folderEntry{
		{path: "/b"},
		{path: "/d", isGit: true},
		{path: "/a"},
		{path: "/c", isGit: true},
	}
	sortFolderEntries(entries)
	want := []string{"/c", "/d", "/a", "/b"}
	for i, p := range want {
		if entries[i].path != p {
			t.Errorf("entry %d: want %q, got %q (full: %+v)", i, p, entries[i].path, entries)
		}
	}
}

// typeInto feeds a string into the picker one key at a time, the way a paste
// arrives on a terminal.
func typeInto(m *FolderPickerModel, text string) {
	for _, r := range text {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestFolderPicker_RepoURLBecomesACloneOffer(t *testing.T) {
	m := NewFolderPickerModel(100, 40)
	typeInto(m, "https://github.com/bborn/taskyou")

	if m.repoRef == nil {
		t.Fatalf("typed repo URL should be recognised, got repoErr %q", m.repoErr)
	}
	if got := m.repoRef.Slug(); got != "bborn/taskyou" {
		t.Errorf("repoRef = %q, want bborn/taskyou", got)
	}
	if view := m.View(); !strings.Contains(view, "bborn/taskyou") || !strings.Contains(view, "clone") {
		t.Errorf("view should offer to clone the repo, got:\n%s", view)
	}
}

func TestFolderPicker_EnterOnRepoURLRequestsAClone(t *testing.T) {
	m := NewFolderPickerModel(100, 40)
	typeInto(m, "git@github.com:bborn/taskyou.git")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on a repo URL should emit a command")
	}
	msg, ok := cmd().(repoRequestedMsg)
	if !ok {
		t.Fatalf("want repoRequestedMsg, got %T", cmd())
	}
	if msg.ref.Slug() != "bborn/taskyou" || !msg.ref.SSH {
		t.Errorf("ref = %+v, want the ssh form of bborn/taskyou", msg.ref)
	}
}

func TestFolderPicker_PlainTextStaysAFilter(t *testing.T) {
	m := NewFolderPickerModel(100, 40)
	typeInto(m, "taskyou")

	if m.repoRef != nil {
		t.Errorf("a plain filter term should not be read as a repo URL: %+v", m.repoRef)
	}
	if m.repoErr != "" {
		t.Errorf("a plain filter term should not raise an error, got %q", m.repoErr)
	}
}

func TestFolderPicker_BrokenURLGetsAnInlineError(t *testing.T) {
	m := NewFolderPickerModel(100, 40)
	typeInto(m, "https://github.com/bborn")

	if m.repoRef != nil {
		t.Fatalf("an incomplete URL should not be clonable: %+v", m.repoRef)
	}
	if m.repoErr == "" {
		t.Fatal("an incomplete URL should get an inline complaint")
	}
	if !strings.Contains(m.View(), "owner") {
		t.Errorf("the error should name the shape we expect, got:\n%s", m.View())
	}
}

func TestFolderPicker_ExistingDirectoryIsNotARepoURL(t *testing.T) {
	dir := t.TempDir()
	m := NewFolderPickerModel(100, 40)
	typeInto(m, dir)

	if m.repoRef != nil {
		t.Errorf("an existing local path should not be treated as a repo: %+v", m.repoRef)
	}
}
