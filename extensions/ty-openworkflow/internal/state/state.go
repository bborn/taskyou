// Package state manages persistent state for workflow runs and task associations.
package state

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB manages the SQLite state database.
type DB struct {
	db *sql.DB
}

// WorkflowRun represents a workflow run record in the database.
type WorkflowRun struct {
	ID          string
	WorkflowID  string
	TaskID      int64 // Associated TaskYou task
	Adapter     string
	Status      string
	Input       string // JSON
	Output      string // JSON
	Error       string
	StartedAt   time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Workflow represents a deployed workflow definition.
type Workflow struct {
	ID          string
	Name        string
	Description string
	Version     string
	Runtime     string
	Code        string
	Adapter     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Open opens or creates the state database.
func Open(dir string) (*DB, error) {
	dbPath := filepath.Join(dir, "state.db")

	db, err := sql.Open("sqlite", dbPath+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &DB{db: db}, nil
}

// migrate creates the database schema.
func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS workflows (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT,
		version TEXT NOT NULL,
		runtime TEXT NOT NULL,
		code TEXT NOT NULL,
		adapter TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS workflow_runs (
		id TEXT PRIMARY KEY,
		workflow_id TEXT NOT NULL,
		task_id INTEGER,
		adapter TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		input TEXT,
		output TEXT,
		error TEXT,
		started_at DATETIME,
		completed_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (workflow_id) REFERENCES workflows(id)
	);

	CREATE INDEX IF NOT EXISTS idx_workflow_runs_workflow_id ON workflow_runs(workflow_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_runs_task_id ON workflow_runs(task_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_runs_status ON workflow_runs(status);

	CREATE TABLE IF NOT EXISTS task_workflow_mapping (
		task_id INTEGER PRIMARY KEY,
		workflow_id TEXT NOT NULL,
		run_id TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (workflow_id) REFERENCES workflows(id)
	);
	`

	_, err := db.Exec(schema)
	return err
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// SaveWorkflow saves or updates a workflow definition.
func (d *DB) SaveWorkflow(w *Workflow) error {
	_, err := d.db.Exec(`
		INSERT INTO workflows (id, name, description, version, runtime, code, adapter, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			version = excluded.version,
			runtime = excluded.runtime,
			code = excluded.code,
			adapter = excluded.adapter,
			updated_at = CURRENT_TIMESTAMP
	`, w.ID, w.Name, w.Description, w.Version, w.Runtime, w.Code, w.Adapter)

	return err
}

// GetWorkflow retrieves a workflow by ID.
func (d *DB) GetWorkflow(id string) (*Workflow, error) {
	row := d.db.QueryRow(`
		SELECT id, name, description, version, runtime, code, adapter, created_at, updated_at
		FROM workflows WHERE id = ?
	`, id)

	w := &Workflow{}
	err := row.Scan(&w.ID, &w.Name, &w.Description, &w.Version, &w.Runtime, &w.Code, &w.Adapter, &w.CreatedAt, &w.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return w, nil
}

// ListWorkflows returns all workflows.
func (d *DB) ListWorkflows() ([]*Workflow, error) {
	rows, err := d.db.Query(`
		SELECT id, name, description, version, runtime, code, adapter, created_at, updated_at
		FROM workflows ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workflows []*Workflow
	for rows.Next() {
		w := &Workflow{}
		if err := rows.Scan(&w.ID, &w.Name, &w.Description, &w.Version, &w.Runtime, &w.Code, &w.Adapter, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		workflows = append(workflows, w)
	}

	return workflows, rows.Err()
}

// DeleteWorkflow deletes a workflow.
func (d *DB) DeleteWorkflow(id string) error {
	_, err := d.db.Exec("DELETE FROM workflows WHERE id = ?", id)
	return err
}

// SaveRun saves a workflow run.
func (d *DB) SaveRun(r *WorkflowRun) error {
	_, err := d.db.Exec(`
		INSERT INTO workflow_runs (id, workflow_id, task_id, adapter, status, input, output, error, started_at, completed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			status = excluded.status,
			output = excluded.output,
			error = excluded.error,
			completed_at = excluded.completed_at,
			updated_at = CURRENT_TIMESTAMP
	`, r.ID, r.WorkflowID, r.TaskID, r.Adapter, r.Status, r.Input, r.Output, r.Error, r.StartedAt, r.CompletedAt)

	return err
}

// GetRun retrieves a workflow run by ID.
func (d *DB) GetRun(id string) (*WorkflowRun, error) {
	row := d.db.QueryRow(`
		SELECT id, workflow_id, task_id, adapter, status, input, output, error, started_at, completed_at, created_at, updated_at
		FROM workflow_runs WHERE id = ?
	`, id)

	r := &WorkflowRun{}
	err := row.Scan(&r.ID, &r.WorkflowID, &r.TaskID, &r.Adapter, &r.Status, &r.Input, &r.Output, &r.Error, &r.StartedAt, &r.CompletedAt, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return r, nil
}

// ListRuns returns workflow runs, optionally filtered.
func (d *DB) ListRuns(workflowID string, status string, limit int) ([]*WorkflowRun, error) {
	query := "SELECT id, workflow_id, task_id, adapter, status, input, output, error, started_at, completed_at, created_at, updated_at FROM workflow_runs WHERE 1=1"
	args := []any{}

	if workflowID != "" {
		query += " AND workflow_id = ?"
		args = append(args, workflowID)
	}

	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}

	query += " ORDER BY created_at DESC"

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*WorkflowRun
	for rows.Next() {
		r := &WorkflowRun{}
		if err := rows.Scan(&r.ID, &r.WorkflowID, &r.TaskID, &r.Adapter, &r.Status, &r.Input, &r.Output, &r.Error, &r.StartedAt, &r.CompletedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}

	return runs, rows.Err()
}

// GetRunByTaskID retrieves a workflow run by its associated task ID.
func (d *DB) GetRunByTaskID(taskID int64) (*WorkflowRun, error) {
	row := d.db.QueryRow(`
		SELECT id, workflow_id, task_id, adapter, status, input, output, error, started_at, completed_at, created_at, updated_at
		FROM workflow_runs WHERE task_id = ?
		ORDER BY created_at DESC LIMIT 1
	`, taskID)

	r := &WorkflowRun{}
	err := row.Scan(&r.ID, &r.WorkflowID, &r.TaskID, &r.Adapter, &r.Status, &r.Input, &r.Output, &r.Error, &r.StartedAt, &r.CompletedAt, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return r, nil
}

// LinkTaskToWorkflow associates a task with a workflow.
func (d *DB) LinkTaskToWorkflow(taskID int64, workflowID, runID string) error {
	_, err := d.db.Exec(`
		INSERT INTO task_workflow_mapping (task_id, workflow_id, run_id)
		VALUES (?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET
			workflow_id = excluded.workflow_id,
			run_id = excluded.run_id
	`, taskID, workflowID, runID)

	return err
}

// GetTaskWorkflow retrieves the workflow associated with a task.
func (d *DB) GetTaskWorkflow(taskID int64) (workflowID, runID string, err error) {
	row := d.db.QueryRow(`
		SELECT workflow_id, run_id FROM task_workflow_mapping WHERE task_id = ?
	`, taskID)

	err = row.Scan(&workflowID, &runID)
	if err == sql.ErrNoRows {
		return "", "", nil
	}

	return
}

// GetPendingRuns returns runs that are still in progress.
func (d *DB) GetPendingRuns() ([]*WorkflowRun, error) {
	return d.ListRuns("", "running", 0)
}

// UpdateRunStatus updates a run's status and optionally output/error.
func (d *DB) UpdateRunStatus(runID, status, output, errMsg string) error {
	now := time.Now()
	var completedAt *time.Time
	if status == "completed" || status == "failed" || status == "canceled" {
		completedAt = &now
	}

	_, err := d.db.Exec(`
		UPDATE workflow_runs
		SET status = ?, output = ?, error = ?, completed_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, output, errMsg, completedAt, runID)

	return err
}
