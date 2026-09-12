package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// worktreeKind classifies what a task's recorded worktree_path actually points
// at on disk. The database column is just a string, and nothing until now
// checked that the string named a *linked* worktree — the one thing
// `git worktree remove` can act on.
//
// The failure this exists for: a task row whose worktree_path was the project's
// main checkout (a task created against the checkout directly, or a project
// switched from shared-dir to worktree isolation after the fact). Every sweep
// ran `git worktree remove` against it, git answered "fatal: ... is a main
// working tree", and the sweeper tried again an hour later — for five months.
// Removal there can never succeed, and a row pointing at a real checkout is one
// bad code path away from something destructive running against main.
type worktreeKind int

const (
	// worktreeUnknown: git could not answer — the path is gone, is not a git
	// repository, or git itself failed. Callers must not treat this as
	// permission to remove anything, but it is also not proof of a main tree.
	worktreeUnknown worktreeKind = iota
	// worktreeLinked: a linked worktree, i.e. `git worktree remove` can act on it.
	worktreeLinked
	// worktreeMain: a main working tree. Never removable as a worktree.
	worktreeMain
)

// classifyWorktreePath reports whether path is a linked worktree, a main working
// tree, or something git can't classify.
//
// The test is git's own: a linked worktree has a per-worktree git dir
// (<repo>/.git/worktrees/<name>) that differs from the shared common dir
// (<repo>/.git), while in a main working tree the two are the same directory.
// This is also true from a subdirectory of either, so a path pointing anywhere
// inside the main checkout is correctly classified as main.
func classifyWorktreePath(path string) worktreeKind {
	path = strings.TrimSpace(path)
	if path == "" {
		return worktreeUnknown
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return worktreeUnknown
	}

	gitDir := gitRevParseDir(path, "--absolute-git-dir")
	if gitDir == "" {
		return worktreeUnknown
	}
	commonDir := gitRevParseDir(path, "--git-common-dir")
	if commonDir == "" {
		return worktreeUnknown
	}
	// --git-common-dir answers relative to the working directory for a main tree
	// on most git versions (".git"), absolute for a linked one.
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(path, commonDir)
	}

	if sameDir(gitDir, commonDir) {
		return worktreeMain
	}
	return worktreeLinked
}

// isMainWorkingTree reports whether path is a git main working tree (or a
// directory inside one). False for linked worktrees and for anything git cannot
// classify — the conservative answer, since callers use this to *skip*
// destructive work, and "unknown" should fall through to the normal path where
// a real error is reported and recorded rather than silently swallowed.
func isMainWorkingTree(path string) bool {
	return classifyWorktreePath(path) == worktreeMain
}

// gitRevParseDir runs a single `git rev-parse <flag>` in dir and returns the
// trimmed output, or "" if git failed.
func gitRevParseDir(dir, flag string) string {
	out, err := gitCmd(context.Background(), dir, "rev-parse", flag).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sameDir reports whether two paths name the same directory, resolving symlinks
// (macOS /tmp → /private/tmp being the everyday case) before comparing.
func sameDir(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return resolveSymlinks(filepath.Clean(a)) == resolveSymlinks(filepath.Clean(b))
}
