package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func mkGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
}

func TestDirIsGitRepo(t *testing.T) {
	tmp := t.TempDir()

	if dirIsGitRepo(tmp) {
		t.Fatalf("expected non-git dir to be false")
	}
	if dirIsGitRepo("") {
		t.Fatalf("expected empty path to be false")
	}

	mkGitRepo(t, tmp)
	if !dirIsGitRepo(tmp) {
		t.Fatalf("expected git dir to be true")
	}

	// A .git file (as used by worktrees/submodules) should also count.
	fileRepo := filepath.Join(t.TempDir())
	if err := os.WriteFile(filepath.Join(fileRepo, ".git"), []byte("gitdir: /somewhere\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	if !dirIsGitRepo(fileRepo) {
		t.Fatalf("expected dir with .git file to be a git repo")
	}
}

func TestInferProjectName(t *testing.T) {
	cases := map[string]string{
		"/Users/bruno/Projects/my-app": "my-app",
		"/tmp/foo/":                    "foo",
		"my-app":                       "my-app",
	}
	for in, want := range cases {
		if got := inferProjectName(in); got != want {
			t.Errorf("inferProjectName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadProjectInstructions(t *testing.T) {
	tmp := t.TempDir()

	// Nothing present yet.
	if got, src := readProjectInstructions(tmp); got != "" || src != "" {
		t.Fatalf("expected empty result, got %q from %q", got, src)
	}

	// CLAUDE.md present but AGENTS.md takes priority when both exist.
	if err := os.WriteFile(filepath.Join(tmp, "CLAUDE.md"), []byte("claude instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, src := readProjectInstructions(tmp)
	if got != "claude instructions" || src != "CLAUDE.md" {
		t.Fatalf("got (%q, %q), want claude instructions from CLAUDE.md", got, src)
	}

	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("  agents instructions  "), 0o644); err != nil {
		t.Fatal(err)
	}
	got, src = readProjectInstructions(tmp)
	if got != "agents instructions" || src != "AGENTS.md" {
		t.Fatalf("got (%q, %q), want agents instructions from AGENTS.md", got, src)
	}
}

func TestReadProjectInstructionsTruncates(t *testing.T) {
	tmp := t.TempDir()
	big := make([]byte, maxInferredInstructionLen+500)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := readProjectInstructions(tmp)
	// The raw content must have been cut down to roughly the cap (plus the
	// truncation marker).
	if len(got) > maxInferredInstructionLen+len("\n\n…(truncated)") {
		t.Fatalf("instructions not truncated: len=%d", len(got))
	}
	if !strings.HasSuffix(got, "…(truncated)") {
		t.Fatalf("expected truncation marker, got suffix %q", got[len(got)-20:])
	}
}

func TestDetectProjectFromDir(t *testing.T) {
	tmp := t.TempDir()
	// Not a git repo -> nil.
	if p, _ := detectProjectFromDir(tmp); p != nil {
		t.Fatalf("expected nil for non-git dir")
	}

	mkGitRepo(t, tmp)
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("do the thing"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, src := detectProjectFromDir(tmp)
	if p == nil {
		t.Fatal("expected project, got nil")
	}
	if !p.UseWorktrees {
		t.Errorf("expected UseWorktrees true")
	}
	if p.Instructions != "do the thing" {
		t.Errorf("instructions = %q", p.Instructions)
	}
	if src != "AGENTS.md" {
		t.Errorf("source = %q", src)
	}
	if p.Name != filepath.Base(tmp) {
		t.Errorf("name = %q, want %q", p.Name, filepath.Base(tmp))
	}
}

func TestDetectProjectFromDir_NonGitMarker(t *testing.T) {
	// A dir with only go.mod (no .git) should be detected as a project
	// with UseWorktrees==false.
	markerDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(markerDir, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := detectProjectFromDir(markerDir)
	if p == nil {
		t.Fatal("expected non-nil project for dir with go.mod, got nil")
	}
	if p.UseWorktrees {
		t.Errorf("expected UseWorktrees false for non-git project, got true")
	}
	if p.Name == "" {
		t.Errorf("expected non-empty Name")
	}
	if p.Path == "" {
		t.Errorf("expected non-empty Path")
	}

	// An empty dir (no markers, no git) must return nil.
	emptyDir := t.TempDir()
	if p2, _ := detectProjectFromDir(emptyDir); p2 != nil {
		t.Errorf("expected nil for empty dir, got %+v", p2)
	}
}

func TestUniqueProjectName(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	// nil-safe
	if got := uniqueProjectName(nil, "foo"); got != "foo" {
		t.Fatalf("nil db should return name unchanged, got %q", got)
	}

	if got := uniqueProjectName(database, "myproj"); got != "myproj" {
		t.Fatalf("expected myproj, got %q", got)
	}

	if err := database.CreateProject(&db.Project{Name: "myproj", Path: tmpDir}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if got := uniqueProjectName(database, "myproj"); got != "myproj-2" {
		t.Fatalf("expected myproj-2 after collision, got %q", got)
	}
}

func TestProjectSuggestionDismissedKey(t *testing.T) {
	k1 := projectSuggestionDismissedKey("/tmp/foo/")
	k2 := projectSuggestionDismissedKey("/tmp/foo")
	if k1 != k2 {
		t.Fatalf("expected cleaned paths to produce equal keys: %q vs %q", k1, k2)
	}
}

func TestIsProjectCandidate(t *testing.T) {
	home, _ := os.UserHomeDir()

	// Junk dir: home itself and standard bare children → never a candidate.
	for _, junk := range []string{home, filepath.Join(home, "Desktop"), filepath.Join(home, "Downloads"), "/", "/tmp"} {
		if isProjectCandidate(junk) {
			t.Errorf("isProjectCandidate(%q) = true, want false", junk)
		}
	}

	// A git repo is a candidate.
	gitDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gitDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isProjectCandidate(gitDir) {
		t.Errorf("git repo %q should be a candidate", gitDir)
	}

	// A non-git dir with a project marker is a candidate.
	markerDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(markerDir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isProjectCandidate(markerDir) {
		t.Errorf("dir with go.mod %q should be a candidate", markerDir)
	}

	// An empty, signal-less dir is NOT a candidate.
	emptyDir := t.TempDir()
	if isProjectCandidate(emptyDir) {
		t.Errorf("empty dir %q should not be a candidate", emptyDir)
	}
}
