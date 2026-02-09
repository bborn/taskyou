package db

import (
	"database/sql"
	"fmt"
	"time"
)

// Dependency represents a blocking relationship between two tasks.
// The blocker task must be completed before the blocked task can proceed.
type Dependency struct {
	ID        int64     `json:"id"`
	BlockerID int64     `json:"blocker_id"`
	BlockedID int64     `json:"blocked_id"`
	AutoQueue bool      `json:"auto_queue"` // If true, auto-queue blocked task when unblocked
	CreatedAt time.Time `json:"created_at"`
}

// AddDependency creates a dependency where blockerID blocks blockedID.
// Returns an error if the dependency already exists or would create a cycle.
func (db *DB) AddDependency(blockerID, blockedID int64, autoQueue bool) error {
	if blockerID == blockedID {
		return fmt.Errorf("a task cannot block itself")
	}

	// Check for cycles - blockedID should not be able to reach blockerID
	if db.wouldCreateCycle(blockerID, blockedID) {
		return fmt.Errorf("adding this dependency would create a cycle")
	}

	autoQueueInt := 0
	if autoQueue {
		autoQueueInt = 1
	}

	_, err := db.Exec(`
		INSERT INTO task_dependencies (blocker_id, blocked_id, auto_queue)
		VALUES (?, ?, ?)
	`, blockerID, blockedID, autoQueueInt)
	if err != nil {
		return fmt.Errorf("add dependency: %w", err)
	}

	return nil
}

// wouldCreateCycle checks if adding blockerID -> blockedID would create a cycle.
// A cycle exists if blockedID can reach blockerID through existing dependencies.
func (db *DB) wouldCreateCycle(blockerID, blockedID int64) bool {
	// BFS to check if blockedID can reach blockerID
	visited := make(map[int64]bool)
	queue := []int64{blockedID}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current == blockerID {
			return true
		}

		if visited[current] {
			continue
		}
		visited[current] = true

		// Get all tasks that current blocks
		rows, err := db.Query(`
			SELECT blocked_id FROM task_dependencies WHERE blocker_id = ?
		`, current)
		if err != nil {
			continue
		}

		for rows.Next() {
			var nextID int64
			if err := rows.Scan(&nextID); err == nil {
				queue = append(queue, nextID)
			}
		}
		rows.Close()
	}

	return false
}

// RemoveDependency removes a dependency between two tasks.
func (db *DB) RemoveDependency(blockerID, blockedID int64) error {
	result, err := db.Exec(`
		DELETE FROM task_dependencies WHERE blocker_id = ? AND blocked_id = ?
	`, blockerID, blockedID)
	if err != nil {
		return fmt.Errorf("remove dependency: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("dependency not found")
	}

	return nil
}

// GetBlockers returns all tasks that block the given task.
func (db *DB) GetBlockers(taskID int64) ([]*Task, error) {
	rows, err := db.Query(`
		SELECT t.id, t.title, t.body, t.status, t.type, t.project, COALESCE(t.executor, 'claude'),
		       t.worktree_path, t.branch_name, t.port, t.claude_session_id,
		       COALESCE(t.daemon_session, ''), COALESCE(t.tmux_window_id, ''),
		       COALESCE(t.claude_pane_id, ''), COALESCE(t.shell_pane_id, ''),
		       COALESCE(t.pr_url, ''), COALESCE(t.pr_number, 0),
		       COALESCE(t.dangerous_mode, 0), COALESCE(t.pinned, 0), COALESCE(t.tags, ''), COALESCE(t.summary, ''),
		       t.parent_id, COALESCE(t.output, ''),
		       t.created_at, t.updated_at, t.started_at, t.completed_at,
		       t.last_distilled_at, t.last_accessed_at
		FROM tasks t
		JOIN task_dependencies d ON t.id = d.blocker_id
		WHERE d.blocked_id = ?
		ORDER BY t.id
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("get blockers: %w", err)
	}
	defer rows.Close()

	return scanTaskRows(rows)
}

// GetBlockedBy returns all tasks that are blocked by the given task.
func (db *DB) GetBlockedBy(taskID int64) ([]*Task, error) {
	rows, err := db.Query(`
		SELECT t.id, t.title, t.body, t.status, t.type, t.project, COALESCE(t.executor, 'claude'),
		       t.worktree_path, t.branch_name, t.port, t.claude_session_id,
		       COALESCE(t.daemon_session, ''), COALESCE(t.tmux_window_id, ''),
		       COALESCE(t.claude_pane_id, ''), COALESCE(t.shell_pane_id, ''),
		       COALESCE(t.pr_url, ''), COALESCE(t.pr_number, 0),
		       COALESCE(t.dangerous_mode, 0), COALESCE(t.pinned, 0), COALESCE(t.tags, ''), COALESCE(t.summary, ''),
		       t.parent_id, COALESCE(t.output, ''),
		       t.created_at, t.updated_at, t.started_at, t.completed_at,
		       t.last_distilled_at, t.last_accessed_at
		FROM tasks t
		JOIN task_dependencies d ON t.id = d.blocked_id
		WHERE d.blocker_id = ?
		ORDER BY t.id
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("get blocked by: %w", err)
	}
	defer rows.Close()

	return scanTaskRows(rows)
}

// GetDependency returns the dependency between two tasks, or nil if none exists.
func (db *DB) GetDependency(blockerID, blockedID int64) (*Dependency, error) {
	var dep Dependency
	var autoQueueInt int
	err := db.QueryRow(`
		SELECT id, blocker_id, blocked_id, auto_queue, created_at
		FROM task_dependencies
		WHERE blocker_id = ? AND blocked_id = ?
	`, blockerID, blockedID).Scan(&dep.ID, &dep.BlockerID, &dep.BlockedID, &autoQueueInt, &dep.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get dependency: %w", err)
	}
	dep.AutoQueue = autoQueueInt != 0
	return &dep, nil
}

// GetOpenBlockerCount returns the number of incomplete blockers for a task.
func (db *DB) GetOpenBlockerCount(taskID int64) (int, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*)
		FROM task_dependencies d
		JOIN tasks t ON d.blocker_id = t.id
		WHERE d.blocked_id = ? AND t.status NOT IN (?, ?)
	`, taskID, StatusDone, StatusArchived).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("get open blocker count: %w", err)
	}
	return count, nil
}

// IsBlocked returns true if the task has any incomplete blockers.
func (db *DB) IsBlocked(taskID int64) (bool, error) {
	count, err := db.GetOpenBlockerCount(taskID)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ProcessCompletedBlocker checks tasks blocked by the completed task and
// updates their status if they become unblocked.
// Returns the list of tasks that were unblocked.
func (db *DB) ProcessCompletedBlocker(blockerID int64) ([]*Task, error) {
	// Find all tasks blocked by this one
	rows, err := db.Query(`
		SELECT d.blocked_id, d.auto_queue
		FROM task_dependencies d
		WHERE d.blocker_id = ?
	`, blockerID)
	if err != nil {
		return nil, fmt.Errorf("find blocked tasks: %w", err)
	}

	type blockedInfo struct {
		taskID    int64
		autoQueue bool
	}
	var blockedTasks []blockedInfo

	for rows.Next() {
		var info blockedInfo
		var autoQueueInt int
		if err := rows.Scan(&info.taskID, &autoQueueInt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan blocked task: %w", err)
		}
		info.autoQueue = autoQueueInt != 0
		blockedTasks = append(blockedTasks, info)
	}
	rows.Close()

	var unblocked []*Task

	for _, info := range blockedTasks {
		// Check if this task still has other open blockers
		openCount, err := db.GetOpenBlockerCount(info.taskID)
		if err != nil {
			continue
		}

		if openCount == 0 {
			// Task is now fully unblocked
			task, err := db.GetTask(info.taskID)
			if err != nil || task == nil {
				continue
			}

			// Only update if task is in blocked status
			if task.Status == StatusBlocked {
				newStatus := StatusBacklog
				if info.autoQueue {
					newStatus = StatusQueued
				}
				if err := db.UpdateTaskStatus(info.taskID, newStatus); err != nil {
					continue
				}
				task.Status = newStatus
			}

			unblocked = append(unblocked, task)
		}
	}

	return unblocked, nil
}

// GetAllDependencies returns all dependencies for a given task (both blockers and blocked).
func (db *DB) GetAllDependencies(taskID int64) (blockers []*Task, blockedBy []*Task, err error) {
	blockers, err = db.GetBlockers(taskID)
	if err != nil {
		return nil, nil, err
	}

	blockedBy, err = db.GetBlockedBy(taskID)
	if err != nil {
		return nil, nil, err
	}

	return blockers, blockedBy, nil
}

// SetAutoQueue updates the auto_queue flag for a dependency.
func (db *DB) SetAutoQueue(blockerID, blockedID int64, autoQueue bool) error {
	autoQueueInt := 0
	if autoQueue {
		autoQueueInt = 1
	}

	result, err := db.Exec(`
		UPDATE task_dependencies SET auto_queue = ?
		WHERE blocker_id = ? AND blocked_id = ?
	`, autoQueueInt, blockerID, blockedID)
	if err != nil {
		return fmt.Errorf("set auto queue: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("dependency not found")
	}

	return nil
}

// scanTaskRows is a helper to scan task rows into a slice.
func scanTaskRows(rows *sql.Rows) ([]*Task, error) {
	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
			&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
			&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
			&t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Pinned, &t.Tags, &t.Summary,
			&t.ParentID, &t.Output,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.LastDistilledAt, &t.LastAccessedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
}
