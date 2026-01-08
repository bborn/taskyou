package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Task represents a task in the database.
type Task struct {
	ID          int64
	Title       string
	Body        string
	Status      string
	Type        string
	Project     string
	Priority    string
	Model       string // claude, crush, codex, gemini
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
}

// Task statuses
const (
	StatusPending    = "pending"    // Created but not queued
	StatusQueued     = "queued"     // Waiting to be processed
	StatusProcessing = "processing" // Currently being worked on
	StatusReady      = "ready"      // Completed successfully
	StatusBlocked    = "blocked"    // Needs input/clarification
	StatusClosed     = "closed"     // Done and closed
)

// Task types
const (
	TypeCode     = "code"
	TypeWriting  = "writing"
	TypeThinking = "thinking"
)

// Model options
const (
	ModelClaude = "claude"
	ModelCrush  = "crush"
	ModelCodex  = "codex"
	ModelGemini = "gemini"
)

// CreateTask creates a new task.
func (db *DB) CreateTask(t *Task) error {
	if t.Model == "" {
		t.Model = ModelClaude // default
	}
	result, err := db.Exec(`
		INSERT INTO tasks (title, body, status, type, project, priority, model)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, t.Title, t.Body, t.Status, t.Type, t.Project, t.Priority, t.Model)
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
		SELECT id, title, body, status, type, project, priority,
		       created_at, updated_at, started_at, completed_at
		FROM tasks WHERE id = ?
	`, id).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Priority,
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
	Status   string
	Type     string
	Project  string
	Priority string
	Limit    int
	Offset   int
}

// ListTasks retrieves tasks with optional filters.
func (db *DB) ListTasks(opts ListTasksOptions) ([]*Task, error) {
	query := `
		SELECT id, title, body, status, type, project, priority,
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
	if opts.Priority != "" {
		query += " AND priority = ?"
		args = append(args, opts.Priority)
	}

	// Exclude closed by default unless specifically querying for them
	if opts.Status == "" {
		query += " AND status != 'closed'"
	}

	query += " ORDER BY CASE priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END, created_at DESC"

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
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Priority,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
}

// UpdateTaskStatus updates a task's status.
func (db *DB) UpdateTaskStatus(id int64, status string) error {
	query := "UPDATE tasks SET status = ?, updated_at = CURRENT_TIMESTAMP"
	args := []interface{}{status}

	switch status {
	case StatusProcessing:
		query += ", started_at = CURRENT_TIMESTAMP"
	case StatusReady, StatusBlocked, StatusClosed:
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
			title = ?, body = ?, status = ?, type = ?, project = ?, priority = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, t.Title, t.Body, t.Status, t.Type, t.Project, t.Priority, t.ID)
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
		SELECT id, title, body, status, type, project, priority,
		       created_at, updated_at, started_at, completed_at
		FROM tasks
		WHERE status = ?
		ORDER BY CASE priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END, created_at ASC
		LIMIT 1
	`, StatusQueued).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Priority,
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

// GetQueuedTasks returns all queued tasks.
func (db *DB) GetQueuedTasks() ([]*Task, error) {
	rows, err := db.Query(`
		SELECT id, title, body, status, type, project, priority,
		       created_at, updated_at, started_at, completed_at
		FROM tasks
		WHERE status = ?
		ORDER BY 
			CASE priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END,
			created_at ASC
	`, StatusQueued)
	if err != nil {
		return nil, fmt.Errorf("query queued tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		if err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Priority,
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
	CreatedAt time.Time
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

// Project represents a configured project.
type Project struct {
	ID           int64
	Name         string
	Path         string
	Aliases      string // comma-separated
	Instructions string // project-specific instructions for AI
	CreatedAt    time.Time
}

// CreateProject creates a new project.
func (db *DB) CreateProject(p *Project) error {
	result, err := db.Exec(`
		INSERT INTO projects (name, path, aliases, instructions)
		VALUES (?, ?, ?, ?)
	`, p.Name, p.Path, p.Aliases, p.Instructions)
	if err != nil {
		return fmt.Errorf("insert project: %w", err)
	}
	id, _ := result.LastInsertId()
	p.ID = id
	return nil
}

// UpdateProject updates a project.
func (db *DB) UpdateProject(p *Project) error {
	_, err := db.Exec(`
		UPDATE projects SET name = ?, path = ?, aliases = ?, instructions = ?
		WHERE id = ?
	`, p.Name, p.Path, p.Aliases, p.Instructions, p.ID)
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	return nil
}

// DeleteProject deletes a project.
func (db *DB) DeleteProject(id int64) error {
	_, err := db.Exec("DELETE FROM projects WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}

// ListProjects returns all projects.
func (db *DB) ListProjects() ([]*Project, error) {
	rows, err := db.Query(`
		SELECT id, name, path, aliases, instructions, created_at
		FROM projects ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// GetProjectByName returns a project by name or alias.
func (db *DB) GetProjectByName(name string) (*Project, error) {
	// First try exact name match
	p := &Project{}
	err := db.QueryRow(`
		SELECT id, name, path, aliases, instructions, created_at
		FROM projects WHERE name = ?
	`, name).Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &p.CreatedAt)
	if err == nil {
		return p, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("query project: %w", err)
	}

	// Try alias match
	rows, err := db.Query(`SELECT id, name, path, aliases, instructions, created_at FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Aliases, &p.Instructions, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
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
