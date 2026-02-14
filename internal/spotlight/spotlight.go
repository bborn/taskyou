// Package spotlight syncs worktree changes to the main repo for testing.
package spotlight

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// StateFile returns the path to the spotlight state file in the worktree.
func StateFile(worktreePath string) string {
	return filepath.Join(worktreePath, ".spotlight-active")
}

// IsActive checks if spotlight mode is currently active for the worktree.
func IsActive(worktreePath string) bool {
	_, err := os.Stat(StateFile(worktreePath))
	return err == nil
}

// Start enables spotlight mode and performs initial sync.
func Start(worktreePath, mainRepoDir string) (string, error) {
	if IsActive(worktreePath) {
		return "Spotlight mode is already active. Use 'sync' to sync changes or 'stop' to disable.", nil
	}

	// Check if main repo has uncommitted changes using git diff --quiet (more reliable than parsing output)
	hasChanges := false
	diffCmd := exec.Command("git", "diff", "--quiet")
	diffCmd.Dir = mainRepoDir
	if err := diffCmd.Run(); err != nil {
		hasChanges = true // non-zero exit means changes exist
	}
	diffCachedCmd := exec.Command("git", "diff", "--cached", "--quiet")
	diffCachedCmd.Dir = mainRepoDir
	if err := diffCachedCmd.Run(); err != nil {
		hasChanges = true // staged changes exist
	}

	// Stash changes if any exist
	stashCreated := false
	if hasChanges {
		stashCmd := exec.Command("git", "stash", "push", "-m", "spotlight-backup-"+time.Now().Format("20060102-150405"))
		stashCmd.Dir = mainRepoDir
		if err := stashCmd.Run(); err == nil {
			stashCreated = true
		}
	}

	// Create the state file to track that spotlight is active
	stateContent := fmt.Sprintf("started=%s\nstash_created=%t\n", time.Now().Format(time.RFC3339), stashCreated)
	if err := os.WriteFile(StateFile(worktreePath), []byte(stateContent), 0644); err != nil {
		return "", fmt.Errorf("failed to create spotlight state file: %w", err)
	}

	// Perform initial sync
	syncResult, err := Sync(worktreePath, mainRepoDir)
	if err != nil {
		// Clean up state file if sync failed
		os.Remove(StateFile(worktreePath))
		return "", err
	}

	msg := "🔦 Spotlight mode enabled!\n\n"
	if stashCreated {
		msg += "✓ Main repo changes stashed (will be restored on stop)\n"
	}
	msg += syncResult
	msg += "\n\nTip: Your main repo now has the worktree changes. Run your app from there for testing."
	msg += "\nUse 'sync' to push more changes or 'stop' when done."

	return msg, nil
}

// Stop disables spotlight mode and restores the main repo.
func Stop(worktreePath, mainRepoDir string) (string, error) {
	if !IsActive(worktreePath) {
		return "Spotlight mode is not active.", nil
	}

	// Read state file to check if we created a stash
	stateData, _ := os.ReadFile(StateFile(worktreePath))
	stashCreated := strings.Contains(string(stateData), "stash_created=true")

	// Restore the main repo to its original state
	// First, discard any uncommitted changes from spotlight
	checkoutCmd := exec.Command("git", "checkout", ".")
	checkoutCmd.Dir = mainRepoDir
	if output, err := checkoutCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to restore main repo (git checkout): %s", strings.TrimSpace(string(output)))
	}

	// Clean any untracked files that were added
	cleanCmd := exec.Command("git", "clean", "-fd")
	cleanCmd.Dir = mainRepoDir
	if output, err := cleanCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to clean main repo (git clean): %s", strings.TrimSpace(string(output)))
	}

	// Pop the stash if we created one
	var stashMsg string
	stashPopFailed := false
	if stashCreated {
		stashPopCmd := exec.Command("git", "stash", "pop")
		stashPopCmd.Dir = mainRepoDir
		if output, err := stashPopCmd.CombinedOutput(); err != nil {
			stashPopFailed = true
			stashMsg = fmt.Sprintf("⚠️ Failed to restore stash: %s\n   Run 'git stash list' in %s to see available stashes.", strings.TrimSpace(string(output)), mainRepoDir)
		} else {
			stashMsg = "✓ Original main repo changes restored from stash"
		}
	}

	// Only remove state file if restoration succeeded (including stash pop)
	if !stashPopFailed {
		os.Remove(StateFile(worktreePath))
	}

	msg := "🔦 Spotlight mode disabled!\n\n"
	msg += "✓ Main repo restored to original state\n"
	if stashMsg != "" {
		msg += stashMsg + "\n"
	}
	if stashPopFailed {
		msg += "\n⚠️ State file preserved due to stash pop failure. Run 'stop' again after resolving."
	}

	return msg, nil
}

// Sync syncs git-tracked files from worktree to main repo.
// It compares files between the worktree and main repo, copying any that differ.
// Also handles file deletions by detecting files that exist in main but not in worktree.
func Sync(worktreePath, mainRepoDir string) (string, error) {
	// Get list of all git-tracked files in the worktree
	lsFilesCmd := exec.Command("git", "ls-files")
	lsFilesCmd.Dir = worktreePath
	lsFilesOutput, err := lsFilesCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to list tracked files: %w", err)
	}

	// Also get untracked files (new files not yet added)
	untrackedCmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	untrackedCmd.Dir = worktreePath
	untrackedOutput, _ := untrackedCmd.Output()

	// Get deleted files (tracked but removed from worktree)
	deletedCmd := exec.Command("git", "diff", "--name-only", "--diff-filter=D", "HEAD")
	deletedCmd.Dir = worktreePath
	deletedOutput, _ := deletedCmd.Output()

	// Build set of all files to sync
	fileSet := make(map[string]bool)
	for _, file := range strings.Split(strings.TrimSpace(string(lsFilesOutput)), "\n") {
		if file != "" {
			fileSet[file] = true
		}
	}
	for _, file := range strings.Split(strings.TrimSpace(string(untrackedOutput)), "\n") {
		if file != "" {
			fileSet[file] = true
		}
	}

	// Build set of deleted files
	deletedSet := make(map[string]bool)
	for _, file := range strings.Split(strings.TrimSpace(string(deletedOutput)), "\n") {
		if file != "" {
			deletedSet[file] = true
		}
	}

	// Clean paths for validation
	cleanWorktree := filepath.Clean(worktreePath)
	cleanMainRepo := filepath.Clean(mainRepoDir)

	// Copy files that differ between worktree and main repo
	var synced, unchanged, deleted, failed int
	for file := range fileSet {
		if file == ".spotlight-active" || file == "" {
			continue
		}

		// Validate path to prevent path traversal attacks
		cleanFile := filepath.Clean(file)
		if cleanFile == ".." || strings.HasPrefix(cleanFile, ".."+string(os.PathSeparator)) || filepath.IsAbs(cleanFile) {
			failed++
			continue
		}

		srcPath := filepath.Join(cleanWorktree, cleanFile)
		dstPath := filepath.Join(cleanMainRepo, cleanFile)

		// Ensure destination is within mainRepoDir
		if !strings.HasPrefix(filepath.Clean(dstPath), cleanMainRepo+string(os.PathSeparator)) && filepath.Clean(dstPath) != cleanMainRepo {
			failed++
			continue
		}

		// Check if source exists
		srcInfo, err := os.Stat(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				// File tracked but doesn't exist - skip
				continue
			}
			failed++
			continue
		}

		// Skip directories
		if srcInfo.IsDir() {
			continue
		}

		// Read source file
		srcData, err := os.ReadFile(srcPath)
		if err != nil {
			failed++
			continue
		}

		// Check if destination exists and is the same (use bytes.Equal for efficiency)
		dstData, err := os.ReadFile(dstPath)
		if err == nil && bytes.Equal(srcData, dstData) {
			unchanged++
			continue
		}

		// Ensure destination directory exists
		dstDir := filepath.Dir(dstPath)
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			failed++
			continue
		}

		// Copy the file
		if err := os.WriteFile(dstPath, srcData, srcInfo.Mode()); err != nil {
			failed++
			continue
		}

		synced++
	}

	// Handle deleted files - remove them from main repo
	for file := range deletedSet {
		if file == "" {
			continue
		}

		cleanFile := filepath.Clean(file)
		if cleanFile == ".." || strings.HasPrefix(cleanFile, ".."+string(os.PathSeparator)) || filepath.IsAbs(cleanFile) {
			continue
		}

		dstPath := filepath.Join(cleanMainRepo, cleanFile)
		if !strings.HasPrefix(filepath.Clean(dstPath), cleanMainRepo+string(os.PathSeparator)) {
			continue
		}

		if err := os.Remove(dstPath); err == nil {
			deleted++
		}
	}

	if synced == 0 && deleted == 0 && failed == 0 {
		return "No changes to sync (worktree matches main repo).", nil
	}

	result := fmt.Sprintf("✓ Synced %d file(s) from worktree to main repo", synced)
	if deleted > 0 {
		result += fmt.Sprintf(", deleted %d", deleted)
	}
	if unchanged > 0 {
		result += fmt.Sprintf(" (%d unchanged)", unchanged)
	}
	if failed > 0 {
		result += fmt.Sprintf(" (%d failed)", failed)
	}

	return result, nil
}

// Status returns the current spotlight status.
func Status(worktreePath, mainRepoDir string) (string, error) {
	if !IsActive(worktreePath) {
		return "🔦 Spotlight mode: INACTIVE\n\nUse 'start' to enable spotlight mode and sync worktree changes to the main repo for testing.", nil
	}

	// Read state file for details
	stateData, _ := os.ReadFile(StateFile(worktreePath))

	// Count pending changes
	diffCmd := exec.Command("git", "diff", "--name-only", "HEAD")
	diffCmd.Dir = worktreePath
	diffOutput, _ := diffCmd.Output()
	changedCount := len(strings.Split(strings.TrimSpace(string(diffOutput)), "\n"))
	if strings.TrimSpace(string(diffOutput)) == "" {
		changedCount = 0
	}

	msg := "🔦 Spotlight mode: ACTIVE\n\n"
	msg += fmt.Sprintf("Worktree: %s\n", worktreePath)
	msg += fmt.Sprintf("Main repo: %s\n", mainRepoDir)
	if len(stateData) > 0 {
		for _, line := range strings.Split(string(stateData), "\n") {
			if strings.HasPrefix(line, "started=") {
				msg += fmt.Sprintf("Started: %s\n", strings.TrimPrefix(line, "started="))
			}
		}
	}
	msg += fmt.Sprintf("\nPending changes: %d file(s)\n", changedCount)
	msg += "\nUse 'sync' to push changes or 'stop' to disable and restore main repo."

	return msg, nil
}
