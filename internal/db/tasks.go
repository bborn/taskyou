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
	Executor        string // Task executor: "claude" (default), "codex", "gemini"
	WorktreePath    string
	BranchName      string
	Port            int    // Unique port for running the application in this task's worktree
	ClaudeSessionID string // Claude session ID for resuming conversations
	DaemonSession   string // tmux daemon session name (e.g., "task-daemon-12345")
	TmuxWindowID    string // tmux window ID (e.g., "@1234") for unique window identification
	ClaudePaneID    string // tmux pane ID (e.g., "%1234") for the Claude/executor pane
	ShellPaneID     string // tmux pane ID (e.g., "%1235") for the shell pane
	PRURL           string // Pull request URL (if associated with a PR)
	PRNumber        int    // Pull request number (if associated with a PR)
	DangerousMode   bool   // Whether task is running in dangerous mode (--dangerously-skip-permissions)
	Pinned          bool   // Whether the task is pinned to the top of its column
	Tags            string // Comma-separated tags for categorization (e.g., "customer-support,email,influence-kit")
	Summary         string // Distilled summary of what was accomplished (for search and context)
	CreatedAt       LocalTime
	UpdatedAt       LocalTime
	StartedAt       *LocalTime
	CompletedAt     *LocalTime
	// Distillation tracking
	LastDistilledAt *LocalTime // When task was last distilled for learnings
	// Multi-executor support
	ActiveSessionID int64 // Currently active executor session (FK to task_executor_sessions.id, 0 = use legacy fields)
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
	ExecutorClaude   = "claude"   // Claude Code CLI (default)
	ExecutorCodex    = "codex"    // OpenAI Codex CLI
	ExecutorGemini   = "gemini"   // Google Gemini CLI
	ExecutorOpenClaw = "openclaw" // OpenClaw AI assistant (https://openclaw.ai)
)

// DefaultExecutor returns the default executor if none is specified.
func DefaultExecutor() string {
	return ExecutorClaude
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

// ExecutorSession represents an executor session for a task.
// Multiple sessions can exist per task, allowing different executors to work on the same task.
type ExecutorSession struct {
	ID            int64
	TaskID        int64
	Executor      string     // "claude", "codex", "gemini", "openclaw"
	SessionID     string     // Executor-specific session ID (e.g., Claude session ID)
	DaemonSession string     // tmux daemon session name
	TmuxWindowID  string     // tmux window ID
	ClaudePaneID  string     // tmux pane ID for executor
	ShellPaneID   string     // tmux pane ID for shell
	Status        string     // "pending", "active", "completed", "failed"
	DangerousMode bool       // Whether running in dangerous mode
	CreatedAt     LocalTime
	StartedAt     *LocalTime
	CompletedAt   *LocalTime
}

// ExecutorSession statuses
const (
	SessionStatusPending   = "pending"   // Session created but not started
	SessionStatusActive    = "active"    // Session is currently running
	SessionStatusCompleted = "completed" // Session finished successfully
	SessionStatusFailed    = "failed"    // Session failed
)

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
		INSERT INTO tasks (title, body, status, type, project, executor)
		VALUES (?, ?, ?, ?, ?, ?)
	`, t.Title, t.Body, t.Status, t.Type, t.Project, t.Executor)
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

	// Save the last used executor for this project
	if t.Executor != "" {
		db.SetLastExecutorForProject(t.Project, t.Executor)
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
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''),
		       COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(pinned, 0), COALESCE(tags, ''), COALESCE(summary, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, COALESCE(active_session_id, 0)
		FROM tasks WHERE id = ?
	`, id).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
		&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
		&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
		&t.PRURL, &t.PRNumber,
		&t.DangerousMode, &t.Pinned, &t.Tags, &t.Summary,
		&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
		&t.LastDistilledAt, &t.ActiveSessionID,
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
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''),
		       COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(pinned, 0), COALESCE(tags, ''), COALESCE(summary, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, COALESCE(active_session_id, 0)
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

	// Pinning takes precedence, then sort done/blocked tasks by completed_at (most recently closed first)
	// and other tasks by created_at (newest first). Use id DESC as secondary sort for consistency.
	query += " ORDER BY pinned DESC, CASE WHEN status IN ('done', 'blocked') THEN completed_at ELSE created_at END DESC, id DESC"

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
			&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
			&t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Pinned, &t.Tags, &t.Summary,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.LastDistilledAt, &t.ActiveSessionID,
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
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''),
		       COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(pinned, 0), COALESCE(tags, ''), COALESCE(summary, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, COALESCE(active_session_id, 0)
		FROM tasks
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
		&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
		&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
		&t.PRURL, &t.PRNumber,
		&t.DangerousMode, &t.Pinned, &t.Tags, &t.Summary,
		&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
		&t.LastDistilledAt, &t.ActiveSessionID,
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
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''),
		       COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(pinned, 0), COALESCE(tags, ''), COALESCE(summary, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, COALESCE(active_session_id, 0)
		FROM tasks
		WHERE (
			title LIKE ? COLLATE NOCASE
			OR project LIKE ? COLLATE NOCASE
			OR CAST(id AS TEXT) LIKE ?
			OR CAST(pr_number AS TEXT) LIKE ?
			OR pr_url LIKE ? COLLATE NOCASE
		)
		ORDER BY pinned DESC, CASE WHEN status IN ('done', 'blocked') THEN completed_at ELSE created_at END DESC, id DESC
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
			&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
			&t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Pinned, &t.Tags, &t.Summary,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.LastDistilledAt, &t.ActiveSessionID,
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
			pinned = ?, tags = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, t.Title, t.Body, t.Status, t.Type, t.Project, t.Executor,
		t.WorktreePath, t.BranchName, t.Port, t.ClaudeSessionID,
		t.DaemonSession, t.PRURL, t.PRNumber, t.DangerousMode,
		t.Pinned, t.Tags, t.ID)
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

// UpdateTaskPinned updates only the pinned flag for a task.
func (db *DB) UpdateTaskPinned(taskID int64, pinned bool) error {
	_, err := db.Exec(`
		UPDATE tasks SET pinned = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, pinned, taskID)
	if err != nil {
		return fmt.Errorf("update task pinned: %w", err)
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

// UpdateTaskPaneIDs updates the tmux pane IDs for a task.
// This is used to track the unique pane IDs (e.g., "%1234") for reliable pane identification
// when joining/breaking panes between the daemon and the TUI.
func (db *DB) UpdateTaskPaneIDs(taskID int64, claudePaneID, shellPaneID string) error {
	_, err := db.Exec(`
		UPDATE tasks SET claude_pane_id = ?, shell_pane_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, claudePaneID, shellPaneID, taskID)
	if err != nil {
		return fmt.Errorf("update task pane ids: %w", err)
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
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''),
		       COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(pinned, 0), COALESCE(tags, ''), COALESCE(summary, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, COALESCE(active_session_id, 0)
		FROM tasks
		WHERE status = ?
		ORDER BY created_at ASC
		LIMIT 1
	`, StatusQueued).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
		&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
		&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
		&t.PRURL, &t.PRNumber,
		&t.DangerousMode, &t.Pinned, &t.Tags, &t.Summary,
		&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
		&t.LastDistilledAt, &t.ActiveSessionID,
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
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''),
		       COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(pinned, 0), COALESCE(tags, ''), COALESCE(summary, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, COALESCE(active_session_id, 0)
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
			&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
			&t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Pinned, &t.Tags, &t.Summary,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.LastDistilledAt, &t.ActiveSessionID,
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
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''),
		       COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(pinned, 0), COALESCE(tags, ''), COALESCE(summary, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, COALESCE(active_session_id, 0)
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
			&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
			&t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Pinned, &t.Tags, &t.Summary,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.LastDistilledAt, &t.ActiveSessionID,
		); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// TaskLog represents a log entry for a task.
type TaskLog struct {
	ID        int64
	TaskID    int64
	SessionID int64  // Executor session ID (0 = legacy, before multi-session support)
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
	ID              int64
	Name            string
	Path            string
	Aliases         string          // comma-separated
	Instructions    string          // project-specific instructions for AI
	Actions         []ProjectAction // actions triggered on task events (stored as JSON)
	Color           string          // hex color for display (e.g., "#61AFEF")
	ClaudeConfigDir string          // override CLAUDE_CONFIG_DIR for this project
	CreatedAt       LocalTime
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
		INSERT INTO projects (name, path, aliases, instructions, actions, color, claude_config_dir)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, p.Name, p.Path, p.Aliases, p.Instructions, string(actionsJSON), p.Color, p.ClaudeConfigDir)
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
		UPDATE projects SET name = ?, path = ?, aliases = ?, instructions = ?, actions = ?, color = ?, claude_config_dir = ?
		WHERE id = ?
	`, p.Name, p.Path, p.Aliases, p.Instructions, string(actionsJSON), p.Color, p.ClaudeConfigDir, p.ID)
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

// ListProjects returns all projects, with "personal" always first.
func (db *DB) ListProjects() ([]*Project, error) {
	rows, err := db.Query(`
		SELECT id, name, path, aliases, instructions, COALESCE(actions, '[]'), COALESCE(color, ''), COALESCE(claude_config_dir, ''), created_at
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
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &actionsJSON, &p.Color, &p.ClaudeConfigDir, &p.CreatedAt); err != nil {
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
		SELECT id, name, path, aliases, instructions, COALESCE(actions, '[]'), COALESCE(color, ''), COALESCE(claude_config_dir, ''), created_at
		FROM projects WHERE name = ?
	`, name).Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &actionsJSON, &p.Color, &p.ClaudeConfigDir, &p.CreatedAt)
	if err == nil {
		json.Unmarshal([]byte(actionsJSON), &p.Actions)
		return p, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("query project: %w", err)
	}

	// Try alias match
	rows, err := db.Query(`SELECT id, name, path, aliases, instructions, COALESCE(actions, '[]'), COALESCE(color, ''), COALESCE(claude_config_dir, ''), created_at FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &actionsJSON, &p.Color, &p.ClaudeConfigDir, &p.CreatedAt); err != nil {
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

// GetProjectContext returns the auto-generated context for a project.
// This context is cached exploration results that can be reused across tasks.
func (db *DB) GetProjectContext(projectName string) (string, error) {
	var context string
	err := db.QueryRow(`SELECT COALESCE(context, '') FROM projects WHERE name = ?`, projectName).Scan(&context)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get project context: %w", err)
	}
	return context, nil
}

// SetProjectContext saves auto-generated context for a project.
// This overwrites any existing context.
func (db *DB) SetProjectContext(projectName string, context string) error {
	result, err := db.Exec(`UPDATE projects SET context = ? WHERE name = ?`, context, projectName)
	if err != nil {
		return fmt.Errorf("set project context: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("project '%s' not found", projectName)
	}
	return nil
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

// GetLastExecutorForProject returns the last used executor for a project.
func (db *DB) GetLastExecutorForProject(project string) (string, error) {
	return db.GetSetting("last_executor_" + project)
}

// SetLastExecutorForProject saves the last used executor for a project.
func (db *DB) SetLastExecutorForProject(project, executor string) error {
	return db.SetSetting("last_executor_"+project, executor)
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

// UpdateTaskStartedAt updates the started_at timestamp for a task.
// This is primarily used for testing.
func (db *DB) UpdateTaskStartedAt(taskID int64, t time.Time) error {
	_, err := db.Exec(`
		UPDATE tasks SET started_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, LocalTime{Time: t}, taskID)
	if err != nil {
		return fmt.Errorf("update task started_at: %w", err)
	}
	return nil
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

// CreateExecutorSession creates a new executor session for a task.
func (db *DB) CreateExecutorSession(s *ExecutorSession) error {
	result, err := db.Exec(`
		INSERT INTO task_executor_sessions (task_id, executor, session_id, daemon_session, tmux_window_id,
		                                    claude_pane_id, shell_pane_id, status, dangerous_mode, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, s.TaskID, s.Executor, s.SessionID, s.DaemonSession, s.TmuxWindowID,
		s.ClaudePaneID, s.ShellPaneID, s.Status, s.DangerousMode, s.StartedAt)
	if err != nil {
		return fmt.Errorf("insert executor session: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}
	s.ID = id
	return nil
}

// GetExecutorSession retrieves an executor session by ID.
func (db *DB) GetExecutorSession(id int64) (*ExecutorSession, error) {
	s := &ExecutorSession{}
	err := db.QueryRow(`
		SELECT id, task_id, executor, COALESCE(session_id, ''), COALESCE(daemon_session, ''),
		       COALESCE(tmux_window_id, ''), COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       status, COALESCE(dangerous_mode, 0), created_at, started_at, completed_at
		FROM task_executor_sessions WHERE id = ?
	`, id).Scan(
		&s.ID, &s.TaskID, &s.Executor, &s.SessionID, &s.DaemonSession,
		&s.TmuxWindowID, &s.ClaudePaneID, &s.ShellPaneID,
		&s.Status, &s.DangerousMode, &s.CreatedAt, &s.StartedAt, &s.CompletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query executor session: %w", err)
	}
	return s, nil
}

// GetExecutorSessionsForTask retrieves all executor sessions for a task.
func (db *DB) GetExecutorSessionsForTask(taskID int64) ([]*ExecutorSession, error) {
	rows, err := db.Query(`
		SELECT id, task_id, executor, COALESCE(session_id, ''), COALESCE(daemon_session, ''),
		       COALESCE(tmux_window_id, ''), COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       status, COALESCE(dangerous_mode, 0), created_at, started_at, completed_at
		FROM task_executor_sessions WHERE task_id = ?
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query executor sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*ExecutorSession
	for rows.Next() {
		s := &ExecutorSession{}
		if err := rows.Scan(
			&s.ID, &s.TaskID, &s.Executor, &s.SessionID, &s.DaemonSession,
			&s.TmuxWindowID, &s.ClaudePaneID, &s.ShellPaneID,
			&s.Status, &s.DangerousMode, &s.CreatedAt, &s.StartedAt, &s.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan executor session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// GetActiveExecutorSession retrieves the active executor session for a task.
func (db *DB) GetActiveExecutorSession(taskID int64) (*ExecutorSession, error) {
	s := &ExecutorSession{}
	err := db.QueryRow(`
		SELECT id, task_id, executor, COALESCE(session_id, ''), COALESCE(daemon_session, ''),
		       COALESCE(tmux_window_id, ''), COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       status, COALESCE(dangerous_mode, 0), created_at, started_at, completed_at
		FROM task_executor_sessions
		WHERE task_id = ? AND status = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, taskID, SessionStatusActive).Scan(
		&s.ID, &s.TaskID, &s.Executor, &s.SessionID, &s.DaemonSession,
		&s.TmuxWindowID, &s.ClaudePaneID, &s.ShellPaneID,
		&s.Status, &s.DangerousMode, &s.CreatedAt, &s.StartedAt, &s.CompletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query active executor session: %w", err)
	}
	return s, nil
}

// UpdateExecutorSession updates an executor session.
func (db *DB) UpdateExecutorSession(s *ExecutorSession) error {
	_, err := db.Exec(`
		UPDATE task_executor_sessions SET
			executor = ?, session_id = ?, daemon_session = ?, tmux_window_id = ?,
			claude_pane_id = ?, shell_pane_id = ?, status = ?, dangerous_mode = ?,
			started_at = ?, completed_at = ?
		WHERE id = ?
	`, s.Executor, s.SessionID, s.DaemonSession, s.TmuxWindowID,
		s.ClaudePaneID, s.ShellPaneID, s.Status, s.DangerousMode,
		s.StartedAt, s.CompletedAt, s.ID)
	if err != nil {
		return fmt.Errorf("update executor session: %w", err)
	}
	return nil
}

// SetActiveExecutorSession sets the active executor session for a task.
func (db *DB) SetActiveExecutorSession(taskID int64, sessionID int64) error {
	_, err := db.Exec(`
		UPDATE tasks SET active_session_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
	`, sessionID, taskID)
	if err != nil {
		return fmt.Errorf("set active executor session: %w", err)
	}
	return nil
}

// DeleteExecutorSession deletes an executor session.
func (db *DB) DeleteExecutorSession(id int64) error {
	_, err := db.Exec("DELETE FROM task_executor_sessions WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete executor session: %w", err)
	}
	return nil
}

// AppendTaskLogForSession appends a log entry to a task for a specific session.
func (db *DB) AppendTaskLogForSession(taskID int64, sessionID int64, lineType, content string) error {
	_, err := db.Exec(`
		INSERT INTO task_logs (task_id, session_id, line_type, content)
		VALUES (?, ?, ?, ?)
	`, taskID, sessionID, lineType, content)
	if err != nil {
		return fmt.Errorf("insert task log for session: %w", err)
	}
	return nil
}

// GetTaskLogsForSession retrieves logs for a task filtered by session.
// If sessionID is 0, returns all logs (legacy behavior).
// Otherwise, returns logs for the specific session plus any legacy logs (session_id = 0).
func (db *DB) GetTaskLogsForSession(taskID int64, sessionID int64, limit int) ([]*TaskLog, error) {
	if limit <= 0 {
		limit = 1000
	}

	rows, err := db.Query(`
		SELECT id, task_id, COALESCE(session_id, 0), line_type, content, created_at
		FROM task_logs
		WHERE task_id = ? AND (session_id = ? OR session_id = 0 OR session_id IS NULL)
		ORDER BY id DESC
		LIMIT ?
	`, taskID, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("query task logs for session: %w", err)
	}
	defer rows.Close()

	var logs []*TaskLog
	for rows.Next() {
		l := &TaskLog{}
		err := rows.Scan(&l.ID, &l.TaskID, &l.SessionID, &l.LineType, &l.Content, &l.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan task log: %w", err)
		}
		logs = append(logs, l)
	}

	return logs, nil
}
