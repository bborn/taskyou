package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
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
	EffortLevel     string // Per-task Claude effort override ("" = use global/Claude default; otherwise low/medium/high/xhigh/max)
	Model           string // Per-task Claude model override ("" = use global/Claude default; otherwise an alias like opus/sonnet/haiku or a full model name)
	ClaudeConfigDir string // Per-task CLAUDE_CONFIG_DIR override ("" = use the project's/default config dir). Lets a single step route through a different Claude config (e.g. an ollama-backed one) without changing the project.
	EnvJSON         string // Per-task env overrides for the spawned Claude, stored as a JSON object (e.g. {"ANTHROPIC_BASE_URL":"http://127.0.0.1:11434","ANTHROPIC_AUTH_TOKEN":"ollama"}). Injected as a process-env prefix on the claude command so a step can route through a non-Anthropic proxy (ollama) WITHOUT swapping CLAUDE_CONFIG_DIR — the default config dir (plugins, MCP, trusted worktrees) stays intact and process env wins over stored creds. "" = no overrides.
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
	PRInfoJSON      string // Cached PR state as JSON (state, checks, mergeable, etc.)
	DangerousMode   bool   // Whether task is running in dangerous mode (--dangerously-skip-permissions). Kept for backward compat; PermissionMode is authoritative.
	PermissionMode  string // Permission mode for execution: "default" (prompt), "accept-edits" (Claude's acceptEdits — auto-accept file edits, still prompts for risky actions), "auto" (Claude Code's auto mode — classifier auto-approves safe actions, blocks risky ones), "dangerous" (skip permissions). Empty falls back to DangerousMode/global default.
	RemoteControl   bool   // Whether to launch claude with --remote-control (interactive, remote-drivable)
	Pinned          bool   // Whether the task is pinned to the top of its column
	Tags            string // Comma-separated tags for categorization (e.g., "customer-support,email,influence-kit")
	SourceBranch    string // Existing branch to checkout for worktree (e.g., "fix/ui-overflow") instead of creating new branch
	Summary         string // Distilled summary of what was accomplished (for search and context)
	CreatedAt       LocalTime
	UpdatedAt       LocalTime
	StartedAt       *LocalTime
	CompletedAt     *LocalTime
	// Distillation tracking
	LastDistilledAt *LocalTime // When task was last distilled for learnings
	// UI tracking
	LastAccessedAt *LocalTime // When task was last accessed/opened in the UI
	// Archive state for preserving worktree state when archiving
	ArchiveRef          string // Git ref storing stashed changes (e.g., "refs/task-archive/123")
	ArchiveCommit       string // Commit hash at time of archiving
	ArchiveWorktreePath string // Original worktree path before archiving
	ArchiveBranchName   string // Original branch name before archiving
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

// Permission modes control how the underlying agent handles permission prompts.
// They map one-to-one onto Claude Code's --permission-mode choices (plus the
// --dangerously-skip-permissions shortcut). There are four sets, from most to
// least gated: default → accept-edits → auto → dangerous.
//
// HISTORY: TaskYou originally used the value "auto" for what is really Claude's
// acceptEdits mode — that predated Claude Code shipping its own "auto mode". The
// value "auto" now means Claude Code's actual auto mode (--permission-mode
// auto), and the old acceptEdits set has the explicit value "accept-edits".
// A one-time DB migration rewrites pre-existing "auto" rows (which meant
// acceptEdits) to "accept-edits"; see migrate() in sqlite.go.
const (
	// PermissionModeDefault prompts for every permission (the historical default).
	// Maps to Claude's --permission-mode default.
	PermissionModeDefault = "default"
	// PermissionModeAcceptEdits auto-accepts file edits but still prompts for
	// risky actions (shell, network, etc.). Maps to Claude's
	// --permission-mode acceptEdits.
	PermissionModeAcceptEdits = "accept-edits"
	// PermissionModeAuto is Claude Code's auto mode (--permission-mode auto): an
	// AI classifier auto-approves a broad set of safe actions (edits and safe
	// commands) while still hard-denying dangerous ones. More autonomous than
	// accept-edits, but far safer than fully bypassing permissions.
	PermissionModeAuto = "auto"
	// PermissionModeDangerous bypasses all permission checks
	// (Claude's --dangerously-skip-permissions).
	PermissionModeDangerous = "dangerous"
)

// NormalizePermissionMode coerces a raw value into a known permission mode.
// "prompt" and "" are treated as default; Claude's own "acceptEdits" spelling
// (and "accept_edits") normalizes to PermissionModeAcceptEdits. Matching is
// case- and whitespace-insensitive. Unknown values return "".
func NormalizePermissionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case PermissionModeAcceptEdits, "accept_edits", "acceptedits":
		return PermissionModeAcceptEdits
	case PermissionModeAuto:
		return PermissionModeAuto
	case PermissionModeDangerous:
		return PermissionModeDangerous
	case PermissionModeDefault, "prompt":
		return PermissionModeDefault
	}
	return ""
}

// PermissionModeCycle is the order the UI cycles permission modes, from most to
// least gated. Cycling keeps the four modes reachable on a running task; each
// step relaunches the live session so it matches the stored mode.
var PermissionModeCycle = []string{
	PermissionModeDefault,
	PermissionModeAcceptEdits,
	PermissionModeAuto,
	PermissionModeDangerous,
}

// NextPermissionMode returns the next mode after the given one in
// PermissionModeCycle, wrapping around. Unknown input starts the cycle at
// accept-edits (one past default), so "advance from wherever you are" is sane.
func NextPermissionMode(mode string) string {
	mode = NormalizePermissionMode(mode)
	for i, m := range PermissionModeCycle {
		if m == mode {
			return PermissionModeCycle[(i+1)%len(PermissionModeCycle)]
		}
	}
	return PermissionModeAcceptEdits
}

// PermissionModeLabel returns a short human-facing label for a permission mode.
func PermissionModeLabel(mode string) string {
	switch NormalizePermissionMode(mode) {
	case PermissionModeAcceptEdits:
		return "Accept-edits"
	case PermissionModeAuto:
		return "Auto"
	case PermissionModeDangerous:
		return "Dangerous"
	default:
		return "Prompt"
	}
}

// GlobalDefaultPermissionMode returns the fallback permission mode used when a
// project has no explicit default. It can be overridden with the
// TASKYOU_DEFAULT_PERMISSION_MODE environment variable. Defaults to "auto"
// (Claude Code's auto mode) so tasks start maximally unblocked while the
// classifier still gates dangerous actions; set the env var to "accept-edits"
// or "default" to dial back autonomy.
func GlobalDefaultPermissionMode() string {
	if m := NormalizePermissionMode(os.Getenv("TASKYOU_DEFAULT_PERMISSION_MODE")); m != "" {
		return m
	}
	return PermissionModeAuto
}

// EffectivePermissionMode resolves the permission mode a task should run with,
// falling back to the legacy DangerousMode boolean and finally the default.
func (t *Task) EffectivePermissionMode() string {
	if m := NormalizePermissionMode(t.PermissionMode); m != "" {
		return m
	}
	if t.DangerousMode {
		return PermissionModeDangerous
	}
	return PermissionModeDefault
}

// IsDangerous reports whether the task runs with permissions fully bypassed.
func (t *Task) IsDangerous() bool {
	return t.EffectivePermissionMode() == PermissionModeDangerous
}

// IsAutoPermission reports whether the task runs in Claude Code's auto mode.
func (t *Task) IsAutoPermission() bool {
	return t.EffectivePermissionMode() == PermissionModeAuto
}

// IsAcceptEdits reports whether the task runs in accept-edits (acceptEdits) mode.
func (t *Task) IsAcceptEdits() bool {
	return t.EffectivePermissionMode() == PermissionModeAcceptEdits
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
	ExecutorOpenCode = "opencode" // OpenCode AI assistant (https://opencode.ai)
	ExecutorPi       = "pi"       // Pi coding agent (https://github.com/mariozechner/pi-coding-agent)
)

// DefaultExecutor returns the default executor if none is specified.
func DefaultExecutor() string {
	return ExecutorClaude
}

// Effort levels are per-task overrides for Claude's reasoning effort (claude --effort).
// An empty value means "no override" — the task uses Claude's global default, leaving
// the user's global setting untouched.
const (
	EffortLow    = "low"
	EffortMedium = "medium"
	EffortHigh   = "high"
	EffortXHigh  = "xhigh"
	EffortMax    = "max"
)

// EffortLevels returns the valid per-task effort override values, in ascending order.
func EffortLevels() []string {
	return []string{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}
}

// IsValidEffortLevel reports whether s is a valid effort level. The empty string is
// valid and means "use the global/Claude default" (no per-task override).
func IsValidEffortLevel(s string) bool {
	switch s {
	case "", EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax:
		return true
	default:
		return false
	}
}

// EnvMap parses a task's EnvJSON override blob into a map. A malformed or
// empty blob yields an empty (never nil) map, so callers can range over it
// without a nil check. This is the read side of the per-step env feature: the
// pipeline stores a step's `env:` map as JSON in EnvJSON, and the executor
// renders it into a process-env prefix on the claude command.
func (t *Task) EnvMap() map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(t.EnvJSON) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(t.EnvJSON), &out); err != nil {
		return map[string]string{}
	}
	return out
}

// Model overrides are per-task selections for Claude's model (claude --model).
// An empty value means "no override" — the task uses Claude's global default,
// leaving the user's global setting untouched. The aliases below are accepted by
// the Claude CLI's --model flag; a full model name (e.g. "claude-opus-4-8") is
// also valid and passed through unchanged.
const (
	ModelFable  = "fable"
	ModelOpus   = "opus"
	ModelSonnet = "sonnet"
	ModelHaiku  = "haiku"
)

// ModelOptions returns the per-task model override aliases offered in the UI.
// The Claude CLI also accepts full model names, so this is a convenience list,
// not an exhaustive set. Keep it in sync with the aliases the Claude CLI supports
// as new models ship (e.g. "fable" was added alongside opus/sonnet/haiku).
func ModelOptions() []string {
	return []string{ModelOpus, ModelSonnet, ModelHaiku, ModelFable}
}

// IsValidModel reports whether s is an acceptable per-task model override. The
// empty string is valid and means "use the global/Claude default" (no per-task
// override). Any non-empty value is accepted because the Claude CLI validates
// the model name itself and supports both aliases and full model IDs.
func IsValidModel(s string) bool {
	return true
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

	// Validate that the project exists and resolve aliases to canonical name
	project, err := db.GetProjectByName(t.Project)
	if err != nil {
		return fmt.Errorf("validate project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("%w: %s", ErrProjectNotFound, t.Project)
	}
	t.Project = project.Name

	// Resolve the permission mode: an explicit value wins, otherwise inherit the
	// project's configured default so tasks start in the right mode without a
	// manual per-session toggle.
	t.PermissionMode = NormalizePermissionMode(t.PermissionMode)
	if t.PermissionMode == "" {
		if t.DangerousMode {
			t.PermissionMode = PermissionModeDangerous
		} else {
			t.PermissionMode = project.EffectiveDefaultPermissionMode()
		}
	}
	// Keep the legacy boolean consistent with the resolved mode.
	t.DangerousMode = t.PermissionMode == PermissionModeDangerous

	result, err := db.Exec(`
		INSERT INTO tasks (title, body, status, type, project, executor, pinned, tags, source_branch, dangerous_mode, permission_mode, remote_control, effort_level, model, claude_config_dir, env)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, t.Title, t.Body, t.Status, t.Type, t.Project, t.Executor, t.Pinned, t.Tags, t.SourceBranch, t.DangerousMode, t.PermissionMode, t.RemoteControl, t.EffortLevel, t.Model, t.ClaudeConfigDir, t.EnvJSON)
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

	// Save the last used effort and permission mode so the next task in this
	// project defaults to the same choices. Effort is stored even when empty
	// ("default") so switching back to the default sticks.
	if t.Project != "" {
		db.SetLastEffortForProject(t.Project, t.EffortLevel)
		db.SetLastModelForProject(t.Project, t.Model)
		db.SetLastPermissionForProject(t.Project, t.PermissionMode)
	}

	// Save the last used project
	if t.Project != "" {
		db.SetLastUsedProject(t.Project)
	}

	// Fetch the complete task and emit created event
	createdTask, err := db.GetTask(id)
	if err == nil && createdTask != nil {
		db.emitTaskCreated(createdTask)
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
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0), COALESCE(pr_info_json, ''),
		       COALESCE(dangerous_mode, 0), COALESCE(permission_mode, ''), COALESCE(remote_control, 0), COALESCE(pinned, 0), COALESCE(tags, ''),
		       COALESCE(source_branch, ''), COALESCE(summary, ''), COALESCE(effort_level, ''), COALESCE(model, ''), COALESCE(claude_config_dir, ''), COALESCE(env, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, last_accessed_at,
		       COALESCE(archive_ref, ''), COALESCE(archive_commit, ''),
		       COALESCE(archive_worktree_path, ''), COALESCE(archive_branch_name, '')
		FROM tasks WHERE id = ?
	`, id).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
		&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
		&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
		&t.PRURL, &t.PRNumber, &t.PRInfoJSON,
		&t.DangerousMode, &t.PermissionMode, &t.RemoteControl, &t.Pinned, &t.Tags,
		&t.SourceBranch, &t.Summary, &t.EffortLevel, &t.Model, &t.ClaudeConfigDir, &t.EnvJSON,
		&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
		&t.LastDistilledAt, &t.LastAccessedAt,
		&t.ArchiveRef, &t.ArchiveCommit, &t.ArchiveWorktreePath, &t.ArchiveBranchName,
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
	Status         string
	Type           string
	Project        string
	Tag            string // Filter to tasks carrying this exact tag (delimiter-safe; "gm:cortex" does not match "gm:cortex-2")
	Limit          int
	Offset         int
	IncludeClosed  bool // Include closed tasks even when Status is empty
	OrderByRecency bool // Sort purely by recency, ignoring pinned-first ordering
}

// ListTasks retrieves tasks with optional filters.
func (db *DB) ListTasks(opts ListTasksOptions) ([]*Task, error) {
	query := `
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''),
		       COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0), COALESCE(pr_info_json, ''),
		       COALESCE(dangerous_mode, 0), COALESCE(permission_mode, ''), COALESCE(remote_control, 0), COALESCE(pinned, 0), COALESCE(tags, ''),
		       COALESCE(source_branch, ''), COALESCE(summary, ''), COALESCE(effort_level, ''), COALESCE(model, ''), COALESCE(claude_config_dir, ''), COALESCE(env, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, last_accessed_at,
		       COALESCE(archive_ref, ''), COALESCE(archive_commit, ''),
		       COALESCE(archive_worktree_path, ''), COALESCE(archive_branch_name, '')
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
		// Resolve alias to canonical project name
		projectName := opts.Project
		if p, err := db.GetProjectByName(opts.Project); err == nil && p != nil {
			projectName = p.Name
		}
		query += " AND project = ?"
		args = append(args, projectName)
	}
	if opts.Tag != "" {
		// Tags are stored comma-separated (e.g. "a,gm:cortex,b"). A naive
		// LIKE '%gm:cortex%' would false-match "gm:cortex-2", so normalize the
		// stored value to ",a,gm:cortex,b," and match the delimited ",tag,".
		// Escape LIKE metacharacters (\, %, _) in the needle so a tag value
		// containing them matches literally rather than as a wildcard pattern.
		needle := strings.ReplaceAll(opts.Tag, " ", "")
		needle = strings.ReplaceAll(needle, `\`, `\\`)
		needle = strings.ReplaceAll(needle, "%", `\%`)
		needle = strings.ReplaceAll(needle, "_", `\_`)
		query += ` AND (',' || REPLACE(COALESCE(tags, ''), ' ', '') || ',') LIKE ? ESCAPE '\'`
		args = append(args, "%,"+needle+",%")
	}

	// Exclude done and archived by default unless specifically querying for them or includeClosed is set
	if opts.Status == "" && !opts.IncludeClosed {
		query += " AND status NOT IN ('done', 'archived')"
	}

	// Sort done/blocked tasks by completed_at (most recently closed first) and
	// other tasks by created_at (newest first). Use id DESC as secondary sort for
	// consistency. Pinning takes precedence unless OrderByRecency is set: a capped
	// slice (e.g. the kanban's Done column) must select the most recent tasks, and
	// pinned-first selection would let old pinned tasks crowd newer ones out of the
	// limit entirely.
	recency := " CASE WHEN status IN ('done', 'blocked') THEN completed_at ELSE created_at END DESC, id DESC"
	if opts.OrderByRecency {
		query += " ORDER BY" + recency
	} else {
		query += " ORDER BY pinned DESC," + recency
	}

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
			&t.PRURL, &t.PRNumber, &t.PRInfoJSON,
			&t.DangerousMode, &t.PermissionMode, &t.RemoteControl, &t.Pinned, &t.Tags,
			&t.SourceBranch, &t.Summary, &t.EffortLevel, &t.Model, &t.ClaudeConfigDir, &t.EnvJSON,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.LastDistilledAt, &t.LastAccessedAt,
			&t.ArchiveRef, &t.ArchiveCommit, &t.ArchiveWorktreePath, &t.ArchiveBranchName,
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
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0), COALESCE(pr_info_json, ''),
		       COALESCE(dangerous_mode, 0), COALESCE(permission_mode, ''), COALESCE(remote_control, 0), COALESCE(pinned, 0), COALESCE(tags, ''),
		       COALESCE(source_branch, ''), COALESCE(summary, ''), COALESCE(effort_level, ''), COALESCE(model, ''), COALESCE(claude_config_dir, ''), COALESCE(env, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, last_accessed_at,
		       COALESCE(archive_ref, ''), COALESCE(archive_commit, ''),
		       COALESCE(archive_worktree_path, ''), COALESCE(archive_branch_name, '')
		FROM tasks
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
		&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
		&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
		&t.PRURL, &t.PRNumber, &t.PRInfoJSON,
		&t.DangerousMode, &t.PermissionMode, &t.RemoteControl, &t.Pinned, &t.Tags,
		&t.SourceBranch, &t.Summary, &t.EffortLevel, &t.Model, &t.ClaudeConfigDir, &t.EnvJSON,
		&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
		&t.LastDistilledAt, &t.LastAccessedAt,
		&t.ArchiveRef, &t.ArchiveCommit, &t.ArchiveWorktreePath, &t.ArchiveBranchName,
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
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0), COALESCE(pr_info_json, ''),
		       COALESCE(dangerous_mode, 0), COALESCE(permission_mode, ''), COALESCE(remote_control, 0), COALESCE(pinned, 0), COALESCE(tags, ''),
		       COALESCE(source_branch, ''), COALESCE(summary, ''), COALESCE(effort_level, ''), COALESCE(model, ''), COALESCE(claude_config_dir, ''), COALESCE(env, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, last_accessed_at,
		       COALESCE(archive_ref, ''), COALESCE(archive_commit, ''),
		       COALESCE(archive_worktree_path, ''), COALESCE(archive_branch_name, '')
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
			&t.PRURL, &t.PRNumber, &t.PRInfoJSON,
			&t.DangerousMode, &t.PermissionMode, &t.RemoteControl, &t.Pinned, &t.Tags,
			&t.SourceBranch, &t.Summary, &t.EffortLevel, &t.Model, &t.ClaudeConfigDir, &t.EnvJSON,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.LastDistilledAt, &t.LastAccessedAt,
			&t.ArchiveRef, &t.ArchiveCommit, &t.ArchiveWorktreePath, &t.ArchiveBranchName,
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
	// Get old task to track status change
	oldTask, _ := db.GetTask(id)
	oldStatus := ""
	if oldTask != nil {
		oldStatus = oldTask.Status
	}

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

	// Emit status change event if status actually changed
	if oldStatus != "" && oldStatus != status {
		updatedTask, err := db.GetTask(id)
		if err == nil && updatedTask != nil {
			changes := map[string]interface{}{
				"status": map[string]string{
					"old": oldStatus,
					"new": status,
				},
			}
			db.emitTaskUpdated(updatedTask, changes)
			// Also emit lifecycle events so external watchers can react
			// to blocked/completed transitions without parsing update metadata.
			// These fire for every caller of UpdateTaskStatus — Claude hooks,
			// MCP, CLI, TUI, and the executor — as long as an emitter is registered.
			switch status {
			case StatusBlocked:
				db.emitTaskBlocked(updatedTask, "status change")
			case StatusDone:
				db.emitTaskCompleted(updatedTask)
			}
		}
	}

	// Process dependent tasks when a blocker is completed. Best-effort: a dropped
	// write is recovered by the daemon's RequeueReadyTasks sweep, but log it so a
	// stalled workflow isn't a silent mystery.
	if status == StatusDone || status == StatusArchived {
		if _, err := db.ProcessCompletedBlocker(id); err != nil {
			log.Printf("ProcessCompletedBlocker(%d): %v", id, err)
		}
	}

	return nil
}

// UpdateTask updates a task's fields.
func (db *DB) UpdateTask(t *Task) error {
	// Get old task to track changes
	oldTask, _ := db.GetTask(t.ID)

	_, err := db.Exec(`
		UPDATE tasks SET
			title = ?, body = ?, status = ?, type = ?, project = ?, executor = ?,
			worktree_path = ?, branch_name = ?, port = ?, claude_session_id = ?,
			daemon_session = ?, pr_url = ?, pr_number = ?, pr_info_json = ?, dangerous_mode = ?, permission_mode = ?, remote_control = ?,
			pinned = ?, tags = ?, source_branch = ?, effort_level = ?, model = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, t.Title, t.Body, t.Status, t.Type, t.Project, t.Executor,
		t.WorktreePath, t.BranchName, t.Port, t.ClaudeSessionID,
		t.DaemonSession, t.PRURL, t.PRNumber, t.PRInfoJSON, t.DangerousMode, t.PermissionMode, t.RemoteControl,
		t.Pinned, t.Tags, t.SourceBranch, t.EffortLevel, t.Model, t.ID)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}

	// Track changes for event emission
	if oldTask != nil {
		changes := make(map[string]interface{})
		if oldTask.Title != t.Title {
			changes["title"] = map[string]string{"old": oldTask.Title, "new": t.Title}
		}
		if oldTask.Body != t.Body {
			changes["body"] = map[string]string{"old": oldTask.Body, "new": t.Body}
		}
		if oldTask.Status != t.Status {
			changes["status"] = map[string]string{"old": oldTask.Status, "new": t.Status}
		}
		if oldTask.Type != t.Type {
			changes["type"] = map[string]string{"old": oldTask.Type, "new": t.Type}
		}
		if oldTask.Project != t.Project {
			changes["project"] = map[string]string{"old": oldTask.Project, "new": t.Project}
		}
		if len(changes) > 0 {
			db.emitTaskUpdated(t, changes)
		}
	}

	return nil
}

// UpdateTaskPRInfo updates only the PR-related fields for a task.
// This is used to persist PR state from GitHub API responses without touching other fields.
//
// When the cached PR JSON actually changes, it records a board-change event so the
// HTTP API's SSE stream re-pushes the board — this is how a live PR badge reaches
// the web/desktop without a restart. We compare against the stored value first so
// idle refreshes (state unchanged) don't spam the change feed every tick.
func (db *DB) UpdateTaskPRInfo(taskID int64, prURL string, prNumber int, prInfoJSON string) error {
	var prevJSON string
	// Best-effort read of the prior value to detect real changes. A scan error
	// (e.g. task gone) leaves prevJSON empty, so we fall through and emit — the
	// UPDATE below will no-op on a missing row anyway.
	_ = db.QueryRow(`SELECT pr_info_json FROM tasks WHERE id = ?`, taskID).Scan(&prevJSON)

	_, err := db.Exec(`
		UPDATE tasks SET pr_url = ?, pr_number = ?, pr_info_json = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, prURL, prNumber, prInfoJSON, taskID)
	if err != nil {
		return fmt.Errorf("update task pr info: %w", err)
	}

	if prevJSON != prInfoJSON {
		db.recordEvent("task.updated", taskID, "pr status")
	}
	return nil
}

// prAutoDoneLineType is the task_logs line_type used to record that a specific PR
// already drove a task to 'done'. It is an internal marker (not shown in the UI)
// that makes auto-completion idempotent: once a task has been auto-completed for
// PR #N, reopening the task and finishing again against that same merged/closed PR
// must NOT bounce it straight back to 'done'. A genuinely new PR (different number)
// is not marked, so it can still auto-complete when it merges.
const prAutoDoneLineType = "pr_done_marker"

// MarkPRAutoCompleted records that PR #prNumber has auto-completed this task, so a
// later reconcile pass won't re-complete the task for the same PR after a human
// reopens it to keep working.
func (db *DB) MarkPRAutoCompleted(taskID int64, prNumber int) error {
	return db.AppendTaskLog(taskID, prAutoDoneLineType, fmt.Sprintf("%d", prNumber))
}

// WasPRAutoCompleted reports whether PR #prNumber has already auto-completed this
// task in a prior reconcile pass.
func (db *DB) WasPRAutoCompleted(taskID int64, prNumber int) (bool, error) {
	var exists int
	err := db.QueryRow(`
		SELECT 1 FROM task_logs
		WHERE task_id = ? AND line_type = ? AND content = ?
		LIMIT 1
	`, taskID, prAutoDoneLineType, fmt.Sprintf("%d", prNumber)).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query pr auto-done marker: %w", err)
	}
	return true, nil
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

// UpdateTaskDangerousMode updates the dangerous_mode flag for a task, keeping
// the authoritative permission_mode column in sync. Toggling dangerous off
// resets the task to the default (prompt) mode.
func (db *DB) UpdateTaskDangerousMode(taskID int64, dangerousMode bool) error {
	mode := PermissionModeDefault
	if dangerousMode {
		mode = PermissionModeDangerous
	}
	_, err := db.Exec(`
		UPDATE tasks SET dangerous_mode = ?, permission_mode = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, dangerousMode, mode, taskID)
	if err != nil {
		return fmt.Errorf("update task dangerous mode: %w", err)
	}
	return nil
}

// UpdateTaskPermissionMode updates the permission_mode for a task and keeps the
// legacy dangerous_mode boolean consistent.
func (db *DB) UpdateTaskPermissionMode(taskID int64, mode string) error {
	mode = NormalizePermissionMode(mode)
	if mode == "" {
		mode = PermissionModeDefault
	}
	_, err := db.Exec(`
		UPDATE tasks SET permission_mode = ?, dangerous_mode = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, mode, mode == PermissionModeDangerous, taskID)
	if err != nil {
		return fmt.Errorf("update task permission mode: %w", err)
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

	// Emit pin/unpin event
	task, err := db.GetTask(taskID)
	if err == nil && task != nil {
		if pinned {
			db.emitTaskPinned(task)
		} else {
			db.emitTaskUnpinned(task)
		}
	}

	return nil
}

// UpdateTaskSummary updates the task summary and distillation timestamp.
func (db *DB) UpdateTaskSummary(taskID int64, summary string) error {
	oldTask, _ := db.GetTask(taskID)
	oldSummary := ""
	if oldTask != nil {
		oldSummary = oldTask.Summary
	}

	_, err := db.Exec(`
		UPDATE tasks
		SET summary = ?, last_distilled_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, summary, taskID)
	if err != nil {
		return fmt.Errorf("update task summary: %w", err)
	}

	if oldTask != nil && oldSummary != summary {
		task, err := db.GetTask(taskID)
		if err == nil && task != nil {
			db.emitTaskUpdated(task, map[string]interface{}{
				"summary": map[string]string{
					"old": oldSummary,
					"new": summary,
				},
			})
		}
	}

	return nil
}

// ClearTaskWorktreePath clears just the worktree_path for a task.
// Used during async archive cleanup to avoid overwriting other fields.
func (db *DB) ClearTaskWorktreePath(taskID int64) error {
	_, err := db.Exec(`
		UPDATE tasks SET worktree_path = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, taskID)
	if err != nil {
		return fmt.Errorf("clear task worktree path: %w", err)
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

// UpdateTaskLastAccessedAt updates the last_accessed_at timestamp for a task.
// This is used to track when a task was last accessed/opened in the UI,
// enabling the command palette to show recently visited tasks first.
func (db *DB) UpdateTaskLastAccessedAt(taskID int64) error {
	_, err := db.Exec(`
		UPDATE tasks SET last_accessed_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, taskID)
	if err != nil {
		return fmt.Errorf("update task last accessed at: %w", err)
	}
	return nil
}

// ClearTaskTmuxIDs clears all tmux-related IDs for a task.
// This should be called before retrying/restarting a task to prevent stale references.
// Clears: tmux_window_id, claude_pane_id, shell_pane_id
func (db *DB) ClearTaskTmuxIDs(taskID int64) error {
	_, err := db.Exec(`
		UPDATE tasks SET tmux_window_id = '', claude_pane_id = '', shell_pane_id = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, taskID)
	if err != nil {
		return fmt.Errorf("clear task tmux ids: %w", err)
	}
	return nil
}

// DeleteTask deletes a task.
func (db *DB) DeleteTask(id int64) error {
	// Get task before deleting for event emission
	task, _ := db.GetTask(id)
	title := ""
	if task != nil {
		title = task.Title
	}

	_, err := db.Exec("DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}

	// Emit delete event
	db.emitTaskDeleted(id, title)

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
// Also clears stale tmux window/pane IDs to prevent duplicate window issues.
func (db *DB) RetryTask(id int64, feedback string) error {
	// Add continuation marker to logs
	db.AppendTaskLog(id, "system", "--- Continuation ---")

	// Log feedback if provided
	if feedback != "" {
		db.AppendTaskLog(id, "text", "Feedback: "+feedback)
	}

	// Clear stale tmux IDs to prevent duplicate window issues on retry
	// The new window/pane IDs will be set when the task restarts
	if err := db.ClearTaskTmuxIDs(id); err != nil {
		// Log but don't fail - this is cleanup, not critical
		// The executor will still handle duplicates via name-based cleanup
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
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0), COALESCE(pr_info_json, ''),
		       COALESCE(dangerous_mode, 0), COALESCE(permission_mode, ''), COALESCE(remote_control, 0), COALESCE(pinned, 0), COALESCE(tags, ''),
		       COALESCE(source_branch, ''), COALESCE(summary, ''), COALESCE(effort_level, ''), COALESCE(model, ''), COALESCE(claude_config_dir, ''), COALESCE(env, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, last_accessed_at,
		       COALESCE(archive_ref, ''), COALESCE(archive_commit, ''),
		       COALESCE(archive_worktree_path, ''), COALESCE(archive_branch_name, '')
		FROM tasks
		WHERE status = ?
		ORDER BY created_at ASC
		LIMIT 1
	`, StatusQueued).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
		&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
		&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
		&t.PRURL, &t.PRNumber, &t.PRInfoJSON,
		&t.DangerousMode, &t.PermissionMode, &t.RemoteControl, &t.Pinned, &t.Tags,
		&t.SourceBranch, &t.Summary, &t.EffortLevel, &t.Model, &t.ClaudeConfigDir, &t.EnvJSON,
		&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
		&t.LastDistilledAt, &t.LastAccessedAt,
		&t.ArchiveRef, &t.ArchiveCommit, &t.ArchiveWorktreePath, &t.ArchiveBranchName,
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
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0), COALESCE(pr_info_json, ''),
		       COALESCE(dangerous_mode, 0), COALESCE(permission_mode, ''), COALESCE(remote_control, 0), COALESCE(pinned, 0), COALESCE(tags, ''),
		       COALESCE(source_branch, ''), COALESCE(summary, ''), COALESCE(effort_level, ''), COALESCE(model, ''), COALESCE(claude_config_dir, ''), COALESCE(env, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, last_accessed_at,
		       COALESCE(archive_ref, ''), COALESCE(archive_commit, ''),
		       COALESCE(archive_worktree_path, ''), COALESCE(archive_branch_name, '')
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
			&t.PRURL, &t.PRNumber, &t.PRInfoJSON,
			&t.DangerousMode, &t.PermissionMode, &t.RemoteControl, &t.Pinned, &t.Tags,
			&t.SourceBranch, &t.Summary, &t.EffortLevel, &t.Model, &t.ClaudeConfigDir, &t.EnvJSON,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.LastDistilledAt, &t.LastAccessedAt,
			&t.ArchiveRef, &t.ArchiveCommit, &t.ArchiveWorktreePath, &t.ArchiveBranchName,
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

// HasQuestionLog reports whether the task ever recorded a needs-input question
// (line_type "question"). The workflow "finished but couldn't signal" sweep uses
// this to avoid auto-completing a step that is genuinely waiting on a human answer.
func (db *DB) HasQuestionLog(taskID int64) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM task_logs WHERE task_id = ? AND line_type = 'question'`, taskID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// HasLogLineContaining reports whether the task has any log line whose content
// contains substr. Used to make a periodic sweep idempotent — e.g. logging a
// "parked for merge review" note exactly once for a terminal workflow step.
func (db *DB) HasLogLineContaining(taskID int64, substr string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM task_logs WHERE task_id = ? AND content LIKE ?`, taskID, "%"+substr+"%").Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// SetTaskBaseCommit records the commit a task's worktree was created at. Written once,
// right after the worktree exists and before any agent session starts.
func (db *DB) SetTaskBaseCommit(taskID int64, sha string) error {
	_, err := db.Exec(`UPDATE tasks SET base_commit = ? WHERE id = ?`, sha, taskID)
	return err
}

// GetTaskBaseCommit returns the commit a task's worktree was created at, or "" if it
// was never recorded (a task from before the column existed).
func (db *DB) GetTaskBaseCommit(taskID int64) (string, error) {
	var sha string
	err := db.QueryRow(`SELECT COALESCE(base_commit, '') FROM tasks WHERE id = ?`, taskID).Scan(&sha)
	if err != nil {
		return "", err
	}
	return sha, nil
}

// SetTaskBaseDirty records the paths already dirty in the task's worktree when it
// started (newline-separated). Worktree init regularly rewrites tracked files, so this is
// the baseline that "did the step leave uncommitted work?" is measured against.
func (db *DB) SetTaskBaseDirty(taskID int64, paths string) error {
	_, err := db.Exec(`UPDATE tasks SET base_dirty = ? WHERE id = ?`, paths, taskID)
	return err
}

// GetTaskBaseDirty returns the paths dirty in the worktree when the task started.
func (db *DB) GetTaskBaseDirty(taskID int64) (string, error) {
	var s string
	err := db.QueryRow(`SELECT COALESCE(base_dirty, '') FROM tasks WHERE id = ?`, taskID).Scan(&s)
	if err != nil {
		return "", err
	}
	return s, nil
}

// HasSessionStarted reports whether the task's executor session actually began. A task
// flips to 'processing' and then spends tens of seconds on worktree setup (clone, bundle,
// migrations) before any session exists — so this, not the absence of a tmux window, is
// how to tell "hasn't started yet" from "ran and finished". Conflating the two let the
// sweep complete steps before their agent ever launched.
func (db *DB) HasSessionStarted(taskID int64) (bool, error) {
	var n int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM task_logs
		WHERE task_id = ? AND (content LIKE 'Starting new session%' OR content LIKE 'Resuming%')
	`, taskID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
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

// GetLatestLogPerTask returns the most recent log entry for each of the given task IDs.
// Returns a map of taskID -> latest TaskLog. Uses a single efficient query.
func (db *DB) GetLatestLogPerTask(taskIDs []int64) (map[int64]*TaskLog, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}

	// Build placeholders
	placeholders := make([]string, len(taskIDs))
	args := make([]interface{}, len(taskIDs))
	for i, id := range taskIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT tl.id, tl.task_id, tl.line_type, tl.content, tl.created_at
		FROM task_logs tl
		INNER JOIN (
			SELECT task_id, MAX(id) as max_id
			FROM task_logs
			WHERE task_id IN (%s)
			GROUP BY task_id
		) latest ON tl.id = latest.max_id
	`, strings.Join(placeholders, ","))

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query latest logs: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]*TaskLog)
	for rows.Next() {
		l := &TaskLog{}
		err := rows.Scan(&l.ID, &l.TaskID, &l.LineType, &l.Content, &l.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan latest log: %w", err)
		}
		result[l.TaskID] = l
	}
	return result, nil
}

// GetConversationHistoryLogs retrieves only logs relevant for building conversation history.
// This is much more efficient than GetTaskLogs for the executor's prompt building,
// as it skips large output/tool log content that isn't needed for conversation context.
// Only fetches: continuation markers, questions, and user feedback.
func (db *DB) GetConversationHistoryLogs(taskID int64) ([]*TaskLog, error) {
	rows, err := db.Query(`
		SELECT id, task_id, line_type, content, created_at
		FROM task_logs
		WHERE task_id = ?
		  AND (
		    (line_type = 'system' AND content = '--- Continuation ---')
		    OR line_type = 'question'
		    OR (line_type = 'text' AND content LIKE 'Feedback: %')
		  )
		ORDER BY id ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query conversation history logs: %w", err)
	}
	defer rows.Close()

	var logs []*TaskLog
	for rows.Next() {
		l := &TaskLog{}
		err := rows.Scan(&l.ID, &l.TaskID, &l.LineType, &l.Content, &l.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan conversation history log: %w", err)
		}
		logs = append(logs, l)
	}

	return logs, nil
}

// HasContinuationMarker checks if a task has any continuation markers.
// This is a fast EXISTS-style query to avoid loading logs just to check.
func (db *DB) HasContinuationMarker(taskID int64) (bool, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM task_logs
		WHERE task_id = ? AND line_type = 'system' AND content = '--- Continuation ---'
		LIMIT 1
	`, taskID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check continuation marker: %w", err)
	}
	return count > 0, nil
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
	UseWorktrees    bool            // whether to use git worktrees for task isolation (default true)
	// DefaultPermissionMode is the permission mode new tasks in this project
	// inherit ("default", "auto", "dangerous"). Empty means use the global default.
	DefaultPermissionMode string
	CreatedAt             LocalTime
}

// UsesWorktrees returns whether this project uses git worktrees for task isolation.
// Defaults to true for backward compatibility.
func (p *Project) UsesWorktrees() bool {
	return p.UseWorktrees
}

// EffectiveDefaultPermissionMode returns the permission mode new tasks in this
// project should inherit, falling back to the global default when unset.
func (p *Project) EffectiveDefaultPermissionMode() string {
	if m := NormalizePermissionMode(p.DefaultPermissionMode); m != "" {
		return m
	}
	return GlobalDefaultPermissionMode()
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

// boolToInt converts a bool to an int for SQLite storage (1=true, 0=false).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// CreateProject creates a new project.
func (db *DB) CreateProject(p *Project) error {
	actionsJSON, _ := json.Marshal(p.Actions)
	result, err := db.Exec(`
		INSERT INTO projects (name, path, aliases, instructions, actions, color, claude_config_dir, use_worktrees, default_permission_mode)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.Name, p.Path, p.Aliases, p.Instructions, string(actionsJSON), p.Color, p.ClaudeConfigDir, boolToInt(p.UseWorktrees), NormalizePermissionMode(p.DefaultPermissionMode))
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
		UPDATE projects SET name = ?, path = ?, aliases = ?, instructions = ?, actions = ?, color = ?, claude_config_dir = ?, use_worktrees = ?, default_permission_mode = ?
		WHERE id = ?
	`, p.Name, p.Path, p.Aliases, p.Instructions, string(actionsJSON), p.Color, p.ClaudeConfigDir, boolToInt(p.UseWorktrees), NormalizePermissionMode(p.DefaultPermissionMode), p.ID)
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
		SELECT id, name, path, aliases, instructions, COALESCE(actions, '[]'), COALESCE(color, ''), COALESCE(claude_config_dir, ''), COALESCE(use_worktrees, 1), COALESCE(default_permission_mode, ''), created_at
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
		var useWorktrees int
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &actionsJSON, &p.Color, &p.ClaudeConfigDir, &useWorktrees, &p.DefaultPermissionMode, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		json.Unmarshal([]byte(actionsJSON), &p.Actions)
		p.UseWorktrees = useWorktrees != 0
		projects = append(projects, p)
	}
	return projects, nil
}

// GetProjectByName returns a project by name or alias.
func (db *DB) GetProjectByName(name string) (*Project, error) {
	// First try exact name match
	p := &Project{}
	var actionsJSON string
	var useWorktrees int
	err := db.QueryRow(`
		SELECT id, name, path, aliases, instructions, COALESCE(actions, '[]'), COALESCE(color, ''), COALESCE(claude_config_dir, ''), COALESCE(use_worktrees, 1), COALESCE(default_permission_mode, ''), created_at
		FROM projects WHERE name = ?
	`, name).Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &actionsJSON, &p.Color, &p.ClaudeConfigDir, &useWorktrees, &p.DefaultPermissionMode, &p.CreatedAt)
	if err == nil {
		json.Unmarshal([]byte(actionsJSON), &p.Actions)
		p.UseWorktrees = useWorktrees != 0
		return p, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("query project: %w", err)
	}

	// Try alias match
	rows, err := db.Query(`SELECT id, name, path, aliases, instructions, COALESCE(actions, '[]'), COALESCE(color, ''), COALESCE(claude_config_dir, ''), COALESCE(use_worktrees, 1), COALESCE(default_permission_mode, ''), created_at FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &actionsJSON, &p.Color, &p.ClaudeConfigDir, &useWorktrees, &p.DefaultPermissionMode, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		json.Unmarshal([]byte(actionsJSON), &p.Actions)
		p.UseWorktrees = useWorktrees != 0
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

// GetLastEffortForProject returns the last used Claude effort override for a
// project ("" means the global/Claude default was used).
func (db *DB) GetLastEffortForProject(project string) (string, error) {
	return db.GetSetting("last_effort_" + project)
}

// SetLastEffortForProject saves the last used Claude effort override for a
// project so the next task in it defaults to the same choice.
func (db *DB) SetLastEffortForProject(project, effort string) error {
	return db.SetSetting("last_effort_"+project, effort)
}

// GetLastModelForProject returns the last used Claude model override for a
// project ("" means the global/Claude default was used).
func (db *DB) GetLastModelForProject(project string) (string, error) {
	return db.GetSetting("last_model_" + project)
}

// SetLastModelForProject saves the last used Claude model override for a project
// so the next task in it defaults to the same choice.
func (db *DB) SetLastModelForProject(project, model string) error {
	return db.SetSetting("last_model_"+project, model)
}

// GetLastPermissionForProject returns the last used permission mode for a
// project ("" means none recorded yet).
func (db *DB) GetLastPermissionForProject(project string) (string, error) {
	return db.GetSetting("last_permission_" + project)
}

// SetLastPermissionForProject saves the last used permission mode for a project
// so the next task in it defaults to the same choice.
func (db *DB) SetLastPermissionForProject(project, mode string) error {
	return db.SetSetting("last_permission_"+project, mode)
}

// IsFirstRun returns true if this is the first time the app is being used.
// This is determined by checking if onboarding has been completed.
func (db *DB) IsFirstRun() bool {
	val, err := db.GetSetting("onboarding_completed")
	if err != nil || val != "true" {
		return true
	}
	return false
}

// CompleteOnboarding marks the onboarding as complete.
func (db *DB) CompleteOnboarding() error {
	return db.SetSetting("onboarding_completed", "true")
}

// GetExecutorUsageByProject returns a map of executor names to their usage counts for a project.
// This counts how many tasks have been created with each executor for the given project.
func (db *DB) GetExecutorUsageByProject(project string) (map[string]int, error) {
	rows, err := db.Query(`
		SELECT COALESCE(executor, 'claude'), COUNT(*) as count
		FROM tasks
		WHERE project = ?
		GROUP BY executor
		ORDER BY count DESC
	`, project)
	if err != nil {
		return nil, fmt.Errorf("query executor usage: %w", err)
	}
	defer rows.Close()

	usage := make(map[string]int)
	for rows.Next() {
		var executor string
		var count int
		if err := rows.Scan(&executor, &count); err != nil {
			return nil, fmt.Errorf("scan executor usage: %w", err)
		}
		usage[executor] = count
	}
	return usage, nil
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

// SaveArchiveState saves the archive state for a task.
// This stores the git ref, commit hash, worktree path, and branch name
// that were active at the time of archiving.
func (db *DB) SaveArchiveState(taskID int64, archiveRef, archiveCommit, worktreePath, branchName string) error {
	_, err := db.Exec(`
		UPDATE tasks SET
			archive_ref = ?,
			archive_commit = ?,
			archive_worktree_path = ?,
			archive_branch_name = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, archiveRef, archiveCommit, worktreePath, branchName, taskID)
	if err != nil {
		return fmt.Errorf("save archive state: %w", err)
	}
	return nil
}

// ClearArchiveState clears the archive state for a task after unarchiving.
func (db *DB) ClearArchiveState(taskID int64) error {
	_, err := db.Exec(`
		UPDATE tasks SET
			archive_ref = '',
			archive_commit = '',
			archive_worktree_path = '',
			archive_branch_name = '',
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, taskID)
	if err != nil {
		return fmt.Errorf("clear archive state: %w", err)
	}
	return nil
}

// HasArchiveState returns true if the task has saved archive state.
func (t *Task) HasArchiveState() bool {
	return t.ArchiveRef != "" && t.ArchiveCommit != ""
}

// GetStaleWorktreeTasks returns done/archived tasks that have worktree paths set
// and were completed more than maxAge ago. These are candidates for cleanup.
func (db *DB) GetStaleWorktreeTasks(maxAge time.Duration) ([]*Task, error) {
	cutoff := time.Now().Add(-maxAge).UTC()
	query := `
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''),
		       COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0), COALESCE(pr_info_json, ''),
		       COALESCE(dangerous_mode, 0), COALESCE(permission_mode, ''), COALESCE(remote_control, 0), COALESCE(pinned, 0), COALESCE(tags, ''),
		       COALESCE(source_branch, ''), COALESCE(summary, ''), COALESCE(effort_level, ''), COALESCE(model, ''), COALESCE(claude_config_dir, ''), COALESCE(env, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, last_accessed_at,
		       COALESCE(archive_ref, ''), COALESCE(archive_commit, ''),
		       COALESCE(archive_worktree_path, ''), COALESCE(archive_branch_name, '')
		FROM tasks
		WHERE worktree_path != ''
		  AND status IN ('done', 'archived')
		  AND completed_at IS NOT NULL
		  AND completed_at < ?
		ORDER BY completed_at ASC
	`
	rows, err := db.Query(query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query stale worktree tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
			&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
			&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
			&t.PRURL, &t.PRNumber, &t.PRInfoJSON,
			&t.DangerousMode, &t.PermissionMode, &t.RemoteControl, &t.Pinned, &t.Tags,
			&t.SourceBranch, &t.Summary, &t.EffortLevel, &t.Model, &t.ClaudeConfigDir, &t.EnvJSON,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.LastDistilledAt, &t.LastAccessedAt,
			&t.ArchiveRef, &t.ArchiveCommit, &t.ArchiveWorktreePath, &t.ArchiveBranchName,
		)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
}
