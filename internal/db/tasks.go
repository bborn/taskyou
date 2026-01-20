package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Task represents a task in the database.
type Task struct {
	ID              int64
	Title           string
	Body            string
	Status          string
	Type            string
	Project         string
	Executor        string // Task executor: "claude" (default), "codex"
	WorktreePath    string
	BranchName      string
	Port            int    // Unique port for running the application in this task's worktree
	ClaudeSessionID string // Claude session ID for resuming conversations
	DaemonSession   string // tmux daemon session name (e.g., "task-daemon-12345")
	TmuxWindowID    string // tmux window ID (e.g., "@1234") for unique window identification
	PRURL           string // Pull request URL (if associated with a PR)
	PRNumber        int    // Pull request number (if associated with a PR)
	DangerousMode   bool   // Whether task is running in dangerous mode (--dangerously-skip-permissions)
	Tags            string // Comma-separated tags for categorization (e.g., "customer-support,email,influence-kit")
	CreatedAt       LocalTime
	UpdatedAt       LocalTime
	StartedAt       *LocalTime
	CompletedAt     *LocalTime
	// Schedule fields for recurring/scheduled tasks
	ScheduledAt *LocalTime // When to next run (nil = not scheduled)
	Recurrence  string     // Recurrence pattern: "", "hourly", "daily", "weekly", "monthly", or cron expression
	LastRunAt   *LocalTime // When last executed (for recurring tasks)
}

// Task statuses
const (
	StatusBacklog    = "backlog"    // Created but not yet started
	StatusQueued     = "queued"     // Waiting to be processed
	StatusProcessing = "processing" // Currently being executed
	StatusBlocked    = "blocked"    // Needs input/clarification
	StatusDone       = "done"       // Completed
	StatusArchived   = "archived"   // Archived (hidden from view)
)

// IsInProgress returns true if the task is actively being worked on.
func IsInProgress(status string) bool {
	return status == StatusQueued || status == StatusProcessing
}

// Task types (default values, actual types are stored in task_types table)
const (
	TypeCode     = "code"
	TypeWriting  = "writing"
	TypeThinking = "thinking"
)

// Task executors
const (
	ExecutorClaude = "claude" // Claude Code CLI (default)
	ExecutorCodex  = "codex"  // OpenAI Codex CLI
)

// DefaultExecutor returns the default executor if none is specified.
func DefaultExecutor() string {
	return ExecutorClaude
}

// Recurrence patterns for scheduled tasks
const (
	RecurrenceNone    = ""        // One-time scheduled task (or not scheduled)
	RecurrenceHourly  = "hourly"  // Run every hour
	RecurrenceDaily   = "daily"   // Run every day
	RecurrenceWeekly  = "weekly"  // Run every week
	RecurrenceMonthly = "monthly" // Run every month
)

// IsScheduled returns true if the task has a scheduled time set.
func (t *Task) IsScheduled() bool {
	return t.ScheduledAt != nil && !t.ScheduledAt.Time.IsZero()
}

// IsRecurring returns true if the task has a recurrence pattern.
func (t *Task) IsRecurring() bool {
	return t.Recurrence != ""
}

// Port allocation constants
const (
	PortRangeStart = 3100 // First port in the allocation range
	PortRangeEnd   = 4099 // Last port in the allocation range (1000 ports total)
)

// TaskType represents a configurable task type with its prompt instructions.
type TaskType struct {
	ID           int64
	Name         string // e.g., "code", "writing", "thinking"
	Label        string // Display label
	Instructions string // Prompt template for this type
	SortOrder    int    // For UI ordering
	IsBuiltin    bool   // Protect default types from deletion
	CreatedAt    LocalTime
}

// ErrProjectNotFound is returned when a task is created with a non-existent project.
var ErrProjectNotFound = fmt.Errorf("project not found")

// CreateTask creates a new task.
func (db *DB) CreateTask(t *Task) error {
	// Default to 'personal' project if not specified
	if t.Project == "" {
		t.Project = "personal"
	}

	// Default to 'claude' executor if not specified
	if t.Executor == "" {
		t.Executor = DefaultExecutor()
	}

	// Validate that the project exists
	project, err := db.GetProjectByName(t.Project)
	if err != nil {
		return fmt.Errorf("validate project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("%w: %s", ErrProjectNotFound, t.Project)
	}

	result, err := db.Exec(`
		INSERT INTO tasks (title, body, status, type, project, executor, scheduled_at, recurrence, last_run_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, t.Title, t.Body, t.Status, t.Type, t.Project, t.Executor, t.ScheduledAt, t.Recurrence, t.LastRunAt)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}
	t.ID = id

	// Save the last used task type for this project
	if t.Type != "" {
		db.SetLastTaskTypeForProject(t.Project, t.Type)
	}

	// Save the last used project
	if t.Project != "" {
		db.SetLastUsedProject(t.Project)
	}

	return nil
}

// GetTask retrieves a task by ID.
func (db *DB) GetTask(id int64) (*Task, error) {
	t := &Task{}
	err := db.QueryRow(`
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''), COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(tags, ''),
		       created_at, updated_at, started_at, completed_at,
		       scheduled_at, recurrence, last_run_at
		FROM tasks WHERE id = ?
	`, id).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
		&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
		&t.DaemonSession, &t.TmuxWindowID, &t.PRURL, &t.PRNumber,
		&t.DangerousMode, &t.Tags,
		&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
		&t.ScheduledAt, &t.Recurrence, &t.LastRunAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query task: %w", err)
	}
	return t, nil
}

// ListTasksOptions defines options for listing tasks.
type ListTasksOptions struct {
	Status        string
	Type          string
	Project       string
	Limit         int
	Offset        int
	IncludeClosed bool // Include closed tasks even when Status is empty
}

// ListTasks retrieves tasks with optional filters.
func (db *DB) ListTasks(opts ListTasksOptions) ([]*Task, error) {
	query := `
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''), COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(tags, ''),
		       created_at, updated_at, started_at, completed_at,
		       scheduled_at, recurrence, last_run_at
		FROM tasks WHERE 1=1
	`
	args := []interface{}{}

	if opts.Status != "" {
		query += " AND status = ?"
		args = append(args, opts.Status)
	}
	if opts.Type != "" {
		query += " AND type = ?"
		args = append(args, opts.Type)
	}
	if opts.Project != "" {
		query += " AND project = ?"
		args = append(args, opts.Project)
	}

	// Exclude done and archived by default unless specifically querying for them or includeClosed is set
	if opts.Status == "" && !opts.IncludeClosed {
		query += " AND status NOT IN ('done', 'archived')"
	}

	// Sort done/blocked tasks by completed_at (most recently closed first),
	// other tasks by created_at (newest first).
	// Use id DESC as secondary sort for consistent ordering when timestamps are equal.
	query += " ORDER BY CASE WHEN status IN ('done', 'blocked') THEN completed_at ELSE created_at END DESC, id DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	} else {
		query += " LIMIT 100"
	}
	if opts.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", opts.Offset)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
			&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
			&t.DaemonSession, &t.TmuxWindowID, &t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Tags,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.ScheduledAt, &t.Recurrence, &t.LastRunAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
}

// GetMostRecentlyCreatedTask returns the task with the most recent created_at timestamp.
// This is used to get the last task's project for defaulting in new task forms.
func (db *DB) GetMostRecentlyCreatedTask() (*Task, error) {
	t := &Task{}
	err := db.QueryRow(`
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''), COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(tags, ''),
		       created_at, updated_at, started_at, completed_at,
		       scheduled_at, recurrence, last_run_at
		FROM tasks
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
		&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
		&t.DaemonSession, &t.TmuxWindowID, &t.PRURL, &t.PRNumber,
		&t.DangerousMode, &t.Tags,
		&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
		&t.ScheduledAt, &t.Recurrence, &t.LastRunAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query most recently created task: %w", err)
	}
	return t, nil
}

// SearchTasks searches for tasks by query string across title, project, ID, and PR number.
// This is used by the command palette to search all tasks, not just the preloaded ones.
func (db *DB) SearchTasks(query string, limit int) ([]*Task, error) {
	if limit <= 0 {
		limit = 50
	}

	// Build search query with LIKE clauses
	sqlQuery := `
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''), COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(tags, ''),
		       created_at, updated_at, started_at, completed_at,
		       scheduled_at, recurrence, last_run_at
		FROM tasks
		WHERE (
			title LIKE ? COLLATE NOCASE
			OR project LIKE ? COLLATE NOCASE
			OR CAST(id AS TEXT) LIKE ?
			OR CAST(pr_number AS TEXT) LIKE ?
			OR pr_url LIKE ? COLLATE NOCASE
		)
		ORDER BY CASE WHEN status IN ('done', 'blocked') THEN completed_at ELSE created_at END DESC, id DESC
		LIMIT ?
	`

	searchPattern := "%" + query + "%"
	rows, err := db.Query(sqlQuery, searchPattern, searchPattern, searchPattern, searchPattern, searchPattern, limit)
	if err != nil {
		return nil, fmt.Errorf("search tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
			&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
			&t.DaemonSession, &t.TmuxWindowID, &t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Tags,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.ScheduledAt, &t.Recurrence, &t.LastRunAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
}

// CountTasksByStatus returns the count of tasks with a given status.
func (db *DB) CountTasksByStatus(status string) (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = ?", status).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count tasks: %w", err)
	}
	return count, nil
}

// MarkTaskStarted sets the started_at timestamp if not already set.
func (db *DB) MarkTaskStarted(id int64) error {
	_, err := db.Exec(`
		UPDATE tasks SET started_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND started_at IS NULL
	`, id)
	return err
}

// UpdateTaskStatus updates a task's status.
func (db *DB) UpdateTaskStatus(id int64, status string) error {
	query := "UPDATE tasks SET status = ?, updated_at = CURRENT_TIMESTAMP"
	args := []interface{}{status}

	switch status {
	case StatusProcessing:
		query += ", started_at = CURRENT_TIMESTAMP"
	case StatusDone, StatusBlocked, StatusArchived:
		query += ", completed_at = CURRENT_TIMESTAMP"
	}

	query += " WHERE id = ?"
	args = append(args, id)

	_, err := db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	return nil
}

// UpdateTask updates a task's fields.
func (db *DB) UpdateTask(t *Task) error {
	_, err := db.Exec(`
		UPDATE tasks SET
			title = ?, body = ?, status = ?, type = ?, project = ?, executor = ?,
			worktree_path = ?, branch_name = ?, port = ?, claude_session_id = ?,
			daemon_session = ?, pr_url = ?, pr_number = ?, dangerous_mode = ?,
			tags = ?, scheduled_at = ?, recurrence = ?, last_run_at = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, t.Title, t.Body, t.Status, t.Type, t.Project, t.Executor,
		t.WorktreePath, t.BranchName, t.Port, t.ClaudeSessionID,
		t.DaemonSession, t.PRURL, t.PRNumber, t.DangerousMode,
		t.Tags, t.ScheduledAt, t.Recurrence, t.LastRunAt, t.ID)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	return nil
}

// UpdateTaskClaudeSessionID updates only the Claude session ID for a task.
func (db *DB) UpdateTaskClaudeSessionID(taskID int64, sessionID string) error {
	_, err := db.Exec(`
		UPDATE tasks SET claude_session_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, sessionID, taskID)
	if err != nil {
		return fmt.Errorf("update task claude session id: %w", err)
	}
	return nil
}

// UpdateTaskDangerousMode updates only the dangerous_mode flag for a task.
func (db *DB) UpdateTaskDangerousMode(taskID int64, dangerousMode bool) error {
	_, err := db.Exec(`
		UPDATE tasks SET dangerous_mode = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, dangerousMode, taskID)
	if err != nil {
		return fmt.Errorf("update task dangerous mode: %w", err)
	}
	return nil
}

// UpdateTaskDaemonSession updates the tmux daemon session name for a task.
// This is used to track which daemon session owns the task's tmux window,
// so we can properly kill the Claude process when the task completes.
func (db *DB) UpdateTaskDaemonSession(taskID int64, daemonSession string) error {
	_, err := db.Exec(`
		UPDATE tasks SET daemon_session = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, daemonSession, taskID)
	if err != nil {
		return fmt.Errorf("update task daemon session: %w", err)
	}
	return nil
}

// UpdateTaskWindowID updates the tmux window ID for a task.
// This is used to track the unique window ID (e.g., "@1234") for reliable window targeting.
func (db *DB) UpdateTaskWindowID(taskID int64, windowID string) error {
	_, err := db.Exec(`
		UPDATE tasks SET tmux_window_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, windowID, taskID)
	if err != nil {
		return fmt.Errorf("update task window id: %w", err)
	}
	return nil
}

// DeleteTask deletes a task.
func (db *DB) DeleteTask(id int64) error {
	_, err := db.Exec("DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	return nil
}

// GetActiveTaskPorts returns all ports currently in use by active (non-done, non-archived) tasks.
func (db *DB) GetActiveTaskPorts() (map[int]bool, error) {
	rows, err := db.Query(`
		SELECT port FROM tasks
		WHERE port > 0 AND status NOT IN (?, ?)
	`, StatusDone, StatusArchived)
	if err != nil {
		return nil, fmt.Errorf("query active ports: %w", err)
	}
	defer rows.Close()

	ports := make(map[int]bool)
	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err != nil {
			return nil, fmt.Errorf("scan port: %w", err)
		}
		ports[port] = true
	}
	return ports, nil
}

// AllocatePort assigns an available port to a task.
// Returns the allocated port, or an error if no ports are available.
func (db *DB) AllocatePort(taskID int64) (int, error) {
	// Get currently used ports
	usedPorts, err := db.GetActiveTaskPorts()
	if err != nil {
		return 0, fmt.Errorf("get active ports: %w", err)
	}

	// Find first available port in range
	for port := PortRangeStart; port <= PortRangeEnd; port++ {
		if !usedPorts[port] {
			// Update task with allocated port
			_, err := db.Exec(`UPDATE tasks SET port = ? WHERE id = ?`, port, taskID)
			if err != nil {
				return 0, fmt.Errorf("update task port: %w", err)
			}
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available ports in range %d-%d", PortRangeStart, PortRangeEnd)
}

// RetryTask clears logs, appends feedback to body, and re-queues a task.
func (db *DB) RetryTask(id int64, feedback string) error {
	// Add continuation marker to logs
	db.AppendTaskLog(id, "system", "--- Continuation ---")

	// Log feedback if provided
	if feedback != "" {
		db.AppendTaskLog(id, "text", "Feedback: "+feedback)
	}

	// Re-queue the task
	return db.UpdateTaskStatus(id, StatusQueued)
}

// GetNextQueuedTask returns the next task to process.
func (db *DB) GetNextQueuedTask() (*Task, error) {
	t := &Task{}
	err := db.QueryRow(`
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''), COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(tags, ''),
		       created_at, updated_at, started_at, completed_at,
		       scheduled_at, recurrence, last_run_at
		FROM tasks
		WHERE status = ?
		ORDER BY created_at ASC
		LIMIT 1
	`, StatusQueued).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
		&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
		&t.DaemonSession, &t.TmuxWindowID, &t.PRURL, &t.PRNumber,
		&t.DangerousMode, &t.Tags,
		&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
		&t.ScheduledAt, &t.Recurrence, &t.LastRunAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query next task: %w", err)
	}
	return t, nil
}

// GetQueuedTasks returns all queued tasks (waiting to be processed).
func (db *DB) GetQueuedTasks() ([]*Task, error) {
	rows, err := db.Query(`
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''), COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(tags, ''),
		       created_at, updated_at, started_at, completed_at,
		       scheduled_at, recurrence, last_run_at
		FROM tasks
		WHERE status = ?
		ORDER BY created_at ASC
	`, StatusQueued)
	if err != nil {
		return nil, fmt.Errorf("query queued tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		if err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
			&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
			&t.DaemonSession, &t.TmuxWindowID, &t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Tags,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.ScheduledAt, &t.Recurrence, &t.LastRunAt,
		); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// GetTasksWithBranches returns tasks that have a branch name and aren't done or archived.
// These are candidates for automatic closure when their PR is merged.
func (db *DB) GetTasksWithBranches() ([]*Task, error) {
	rows, err := db.Query(`
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''), COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(tags, ''),
		       created_at, updated_at, started_at, completed_at,
		       scheduled_at, recurrence, last_run_at
		FROM tasks
		WHERE branch_name != '' AND status NOT IN (?, ?)
		ORDER BY created_at DESC
	`, StatusDone, StatusArchived)
	if err != nil {
		return nil, fmt.Errorf("query tasks with branches: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		if err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
			&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
			&t.DaemonSession, &t.TmuxWindowID, &t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Tags,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.ScheduledAt, &t.Recurrence, &t.LastRunAt,
		); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// GetDueScheduledTasks returns all scheduled tasks that are due to run.
// A task is due if:
// - It has a scheduled_at time that is <= now
// - It is in 'backlog' status (ready to be queued)
// - It is not currently processing
func (db *DB) GetDueScheduledTasks() ([]*Task, error) {
	rows, err := db.Query(`
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''), COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(tags, ''),
		       created_at, updated_at, started_at, completed_at,
		       scheduled_at, recurrence, last_run_at
		FROM tasks
		WHERE scheduled_at IS NOT NULL
		  AND scheduled_at <= CURRENT_TIMESTAMP
		  AND status = ?
		ORDER BY scheduled_at ASC
	`, StatusBacklog)
	if err != nil {
		return nil, fmt.Errorf("query due scheduled tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		if err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
			&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
			&t.DaemonSession, &t.TmuxWindowID, &t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Tags,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.ScheduledAt, &t.Recurrence, &t.LastRunAt,
		); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// GetScheduledTasks returns all tasks that have a scheduled time set (regardless of status).
func (db *DB) GetScheduledTasks() ([]*Task, error) {
	rows, err := db.Query(`
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''), COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(tags, ''),
		       created_at, updated_at, started_at, completed_at,
		       scheduled_at, recurrence, last_run_at
		FROM tasks
		WHERE scheduled_at IS NOT NULL
		ORDER BY scheduled_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query scheduled tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		if err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
			&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
			&t.DaemonSession, &t.TmuxWindowID, &t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Tags,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.ScheduledAt, &t.Recurrence, &t.LastRunAt,
		); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// CalculateNextRunTime calculates the next scheduled time based on recurrence pattern.
// Returns nil if recurrence is empty (one-time task).
func CalculateNextRunTime(recurrence string, fromTime time.Time) *LocalTime {
	if recurrence == "" {
		return nil
	}

	var nextTime time.Time
	switch recurrence {
	case RecurrenceHourly:
		nextTime = fromTime.Add(1 * time.Hour)
	case RecurrenceDaily:
		nextTime = fromTime.AddDate(0, 0, 1)
	case RecurrenceWeekly:
		nextTime = fromTime.AddDate(0, 0, 7)
	case RecurrenceMonthly:
		nextTime = fromTime.AddDate(0, 1, 0)
	default:
		// Unknown recurrence pattern
		return nil
	}

	return &LocalTime{Time: nextTime}
}

// UpdateTaskSchedule updates only the schedule-related fields of a task.
func (db *DB) UpdateTaskSchedule(taskID int64, scheduledAt *LocalTime, recurrence string, lastRunAt *LocalTime) error {
	_, err := db.Exec(`
		UPDATE tasks SET
			scheduled_at = ?,
			recurrence = ?,
			last_run_at = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, scheduledAt, recurrence, lastRunAt, taskID)
	if err != nil {
		return fmt.Errorf("update task schedule: %w", err)
	}
	return nil
}

// QueueScheduledTask queues a scheduled task and updates its schedule for recurring tasks.
// For recurring tasks, it sets the next scheduled_at time; for one-time tasks, it clears scheduled_at.
func (db *DB) QueueScheduledTask(taskID int64) error {
	// Get the task first
	task, err := db.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}
	if task == nil {
		return fmt.Errorf("task %d not found", taskID)
	}

	now := time.Now()

	// Calculate next run time for recurring tasks
	if task.IsRecurring() {
		nextRun := CalculateNextRunTime(task.Recurrence, now)
		task.ScheduledAt = nextRun
		task.LastRunAt = &LocalTime{Time: now}
	} else {
		// One-time task - clear the schedule after queuing
		task.ScheduledAt = nil
		task.LastRunAt = &LocalTime{Time: now}
	}

	// Update status to queued
	task.Status = StatusQueued

	// Save all changes
	return db.UpdateTask(task)
}

// TaskLog represents a log entry for a task.
type TaskLog struct {
	ID        int64
	TaskID    int64
	LineType  string // "output", "tool", "error", "system"
	Content   string
	CreatedAt LocalTime
}

// AppendTaskLog appends a log entry to a task.
func (db *DB) AppendTaskLog(taskID int64, lineType, content string) error {
	_, err := db.Exec(`
		INSERT INTO task_logs (task_id, line_type, content)
		VALUES (?, ?, ?)
	`, taskID, lineType, content)
	if err != nil {
		return fmt.Errorf("insert task log: %w", err)
	}
	return nil
}

// GetTaskLogs retrieves logs for a task.
func (db *DB) GetTaskLogs(taskID int64, limit int) ([]*TaskLog, error) {
	if limit <= 0 {
		limit = 1000
	}

	rows, err := db.Query(`
		SELECT id, task_id, line_type, content, created_at
		FROM task_logs
		WHERE task_id = ?
		ORDER BY id DESC
		LIMIT ?
	`, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("query task logs: %w", err)
	}
	defer rows.Close()

	var logs []*TaskLog
	for rows.Next() {
		l := &TaskLog{}
		err := rows.Scan(&l.ID, &l.TaskID, &l.LineType, &l.Content, &l.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan task log: %w", err)
		}
		logs = append(logs, l)
	}

	return logs, nil
}

// GetTaskLogCount returns the number of logs for a task.
// This is a fast operation useful for checking if logs have changed.
func (db *DB) GetTaskLogCount(taskID int64) (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM task_logs WHERE task_id = ?", taskID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count task logs: %w", err)
	}
	return count, nil
}

// GetTaskLogsSince retrieves logs after a given ID.
func (db *DB) GetTaskLogsSince(taskID int64, sinceID int64) ([]*TaskLog, error) {
	rows, err := db.Query(`
		SELECT id, task_id, line_type, content, created_at
		FROM task_logs
		WHERE task_id = ? AND id > ?
		ORDER BY id ASC
	`, taskID, sinceID)
	if err != nil {
		return nil, fmt.Errorf("query task logs: %w", err)
	}
	defer rows.Close()

	var logs []*TaskLog
	for rows.Next() {
		l := &TaskLog{}
		err := rows.Scan(&l.ID, &l.TaskID, &l.LineType, &l.Content, &l.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan task log: %w", err)
		}
		logs = append(logs, l)
	}

	return logs, nil
}

// ClearTaskLogs clears all logs for a task.
func (db *DB) ClearTaskLogs(taskID int64) error {
	_, err := db.Exec("DELETE FROM task_logs WHERE task_id = ?", taskID)
	if err != nil {
		return fmt.Errorf("clear task logs: %w", err)
	}
	return nil
}

// GetLastQuestion retrieves the most recent question log for a task.
func (db *DB) GetLastQuestion(taskID int64) (string, error) {
	var content string
	err := db.QueryRow(`
		SELECT content
		FROM task_logs
		WHERE task_id = ? AND line_type = 'question'
		ORDER BY id DESC
		LIMIT 1
	`, taskID).Scan(&content)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query last question: %w", err)
	}
	return content, nil
}

// GetRetryFeedback returns the feedback from the most recent retry, or empty string if not a retry.
// Looks for "Feedback: ..." log entry after "--- Continuation ---" marker.
func (db *DB) GetRetryFeedback(taskID int64) (string, error) {
	// Check if there's a continuation marker
	var hasContinuation int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM task_logs
		WHERE task_id = ? AND content = '--- Continuation ---'
	`, taskID).Scan(&hasContinuation)
	if err != nil || hasContinuation == 0 {
		return "", err
	}

	// Get the feedback after the last continuation marker
	var content string
	err = db.QueryRow(`
		SELECT content FROM task_logs
		WHERE task_id = ? AND content LIKE 'Feedback: %'
		AND id > (
			SELECT MAX(id) FROM task_logs
			WHERE task_id = ? AND content = '--- Continuation ---'
		)
		ORDER BY id DESC LIMIT 1
	`, taskID, taskID).Scan(&content)
	if err == sql.ErrNoRows {
		return "", nil // Retry without feedback
	}
	if err != nil {
		return "", err
	}

	// Strip "Feedback: " prefix
	if len(content) > 10 {
		return content[10:], nil
	}
	return "", nil
}

// ProjectAction defines an action that runs on tasks for a project.
type ProjectAction struct {
	Trigger      string `json:"trigger"`      // "on_create", "on_status:queued", etc.
	Instructions string `json:"instructions"` // prompt/instructions for this action
}

// Project represents a configured project.
type Project struct {
	ID           int64
	Name         string
	Path         string
	Aliases      string          // comma-separated
	Instructions string          // project-specific instructions for AI
	Actions      []ProjectAction // actions triggered on task events (stored as JSON)
	Color        string          // hex color for display (e.g., "#61AFEF")
	CreatedAt    LocalTime
}

// GetAction returns the action for a given trigger, or nil if not found.
func (p *Project) GetAction(trigger string) *ProjectAction {
	for i := range p.Actions {
		if p.Actions[i].Trigger == trigger {
			return &p.Actions[i]
		}
	}
	return nil
}

// CreateProject creates a new project.
func (db *DB) CreateProject(p *Project) error {
	actionsJSON, _ := json.Marshal(p.Actions)
	result, err := db.Exec(`
		INSERT INTO projects (name, path, aliases, instructions, actions, color)
		VALUES (?, ?, ?, ?, ?, ?)
	`, p.Name, p.Path, p.Aliases, p.Instructions, string(actionsJSON), p.Color)
	if err != nil {
		return fmt.Errorf("insert project: %w", err)
	}
	id, _ := result.LastInsertId()
	p.ID = id
	return nil
}

// UpdateProject updates a project.
func (db *DB) UpdateProject(p *Project) error {
	actionsJSON, _ := json.Marshal(p.Actions)
	_, err := db.Exec(`
		UPDATE projects SET name = ?, path = ?, aliases = ?, instructions = ?, actions = ?, color = ?
		WHERE id = ?
	`, p.Name, p.Path, p.Aliases, p.Instructions, string(actionsJSON), p.Color, p.ID)
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	return nil
}

// DeleteProject deletes a project.
func (db *DB) DeleteProject(id int64) error {
	// Get the project name to check if it's the personal project
	var name string
	err := db.QueryRow("SELECT name FROM projects WHERE id = ?", id).Scan(&name)
	if err != nil {
		return fmt.Errorf("get project: %w", err)
	}

	// Prevent deletion of the personal project
	if name == "personal" {
		return fmt.Errorf("cannot delete the personal project")
	}

	_, err = db.Exec("DELETE FROM projects WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}

// CountTasksByProject returns the number of tasks associated with a project name.
func (db *DB) CountTasksByProject(projectName string) (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM tasks WHERE project = ?", projectName).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count tasks: %w", err)
	}
	return count, nil
}

// CountMemoriesByProject returns the number of memories associated with a project name.
func (db *DB) CountMemoriesByProject(projectName string) (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM project_memories WHERE project = ?", projectName).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count memories: %w", err)
	}
	return count, nil
}

// ListProjects returns all projects, with "personal" always first.
func (db *DB) ListProjects() ([]*Project, error) {
	rows, err := db.Query(`
		SELECT id, name, path, aliases, instructions, COALESCE(actions, '[]'), COALESCE(color, ''), created_at
		FROM projects ORDER BY CASE WHEN name = 'personal' THEN 0 ELSE 1 END, name
	`)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		p := &Project{}
		var actionsJSON string
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &actionsJSON, &p.Color, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		json.Unmarshal([]byte(actionsJSON), &p.Actions)
		projects = append(projects, p)
	}
	return projects, nil
}

// GetProjectByName returns a project by name or alias.
func (db *DB) GetProjectByName(name string) (*Project, error) {
	// First try exact name match
	p := &Project{}
	var actionsJSON string
	err := db.QueryRow(`
		SELECT id, name, path, aliases, instructions, COALESCE(actions, '[]'), COALESCE(color, ''), created_at
		FROM projects WHERE name = ?
	`, name).Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &actionsJSON, &p.Color, &p.CreatedAt)
	if err == nil {
		json.Unmarshal([]byte(actionsJSON), &p.Actions)
		return p, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("query project: %w", err)
	}

	// Try alias match
	rows, err := db.Query(`SELECT id, name, path, aliases, instructions, COALESCE(actions, '[]'), COALESCE(color, ''), created_at FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &actionsJSON, &p.Color, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		json.Unmarshal([]byte(actionsJSON), &p.Actions)
		for _, alias := range splitAliases(p.Aliases) {
			if alias == name {
				return p, nil
			}
		}
	}
	return nil, nil
}

func splitAliases(aliases string) []string {
	if aliases == "" {
		return nil
	}
	var result []string
	for _, a := range strings.Split(aliases, ",") {
		a = strings.TrimSpace(a)
		if a != "" {
			result = append(result, a)
		}
	}
	return result
}

// GetProjectByPath returns a project whose path matches the given directory.
// It checks if cwd equals or is a subdirectory of any project's path.
func (db *DB) GetProjectByPath(cwd string) (*Project, error) {
	projects, err := db.ListProjects()
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}

	// Clean the cwd for consistent matching
	cwd = filepath.Clean(cwd)

	for _, p := range projects {
		projectPath := filepath.Clean(p.Path)
		// Check if cwd equals or is under the project path
		if cwd == projectPath || strings.HasPrefix(cwd, projectPath+string(filepath.Separator)) {
			return p, nil
		}
	}
	return nil, nil
}

// GetSetting returns a setting value.
func (db *DB) GetSetting(key string) (string, error) {
	var value string
	err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting: %w", err)
	}
	return value, nil
}

// SetSetting sets a setting value.
func (db *DB) SetSetting(key, value string) error {
	_, err := db.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = ?
	`, key, value, value)
	if err != nil {
		return fmt.Errorf("set setting: %w", err)
	}
	return nil
}

// GetAllSettings returns all settings as a map.
func (db *DB) GetAllSettings() (map[string]string, error) {
	rows, err := db.Query("SELECT key, value FROM settings")
	if err != nil {
		return nil, fmt.Errorf("query settings: %w", err)
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		settings[key] = value
	}
	return settings, nil
}

// GetLastTaskTypeForProject returns the last used task type for a project.
func (db *DB) GetLastTaskTypeForProject(project string) (string, error) {
	return db.GetSetting("last_type_" + project)
}

// SetLastTaskTypeForProject saves the last used task type for a project.
func (db *DB) SetLastTaskTypeForProject(project, taskType string) error {
	return db.SetSetting("last_type_"+project, taskType)
}

// GetLastUsedProject returns the last used project name.
func (db *DB) GetLastUsedProject() (string, error) {
	return db.GetSetting("last_used_project")
}

// SetLastUsedProject saves the last used project name.
func (db *DB) SetLastUsedProject(project string) error {
	return db.SetSetting("last_used_project", project)
}

// CreateTaskType creates a new task type.
func (db *DB) CreateTaskType(t *TaskType) error {
	result, err := db.Exec(`
		INSERT INTO task_types (name, label, instructions, sort_order, is_builtin)
		VALUES (?, ?, ?, ?, ?)
	`, t.Name, t.Label, t.Instructions, t.SortOrder, t.IsBuiltin)
	if err != nil {
		return fmt.Errorf("insert task type: %w", err)
	}
	id, _ := result.LastInsertId()
	t.ID = id
	return nil
}

// UpdateTaskType updates a task type.
func (db *DB) UpdateTaskType(t *TaskType) error {
	_, err := db.Exec(`
		UPDATE task_types SET name = ?, label = ?, instructions = ?, sort_order = ?
		WHERE id = ?
	`, t.Name, t.Label, t.Instructions, t.SortOrder, t.ID)
	if err != nil {
		return fmt.Errorf("update task type: %w", err)
	}
	return nil
}

// DeleteTaskType deletes a task type (only non-builtin types can be deleted).
func (db *DB) DeleteTaskType(id int64) error {
	// Check if it's a builtin type
	var isBuiltin bool
	err := db.QueryRow("SELECT is_builtin FROM task_types WHERE id = ?", id).Scan(&isBuiltin)
	if err != nil {
		return fmt.Errorf("get task type: %w", err)
	}
	if isBuiltin {
		return fmt.Errorf("cannot delete builtin task type")
	}

	_, err = db.Exec("DELETE FROM task_types WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete task type: %w", err)
	}
	return nil
}

// ListTaskTypes returns all task types ordered by sort_order.
func (db *DB) ListTaskTypes() ([]*TaskType, error) {
	rows, err := db.Query(`
		SELECT id, name, label, instructions, sort_order, is_builtin, created_at
		FROM task_types ORDER BY sort_order, name
	`)
	if err != nil {
		return nil, fmt.Errorf("query task types: %w", err)
	}
	defer rows.Close()

	var types []*TaskType
	for rows.Next() {
		t := &TaskType{}
		if err := rows.Scan(&t.ID, &t.Name, &t.Label, &t.Instructions, &t.SortOrder, &t.IsBuiltin, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan task type: %w", err)
		}
		types = append(types, t)
	}
	return types, nil
}

// GetTaskType retrieves a task type by ID.
func (db *DB) GetTaskType(id int64) (*TaskType, error) {
	t := &TaskType{}
	err := db.QueryRow(`
		SELECT id, name, label, instructions, sort_order, is_builtin, created_at
		FROM task_types WHERE id = ?
	`, id).Scan(&t.ID, &t.Name, &t.Label, &t.Instructions, &t.SortOrder, &t.IsBuiltin, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query task type: %w", err)
	}
	return t, nil
}

// GetTaskTypeByName retrieves a task type by name.
func (db *DB) GetTaskTypeByName(name string) (*TaskType, error) {
	t := &TaskType{}
	err := db.QueryRow(`
		SELECT id, name, label, instructions, sort_order, is_builtin, created_at
		FROM task_types WHERE name = ?
	`, name).Scan(&t.ID, &t.Name, &t.Label, &t.Instructions, &t.SortOrder, &t.IsBuiltin, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query task type: %w", err)
	}
	return t, nil
}

// ProjectMemory represents a stored learning/context for a project.
type ProjectMemory struct {
	ID           int64
	Project      string
	Category     string // "pattern", "context", "decision", "gotcha", etc.
	Content      string
	SourceTaskID *int64 // Task that generated this memory (optional)
	CreatedAt    LocalTime
	UpdatedAt    LocalTime
}

// Memory categories
const (
	MemoryCategoryPattern  = "pattern"  // Code patterns, conventions
	MemoryCategoryContext  = "context"  // Project-specific context
	MemoryCategoryDecision = "decision" // Architectural decisions
	MemoryCategoryGotcha   = "gotcha"   // Known pitfalls, workarounds
	MemoryCategoryGeneral  = "general"  // General learnings
)

// CreateMemory creates a new project memory.
func (db *DB) CreateMemory(m *ProjectMemory) error {
	result, err := db.Exec(`
		INSERT INTO project_memories (project, category, content, source_task_id)
		VALUES (?, ?, ?, ?)
	`, m.Project, m.Category, m.Content, m.SourceTaskID)
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}
	id, _ := result.LastInsertId()
	m.ID = id
	return nil
}

// UpdateMemory updates an existing memory.
func (db *DB) UpdateMemory(m *ProjectMemory) error {
	_, err := db.Exec(`
		UPDATE project_memories 
		SET content = ?, category = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, m.Content, m.Category, m.ID)
	if err != nil {
		return fmt.Errorf("update memory: %w", err)
	}
	return nil
}

// DeleteMemory deletes a memory by ID.
func (db *DB) DeleteMemory(id int64) error {
	_, err := db.Exec("DELETE FROM project_memories WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	return nil
}

// GetMemory retrieves a memory by ID.
func (db *DB) GetMemory(id int64) (*ProjectMemory, error) {
	m := &ProjectMemory{}
	err := db.QueryRow(`
		SELECT id, project, category, content, source_task_id, created_at, updated_at
		FROM project_memories WHERE id = ?
	`, id).Scan(&m.ID, &m.Project, &m.Category, &m.Content, &m.SourceTaskID, &m.CreatedAt, &m.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query memory: %w", err)
	}
	return m, nil
}

// ListMemoriesOptions defines options for listing memories.
type ListMemoriesOptions struct {
	Project  string
	Category string
	Limit    int
}

// ListMemories retrieves memories with optional filters.
func (db *DB) ListMemories(opts ListMemoriesOptions) ([]*ProjectMemory, error) {
	query := `
		SELECT id, project, category, content, source_task_id, created_at, updated_at
		FROM project_memories WHERE 1=1
	`
	args := []interface{}{}

	if opts.Project != "" {
		query += " AND project = ?"
		args = append(args, opts.Project)
	}
	if opts.Category != "" {
		query += " AND category = ?"
		args = append(args, opts.Category)
	}

	query += " ORDER BY updated_at DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	} else {
		query += " LIMIT 50"
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()

	var memories []*ProjectMemory
	for rows.Next() {
		m := &ProjectMemory{}
		if err := rows.Scan(&m.ID, &m.Project, &m.Category, &m.Content, &m.SourceTaskID, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, nil
}

// GetProjectMemories retrieves all memories for a project (for use in prompts).
func (db *DB) GetProjectMemories(project string, limit int) ([]*ProjectMemory, error) {
	if limit <= 0 {
		limit = 20
	}
	return db.ListMemories(ListMemoriesOptions{
		Project: project,
		Limit:   limit,
	})
}

// Attachment represents a file attached to a task.
type Attachment struct {
	ID        int64
	TaskID    int64
	Filename  string
	MimeType  string
	Size      int64
	Data      []byte
	CreatedAt LocalTime
}

// AddAttachment adds a file attachment to a task.
func (db *DB) AddAttachment(taskID int64, filename, mimeType string, data []byte) (*Attachment, error) {
	result, err := db.Exec(`
		INSERT INTO task_attachments (task_id, filename, mime_type, size, data)
		VALUES (?, ?, ?, ?, ?)
	`, taskID, filename, mimeType, len(data), data)
	if err != nil {
		return nil, fmt.Errorf("insert attachment: %w", err)
	}

	id, _ := result.LastInsertId()
	return &Attachment{
		ID:       id,
		TaskID:   taskID,
		Filename: filename,
		MimeType: mimeType,
		Size:     int64(len(data)),
		Data:     data,
	}, nil
}

// GetAttachment retrieves an attachment by ID.
func (db *DB) GetAttachment(id int64) (*Attachment, error) {
	a := &Attachment{}
	err := db.QueryRow(`
		SELECT id, task_id, filename, mime_type, size, data, created_at
		FROM task_attachments WHERE id = ?
	`, id).Scan(&a.ID, &a.TaskID, &a.Filename, &a.MimeType, &a.Size, &a.Data, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get attachment: %w", err)
	}
	return a, nil
}

// ListAttachments retrieves all attachments for a task (without data for efficiency).
func (db *DB) ListAttachments(taskID int64) ([]*Attachment, error) {
	rows, err := db.Query(`
		SELECT id, task_id, filename, mime_type, size, created_at
		FROM task_attachments WHERE task_id = ?
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	defer rows.Close()

	var attachments []*Attachment
	for rows.Next() {
		a := &Attachment{}
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Filename, &a.MimeType, &a.Size, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		attachments = append(attachments, a)
	}
	return attachments, nil
}

// DeleteAttachment removes an attachment.
func (db *DB) DeleteAttachment(id int64) error {
	_, err := db.Exec("DELETE FROM task_attachments WHERE id = ?", id)
	return err
}

// CountAttachments returns the number of attachments for a task.
func (db *DB) CountAttachments(taskID int64) (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM task_attachments WHERE task_id = ?", taskID).Scan(&count)
	return count, err
}

// ListAttachmentsWithData retrieves all attachments for a task including data.
func (db *DB) ListAttachmentsWithData(taskID int64) ([]*Attachment, error) {
	rows, err := db.Query(`
		SELECT id, task_id, filename, mime_type, size, data, created_at
		FROM task_attachments WHERE task_id = ?
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list attachments with data: %w", err)
	}
	defer rows.Close()

	var attachments []*Attachment
	for rows.Next() {
		a := &Attachment{}
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Filename, &a.MimeType, &a.Size, &a.Data, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		attachments = append(attachments, a)
	}
	return attachments, nil
}

// CompactionSummary represents a saved transcript from Claude compaction.
// When Claude compacts its context, we save the full transcript to our database
// so we can preserve the complete conversation history even after Claude's
// internal context is summarized.
type CompactionSummary struct {
	ID                 int64
	TaskID             int64
	SessionID          string
	Trigger            string // "manual" or "auto"
	PreTokens          int    // Estimated tokens before compaction
	Summary            string // Full JSONL transcript content (named "summary" for schema compatibility)
	CustomInstructions string // For manual /compact with custom instructions
	CreatedAt          LocalTime
}

// SaveCompactionSummary stores a compaction summary for a task.
func (db *DB) SaveCompactionSummary(summary *CompactionSummary) error {
	_, err := db.Exec(`
		INSERT INTO task_compaction_summaries
		(task_id, session_id, trigger, pre_tokens, summary, custom_instructions)
		VALUES (?, ?, ?, ?, ?, ?)
	`, summary.TaskID, summary.SessionID, summary.Trigger, summary.PreTokens, summary.Summary, summary.CustomInstructions)
	if err != nil {
		return fmt.Errorf("insert compaction summary: %w", err)
	}
	return nil
}

// GetCompactionSummaries retrieves all compaction summaries for a task.
func (db *DB) GetCompactionSummaries(taskID int64) ([]*CompactionSummary, error) {
	rows, err := db.Query(`
		SELECT id, task_id, session_id, trigger, pre_tokens, summary, custom_instructions, created_at
		FROM task_compaction_summaries
		WHERE task_id = ?
		ORDER BY id ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query compaction summaries: %w", err)
	}
	defer rows.Close()

	var summaries []*CompactionSummary
	for rows.Next() {
		s := &CompactionSummary{}
		err := rows.Scan(&s.ID, &s.TaskID, &s.SessionID, &s.Trigger, &s.PreTokens, &s.Summary, &s.CustomInstructions, &s.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan compaction summary: %w", err)
		}
		summaries = append(summaries, s)
	}
	return summaries, nil
}

// GetLatestCompactionSummary retrieves the most recent compaction summary for a task.
func (db *DB) GetLatestCompactionSummary(taskID int64) (*CompactionSummary, error) {
	s := &CompactionSummary{}
	err := db.QueryRow(`
		SELECT id, task_id, session_id, trigger, pre_tokens, summary, custom_instructions, created_at
		FROM task_compaction_summaries
		WHERE task_id = ?
		ORDER BY id DESC
		LIMIT 1
	`, taskID).Scan(&s.ID, &s.TaskID, &s.SessionID, &s.Trigger, &s.PreTokens, &s.Summary, &s.CustomInstructions, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query latest compaction summary: %w", err)
	}
	return s, nil
}

// ClearCompactionSummaries clears all compaction summaries for a task.
func (db *DB) ClearCompactionSummaries(taskID int64) error {
	_, err := db.Exec("DELETE FROM task_compaction_summaries WHERE task_id = ?", taskID)
	if err != nil {
		return fmt.Errorf("clear compaction summaries: %w", err)
	}
	return nil
}

// TaskSearchResult represents a search result from the FTS5 index.
type TaskSearchResult struct {
	TaskID            int64
	Project           string
	Title             string
	Body              string
	Tags              string
	TranscriptExcerpt string
	Rank              float64 // FTS5 rank score (lower is better match)
}

// IndexTaskForSearch adds or updates a task in the FTS5 search index.
// Call this after task completion to make it searchable.
func (db *DB) IndexTaskForSearch(taskID int64, project, title, body, tags, transcriptExcerpt string) error {
	// First delete any existing entry
	_, err := db.Exec(`DELETE FROM task_search WHERE task_id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("delete existing search entry: %w", err)
	}

	// Insert new entry
	_, err = db.Exec(`
		INSERT INTO task_search (task_id, project, title, body, tags, transcript_excerpt)
		VALUES (?, ?, ?, ?, ?, ?)
	`, taskID, project, title, body, tags, transcriptExcerpt)
	if err != nil {
		return fmt.Errorf("insert search entry: %w", err)
	}
	return nil
}

// RemoveTaskFromSearch removes a task from the FTS5 search index.
func (db *DB) RemoveTaskFromSearch(taskID int64) error {
	_, err := db.Exec(`DELETE FROM task_search WHERE task_id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("delete search entry: %w", err)
	}
	return nil
}

// FTSSearchOptions defines options for FTS5 full-text search.
type FTSSearchOptions struct {
	Query   string // The search query (FTS5 syntax supported)
	Project string // Optional: filter by project
	Limit   int    // Maximum results (default 10)
}

// SearchTasksFTS performs a full-text search on the FTS5 task index.
// Returns matching tasks ordered by relevance (best matches first).
// Note: This is different from SearchTasks which does simple LIKE matching.
func (db *DB) SearchTasksFTS(opts FTSSearchOptions) ([]*TaskSearchResult, error) {
	if opts.Query == "" {
		return nil, nil
	}
	if opts.Limit <= 0 {
		opts.Limit = 10
	}

	query := `
		SELECT task_id, project, title, body, tags, transcript_excerpt, rank
		FROM task_search
		WHERE task_search MATCH ?
	`
	args := []interface{}{opts.Query}

	if opts.Project != "" {
		query += ` AND project = ?`
		args = append(args, opts.Project)
	}

	query += ` ORDER BY rank LIMIT ?`
	args = append(args, opts.Limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("search tasks: %w", err)
	}
	defer rows.Close()

	var results []*TaskSearchResult
	for rows.Next() {
		r := &TaskSearchResult{}
		if err := rows.Scan(&r.TaskID, &r.Project, &r.Title, &r.Body, &r.Tags, &r.TranscriptExcerpt, &r.Rank); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		results = append(results, r)
	}
	return results, nil
}

// FindSimilarTasks searches for completed tasks similar to the given task.
// Uses the task's title, body, and tags to find relevant past work.
func (db *DB) FindSimilarTasks(task *Task, limit int) ([]*TaskSearchResult, error) {
	if limit <= 0 {
		limit = 5
	}

	// Build search query from task title and first part of body
	searchTerms := task.Title
	if task.Tags != "" {
		searchTerms += " " + strings.ReplaceAll(task.Tags, ",", " ")
	}
	// Add first 100 chars of body for context
	if len(task.Body) > 0 {
		bodySnippet := task.Body
		if len(bodySnippet) > 100 {
			bodySnippet = bodySnippet[:100]
		}
		searchTerms += " " + bodySnippet
	}

	// Escape special FTS5 characters and create OR query for flexibility
	words := strings.Fields(searchTerms)
	var escapedWords []string
	for _, w := range words {
		// Remove special characters that could break FTS5
		w = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, w)
		if len(w) >= 3 { // Only include words with 3+ chars
			escapedWords = append(escapedWords, w)
		}
	}

	if len(escapedWords) == 0 {
		return nil, nil
	}

	// Create FTS5 OR query
	ftsQuery := strings.Join(escapedWords, " OR ")

	return db.SearchTasksFTS(FTSSearchOptions{
		Query:   ftsQuery,
		Project: task.Project,
		Limit:   limit + 1, // Get one extra to exclude current task
	})
}

// GetTagsList returns all unique tags used across all tasks.
func (db *DB) GetTagsList() ([]string, error) {
	rows, err := db.Query(`SELECT DISTINCT tags FROM tasks WHERE tags != ''`)
	if err != nil {
		return nil, fmt.Errorf("query tags: %w", err)
	}
	defer rows.Close()

	tagSet := make(map[string]bool)
	for rows.Next() {
		var tags string
		if err := rows.Scan(&tags); err != nil {
			return nil, fmt.Errorf("scan tags: %w", err)
		}
		for _, tag := range strings.Split(tags, ",") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				tagSet[tag] = true
			}
		}
	}

	var result []string
	for tag := range tagSet {
		result = append(result, tag)
	}
	return result, nil
}
