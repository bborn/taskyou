// Package tasks provides access to the TaskYou database for sync operations.
package tasks

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the TaskYou database.
type DB struct {
	db *sql.DB
}

// Task represents a task from TaskYou.
type Task struct {
	ID          int64
	Title       string
	Body        string
	Status      string
	Project     string
	Type        string
	Tags        string
	CreatedAt   time.Time
	CompletedAt *time.Time
	Summary     string
	Logs        []LogEntry
}

// LogEntry represents a task log entry.
type LogEntry struct {
	Type    string
	Message string
	Time    time.Time
}

// ListOptions specifies options for listing tasks.
type ListOptions struct {
	Statuses  []string
	Project   string
	NotSynced bool
}

// SyncStats holds sync statistics.
type SyncStats struct {
	Total   int
	Synced  int
	Pending int
}

// Open opens the TaskYou database.
func Open(path string) (*DB, error) {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".local", "share", "taskyou", "tasks.db")
	}

	// Expand ~ in path
	if path[0] == '~' {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[1:])
	}

	// Check file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("database not found: %s", path)
	}

	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Ensure qmd_synced column exists (for tracking synced tasks)
	// We'll use a separate tracking file instead of modifying TaskYou's DB
	return &DB{db: db}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

// ListTasks returns tasks matching the options.
func (d *DB) ListTasks(opts ListOptions) ([]Task, error) {
	query := `
		SELECT id, title, body, status, project, type, tags, created_at, completed_at
		FROM tasks
		WHERE 1=1
	`
	args := []interface{}{}

	if len(opts.Statuses) > 0 {
		placeholders := ""
		for i, s := range opts.Statuses {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, s)
		}
		query += fmt.Sprintf(" AND status IN (%s)", placeholders)
	}

	if opts.Project != "" {
		query += " AND project = ?"
		args = append(args, opts.Project)
	}

	query += " ORDER BY completed_at DESC, id DESC"

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		var completedAt sql.NullTime

		if err := rows.Scan(&t.ID, &t.Title, &t.Body, &t.Status, &t.Project, &t.Type, &t.Tags, &t.CreatedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}

		if completedAt.Valid {
			t.CompletedAt = &completedAt.Time
		}

		tasks = append(tasks, t)
	}

	return tasks, nil
}

// GetTask returns a single task by ID.
func (d *DB) GetTask(id int64) (*Task, error) {
	var t Task
	var completedAt sql.NullTime

	err := d.db.QueryRow(`
		SELECT id, title, body, status, project, type, tags, created_at, completed_at
		FROM tasks WHERE id = ?
	`, id).Scan(&t.ID, &t.Title, &t.Body, &t.Status, &t.Project, &t.Type, &t.Tags, &t.CreatedAt, &completedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	if completedAt.Valid {
		t.CompletedAt = &completedAt.Time
	}

	return &t, nil
}

// GetTaskLogs returns logs for a task.
func (d *DB) GetTaskLogs(taskID int64, limit int) ([]LogEntry, error) {
	query := `
		SELECT type, message, created_at
		FROM task_logs
		WHERE task_id = ?
		ORDER BY created_at DESC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := d.db.Query(query, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to query logs: %w", err)
	}
	defer rows.Close()

	var logs []LogEntry
	for rows.Next() {
		var l LogEntry
		if err := rows.Scan(&l.Type, &l.Message, &l.Time); err != nil {
			return nil, fmt.Errorf("failed to scan log: %w", err)
		}
		logs = append(logs, l)
	}

	return logs, nil
}

// MarkSynced marks a task as synced to QMD.
// Note: Uses a separate tracking file to avoid modifying TaskYou's DB.
func (d *DB) MarkSynced(taskID int64) error {
	// For now, this is a no-op. In a full implementation, we'd track synced
	// tasks in a separate SQLite database: ~/.local/share/ty-qmd/sync.db
	return nil
}

// SyncStats returns sync statistics.
func (d *DB) SyncStats() (*SyncStats, error) {
	var total int
	err := d.db.QueryRow(`
		SELECT COUNT(*) FROM tasks WHERE status IN ('done', 'archived')
	`).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("failed to count tasks: %w", err)
	}

	// For now, assume nothing is synced (would check sync tracking DB)
	return &SyncStats{
		Total:   total,
		Synced:  0,
		Pending: total,
	}, nil
}
