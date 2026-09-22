package web

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// unarchiveGitRepo creates a real git repository with one commit and returns
// its path. Mirrors the executor package's fixture so this test can drive the
// real ArchiveWorktree/UnarchiveWorktree path (real `git worktree` calls) from
// the web package.
func unarchiveGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "initial")
	return dir
}

// unarchiveAddWorktree creates a linked worktree of repo at path on a new branch.
func unarchiveAddWorktree(t *testing.T, repo, path, branch string) string {
	t.Helper()
	cmd := exec.Command("git", "worktree", "add", "-b", branch, path)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return path
}

// TestUnarchivedRemoteTaskStagesBrowserArtifactsLocally is the regression test
// for the bug where a previously-remote task, after the archive→unarchive flow,
// had its browser screenshots/DOM snapshots dropped and HOWTO.md skipped: the
// flow left PlacementTarget pointed at the stale remote host while the worktree
// was sitting right here, so resolveTaskRoot (via TaskCodeLocation.Remote())
// returned "" and refused to stage anything.
//
// This drives the full chain through the public production surface — the same
// APIs `ty place`, the board's archive sweep, and setupWorktree's restore step
// call — then hands the resulting task to the web Server that owns
// resolveTaskRoot / materializeBrowserResult / ensureBrowserHowto. With the fix
// (UnarchiveWorktree clears the stale remote placement when it restores the
// worktree here), the agent gets its screenshots and its HOWTO.
func TestUnarchivedRemoteTaskStagesBrowserArtifactsLocally(t *testing.T) {
	root := t.TempDir()
	repo := unarchiveGitRepo(t, filepath.Join(root, "proj"))
	database, err := db.Open(filepath.Join(root, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "proj", Path: repo, UseWorktrees: true}); err != nil {
		t.Fatal(err)
	}
	exec := executor.New(database, config.New(database))
	// The same Server the browser bridge runs on, sharing the DB so it sees
	// exactly the task the executor moved, archived, and unarchived.
	srv := New(Config{Addr: ":0", DB: database, CmdRunner: &mockRunner{}})

	// A done task with a real local worktree — the precondition for the sweep
	// to archive it later.
	wt := filepath.Join(repo, ".task-worktrees", "1-original")
	unarchiveAddWorktree(t, repo, wt, "task/1")
	task := &db.Task{Title: "previously remote", Project: "proj", Type: db.TypeCode}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`UPDATE tasks SET worktree_path = ?, branch_name = ?, status = 'done' WHERE id = ?`,
		wt, "task/1", task.ID); err != nil {
		t.Fatal(err)
	}

	// Use a stub ssh on PATH — the same convention the remote-terminal probe
	// tests use — so PlaceTask's Preflight on the remote host succeeds without
	// a real peer. The bug is independent of whether work was actually carried.
	writeSSHStub(t, "#!/bin/sh\necho /home/olgm/.task-worktrees/1\n")

	// Step 1: place the task on a remote host (--force skips the carry
	// round-trip). CommitTaskPlacement writes placement_target / workdir and
	// leaves the local worktree_path in place — exactly what the sweep later
	// targets.
	if _, err := executor.PlaceTask(context.Background(), database, task.ID, "ol-agents",
		"/home/olgm/.task-worktrees/1", true); err != nil {
		t.Fatalf("PlaceTask: %v", err)
	}
	afterPlace, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterPlace.WorktreePath == "" {
		t.Fatal("PlaceTask dropped the local worktree_path; the sweep needs it to target this task")
	}
	if afterPlace.PlacementTarget != "ol-agents" {
		t.Fatalf("after PlaceTask, PlacementTarget = %q, want ol-agents", afterPlace.PlacementTarget)
	}

	// Step 2: archive (the stale-worktree sweep, or a manual archive). Saves
	// archive state and removes the local worktree; placement is left stale.
	if err := exec.ArchiveWorktree(afterPlace); err != nil {
		t.Fatalf("ArchiveWorktree: %v", err)
	}
	afterArchive, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterArchive.ArchiveRef == "" {
		t.Fatal("ArchiveWorktree did not save archive state")
	}
	if afterArchive.WorktreePath != "" {
		t.Errorf("after archive, worktree_path = %q, want cleared", afterArchive.WorktreePath)
	}
	if afterArchive.PlacementTarget != "ol-agents" {
		t.Errorf("after archive, PlacementTarget = %q (the bug: archive leaves it stale)", afterArchive.PlacementTarget)
	}

	// Step 3: unarchive (setupWorktree during the next spawn, or the board's
	// unarchive button). Restores the worktree HERE and, with the fix, clears
	// the stale remote placement at the same moment.
	if err := exec.UnarchiveWorktree(afterArchive); err != nil {
		t.Fatalf("UnarchiveWorktree: %v", err)
	}
	afterUnarchive, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterUnarchive.ArchiveRef != "" {
		t.Errorf("after unarchive, ArchiveRef = %q, want cleared", afterUnarchive.ArchiveRef)
	}
	if afterUnarchive.WorktreePath == "" {
		t.Fatal("unarchive did not restore a local worktree path")
	}
	if st, err := os.Stat(afterUnarchive.WorktreePath); err != nil || !st.IsDir() {
		t.Fatalf("unarchive did not restore a real local worktree directory: %v (path=%q)", err, afterUnarchive.WorktreePath)
	}
	if afterUnarchive.PlacementTarget != "" {
		t.Errorf("after unarchive, PlacementTarget = %q, want cleared (the worktree is on THIS machine now)",
			afterUnarchive.PlacementTarget)
	}

	// The joined assertion: the browser bridge, asked about the unarchived
	// local task, stages payloads into the worktree instead of dropping them.

	// resolveTaskRoot returns the restored worktree — not "".
	if got := srv.resolveTaskRoot(afterUnarchive); got != afterUnarchive.WorktreePath {
		t.Errorf("resolveTaskRoot = %q, want the restored local worktree %q", got, afterUnarchive.WorktreePath)
	}

	// A screenshot is staged to a file path and the base64 payload is gone.
	shot := json.RawMessage(`{"ok":true,"data":"data:image/png;base64,` + tinyPNG + `"}`)
	result, ok := srv.materializeBrowserResult(afterUnarchive, "screenshot", shot).(map[string]interface{})
	if !ok {
		t.Fatalf("screenshot result is not an object: %#v", result)
	}
	if _, inlined := result["data"]; inlined {
		t.Error("the screenshot base64 was inlined into the agent's output instead of being staged")
	}
	path, _ := result["path"].(string)
	if path == "" {
		t.Fatal("the screenshot was not staged to a file path")
	}
	if _, err := os.Stat(filepath.Join(afterUnarchive.WorktreePath, path)); err != nil {
		t.Errorf("staged screenshot file does not exist at %s: %v", path, err)
	}
	if problem, _ := result["error"].(string); problem != "" && strings.Contains(problem, "ol-agents") {
		t.Errorf("the screenshot was dropped with a stale-host error: %q", problem)
	}

	// A DOM snapshot is staged the same way.
	snap := json.RawMessage(`{"ok":true,"html":"<html>boom</html>","title":"t","url":"u"}`)
	snapResult, ok := srv.materializeBrowserResult(afterUnarchive, "snapshot", snap).(map[string]interface{})
	if !ok {
		t.Fatalf("snapshot result is not an object: %#v", snapResult)
	}
	if _, inlined := snapResult["html"]; inlined {
		t.Error("the DOM snapshot html was inlined instead of being staged")
	}
	if snapPath, _ := snapResult["path"].(string); snapPath == "" {
		t.Error("the DOM snapshot was not staged to a file path")
	} else if _, err := os.Stat(filepath.Join(afterUnarchive.WorktreePath, snapPath)); err != nil {
		t.Errorf("staged snapshot file does not exist at %s: %v", snapPath, err)
	}

	// The HOWTO cheat-sheet is written so the agent can discover the bridge.
	srv.ensureBrowserHowto(afterUnarchive)
	howto := filepath.Join(afterUnarchive.WorktreePath, ".taskyou", "browser", "HOWTO.md")
	if _, err := os.Stat(howto); err != nil {
		t.Errorf("HOWTO.md was not written at %s: %v (the agent may never learn the bridge exists)", howto, err)
	}

	// A plain (non-bulky) browser action is still passed through untouched.
	// (The relay does not care which machine the agent is on; only staging does.)
	clicked, _ := srv.materializeBrowserResult(afterUnarchive, "click",
		json.RawMessage(`{"ok":true,"clicked":"#save"}`)).(map[string]interface{})
	if clicked["clicked"] != "#save" {
		t.Errorf("a plain browser action was altered for the unarchived task: %#v", clicked)
	}
}
