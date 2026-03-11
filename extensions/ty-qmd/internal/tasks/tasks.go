// Package tasks provides read-only access to the TaskYou database for sync operations.
package tasks

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bborn/workflow/internal/db"
	_ "modernc.org/sqlite"
)

// DB wraps a read-only connection to the TaskYou database.
type DB struct {
	conn *sql.DB
}

// ListOptions specifies options for listing tasks.
type ListOptions struct {
	Statuses []string
	Project  string
}

// Open opens the TaskYou database in read-only mode.
func Open(path string) (*DB, error) {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".local", "share", "taskyou", "tasks.db")
	}

	// Expand ~ in path
	if len(path) > 0 && path[0] == '~' {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[1:])
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("database not found: %s", path)
	}

	conn, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	return &DB{conn: conn}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.conn.Close()
}

// ListTasks returns tasks matching the options.
func (d *DB) ListTasks(opts ListOptions) ([]*db.Task, error) {
	query := `
		SELECT id, title, body, status, project, type, tags, summary, created_at, completed_at
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

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*db.Task
	for rows.Next() {
		var t db.Task
		var completedAt sql.NullTime

		if err := rows.Scan(&t.ID, &t.Title, &t.Body, &t.Status, &t.Project, &t.Type, &t.Tags, &t.Summary, &t.CreatedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}

		if completedAt.Valid {
			lt := db.LocalTime{Time: completedAt.Time}
			t.CompletedAt = &lt
		}

		tasks = append(tasks, &t)
	}

	return tasks, nil
}

// GetTaskLogs returns logs for a task.
func (d *DB) GetTaskLogs(taskID int64, limit int) ([]*db.TaskLog, error) {
	query := `
		SELECT id, task_id, line_type, content, created_at
		FROM task_logs
		WHERE task_id = ?
		ORDER BY created_at DESC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := d.conn.Query(query, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to query logs: %w", err)
	}
	defer rows.Close()

	var logs []*db.TaskLog
	for rows.Next() {
		var l db.TaskLog
		if err := rows.Scan(&l.ID, &l.TaskID, &l.LineType, &l.Content, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan log: %w", err)
		}
		logs = append(logs, &l)
	}

	return logs, nil
}
