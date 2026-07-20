package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests cover symlinkClaudeConfig against a real git repo + worktree, because
// the whole point of the function is how its filesystem layout interacts with git's
// view of the worktree. The failure mode being guarded against is a worktree whose
// branch tracks content under .claude: replacing that directory with a symlink to the
// main project makes every tracked file show up as deleted, and agents then commit
// the deletion.

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newClaudeConfigRepo builds a project repo whose initial commit contains the given
// tracked files, then adds a worktree on a fresh branch. Returns both paths.
func newClaudeConfigRepo(t *testing.T, tracked map[string]string) (projectDir, worktreePath string) {
	t.Helper()
	root := t.TempDir()
	projectDir = filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	gitRun(t, projectDir, "init", "-b", "main")
	gitRun(t, projectDir, "config", "user.email", "test@example.com")
	gitRun(t, projectDir, "config", "user.name", "Test User")
	gitRun(t, projectDir, "config", "commit.gpgsign", "false")

	writeFile(t, filepath.Join(projectDir, "README.md"), "seed\n")
	for rel, content := range tracked {
		writeFile(t, filepath.Join(projectDir, rel), content)
	}
	gitRun(t, projectDir, "add", "-A")
	gitRun(t, projectDir, "commit", "-m", "init")

	worktreePath = filepath.Join(root, "wt")
	gitRun(t, projectDir, "worktree", "add", worktreePath, "-b", "task/1")
	return projectDir, worktreePath
}

// assertWorktreeClean fails if git reports any change in the worktree. A tracked
// .claude file showing as deleted is exactly the bug under test.
func assertWorktreeClean(t *testing.T, worktreePath string) {
	t.Helper()
	status := gitRun(t, worktreePath, "status", "--porcelain")
	if strings.TrimSpace(status) != "" {
		t.Errorf("worktree should be clean, got:\n%s", status)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}

// When the branch tracks nothing under .claude, the worktree keeps inheriting the
// project's config wholesale via a symlink — the original behavior.
func TestSymlinkClaudeConfigUntrackedUsesSymlink(t *testing.T) {
	projectDir, worktreePath := newClaudeConfigRepo(t, nil)
	writeFile(t, filepath.Join(projectDir, ".claude", "settings.local.json"), `{"local":true}`)

	if err := symlinkClaudeConfig(projectDir, worktreePath); err != nil {
		t.Fatalf("symlinkClaudeConfig: %v", err)
	}

	target, err := os.Readlink(filepath.Join(worktreePath, ".claude"))
	if err != nil {
		t.Fatalf("worktree .claude should be a symlink: %v", err)
	}
	if want := filepath.Join(projectDir, ".claude"); target != want {
		t.Errorf("symlink target = %q, want %q", target, want)
	}
	assertWorktreeClean(t, worktreePath)
}

// The bug this PR fixes: tracked .claude content must survive worktree setup.
func TestSymlinkClaudeConfigPreservesTrackedContent(t *testing.T) {
	projectDir, worktreePath := newClaudeConfigRepo(t, map[string]string{
		".claude/skills/demo.md": "tracked skill\n",
	})

	if err := symlinkClaudeConfig(projectDir, worktreePath); err != nil {
		t.Fatalf("symlinkClaudeConfig: %v", err)
	}

	if _, err := os.Readlink(filepath.Join(worktreePath, ".claude")); err == nil {
		t.Fatal("worktree .claude was replaced by a symlink, deleting tracked content")
	}
	assertFileContent(t, filepath.Join(worktreePath, ".claude", "skills", "demo.md"), "tracked skill\n")
	assertWorktreeClean(t, worktreePath)
}

// Partial tracking is the common case (".claude/*" plus "!.claude/settings.json").
// Tracked files must survive AND the project's local-only config must still reach
// the worktree, or the executor silently loses hooks, agents and local settings.
func TestSymlinkClaudeConfigPartialTrackingLinksLocalEntries(t *testing.T) {
	projectDir, worktreePath := newClaudeConfigRepo(t, map[string]string{
		".claude/settings.json":    `{"tracked":true}`,
		".claude/skills/shared.md": "tracked skill\n",
	})
	// Local-only config living alongside the tracked files in the main project.
	writeFile(t, filepath.Join(projectDir, ".claude", "settings.local.json"), `{"local":true}`)
	writeFile(t, filepath.Join(projectDir, ".claude", "skills", "private.md"), "local skill\n")

	if err := symlinkClaudeConfig(projectDir, worktreePath); err != nil {
		t.Fatalf("symlinkClaudeConfig: %v", err)
	}

	assertFileContent(t, filepath.Join(worktreePath, ".claude", "settings.json"), `{"tracked":true}`)
	assertFileContent(t, filepath.Join(worktreePath, ".claude", "skills", "shared.md"), "tracked skill\n")
	// Local entries are reachable from the worktree, including inside a directory
	// that also holds tracked files.
	assertFileContent(t, filepath.Join(worktreePath, ".claude", "settings.local.json"), `{"local":true}`)
	assertFileContent(t, filepath.Join(worktreePath, ".claude", "skills", "private.md"), "local skill\n")
	// The links must not pollute git status.
	assertWorktreeClean(t, worktreePath)
}

// A worktree already damaged by the previous behavior (.claude replaced by a symlink)
// must heal on the next setup pass. git ls-files reads the index, so the tracked
// files are still listed even though the working tree has lost them.
func TestSymlinkClaudeConfigRepairsStaleSymlink(t *testing.T) {
	projectDir, worktreePath := newClaudeConfigRepo(t, map[string]string{
		".claude/skills/demo.md": "tracked skill\n",
	})
	writeFile(t, filepath.Join(projectDir, ".claude", "settings.local.json"), `{"local":true}`)

	// Reproduce the damage the old code caused.
	worktreeClaudeDir := filepath.Join(worktreePath, ".claude")
	if err := os.RemoveAll(worktreeClaudeDir); err != nil {
		t.Fatalf("remove worktree .claude: %v", err)
	}
	if err := os.Symlink(filepath.Join(projectDir, ".claude"), worktreeClaudeDir); err != nil {
		t.Fatalf("symlink worktree .claude: %v", err)
	}

	if err := symlinkClaudeConfig(projectDir, worktreePath); err != nil {
		t.Fatalf("symlinkClaudeConfig: %v", err)
	}

	if _, err := os.Readlink(worktreeClaudeDir); err == nil {
		t.Fatal("stale .claude symlink was left in place, tracked content still missing")
	}
	assertFileContent(t, filepath.Join(worktreeClaudeDir, "skills", "demo.md"), "tracked skill\n")
	assertWorktreeClean(t, worktreePath)
}
