package db

import (
	"database/sql"
	"fmt"
)

// TaskPane represents a tmux pane associated with a task.
// Each task can have multiple panes of different types (shell, claude, etc.).
type TaskPane struct {
	ID        int64
	TaskID    int64
	PaneID    string // tmux pane ID (e.g., "%1234")
	PaneType  string // "claude", "shell", "claude-extra", "shell-extra"
	Title     string // optional custom title
	CreatedAt LocalTime
}

// Pane types
const (
	PaneTypeClaude      = "claude"       // Primary Claude/executor pane
	PaneTypeShell       = "shell"        // Primary shell pane
	PaneTypeClaudeExtra = "claude-extra" // Additional Claude panes
	PaneTypeShellExtra  = "shell-extra"  // Additional shell panes
)

// CreateTaskPane creates a new pane record for a task.
func (db *DB) CreateTaskPane(pane *TaskPane) error {
	result, err := db.Exec(`
		INSERT INTO task_panes (task_id, pane_id, pane_type, title)
		VALUES (?, ?, ?, ?)
	`, pane.TaskID, pane.PaneID, pane.PaneType, pane.Title)
	if err != nil {
		return fmt.Errorf("insert task pane: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}
	pane.ID = id
	return nil
}

// GetTaskPanes retrieves all panes for a task.
func (db *DB) GetTaskPanes(taskID int64) ([]*TaskPane, error) {
	rows, err := db.Query(`
		SELECT id, task_id, pane_id, pane_type, COALESCE(title, ''), created_at
		FROM task_panes
		WHERE task_id = ?
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query task panes: %w", err)
	}
	defer rows.Close()

	var panes []*TaskPane
	for rows.Next() {
		p := &TaskPane{}
		if err := rows.Scan(&p.ID, &p.TaskID, &p.PaneID, &p.PaneType, &p.Title, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan task pane: %w", err)
		}
		panes = append(panes, p)
	}
	return panes, nil
}

// GetTaskPaneByID retrieves a specific pane by ID.
func (db *DB) GetTaskPaneByID(id int64) (*TaskPane, error) {
	p := &TaskPane{}
	err := db.QueryRow(`
		SELECT id, task_id, pane_id, pane_type, COALESCE(title, ''), created_at
		FROM task_panes
		WHERE id = ?
	`, id).Scan(&p.ID, &p.TaskID, &p.PaneID, &p.PaneType, &p.Title, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query task pane: %w", err)
	}
	return p, nil
}

// UpdateTaskPaneTitle updates the title of a pane.
func (db *DB) UpdateTaskPaneTitle(id int64, title string) error {
	_, err := db.Exec(`
		UPDATE task_panes SET title = ?
		WHERE id = ?
	`, title, id)
	if err != nil {
		return fmt.Errorf("update task pane title: %w", err)
	}
	return nil
}

// DeleteTaskPane deletes a pane record.
func (db *DB) DeleteTaskPane(id int64) error {
	_, err := db.Exec("DELETE FROM task_panes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete task pane: %w", err)
	}
	return nil
}

// DeleteTaskPaneByPaneID deletes a pane by its tmux pane ID.
func (db *DB) DeleteTaskPaneByPaneID(taskID int64, paneID string) error {
	_, err := db.Exec("DELETE FROM task_panes WHERE task_id = ? AND pane_id = ?", taskID, paneID)
	if err != nil {
		return fmt.Errorf("delete task pane by pane id: %w", err)
	}
	return nil
}

// ClearTaskPanes deletes all panes for a task.
func (db *DB) ClearTaskPanes(taskID int64) error {
	_, err := db.Exec("DELETE FROM task_panes WHERE task_id = ?", taskID)
	if err != nil {
		return fmt.Errorf("clear task panes: %w", err)
	}
	return nil
}

// GetPrimaryPanes returns the primary Claude and Shell panes for a task.
// Returns (claudePane, shellPane, error)
func (db *DB) GetPrimaryPanes(taskID int64) (*TaskPane, *TaskPane, error) {
	panes, err := db.GetTaskPanes(taskID)
	if err != nil {
		return nil, nil, err
	}

	var claudePane, shellPane *TaskPane
	for _, p := range panes {
		if p.PaneType == PaneTypeClaude && claudePane == nil {
			claudePane = p
		} else if p.PaneType == PaneTypeShell && shellPane == nil {
			shellPane = p
		}
	}
	return claudePane, shellPane, nil
}

// SyncPaneFromTask syncs the primary pane IDs from the task table to task_panes.
// This is used during migration to populate the task_panes table from existing data.
func (db *DB) SyncPaneFromTask(taskID int64, claudePaneID, shellPaneID string) error {
	// Clear existing primary panes
	_, err := db.Exec(`
		DELETE FROM task_panes
		WHERE task_id = ? AND pane_type IN (?, ?)
	`, taskID, PaneTypeClaude, PaneTypeShell)
	if err != nil {
		return fmt.Errorf("clear primary panes: %w", err)
	}

	// Create Claude pane if it exists
	if claudePaneID != "" {
		_, err = db.Exec(`
			INSERT INTO task_panes (task_id, pane_id, pane_type, title)
			VALUES (?, ?, ?, ?)
		`, taskID, claudePaneID, PaneTypeClaude, "Claude")
		if err != nil {
			return fmt.Errorf("insert claude pane: %w", err)
		}
	}

	// Create Shell pane if it exists
	if shellPaneID != "" {
		_, err = db.Exec(`
			INSERT INTO task_panes (task_id, pane_id, pane_type, title)
			VALUES (?, ?, ?, ?)
		`, taskID, shellPaneID, PaneTypeShell, "Shell")
		if err != nil {
			return fmt.Errorf("insert shell pane: %w", err)
		}
	}

	return nil
}
