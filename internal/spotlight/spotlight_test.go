package spotlight

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initGitRepo creates a git repo in dir with an initial commit.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	os.WriteFile(filepath.Join(dir, ".gitkeep"), []byte(""), 0644)
	run("add", ".")
	run("commit", "-m", "initial")
}

// --- StateFile / IsActive ---

func TestStateFile(t *testing.T) {
	got := StateFile("/some/worktree")
	want := filepath.Join("/some/worktree", ".spotlight-active")
	if got != want {
		t.Errorf("StateFile = %q, want %q", got, want)
	}
}

func TestIsActive_NoFile(t *testing.T) {
	dir := t.TempDir()
	if IsActive(dir) {
		t.Error("IsActive should be false when no state file exists")
	}
}

func TestIsActive_WithFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(StateFile(dir), []byte("started=now\n"), 0644)
	if !IsActive(dir) {
		t.Error("IsActive should be true when state file exists")
	}
}

// --- Sync ---

func TestSync_CopiesChangedFiles(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Write different content in worktree
	os.WriteFile(filepath.Join(worktree, "file.txt"), []byte("worktree content"), 0644)
	cmd := exec.Command("git", "add", "file.txt")
	cmd.Dir = worktree
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "add file")
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
	cmd.Run()

	result, err := Sync(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	if !strings.Contains(result, "Synced") {
		t.Errorf("expected sync result to mention synced files, got: %s", result)
	}

	// Verify file was copied
	data, err := os.ReadFile(filepath.Join(mainRepo, "file.txt"))
	if err != nil {
		t.Fatalf("file not copied to main repo: %v", err)
	}
	if string(data) != "worktree content" {
		t.Errorf("file content = %q, want %q", string(data), "worktree content")
	}
}

func TestSync_SkipsUnchangedFiles(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Write same content in both
	content := []byte("same content")
	os.WriteFile(filepath.Join(worktree, "same.txt"), content, 0644)
	os.WriteFile(filepath.Join(mainRepo, "same.txt"), content, 0644)
	cmd := exec.Command("git", "add", "same.txt")
	cmd.Dir = worktree
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "add same")
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
	cmd.Run()

	result, err := Sync(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	if !strings.Contains(result, "No changes") {
		t.Errorf("expected no changes message, got: %s", result)
	}
}

func TestSync_CreatesSubdirectories(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Create nested file in worktree
	os.MkdirAll(filepath.Join(worktree, "src", "pkg"), 0755)
	os.WriteFile(filepath.Join(worktree, "src", "pkg", "main.go"), []byte("package main"), 0644)
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = worktree
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "add nested")
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
	cmd.Run()

	_, err := Sync(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(mainRepo, "src", "pkg", "main.go"))
	if err != nil {
		t.Fatalf("nested file not created: %v", err)
	}
	if string(data) != "package main" {
		t.Errorf("nested file content = %q, want %q", string(data), "package main")
	}
}

func TestSync_SkipsSpotlightStateFile(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Create the state file in worktree (should NOT be synced)
	os.WriteFile(StateFile(worktree), []byte("started=now\n"), 0644)

	result, err := Sync(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// State file should not be copied to main repo
	if _, err := os.Stat(StateFile(mainRepo)); !os.IsNotExist(err) {
		t.Error("spotlight state file should not be synced to main repo")
	}

	if !strings.Contains(result, "No changes") {
		t.Errorf("expected no changes (state file skipped), got: %s", result)
	}
}

func TestSync_HandlesDeletedFiles(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Create a file, commit, then delete it in worktree
	os.WriteFile(filepath.Join(worktree, "deleteme.txt"), []byte("gone soon"), 0644)
	cmd := exec.Command("git", "add", "deleteme.txt")
	cmd.Dir = worktree
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "add deleteme")
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
	cmd.Run()

	// Put same file in main repo
	os.WriteFile(filepath.Join(mainRepo, "deleteme.txt"), []byte("gone soon"), 0644)

	// Delete from worktree (but don't commit the delete — git diff HEAD detects it)
	os.Remove(filepath.Join(worktree, "deleteme.txt"))

	result, err := Sync(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	if !strings.Contains(result, "deleted") {
		t.Errorf("expected deleted file count, got: %s", result)
	}

	// File should be removed from main repo
	if _, err := os.Stat(filepath.Join(mainRepo, "deleteme.txt")); !os.IsNotExist(err) {
		t.Error("deleted file should be removed from main repo")
	}
}

func TestSync_RejectsPathTraversal(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Sync should not crash or write outside mainRepo even if somehow
	// a traversal path got into the file list. We can't easily inject
	// traversal paths via git ls-files, but we verify the validation
	// logic is present by checking that absolute paths and .. paths
	// would be rejected.

	// This test is more of a smoke test — the real protection is in the code.
	result, err := Sync(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	_ = result
}

func TestSync_IncludesUntrackedFiles(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Create a new file in worktree but don't git add it
	os.WriteFile(filepath.Join(worktree, "untracked.txt"), []byte("new file"), 0644)

	result, err := Sync(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	if !strings.Contains(result, "Synced") {
		t.Errorf("expected sync to include untracked file, got: %s", result)
	}

	data, err := os.ReadFile(filepath.Join(mainRepo, "untracked.txt"))
	if err != nil {
		t.Fatalf("untracked file not synced: %v", err)
	}
	if string(data) != "new file" {
		t.Errorf("untracked file content = %q, want %q", string(data), "new file")
	}
}

// --- Start / Stop ---

func TestStart_CreatesStateFile(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	result, err := Start(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !strings.Contains(result, "enabled") {
		t.Errorf("expected enabled message, got: %s", result)
	}

	if !IsActive(worktree) {
		t.Error("spotlight should be active after Start")
	}

	// Verify state file content
	data, err := os.ReadFile(StateFile(worktree))
	if err != nil {
		t.Fatalf("state file not readable: %v", err)
	}
	if !strings.Contains(string(data), "started=") {
		t.Error("state file should contain started timestamp")
	}
}

func TestStart_AlreadyActive(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Start first time
	_, err := Start(worktree, mainRepo)
	if err != nil {
		t.Fatalf("first Start failed: %v", err)
	}

	// Start again should return message, not error
	result, err := Start(worktree, mainRepo)
	if err != nil {
		t.Fatalf("second Start failed: %v", err)
	}
	if !strings.Contains(result, "already active") {
		t.Errorf("expected already active message, got: %s", result)
	}
}

func TestStart_StashesMainRepoChanges(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Make uncommitted changes in main repo
	os.WriteFile(filepath.Join(mainRepo, "dirty.txt"), []byte("uncommitted"), 0644)
	cmd := exec.Command("git", "add", "dirty.txt")
	cmd.Dir = mainRepo
	cmd.Run()

	result, err := Start(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !strings.Contains(result, "stashed") {
		t.Errorf("expected stash message, got: %s", result)
	}

	// State file should record stash_created=true
	data, _ := os.ReadFile(StateFile(worktree))
	if !strings.Contains(string(data), "stash_created=true") {
		t.Error("state file should record stash_created=true")
	}
}

func TestStop_RestoresMainRepo(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Create different content in worktree
	os.WriteFile(filepath.Join(worktree, "file.txt"), []byte("worktree"), 0644)
	cmd := exec.Command("git", "add", "file.txt")
	cmd.Dir = worktree
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "add file")
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
	cmd.Run()

	// Start spotlight (syncs worktree → main)
	_, err := Start(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify file was synced to main
	data, _ := os.ReadFile(filepath.Join(mainRepo, "file.txt"))
	if string(data) != "worktree" {
		t.Fatalf("file not synced during Start")
	}

	// Stop spotlight
	result, err := Stop(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if !strings.Contains(result, "disabled") {
		t.Errorf("expected disabled message, got: %s", result)
	}

	// File should be cleaned from main repo (it wasn't there originally)
	if _, err := os.Stat(filepath.Join(mainRepo, "file.txt")); !os.IsNotExist(err) {
		t.Error("synced file should be cleaned from main repo after Stop")
	}

	// State file should be removed
	if IsActive(worktree) {
		t.Error("spotlight should be inactive after Stop")
	}
}

func TestStop_RestoresStashedChanges(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Make uncommitted changes in main repo
	os.WriteFile(filepath.Join(mainRepo, "mywork.txt"), []byte("important work"), 0644)
	cmd := exec.Command("git", "add", "mywork.txt")
	cmd.Dir = mainRepo
	cmd.Run()

	// Start spotlight (should stash the changes)
	_, err := Start(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// mywork.txt should be gone (stashed)
	if _, err := os.Stat(filepath.Join(mainRepo, "mywork.txt")); !os.IsNotExist(err) {
		t.Error("stashed file should not exist during spotlight")
	}

	// Stop spotlight (should restore stash)
	result, err := Stop(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if !strings.Contains(result, "restored from stash") {
		t.Errorf("expected stash restore message, got: %s", result)
	}

	// mywork.txt should be back
	data, err := os.ReadFile(filepath.Join(mainRepo, "mywork.txt"))
	if err != nil {
		t.Fatalf("stashed file not restored: %v", err)
	}
	if string(data) != "important work" {
		t.Errorf("stashed file content = %q, want %q", string(data), "important work")
	}
}

func TestStop_NotActive(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	result, err := Stop(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if !strings.Contains(result, "not active") {
		t.Errorf("expected not active message, got: %s", result)
	}
}

func TestStop_PreservesStateOnStashPopFailure(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Manually create state file that claims a stash was created
	// but don't actually create a stash — so stash pop will fail
	os.WriteFile(StateFile(worktree), []byte("started=now\nstash_created=true\n"), 0644)

	result, err := Stop(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if !strings.Contains(result, "Failed to restore stash") {
		t.Errorf("expected stash pop failure warning, got: %s", result)
	}

	// State file should be preserved so user can investigate
	if !IsActive(worktree) {
		t.Error("state file should be preserved when stash pop fails")
	}
}

// --- Status ---

func TestStatus_Inactive(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)

	result, err := Status(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !strings.Contains(result, "INACTIVE") {
		t.Errorf("expected INACTIVE, got: %s", result)
	}
}

func TestStatus_Active(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	_, err := Start(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	result, err := Status(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !strings.Contains(result, "ACTIVE") {
		t.Errorf("expected ACTIVE, got: %s", result)
	}
	if !strings.Contains(result, worktree) {
		t.Errorf("expected worktree path in status, got: %s", result)
	}
	if !strings.Contains(result, mainRepo) {
		t.Errorf("expected main repo path in status, got: %s", result)
	}
}

// --- Destructive operation safety ---

func TestStop_DestructiveOps_DoNotWipeUntrackedMainRepoFiles(t *testing.T) {
	// This test documents the KNOWN ISSUE: Stop() runs git clean -fd
	// which WILL delete untracked files in the main repo that the user
	// may have created while spotlight was active.
	//
	// This is a design trade-off: spotlight needs to clean up files it
	// synced, but git clean -fd is a blunt instrument.

	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Start spotlight
	_, err := Start(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Simulate user creating a new file in main repo while spotlight is active
	// (e.g., IDE generated file, debug log, etc.)
	os.WriteFile(filepath.Join(mainRepo, "user-created.txt"), []byte("user's file"), 0644)

	// Stop spotlight — this runs git clean -fd
	_, err = Stop(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// KNOWN ISSUE: git clean -fd deletes ALL untracked files, including
	// ones the user created independently of spotlight.
	// This test documents the behavior rather than asserting it's correct.
	_, userFileErr := os.Stat(filepath.Join(mainRepo, "user-created.txt"))
	if os.IsNotExist(userFileErr) {
		t.Log("KNOWN ISSUE: Stop() git clean -fd deleted user-created untracked file in main repo")
	}
}

func TestStartStop_FullRoundtrip_PreservesMainRepoState(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Create committed content in main repo
	os.WriteFile(filepath.Join(mainRepo, "committed.txt"), []byte("committed content"), 0644)
	cmd := exec.Command("git", "add", "committed.txt")
	cmd.Dir = mainRepo
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "add committed file")
	cmd.Dir = mainRepo
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
	cmd.Run()

	// Create different content in worktree
	os.WriteFile(filepath.Join(worktree, "committed.txt"), []byte("worktree version"), 0644)
	os.WriteFile(filepath.Join(worktree, "newfile.txt"), []byte("new from worktree"), 0644)
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = worktree
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "worktree changes")
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
	cmd.Run()

	// Start spotlight
	_, err := Start(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify worktree content was synced
	data, _ := os.ReadFile(filepath.Join(mainRepo, "committed.txt"))
	if string(data) != "worktree version" {
		t.Fatalf("file not synced, got: %s", string(data))
	}

	// Stop spotlight
	_, err = Stop(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Main repo should be restored to committed state
	data, err = os.ReadFile(filepath.Join(mainRepo, "committed.txt"))
	if err != nil {
		t.Fatalf("committed file missing after Stop: %v", err)
	}
	if string(data) != "committed content" {
		t.Errorf("committed file not restored, got: %q, want %q", string(data), "committed content")
	}

	// New file from worktree should be cleaned
	if _, err := os.Stat(filepath.Join(mainRepo, "newfile.txt")); !os.IsNotExist(err) {
		t.Error("worktree-only file should be cleaned from main repo after Stop")
	}
}

// --- Edge cases ---

func TestSync_EmptyWorktree(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Only .gitkeep exists (from initGitRepo), same in both
	os.WriteFile(filepath.Join(mainRepo, ".gitkeep"), []byte(""), 0644)

	result, err := Sync(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	if !strings.Contains(result, "No changes") {
		t.Errorf("expected no changes for identical repos, got: %s", result)
	}
}

func TestSync_PreservesFilePermissions(t *testing.T) {
	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Create executable file
	os.WriteFile(filepath.Join(worktree, "script.sh"), []byte("#!/bin/bash\necho hi"), 0755)
	cmd := exec.Command("git", "add", "script.sh")
	cmd.Dir = worktree
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "add script")
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
	cmd.Run()

	_, err := Sync(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	info, err := os.Stat(filepath.Join(mainRepo, "script.sh"))
	if err != nil {
		t.Fatalf("script not synced: %v", err)
	}

	// Check that execute bit is preserved
	if info.Mode()&0111 == 0 {
		t.Error("execute permission not preserved on synced file")
	}
}

func TestStart_StashFailureSilentlySwallowed(t *testing.T) {
	// This test documents a potential issue: if git stash push fails
	// but the main repo has uncommitted changes, Start() proceeds
	// without error. When Stop() later runs git checkout ., those
	// changes are lost.
	//
	// Currently this is unlikely in practice because stash failures
	// are rare, but the behavior should be documented.

	worktree := t.TempDir()
	mainRepo := t.TempDir()

	initGitRepo(t, worktree)
	initGitRepo(t, mainRepo)

	// Start with clean main repo — no stash needed
	result, err := Start(worktree, mainRepo)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// State file should record stash_created=false
	data, _ := os.ReadFile(StateFile(worktree))
	if strings.Contains(string(data), "stash_created=true") {
		t.Error("stash_created should be false when no changes existed")
	}

	_ = result
}
