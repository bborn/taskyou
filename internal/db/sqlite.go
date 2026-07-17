// Package db provides SQLite database operations.
package db

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	path         string
	eventEmitter EventEmitter
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

	// Pass PRAGMAs via DSN so they are applied to EVERY new connection.
	// This is critical because SetConnMaxLifetime causes periodic connection
	// recycling, and PRAGMAs set via Exec() only apply to the initial
	// connection — new connections from the pool would lack busy_timeout
	// and fail immediately with SQLITE_BUSY under contention.
	// Note: the bare _busy_timeout DSN param does NOT work with
	// modernc.org/sqlite, but _pragma=busy_timeout(N) does.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Use a single connection to ensure PRAGMAs apply consistently.
	// SQLite only supports one writer at a time, so multiple connections
	// just create contention. A single connection with busy_timeout
	// handles concurrent goroutines via database/sql's built-in serialization.
	db.SetMaxOpenConns(1)

	// Recycle connections periodically so the next query opens a fresh
	// connection that re-reads the WAL index from the shared-memory file.
	// modernc.org/sqlite (pure-Go) uses file I/O instead of mmap for the
	// WAL shared-memory, so a long-lived connection may cache a stale WAL
	// index and miss writes from other processes (e.g., CLI commands).
	// PRAGMAs are re-applied automatically via the DSN _pragma parameters.
	db.SetConnMaxLifetime(2 * time.Second)

	wrapped := &DB{DB: db, path: path}

	// Run migrations
	if err := wrapped.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return wrapped, nil
}

// permModeAutoMigrationKey guards the one-time rewrite of legacy "auto"
// permission_mode rows (which meant acceptEdits) to "accept-edits", now that
// "auto" denotes Claude Code's real auto mode.
const permModeAutoMigrationKey = "migration:auto_means_accept_edits_v1"

// modelClaudeSlugMigrationKey guards the one-time repair of tasks whose model
// override is the literal "claude". That value is the executor slug, never a
// valid Claude model alias: an early version of the model column shipped with
// `DEFAULT 'claude'` (removed in a later release) and CreateTask did not write
// the column for months, so every task inserted in that window fell through to
// that default. Left in place it launches `claude --model claude`, which the CLI
// rejects. Rewrite those rows to "" (no override / Claude's global default).
const modelClaudeSlugMigrationKey = "migration:clear_model_claude_slug_v1"

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

		`CREATE TABLE IF NOT EXISTS event_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			task_id INTEGER,
			message TEXT DEFAULT '',
			metadata TEXT DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE INDEX IF NOT EXISTS idx_event_log_task_id ON event_log(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_event_log_event_type ON event_log(event_type)`,
		`CREATE INDEX IF NOT EXISTS idx_event_log_created_at ON event_log(created_at)`,

		// Task dependencies table for blocking/blocked relationships
		`CREATE TABLE IF NOT EXISTS task_dependencies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			blocker_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			blocked_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			auto_queue INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(blocker_id, blocked_id),
			CHECK(blocker_id != blocked_id)
		)`,

		`CREATE INDEX IF NOT EXISTS idx_task_dependencies_blocker ON task_dependencies(blocker_id)`,
		`CREATE INDEX IF NOT EXISTS idx_task_dependencies_blocked ON task_dependencies(blocked_id)`,

		// Composite index for efficient conversation history queries
		// Used by GetConversationHistoryLogs and HasContinuationMarker
		`CREATE INDEX IF NOT EXISTS idx_task_logs_task_line_type ON task_logs(task_id, line_type)`,

		// Routine run history (routine definitions live on disk under
		// ~/.config/task/routines; see internal/routine)
		`CREATE TABLE IF NOT EXISTS routine_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			routine TEXT NOT NULL,
			status TEXT DEFAULT 'running',
			exit_code INTEGER DEFAULT 0,
			output TEXT DEFAULT '',
			log_path TEXT DEFAULT '',
			started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			finished_at DATETIME
		)`,

		`CREATE INDEX IF NOT EXISTS idx_routine_runs_routine ON routine_runs(routine, id)`,

		// Pipeline artifacts: inter-phase document hand-off for workflows (e.g. the
		// rpi workflow). Keyed by (branch, name) so a document phase can write a full
		// markdown document that a later phase on the same shared branch reads back,
		// without committing docs to git. Modeled on projects.context, but that is a
		// 1:1 column and cannot carry a (branch, phase) key — hence a dedicated table.
		`CREATE TABLE IF NOT EXISTS pipeline_artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			branch TEXT NOT NULL,
			name   TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(branch, name)
		)`,

		`CREATE INDEX IF NOT EXISTS idx_pipeline_artifacts_branch ON pipeline_artifacts(branch)`,

		// Pipeline step verify: the opt-in evidence-gate command for a workflow step,
		// keyed by the step's task id. A workflow YAML step can set `verify: <command>`;
		// when that step's agent calls taskyou_complete the daemon runs the command in
		// the worktree and rejects the completion on a non-zero exit. Kept in its own
		// task-keyed table (not a tasks column) because a shell command can't ride a
		// tag token the way the boolean `gate` marker does, and to avoid touching the
		// hand-duplicated tasks SELECT/Scan sites.
		`CREATE TABLE IF NOT EXISTS pipeline_step_verify (
			task_id INTEGER PRIMARY KEY,
			command TEXT NOT NULL DEFAULT ''
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
		`ALTER TABLE tasks ADD COLUMN recurrence TEXT DEFAULT ''`, // Deprecated recurrence pattern (empty = one-time)
		`ALTER TABLE tasks ADD COLUMN last_run_at DATETIME`,       // When last executed (for scheduled tasks)
		// Claude session tracking
		`ALTER TABLE tasks ADD COLUMN claude_session_id TEXT DEFAULT ''`, // Claude session ID for resuming conversations
		// Project color column
		`ALTER TABLE projects ADD COLUMN color TEXT DEFAULT ''`,             // Hex color for project label (e.g., "#61AFEF")
		`ALTER TABLE projects ADD COLUMN claude_config_dir TEXT DEFAULT ''`, // Per-project CLAUDE_CONFIG_DIR override
		// PR tracking columns
		`ALTER TABLE tasks ADD COLUMN pr_url TEXT DEFAULT ''`,      // Pull request URL (if associated with a PR)
		`ALTER TABLE tasks ADD COLUMN pr_number INTEGER DEFAULT 0`, // Pull request number (if associated with a PR)
		// Dangerous mode tracking
		`ALTER TABLE tasks ADD COLUMN dangerous_mode INTEGER DEFAULT 0`, // Whether running with --dangerously-skip-permissions
		// Task pinning
		`ALTER TABLE tasks ADD COLUMN pinned INTEGER DEFAULT 0`, // Whether task is pinned to top of column
		// Daemon session tracking for process management
		`ALTER TABLE tasks ADD COLUMN daemon_session TEXT DEFAULT ''`, // tmux daemon session name for killing Claude
		// Task tagging for categorization and search
		`ALTER TABLE tasks ADD COLUMN tags TEXT DEFAULT ''`, // comma-separated tags for categorization (e.g., "customer-support,email,influence-kit")
		// Task executor - which CLI to use for task execution
		`ALTER TABLE tasks ADD COLUMN executor TEXT DEFAULT 'claude'`, // Task executor: "claude" (default), "codex"
		// Tmux window ID for unique window identification (avoids duplicate window issues)
		`ALTER TABLE tasks ADD COLUMN tmux_window_id TEXT DEFAULT ''`, // tmux window ID (e.g., "@1234")
		// Distilled task summary for search indexing and context
		`ALTER TABLE tasks ADD COLUMN summary TEXT DEFAULT ''`, // Distilled summary of what was accomplished
		// Last distillation timestamp for tracking when to re-distill
		`ALTER TABLE tasks ADD COLUMN last_distilled_at DATETIME`, // When task was last distilled
		// Tmux pane IDs for deterministic pane identification (avoids index-based guessing)
		`ALTER TABLE tasks ADD COLUMN claude_pane_id TEXT DEFAULT ''`, // tmux pane ID for Claude/executor pane (e.g., "%1234")
		`ALTER TABLE tasks ADD COLUMN shell_pane_id TEXT DEFAULT ''`,  // tmux pane ID for shell pane (e.g., "%1235")
		// Auto-generated project context for caching exploration results
		`ALTER TABLE projects ADD COLUMN context TEXT DEFAULT ''`, // Auto-generated project context (codebase summary, patterns, etc.)
		// Last accessed timestamp for tracking recently visited tasks in command palette
		`ALTER TABLE tasks ADD COLUMN last_accessed_at DATETIME`, // When task was last accessed/opened in UI
		// Archive state columns for preserving worktree state when archiving
		`ALTER TABLE tasks ADD COLUMN archive_ref TEXT DEFAULT ''`,           // Git ref storing stashed changes (e.g., "refs/task-archive/123")
		`ALTER TABLE tasks ADD COLUMN archive_commit TEXT DEFAULT ''`,        // Commit hash at time of archiving
		`ALTER TABLE tasks ADD COLUMN archive_worktree_path TEXT DEFAULT ''`, // Original worktree path before archiving
		`ALTER TABLE tasks ADD COLUMN archive_branch_name TEXT DEFAULT ''`,   // Original branch name before archiving
		// Source branch for checking out existing branches in worktrees (e.g., for QA deployments)
		`ALTER TABLE tasks ADD COLUMN source_branch TEXT DEFAULT ''`, // Existing branch to checkout instead of creating new branch
		// Cached PR state as JSON for instant display on startup (avoids waiting for GitHub API)
		`ALTER TABLE tasks ADD COLUMN pr_info_json TEXT DEFAULT ''`, // JSON blob of github.PRInfo
		// Whether project uses git worktrees for task isolation (1=yes, 0=no, default 1 for backward compat)
		`ALTER TABLE projects ADD COLUMN use_worktrees INTEGER DEFAULT 1`,
		// Permission mode for task execution: "default" (prompt), "accept-edits" (acceptEdits), "auto" (Claude Code auto mode), "dangerous" (skip permissions)
		`ALTER TABLE tasks ADD COLUMN permission_mode TEXT DEFAULT ''`,
		// Per-project default permission mode inherited by new tasks (empty = global default)
		`ALTER TABLE projects ADD COLUMN default_permission_mode TEXT DEFAULT ''`,
		// Per-task Claude effort override ("" = use global/Claude default, otherwise low/medium/high/xhigh/max)
		`ALTER TABLE tasks ADD COLUMN effort_level TEXT DEFAULT ''`,

		`ALTER TABLE tasks ADD COLUMN remote_control INTEGER DEFAULT 0`, // Whether to launch claude with --remote-control

		// Per-task Claude model override ("" = use global/Claude default, otherwise an alias like opus/sonnet/haiku or a full model name)
		`ALTER TABLE tasks ADD COLUMN model TEXT DEFAULT ''`,

		// Per-task CLAUDE_CONFIG_DIR override ("" = use the project's/default config dir).
		// Lets a single task (e.g. a workflow step) route through a different Claude config —
		// e.g. an ollama-backed config — without changing the project's setting.
		`ALTER TABLE tasks ADD COLUMN claude_config_dir TEXT DEFAULT ''`,

		// Per-task env overrides for the spawned Claude, stored as a JSON object
		// (see Task.EnvJSON / EnvMap). Injected as a process-env prefix on the
		// claude command so a step can route through a non-Anthropic proxy (ollama)
		// without swapping CLAUDE_CONFIG_DIR.
		`ALTER TABLE tasks ADD COLUMN env TEXT DEFAULT ''`,
		// The commit a task's worktree was created at. A workflow step that has done no
		// work still sits at this commit, so it is what distinguishes "hasn't produced
		// anything" from "finished" — without it the auto-complete sweep cannot tell a
		// freshly-created worktree (clean, HEAD already on origin) from a completed step
		// and will silently mark unstarted steps done.
		`ALTER TABLE tasks ADD COLUMN base_commit TEXT DEFAULT ''`,
		// The paths already dirty in the worktree when the step started. A project's
		// worktree init (bundle, migrate) routinely rewrites tracked files — Rails'
		// db/structure.sql is the classic — so "worktree is clean" is never true and can
		// never be the completion signal. What matters is that the step left nothing
		// uncommitted that wasn't already dirty before it ran.
		`ALTER TABLE tasks ADD COLUMN base_dirty TEXT DEFAULT ''`,
		// Soft-delete timestamp. NULL = live task. Non-NULL = trashed (hidden from
		// the board, still fully recoverable via 'task restore') until the daemon
		// trash sweep hard-deletes it after the configured retention. Deleting a task
		// no longer destroys its worktree or Claude transcript up front — that only
		// happens when the sweep fires, and even then the transcript is preserved.
		`ALTER TABLE tasks ADD COLUMN deleted_at DATETIME`,
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

	// One-time: the value "auto" historically meant Claude's acceptEdits mode.
	// It now means Claude Code's real auto mode, so rewrite pre-existing rows to
	// the explicit "accept-edits" value to preserve their original behavior.
	// Guarded by a settings flag so it runs exactly once and never clobbers tasks
	// a user later sets to the new auto mode.
	if done, _ := db.GetSetting(permModeAutoMigrationKey); done == "" {
		db.Exec(`UPDATE tasks SET permission_mode = 'accept-edits' WHERE permission_mode = 'auto'`)
		db.Exec(`UPDATE projects SET default_permission_mode = 'accept-edits' WHERE default_permission_mode = 'auto'`)
		db.SetSetting(permModeAutoMigrationKey, "done")
	}

	// One-time: clear the invalid "claude" model override left by an early
	// `DEFAULT 'claude'` on the model column (see modelClaudeSlugMigrationKey).
	// "claude" is the executor slug, not a model alias, so no task should carry
	// it; rewriting to "" restores Claude's global default.
	if done, _ := db.GetSetting(modelClaudeSlugMigrationKey); done == "" {
		db.Exec(`UPDATE tasks SET model = '' WHERE model = 'claude'`)
		db.SetSetting(modelClaudeSlugMigrationKey, "done")
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

	// Resolve any task project aliases to canonical project names
	if err := db.migrateProjectAliases(); err != nil {
		return fmt.Errorf("migrate project aliases: %w", err)
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
		INSERT INTO projects (name, path, aliases, instructions, claude_config_dir)
		VALUES ('personal', ?, '', 'Default project for personal tasks', '')
	`, personalDir)
	if err != nil {
		return fmt.Errorf("insert personal project: %w", err)
	}

	return nil
}

// initGitRepo initializes a git repository at the given path with an initial commit.
// The initial commit is required for worktree support to work properly.
func initGitRepo(path string) error {
	// Create directory if needed
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	// Create initial README before git init
	readmePath := filepath.Join(path, "README.md")
	readme := `# Personal Tasks

This is the default workspace for personal tasks.
`
	if err := os.WriteFile(readmePath, []byte(readme), 0644); err != nil {
		return fmt.Errorf("write README: %w", err)
	}

	// Use git command to initialize - this ensures proper git structure
	cmd := exec.Command("git", "init")
	cmd.Dir = path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init: %v\n%s", err, output)
	}

	// Stage the README
	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %v\n%s", err, output)
	}

	// Configure local git user for this repo (required for CI environments)
	cmd = exec.Command("git", "config", "user.email", "task@local")
	cmd.Dir = path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git config user.email: %v\n%s", err, output)
	}

	cmd = exec.Command("git", "config", "user.name", "Task")
	cmd.Dir = path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git config user.name: %v\n%s", err, output)
	}

	// Create initial commit - this is required for worktrees to work
	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %v\n%s", err, output)
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

// migrateProjectAliases finds tasks whose project field contains an alias
// instead of the canonical project name, and updates them to use the canonical name.
func (db *DB) migrateProjectAliases() error {
	projects, err := db.ListProjects()
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}

	// Build a map of alias -> canonical name
	for _, p := range projects {
		for _, alias := range splitAliases(p.Aliases) {
			// Update any tasks that have the alias as their project
			_, err := db.Exec(`UPDATE tasks SET project = ? WHERE project = ?`, p.Name, alias)
			if err != nil {
				return fmt.Errorf("update tasks for alias %q -> %q: %w", alias, p.Name, err)
			}
		}
	}

	return nil
}

// defaultCodeTaskTypeInstructions is the default instructions template for the built-in
// "code" task type. It carries only code-task workflow steering (explore, implement, test,
// commit, open a PR) — deliberately NOT operational guidance like the worktree-safety
// constraint or taskyou_get_project_context usage. That guidance is intrinsic to how
// taskyou runs a task (every task type, conditional on worktrees) and is injected by the
// executor for all task types (see Executor.buildUniversalGuidance), so it must not be
// baked into a single task type's editable template.
const defaultCodeTaskTypeInstructions = `You are working on: {{project}}

{{project_instructions}}

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
- Note any follow-up items or concerns`

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
			Name:         "code",
			Label:        "Code",
			Instructions: defaultCodeTaskTypeInstructions,
			SortOrder:    1,
		},
		{
			Name:  "writing",
			Label: "Writing",
			Instructions: `You are a skilled writer. Please complete this task:

{{project_instructions}}

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

// RecoverStaleTmuxRefs clears stale daemon_session and tmux_window_id references
// from tasks. Called automatically on daemon startup to recover from crashes.
// Returns (staleDaemonCount, staleWindowCount) of cleaned references.
func (db *DB) RecoverStaleTmuxRefs(activeSessions map[string]bool, validWindowIDs map[string]bool) (int, int, error) {
	var staleDaemonCount, staleWindowCount int

	// Count and clear stale daemon_session references
	if len(activeSessions) > 0 {
		sessionList := quotedList(activeSessions)
		row := db.QueryRow(`
			SELECT COUNT(*) FROM tasks
			WHERE daemon_session IS NOT NULL
			AND daemon_session != ''
			AND daemon_session NOT IN (` + sessionList + `)
		`)
		row.Scan(&staleDaemonCount)

		if staleDaemonCount > 0 {
			_, err := db.Exec(`
				UPDATE tasks SET daemon_session = NULL
				WHERE daemon_session IS NOT NULL
				AND daemon_session != ''
				AND daemon_session NOT IN (` + sessionList + `)
			`)
			if err != nil {
				return 0, 0, fmt.Errorf("clear stale daemon sessions: %w", err)
			}
		}
	} else {
		// No active sessions - clear all daemon_session refs
		row := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE daemon_session IS NOT NULL AND daemon_session != ''`)
		row.Scan(&staleDaemonCount)
		if staleDaemonCount > 0 {
			_, err := db.Exec(`UPDATE tasks SET daemon_session = NULL WHERE daemon_session IS NOT NULL AND daemon_session != ''`)
			if err != nil {
				return 0, 0, fmt.Errorf("clear all daemon sessions: %w", err)
			}
		}
	}

	// Count and clear stale tmux_window_id references
	if len(validWindowIDs) > 0 {
		windowList := quotedList(validWindowIDs)
		row := db.QueryRow(`
			SELECT COUNT(*) FROM tasks
			WHERE tmux_window_id IS NOT NULL
			AND tmux_window_id != ''
			AND tmux_window_id NOT IN (` + windowList + `)
		`)
		row.Scan(&staleWindowCount)

		if staleWindowCount > 0 {
			_, err := db.Exec(`
				UPDATE tasks SET tmux_window_id = NULL
				WHERE tmux_window_id IS NOT NULL
				AND tmux_window_id != ''
				AND tmux_window_id NOT IN (` + windowList + `)
			`)
			if err != nil {
				return staleDaemonCount, 0, fmt.Errorf("clear stale window IDs: %w", err)
			}
		}
	} else {
		// No valid windows - clear all tmux_window_id refs
		row := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE tmux_window_id IS NOT NULL AND tmux_window_id != ''`)
		row.Scan(&staleWindowCount)
		if staleWindowCount > 0 {
			_, err := db.Exec(`UPDATE tasks SET tmux_window_id = NULL WHERE tmux_window_id IS NOT NULL AND tmux_window_id != ''`)
			if err != nil {
				return staleDaemonCount, 0, fmt.Errorf("clear all window IDs: %w", err)
			}
		}
	}

	return staleDaemonCount, staleWindowCount, nil
}

// quotedList returns a SQL-safe comma-separated list of quoted strings.
func quotedList(items map[string]bool) string {
	if len(items) == 0 {
		return "''"
	}
	var parts []string
	for item := range items {
		// Simple SQL escaping - replace single quotes with two single quotes
		escaped := strings.ReplaceAll(item, "'", "''")
		parts = append(parts, "'"+escaped+"'")
	}
	return strings.Join(parts, ",")
}
