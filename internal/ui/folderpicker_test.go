package ui

import (
	"os"
	"path/filepath"
	"testing"
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
