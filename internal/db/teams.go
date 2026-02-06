package db

import (
	"fmt"
)

// TeamStatus summarizes the progress of a task's child team.
type TeamStatus struct {
	Total      int `json:"total"`
	Queued     int `json:"queued"`
	Processing int `json:"processing"`
	Blocked    int `json:"blocked"`
	Done       int `json:"done"`
	Backlog    int `json:"backlog"`
}

// IsComplete returns true if all child tasks are done.
func (ts *TeamStatus) IsComplete() bool {
	return ts.Total > 0 && ts.Done == ts.Total
}

// ActiveCount returns the number of non-done tasks.
func (ts *TeamStatus) ActiveCount() int {
	return ts.Total - ts.Done
}

// GetChildTasks returns all tasks whose parent_id matches the given task ID.
func (db *DB) GetChildTasks(parentID int64) ([]*Task, error) {
	rows, err := db.Query(`
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''),
		       COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(pinned, 0), COALESCE(tags, ''),
		       COALESCE(source_branch, ''), COALESCE(summary, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, last_accessed_at,
		       COALESCE(archive_ref, ''), COALESCE(archive_commit, ''),
		       COALESCE(archive_worktree_path, ''), COALESCE(archive_branch_name, ''),
		       COALESCE(parent_id, 0)
		FROM tasks
		WHERE parent_id = ?
		ORDER BY created_at ASC
	`, parentID)
	if err != nil {
		return nil, fmt.Errorf("get child tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
			&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
			&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
			&t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Pinned, &t.Tags,
			&t.SourceBranch, &t.Summary,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.LastDistilledAt, &t.LastAccessedAt,
			&t.ArchiveRef, &t.ArchiveCommit, &t.ArchiveWorktreePath, &t.ArchiveBranchName,
			&t.ParentID,
		)
		if err != nil {
			return nil, fmt.Errorf("scan child task: %w", err)
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
}

// GetTeamStatus returns an aggregate status of all child tasks for a parent.
func (db *DB) GetTeamStatus(parentID int64) (*TeamStatus, error) {
	rows, err := db.Query(`
		SELECT status, COUNT(*) as cnt
		FROM tasks
		WHERE parent_id = ?
		GROUP BY status
	`, parentID)
	if err != nil {
		return nil, fmt.Errorf("get team status: %w", err)
	}
	defer rows.Close()

	ts := &TeamStatus{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan team status: %w", err)
		}
		ts.Total += count
		switch status {
		case StatusQueued:
			ts.Queued = count
		case StatusProcessing:
			ts.Processing = count
		case StatusBlocked:
			ts.Blocked = count
		case StatusDone, StatusArchived:
			ts.Done += count
		case StatusBacklog:
			ts.Backlog = count
		}
	}

	return ts, nil
}

// HasChildTasks returns true if the task has any child tasks.
func (db *DB) HasChildTasks(taskID int64) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE parent_id = ?`, taskID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count child tasks: %w", err)
	}
	return count > 0, nil
}

// GetTeamStatusMap returns a map of parent task IDs to their team statuses.
// This is used by the UI to efficiently display team indicators on all parent tasks.
func (db *DB) GetTeamStatusMap() (map[int64]*TeamStatus, error) {
	rows, err := db.Query(`
		SELECT parent_id, status, COUNT(*) as cnt
		FROM tasks
		WHERE parent_id > 0
		GROUP BY parent_id, status
	`)
	if err != nil {
		return nil, fmt.Errorf("get team status map: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]*TeamStatus)
	for rows.Next() {
		var parentID int64
		var status string
		var count int
		if err := rows.Scan(&parentID, &status, &count); err != nil {
			return nil, fmt.Errorf("scan team status map: %w", err)
		}

		ts, ok := result[parentID]
		if !ok {
			ts = &TeamStatus{}
			result[parentID] = ts
		}
		ts.Total += count
		switch status {
		case StatusQueued:
			ts.Queued = count
		case StatusProcessing:
			ts.Processing = count
		case StatusBlocked:
			ts.Blocked = count
		case StatusDone, StatusArchived:
			ts.Done += count
		case StatusBacklog:
			ts.Backlog = count
		}
	}

	return result, nil
}
