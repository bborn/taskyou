package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Task represents a task in the database.
type Task struct {
	ID           int64
	Title        string
	Body         string
	Status       string
	Type         string
	Project      string
	WorktreePath string
	BranchName   string
	CreatedAt    LocalTime
	UpdatedAt    LocalTime
	StartedAt    *LocalTime
	CompletedAt  *LocalTime
}

// Task statuses
const (
	StatusBacklog    = "backlog"    // Created but not yet started
	StatusQueued     = "queued"     // Waiting to be processed
	StatusProcessing = "processing" // Currently being executed
	StatusBlocked    = "blocked"    // Needs input/clarification
	StatusDone       = "done"       // Completed
)

// IsInProgress returns true if the task is actively being worked on.
func IsInProgress(status string) bool {
	return status == StatusQueued || status == StatusProcessing
}

// Task types
const (
	TypeCode     = "code"
	TypeWriting  = "writing"
	TypeThinking = "thinking"
)

// CreateTask creates a new task.
func (db *DB) CreateTask(t *Task) error {
	// Default to 'personal' project if not specified
	if t.Project == "" {
		t.Project = "personal"
	}

	result, err := db.Exec(`
		INSERT INTO tasks (title, body, status, type, project)
		VALUES (?, ?, ?, ?, ?)
	`, t.Title, t.Body, t.Status, t.Type, t.Project)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}
	t.ID = id

	return nil
}

// GetTask retrieves a task by ID.
func (db *DB) GetTask(id int64) (*Task, error) {
	t := &Task{}
	err := db.QueryRow(`
		SELECT id, title, body, status, type, project,
		       worktree_path, branch_name,
		       created_at, updated_at, started_at, completed_at
		FROM tasks WHERE id = ?
	`, id).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project,
		&t.WorktreePath, &t.BranchName,
		&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
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
		SELECT id, title, body, status, type, project,
		       worktree_path, branch_name,
		       created_at, updated_at, started_at, completed_at
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

	// Exclude done by default unless specifically querying for them or includeClosed is set
	if opts.Status == "" && !opts.IncludeClosed {
		query += " AND status != 'done'"
	}

	query += " ORDER BY created_at DESC"

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
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project,
			&t.WorktreePath, &t.BranchName,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
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
	case StatusDone, StatusBlocked:
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
			title = ?, body = ?, status = ?, type = ?, project = ?,
			worktree_path = ?, branch_name = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, t.Title, t.Body, t.Status, t.Type, t.Project,
		t.WorktreePath, t.BranchName, t.ID)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
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
		SELECT id, title, body, status, type, project,
		       worktree_path, branch_name,
		       created_at, updated_at, started_at, completed_at
		FROM tasks
		WHERE status = ?
		ORDER BY created_at ASC
		LIMIT 1
	`, StatusQueued).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project,
		&t.WorktreePath, &t.BranchName,
		&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
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
		SELECT id, title, body, status, type, project,
		       worktree_path, branch_name,
		       created_at, updated_at, started_at, completed_at
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
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project,
			&t.WorktreePath, &t.BranchName,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// GetTasksWithBranches returns tasks that have a branch name and aren't done.
// These are candidates for automatic closure when their PR is merged.
func (db *DB) GetTasksWithBranches() ([]*Task, error) {
	rows, err := db.Query(`
		SELECT id, title, body, status, type, project,
		       worktree_path, branch_name,
		       created_at, updated_at, started_at, completed_at
		FROM tasks
		WHERE branch_name != '' AND status != ?
		ORDER BY created_at DESC
	`, StatusDone)
	if err != nil {
		return nil, fmt.Errorf("query tasks with branches: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		if err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project,
			&t.WorktreePath, &t.BranchName,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
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

// GetTaskLogs retrieves logs for a task.
func (db *DB) GetTaskLogs(taskID int64, limit int) ([]*TaskLog, error) {
	if limit <= 0 {
		limit = 1000
	}

	rows, err := db.Query(`
		SELECT id, task_id, line_type, content, created_at
		FROM task_logs
		WHERE task_id = ?
		ORDER BY id ASC
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
		INSERT INTO projects (name, path, aliases, instructions, actions)
		VALUES (?, ?, ?, ?, ?)
	`, p.Name, p.Path, p.Aliases, p.Instructions, string(actionsJSON))
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
		UPDATE projects SET name = ?, path = ?, aliases = ?, instructions = ?, actions = ?
		WHERE id = ?
	`, p.Name, p.Path, p.Aliases, p.Instructions, string(actionsJSON), p.ID)
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

// ListProjects returns all projects.
func (db *DB) ListProjects() ([]*Project, error) {
	rows, err := db.Query(`
		SELECT id, name, path, aliases, instructions, COALESCE(actions, '[]'), created_at
		FROM projects ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		p := &Project{}
		var actionsJSON string
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &actionsJSON, &p.CreatedAt); err != nil {
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
		SELECT id, name, path, aliases, instructions, COALESCE(actions, '[]'), created_at
		FROM projects WHERE name = ?
	`, name).Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &actionsJSON, &p.CreatedAt)
	if err == nil {
		json.Unmarshal([]byte(actionsJSON), &p.Actions)
		return p, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("query project: %w", err)
	}

	// Try alias match
	rows, err := db.Query(`SELECT id, name, path, aliases, instructions, COALESCE(actions, '[]'), created_at FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &actionsJSON, &p.CreatedAt); err != nil {
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
