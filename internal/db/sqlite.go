// Package db provides SQLite database operations.
package db

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// LocalTime wraps time.Time and converts from UTC to local timezone when scanning.
type LocalTime struct {
	time.Time
}

// Scan implements sql.Scanner, converting scanned time to local timezone.
func (lt *LocalTime) Scan(value interface{}) error {
	if value == nil {
		lt.Time = time.Time{}
		return nil
	}
	switch v := value.(type) {
	case time.Time:
		lt.Time = v.Local()
		return nil
	case string:
		// Parse common SQLite datetime formats
		formats := []string{
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05Z",
			"2006-01-02T15:04:05",
			"2006-01-02",
		}
		for _, format := range formats {
			if t, err := time.Parse(format, v); err == nil {
				lt.Time = t.Local()
				return nil
			}
		}
		return fmt.Errorf("cannot parse time string: %s", v)
	default:
		return fmt.Errorf("cannot scan type %T into LocalTime", value)
	}
}

// Value implements driver.Valuer for inserting LocalTime into database.
func (lt LocalTime) Value() (driver.Value, error) {
	if lt.Time.IsZero() {
		return nil, nil
	}
	return lt.Time.UTC(), nil
}

// DB wraps the SQLite database connection.
type DB struct {
	*sql.DB
}

// Open opens or creates a SQLite database at the given path.
func Open(path string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	// Add busy timeout to handle concurrent access from executor + UI
	dsn := path + "?_busy_timeout=5000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Enable WAL mode for better concurrent access
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	wrapped := &DB{db}

	// Run migrations
	if err := wrapped.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return wrapped, nil
}

// migrate runs database migrations.
func (db *DB) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			body TEXT DEFAULT '',
			status TEXT DEFAULT 'backlog',
			type TEXT DEFAULT '',
			project TEXT DEFAULT '',
			priority TEXT DEFAULT 'normal',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			completed_at DATETIME
		)`,

		`CREATE TABLE IF NOT EXISTS task_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			line_type TEXT DEFAULT 'output',
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			path TEXT NOT NULL,
			aliases TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,

		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project)`,
		`CREATE INDEX IF NOT EXISTS idx_task_logs_task_id ON task_logs(task_id)`,

		`CREATE TABLE IF NOT EXISTS project_memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT 'general',
			content TEXT NOT NULL,
			source_task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE INDEX IF NOT EXISTS idx_project_memories_project ON project_memories(project)`,
		`CREATE INDEX IF NOT EXISTS idx_project_memories_category ON project_memories(category)`,
	}

	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, m)
		}
	}

	// Run ALTER TABLE migrations separately (they may fail if column already exists)
	alterMigrations := []string{
		`ALTER TABLE projects ADD COLUMN instructions TEXT DEFAULT ''`,
	}

	for _, m := range alterMigrations {
		// Ignore "duplicate column" errors for idempotent migrations
		db.Exec(m)
	}

	// Migrate old status values to new simplified statuses
	statusMigrations := []string{
		`UPDATE tasks SET status = 'backlog' WHERE status IN ('pending', 'interrupted')`,
		`UPDATE tasks SET status = 'in_progress' WHERE status IN ('queued', 'triaging', 'processing')`,
		`UPDATE tasks SET status = 'done' WHERE status IN ('ready', 'closed')`,
	}

	for _, m := range statusMigrations {
		db.Exec(m)
	}

	return nil
}

// DefaultPath returns the default database path.
func DefaultPath() string {
	// Check for explicit path
	if p := os.Getenv("TASK_DB_PATH"); p != "" {
		return p
	}

	// Default to ~/.local/share/task/tasks.db
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "task", "tasks.db")
}
