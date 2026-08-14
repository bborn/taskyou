package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := []struct {
		in, want string
	}{
		{"~/Projects/rails/offerlab", filepath.Join(home, "Projects/rails/offerlab")},
		{"~", home},
		{"/Users/someone/Projects/x", "/Users/someone/Projects/x"},
		{"relative/path", "relative/path"},
		{"", ""},
		// Another user's home is not something we can resolve; leave it alone.
		{"~someone/Projects", "~someone/Projects"},
		// A tilde in the middle is a real (if odd) directory name.
		{"/tmp/a~b", "/tmp/a~b"},
	}
	for _, c := range cases {
		if got := ExpandHomePath(c.in); got != c.want {
			t.Errorf("ExpandHomePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A project path stored as the shell's "~/..." shorthand must never reach a
// consumer that way: nothing downstream goes through a shell, so a literal "~"
// becomes a directory that does not exist and git fails with
// "cannot change to '~/Projects/rails/offerlab'" — which is how a pipeline lost
// its shared branch on origin.
func TestProjectPathIsExpandedOnWriteAndRead(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	database, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	want := filepath.Join(home, "Projects/rails/offerlab")
	p := &Project{Name: "offerlab", Path: "~/Projects/rails/offerlab"}
	if err := database.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	if p.Path != want {
		t.Errorf("CreateProject left the caller holding %q, want %q", p.Path, want)
	}

	got, err := database.GetProjectByName("offerlab")
	if err != nil || got == nil {
		t.Fatalf("GetProjectByName: %v", err)
	}
	if got.Path != want {
		t.Errorf("GetProjectByName path = %q, want %q", got.Path, want)
	}

	list, err := database.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, lp := range list {
		if lp.Name == "offerlab" && lp.Path != want {
			t.Errorf("ListProjects path = %q, want %q", lp.Path, want)
		}
	}
}

// Rows written before the expansion existed (hand-edited, or by an older build)
// must still come back usable.
func TestLegacyTildeRowIsExpandedOnRead(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	database, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if _, err := database.Exec(`INSERT INTO projects (name, path) VALUES (?, ?)`,
		"legacy", "~/Projects/rails/influencekit"); err != nil {
		t.Fatal(err)
	}
	got, err := database.GetProjectByName("legacy")
	if err != nil || got == nil {
		t.Fatalf("GetProjectByName: %v", err)
	}
	if want := filepath.Join(home, "Projects/rails/influencekit"); got.Path != want {
		t.Errorf("legacy row path = %q, want %q", got.Path, want)
	}
}
