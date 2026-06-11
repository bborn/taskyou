package ui

import "testing"

func TestFuzzyFilterFolders(t *testing.T) {
	all := []folderEntry{
		{path: "/home/u/Projects/acme-rocket", isGit: true},
		{path: "/home/u/Projects/rocket-sim", isGit: true},
		{path: "/home/u/work/notes", isGit: false},
	}
	got := fuzzyFilterFolders(all, "rocket")
	if len(got) != 2 {
		t.Fatalf("want 2 matches, got %d (%+v)", len(got), got)
	}
	if len(fuzzyFilterFolders(all, "")) != 3 {
		t.Errorf("empty query should return all entries")
	}
}
