package db

import (
	"context"
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
//
// The cycle check and the insert are atomic: they run inside a single
// BEGIN IMMEDIATE transaction that holds SQLite's write lock from before the
// read until after the insert commits. A concurrent caller that would add the
// inverse edge blocks on the write lock until this transaction commits, and
// its cycle check then observes this edge and rejects the inverse. Without
// this the check-then-insert is a TOCTOU race: two crossing inserts (A->B and
// B->A) can both pass the check and both insert, persisting a 2-cycle that the
// schema cannot reject (UNIQUE(blocker_id, blocked_id) and CHECK(blocker_id !=
// blocked_id) allow it). The DB pool is capped at a single connection
// (internal/db/sqlite.go) and this method pins it for the duration, so
// in-process callers serialize here; BEGIN IMMEDIATE additionally serializes
// against any other OS process that shares the database file (e.g. concurrent
// `ty block` invocations).
func (db *DB) AddDependency(blockerID, blockedID int64, autoQueue bool) error {
	if blockerID == blockedID {
		return fmt.Errorf("a task cannot block itself")
	}

	autoQueueInt := 0
	if autoQueue {
		autoQueueInt = 1
	}

	ctx := context.Background()
	// Pin one connection from the pool and open BEGIN IMMEDIATE on it.
	// modernc.org/sqlite selects the BEGIN variant from the DSN _txlock, not
	// from sql.TxOptions.Isolation, so a plain db.Begin()/BeginTx(...) would be
	// a deferred transaction that does not take the write lock until the first
	// write — too late to protect the read. Issuing BEGIN IMMEDIATE on a pinned
	// *sql.Conn is the targeted way to serialize here without changing the
	// global transaction mode for every other call site. No caller of
	// AddDependency is itself inside a db.Begin() transaction, so pinning the
	// single-connection pool cannot self-deadlock.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("add dependency: acquire connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("add dependency: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	// Check for cycles — adding blockerID -> blockedID would create a cycle if
	// blockedID can already reach blockerID through existing edges. This read
	// runs inside the transaction so a concurrent inverse insert cannot slip
	// in between the check and the insert.
	if wouldCreateCycleOn(ctx, conn, blockerID, blockedID) {
		return fmt.Errorf("adding this dependency would create a cycle")
	}

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO task_dependencies (blocker_id, blocked_id, auto_queue)
		VALUES (?, ?, ?)
	`, blockerID, blockedID, autoQueueInt); err != nil {
		return fmt.Errorf("add dependency: %w", err)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("add dependency: commit: %w", err)
	}
	committed = true
	return nil
}

// wouldCreateCycleOn is the BFS that backs AddDependency's cycle check. It
// returns true if blockedID can reach blockerID by following
// task_dependencies edges (blocker_id -> blocked_id). The caller must hold a
// write transaction (BEGIN IMMEDIATE) across the read and the subsequent
// insert so a concurrent inverse insert cannot slip in between them;
// AddDependency wraps this in such a transaction on a pinned connection.
func wouldCreateCycleOn(ctx context.Context, conn *sql.Conn, blockerID, blockedID int64) bool {
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
		rows, err := conn.QueryContext(ctx, `
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
		       COALESCE(t.dangerous_mode, 0), COALESCE(t.permission_mode, ''), COALESCE(t.pinned, 0), COALESCE(t.tags, ''), COALESCE(t.summary, ''),
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
		       COALESCE(t.dangerous_mode, 0), COALESCE(t.permission_mode, ''), COALESCE(t.pinned, 0), COALESCE(t.tags, ''), COALESCE(t.summary, ''),
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

			// Only flip a task that is actually waiting in the DAG (blocked and
			// never started). A blocked task that has already started is waiting on
			// a human (needs-input / PR review), not on this dependency.
			if task.Status == StatusBlocked && task.StartedAt == nil {
				newStatus := StatusBacklog
				if info.autoQueue {
					newStatus = StatusQueued
				}
				if err := db.SetTaskStatus(info.taskID, newStatus, ActorSystem,
					"every blocker finished, so this dependent is released",
					Observedf("blocker task #%d reached a terminal state; 0 open blockers remain", blockerID)); err != nil {
					// Best-effort: a lost write (e.g. SQLITE_BUSY) leaves the task
					// blocked; RequeueReadyTasks sweeps it up later. Surface it.
					return unblocked, fmt.Errorf("requeue unblocked task %d: %w", info.taskID, err)
				}
				task.Status = newStatus
				unblocked = append(unblocked, task) // only report an actual flip
			}
		}
	}

	return unblocked, nil
}

// RequeueReadyTasks is the eventually-consistent safety net for
// ProcessCompletedBlocker. That runs once, best-effort, the instant a blocker
// completes; a single dropped write (SQLITE_BUSY, a crash) would orphan the
// dependent in 'blocked' forever, stalling a workflow. This sweep re-queues any
// task still waiting in the DAG — blocked, never started — whose blockers have
// all completed. Idempotent: it only touches tasks that are genuinely ready.
// Returns the tasks it moved.
func (db *DB) RequeueReadyTasks() ([]*Task, error) {
	// Candidate = blocked, never-started task that has at least one dependency.
	rows, err := db.Query(`
		SELECT DISTINCT t.id
		FROM tasks t
		JOIN task_dependencies d ON d.blocked_id = t.id
		WHERE t.status = ? AND t.started_at IS NULL
	`, StatusBlocked)
	if err != nil {
		return nil, fmt.Errorf("find ready tasks: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan ready task: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()

	var moved []*Task
	for _, id := range ids {
		openCount, err := db.GetOpenBlockerCount(id)
		if err != nil || openCount != 0 {
			continue
		}
		autoQueue, err := db.hasAutoQueueBlocker(id)
		if err != nil {
			continue
		}
		newStatus := StatusBacklog
		if autoQueue {
			newStatus = StatusQueued
		}
		if err := db.SetTaskStatus(id, newStatus, ActorSweep,
			"safety net: this task's blockers are all finished but it was still held",
			Observedf("0 open blockers remain for task #%d", id)); err != nil {
			continue // try again on the next sweep
		}
		if task, err := db.GetTask(id); err == nil && task != nil {
			moved = append(moved, task)
		}
	}
	return moved, nil
}

// hasAutoQueueBlocker reports whether any of the task's dependencies has
// auto_queue set (so an unblocked task is queued rather than dropped to backlog).
func (db *DB) hasAutoQueueBlocker(taskID int64) (bool, error) {
	var n int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM task_dependencies WHERE blocked_id = ? AND auto_queue = 1
	`, taskID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
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
			&t.DangerousMode, &t.PermissionMode, &t.Pinned, &t.Tags, &t.Summary,
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
