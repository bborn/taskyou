package executor

import "fmt"

// WorktreePathIssue is one task row whose recorded directory is not a linked
// worktree of its project, so nothing that removes worktrees can ever act on it.
type WorktreePathIssue struct {
	TaskID  int64
	Title   string
	Project string
	Field   string // "worktree_path" or "archive_worktree_path"
	Path    string
	Reason  string
	Fixed   bool
}

// AuditWorktreePaths finds task rows whose worktree_path (or the
// archive_worktree_path an unarchive would restore to) names the project's main
// checkout rather than a linked worktree. Those rows are the ones that make the
// stale-worktree sweeper attempt an impossible `git worktree remove`, and they
// leave a real repo checkout recorded where every caller expects a disposable
// worktree.
//
// With fix set, the offending references are cleared. Tasks that are currently
// running are reported but never modified — whatever their path says, something
// is using it right now.
func (e *Executor) AuditWorktreePaths(fix bool) ([]WorktreePathIssue, error) {
	refs, err := e.db.ListWorktreeRefs()
	if err != nil {
		return nil, fmt.Errorf("list worktree refs: %w", err)
	}

	var issues []WorktreePathIssue
	for _, ref := range refs {
		// Shared-dir projects opted out of worktree isolation: worktree_path is
		// *supposed* to be the project directory there, and the sweeper already
		// knows not to remove it.
		if !e.config.ProjectUsesWorktrees(ref.Project) {
			continue
		}
		projectDir := e.getProjectDir(ref.Project)

		e.mu.RLock()
		running := e.runningTasks[ref.TaskID]
		e.mu.RUnlock()

		var found []WorktreePathIssue
		for _, candidate := range []struct{ field, path string }{
			{"worktree_path", ref.WorktreePath},
			{"archive_worktree_path", ref.ArchivePath},
		} {
			if candidate.path == "" {
				continue
			}
			reason := ""
			switch {
			case projectDir != "" && sameDir(candidate.path, projectDir):
				reason = "points at the project's main checkout"
			case isMainWorkingTree(candidate.path):
				reason = "points at a git main working tree"
			default:
				continue
			}
			found = append(found, WorktreePathIssue{
				TaskID:  ref.TaskID,
				Title:   ref.Title,
				Project: ref.Project,
				Field:   candidate.field,
				Path:    candidate.path,
				Reason:  reason,
			})
		}
		if len(found) == 0 {
			continue
		}

		if fix && !running {
			if err := e.db.ClearTaskWorktreeRefs(ref.TaskID); err != nil {
				return nil, fmt.Errorf("clear worktree refs for task %d: %w", ref.TaskID, err)
			}
			for i := range found {
				found[i].Fixed = true
			}
		}
		issues = append(issues, found...)
	}
	return issues, nil
}
