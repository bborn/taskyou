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
	path string
}

// Path returns the path to the database file.
func (db *DB) Path() string {
	return db.path
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

	wrapped := &DB{DB: db, path: path}

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

		`CREATE TABLE IF NOT EXISTS task_attachments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			filename TEXT NOT NULL,
			mime_type TEXT DEFAULT '',
			size INTEGER DEFAULT 0,
			data BLOB NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE INDEX IF NOT EXISTS idx_task_attachments_task_id ON task_attachments(task_id)`,

		`CREATE TABLE IF NOT EXISTS task_types (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			label TEXT NOT NULL,
			instructions TEXT DEFAULT '',
			sort_order INTEGER DEFAULT 0,
			is_builtin INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// Compaction summaries table - stores context summaries when Claude compacts
		`CREATE TABLE IF NOT EXISTS task_compaction_summaries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			session_id TEXT NOT NULL,
			trigger TEXT NOT NULL,
			pre_tokens INTEGER DEFAULT 0,
			summary TEXT NOT NULL,
			custom_instructions TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE INDEX IF NOT EXISTS idx_task_compaction_summaries_task_id ON task_compaction_summaries(task_id)`,

		// FTS5 virtual table for full-text search on tasks
		// This enables searching task title, body, tags, and transcript excerpts
		// Uses standalone table (not content-sync) for flexibility
		`CREATE VIRTUAL TABLE IF NOT EXISTS task_search USING fts5(
			task_id UNINDEXED,
			project,
			title,
			body,
			tags,
			transcript_excerpt,
			tokenize='porter unicode61'
		)`,
	}

	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, m)
		}
	}

	// Run ALTER TABLE migrations separately (they may fail if column already exists)
	alterMigrations := []string{
		`ALTER TABLE projects ADD COLUMN instructions TEXT DEFAULT ''`,
		`ALTER TABLE projects ADD COLUMN actions TEXT DEFAULT '[]'`,
		`ALTER TABLE tasks ADD COLUMN worktree_path TEXT DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN branch_name TEXT DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN port INTEGER DEFAULT 0`,
		// Scheduled task columns
		`ALTER TABLE tasks ADD COLUMN scheduled_at DATETIME`,      // When to next run (null = not scheduled)
		`ALTER TABLE tasks ADD COLUMN recurrence TEXT DEFAULT ''`, // Recurrence pattern (empty = one-time)
		`ALTER TABLE tasks ADD COLUMN last_run_at DATETIME`,       // When last executed (for recurring tasks)
		// Claude session tracking
		`ALTER TABLE tasks ADD COLUMN claude_session_id TEXT DEFAULT ''`, // Claude session ID for resuming conversations
		// Project color column
		`ALTER TABLE projects ADD COLUMN color TEXT DEFAULT ''`, // Hex color for project label (e.g., "#61AFEF")
		// PR tracking columns
		`ALTER TABLE tasks ADD COLUMN pr_url TEXT DEFAULT ''`,      // Pull request URL (if associated with a PR)
		`ALTER TABLE tasks ADD COLUMN pr_number INTEGER DEFAULT 0`, // Pull request number (if associated with a PR)
		// Dangerous mode tracking
		`ALTER TABLE tasks ADD COLUMN dangerous_mode INTEGER DEFAULT 0`, // Whether running with --dangerously-skip-permissions
		// Daemon session tracking for process management
		`ALTER TABLE tasks ADD COLUMN daemon_session TEXT DEFAULT ''`, // tmux daemon session name for killing Claude
		// Task tagging for categorization and search
		`ALTER TABLE tasks ADD COLUMN tags TEXT DEFAULT ''`, // comma-separated tags for categorization (e.g., "customer-support,email,influence-kit")
		// Task executor - which CLI to use for task execution
		`ALTER TABLE tasks ADD COLUMN executor TEXT DEFAULT 'claude'`, // Task executor: "claude" (default), "codex"
		// Tmux window ID for unique window identification (avoids duplicate window issues)
		`ALTER TABLE tasks ADD COLUMN tmux_window_id TEXT DEFAULT ''`, // tmux window ID (e.g., "@1234")
		// Tmux pane IDs for deterministic pane identification (avoids index-based guessing)
		`ALTER TABLE tasks ADD COLUMN claude_pane_id TEXT DEFAULT ''`, // tmux pane ID for Claude/executor pane (e.g., "%1234")
		`ALTER TABLE tasks ADD COLUMN shell_pane_id TEXT DEFAULT ''`,  // tmux pane ID for shell pane (e.g., "%1235")
		// Merge conflict resolution tracking - stores commit SHA when we last attempted to resolve conflicts
		// This prevents infinite loops by only attempting resolution once per commit state
		`ALTER TABLE tasks ADD COLUMN last_conflict_resolve_sha TEXT DEFAULT ''`,
	}

	for _, m := range alterMigrations {
		// Ignore "duplicate column" errors for idempotent migrations
		db.Exec(m)
	}

	// Note: SQLite doesn't support ALTER COLUMN DEFAULT directly
	// The default value change for project column will be handled in the application layer
	// New tasks will get 'personal' as default through the form and executor logic

	// Migrate old status values to new statuses
	statusMigrations := []string{
		`UPDATE tasks SET status = 'backlog' WHERE status IN ('pending', 'interrupted')`,
		`UPDATE tasks SET status = 'queued' WHERE status = 'in_progress'`,
		`UPDATE tasks SET status = 'done' WHERE status IN ('ready', 'closed')`,
	}

	for _, m := range statusMigrations {
		db.Exec(m)
	}

	// Ensure 'personal' project exists
	if err := db.ensurePersonalProject(); err != nil {
		return fmt.Errorf("ensure personal project: %w", err)
	}

	// Migrate tasks with empty project to 'personal'
	db.Exec(`UPDATE tasks SET project = 'personal' WHERE project = ''`)

	// Drop priority column if it exists (SQLite 3.35.0+ supports DROP COLUMN)
	db.Exec(`ALTER TABLE tasks DROP COLUMN priority`)

	// Ensure default task types exist
	if err := db.ensureDefaultTaskTypes(); err != nil {
		return fmt.Errorf("ensure default task types: %w", err)
	}

	// Assign default colors to projects without colors
	if err := db.ensureProjectColors(); err != nil {
		return fmt.Errorf("ensure project colors: %w", err)
	}

	return nil
}

// ensurePersonalProject creates the 'personal' project if it doesn't exist.
// It also creates and initializes the default worktree directory as a git repo.
func (db *DB) ensurePersonalProject() error {
	// Check if personal project already exists
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE name = 'personal'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check personal project: %w", err)
	}

	if count > 0 {
		return nil // Already exists
	}

	// Create personal project directory
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	personalDir := filepath.Join(home, ".local", "share", "task", "personal")

	// Create directory
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		return fmt.Errorf("create personal dir: %w", err)
	}

	// Initialize as git repo if not already
	gitDir := filepath.Join(personalDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		// Initialize git repo
		if err := initGitRepo(personalDir); err != nil {
			return fmt.Errorf("init git repo: %w", err)
		}
	}

	// Create the personal project in database
	_, err = db.Exec(`
		INSERT INTO projects (name, path, aliases, instructions)
		VALUES ('personal', ?, '', 'Default project for personal tasks')
	`, personalDir)
	if err != nil {
		return fmt.Errorf("insert personal project: %w", err)
	}

	return nil
}

// initGitRepo initializes a git repository at the given path.
func initGitRepo(path string) error {
	// Create .git directory
	gitDir := filepath.Join(path, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		return fmt.Errorf("create .git dir: %w", err)
	}

	// Write minimal git config
	configPath := filepath.Join(gitDir, "config")
	config := `[core]
	repositoryformatversion = 0
	filemode = true
	bare = false
	logallrefupdates = true
[init]
	defaultBranch = main
`
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("write git config: %w", err)
	}

	// Create HEAD
	headPath := filepath.Join(gitDir, "HEAD")
	if err := os.WriteFile(headPath, []byte("ref: refs/heads/main\n"), 0644); err != nil {
		return fmt.Errorf("write HEAD: %w", err)
	}

	// Create objects and refs directories
	if err := os.MkdirAll(filepath.Join(gitDir, "objects"), 0755); err != nil {
		return fmt.Errorf("create objects dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0755); err != nil {
		return fmt.Errorf("create refs dir: %w", err)
	}

	// Create initial README
	readmePath := filepath.Join(path, "README.md")
	readme := `# Personal Tasks

This is the default workspace for personal tasks.
`
	if err := os.WriteFile(readmePath, []byte(readme), 0644); err != nil {
		return fmt.Errorf("write README: %w", err)
	}

	return nil
}

// DefaultProjectColors is a palette of distinct colors for projects.
// These are assigned to projects that don't have a color set.
var DefaultProjectColors = []string{
	"#C678DD", // Purple
	"#61AFEF", // Blue
	"#56B6C2", // Cyan
	"#98C379", // Green
	"#E5C07B", // Yellow
	"#E06C75", // Red/Pink
	"#D19A66", // Orange
	"#ABB2BF", // Gray
}

// ensureProjectColors assigns default colors to projects that don't have colors.
func (db *DB) ensureProjectColors() error {
	// Get all projects without colors
	rows, err := db.Query(`SELECT id, name FROM projects WHERE color = '' OR color IS NULL ORDER BY id`)
	if err != nil {
		return fmt.Errorf("query projects without colors: %w", err)
	}
	defer rows.Close()

	var projects []struct {
		ID   int64
		Name string
	}
	for rows.Next() {
		var p struct {
			ID   int64
			Name string
		}
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, p)
	}

	// Assign colors to projects
	for i, p := range projects {
		color := DefaultProjectColors[i%len(DefaultProjectColors)]
		_, err := db.Exec(`UPDATE projects SET color = ? WHERE id = ?`, color, p.ID)
		if err != nil {
			return fmt.Errorf("update project color: %w", err)
		}
	}

	return nil
}

// ensureDefaultTaskTypes creates the default task types if they don't exist.
func (db *DB) ensureDefaultTaskTypes() error {
	// Check if task types already exist
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM task_types`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check task types: %w", err)
	}

	if count > 0 {
		return nil // Types already exist
	}

	// Default task type instructions using template placeholders
	defaults := []struct {
		Name         string
		Label        string
		Instructions string
		SortOrder    int
	}{
		{
			Name:  "code",
			Label: "Code",
			Instructions: `You are working on: {{project}}

{{project_instructions}}

{{memories}}

Task: {{title}}

{{body}}

{{attachments}}

{{history}}

Instructions:
- Explore the codebase to understand the context
- Implement the solution
- Write tests if applicable
- Commit your changes with clear messages
- Submit a pull request when your work is complete

IMPORTANT: Your objective is to submit a PR to complete this task. Always remember to create and submit a pull request as the final step of your work. This is how you signal that the implementation is ready for review and merging.

When finished, provide a summary of what you did:
- List files changed/created
- Describe the key changes made
- Include any relevant links (PRs, commits, etc.)
- Note any follow-up items or concerns`,
			SortOrder: 1,
		},
		{
			Name:  "writing",
			Label: "Writing",
			Instructions: `You are a skilled writer. Please complete this task:

{{project_instructions}}

{{memories}}

Task: {{title}}

Details: {{body}}

{{attachments}}

{{history}}

Write the requested content. Be professional, clear, and match the appropriate tone.
Output the final content, then summarize what you created.`,
			SortOrder: 2,
		},
		{
			Name:  "thinking",
			Label: "Thinking",
			Instructions: `You are a strategic advisor. Analyze this thoroughly:

{{project_instructions}}

{{memories}}

Question: {{title}}

Context: {{body}}

{{attachments}}

{{history}}

Provide:
1. Clear analysis of the question/problem
2. Key considerations and tradeoffs
3. Recommended approach
4. Concrete next steps

Think deeply but be actionable. Summarize your conclusions clearly.`,
			SortOrder: 3,
		},
	}

	for _, d := range defaults {
		_, err := db.Exec(`
			INSERT INTO task_types (name, label, instructions, sort_order, is_builtin)
			VALUES (?, ?, ?, ?, 1)
		`, d.Name, d.Label, d.Instructions, d.SortOrder)
		if err != nil {
			return fmt.Errorf("insert task type %s: %w", d.Name, err)
		}
	}

	return nil
}

// DefaultPath returns the default database path.
func DefaultPath() string {
	// Check for explicit path
	if p := os.Getenv("WORKTREE_DB_PATH"); p != "" {
		return p
	}

	// Default to ~/.local/share/task/tasks.db
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "task", "tasks.db")
}
