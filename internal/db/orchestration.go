package db

import (
	"fmt"
)

// WorkflowStatus represents the aggregate status of a parent task's subtasks.
type WorkflowStatus struct {
	ParentID    int64  `json:"parent_id"`
	ParentTitle string `json:"parent_title"`
	Total       int    `json:"total"`
	Pending     int    `json:"pending"`     // backlog + queued
	Processing  int    `json:"processing"`  // currently executing
	Blocked     int    `json:"blocked"`     // waiting for input
	Done        int    `json:"done"`        // completed
	Archived    int    `json:"archived"`    // archived
	IsComplete  bool   `json:"is_complete"` // all subtasks are done or archived
}

// GetSubtasks returns all child tasks of a parent task.
func (db *DB) GetSubtasks(parentID int64) ([]*Task, error) {
	rows, err := db.Query(`
		SELECT id, title, body, status, type, project, COALESCE(executor, 'claude'),
		       worktree_path, branch_name, port, claude_session_id,
		       COALESCE(daemon_session, ''), COALESCE(tmux_window_id, ''),
		       COALESCE(claude_pane_id, ''), COALESCE(shell_pane_id, ''),
		       COALESCE(pr_url, ''), COALESCE(pr_number, 0),
		       COALESCE(dangerous_mode, 0), COALESCE(pinned, 0), COALESCE(tags, ''),
		       COALESCE(source_branch, ''), COALESCE(summary, ''),
		       parent_id, COALESCE(output, ''),
		       created_at, updated_at, started_at, completed_at,
		       last_distilled_at, last_accessed_at,
		       COALESCE(archive_ref, ''), COALESCE(archive_commit, ''),
		       COALESCE(archive_worktree_path, ''), COALESCE(archive_branch_name, '')
		FROM tasks
		WHERE parent_id = ?
		ORDER BY id ASC
	`, parentID)
	if err != nil {
		return nil, fmt.Errorf("get subtasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Type, &t.Project, &t.Executor,
			&t.WorktreePath, &t.BranchName, &t.Port, &t.ClaudeSessionID,
			&t.DaemonSession, &t.TmuxWindowID, &t.ClaudePaneID, &t.ShellPaneID,
			&t.PRURL, &t.PRNumber,
			&t.DangerousMode, &t.Pinned, &t.Tags,
			&t.SourceBranch, &t.Summary,
			&t.ParentID, &t.Output,
			&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.CompletedAt,
			&t.LastDistilledAt, &t.LastAccessedAt,
			&t.ArchiveRef, &t.ArchiveCommit, &t.ArchiveWorktreePath, &t.ArchiveBranchName,
		)
		if err != nil {
			return nil, fmt.Errorf("scan subtask: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// GetSubtaskCount returns the number of subtasks for a parent task.
func (db *DB) GetSubtaskCount(parentID int64) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE parent_id = ?`, parentID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count subtasks: %w", err)
	}
	return count, nil
}

// SetTaskOutput stores output/results for a task.
// This is used in orchestration to pass context between tasks.
func (db *DB) SetTaskOutput(taskID int64, output string) error {
	_, err := db.Exec(`
		UPDATE tasks SET output = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, output, taskID)
	if err != nil {
		return fmt.Errorf("set task output: %w", err)
	}
	return nil
}

// GetTaskOutput retrieves the output for a task.
func (db *DB) GetTaskOutput(taskID int64) (string, error) {
	var output string
	err := db.QueryRow(`SELECT COALESCE(output, '') FROM tasks WHERE id = ?`, taskID).Scan(&output)
	if err != nil {
		return "", fmt.Errorf("get task output: %w", err)
	}
	return output, nil
}

// GetWorkflowStatus returns the aggregate status of all subtasks for a parent task.
func (db *DB) GetWorkflowStatus(parentID int64) (*WorkflowStatus, error) {
	parent, err := db.GetTask(parentID)
	if err != nil {
		return nil, fmt.Errorf("get parent task: %w", err)
	}
	if parent == nil {
		return nil, fmt.Errorf("parent task %d not found", parentID)
	}

	status := &WorkflowStatus{
		ParentID:    parentID,
		ParentTitle: parent.Title,
	}

	rows, err := db.Query(`
		SELECT status, COUNT(*) as count
		FROM tasks
		WHERE parent_id = ?
		GROUP BY status
	`, parentID)
	if err != nil {
		return nil, fmt.Errorf("get workflow status: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var s string
		var count int
		if err := rows.Scan(&s, &count); err != nil {
			return nil, fmt.Errorf("scan workflow status: %w", err)
		}

		status.Total += count
		switch s {
		case StatusBacklog, StatusQueued:
			status.Pending += count
		case StatusProcessing:
			status.Processing += count
		case StatusBlocked:
			status.Blocked += count
		case StatusDone:
			status.Done += count
		case StatusArchived:
			status.Archived += count
		}
	}

	// Workflow is complete when all subtasks are done or archived
	status.IsComplete = status.Total > 0 && (status.Done+status.Archived) == status.Total

	return status, nil
}

// CheckAndCompleteParent checks if all subtasks of a parent are done/archived,
// and if so, updates the parent task status to done.
// Returns true if the parent was completed.
func (db *DB) CheckAndCompleteParent(parentID int64) (bool, error) {
	status, err := db.GetWorkflowStatus(parentID)
	if err != nil {
		return false, err
	}

	if !status.IsComplete {
		return false, nil
	}

	// Get parent task to check its current status
	parent, err := db.GetTask(parentID)
	if err != nil {
		return false, fmt.Errorf("get parent task: %w", err)
	}
	if parent == nil {
		return false, nil
	}

	// Only auto-complete if parent is still in a pending/processing state
	if parent.Status == StatusDone || parent.Status == StatusArchived {
		return false, nil
	}

	// Collect outputs from all subtasks for the parent's summary
	subtasks, err := db.GetSubtasks(parentID)
	if err != nil {
		return false, fmt.Errorf("get subtasks for summary: %w", err)
	}

	var summaryParts []string
	for _, st := range subtasks {
		if st.Output != "" {
			summaryParts = append(summaryParts, fmt.Sprintf("## Subtask #%d: %s\n%s", st.ID, st.Title, st.Output))
		}
	}

	// Set workflow completion output on parent
	if len(summaryParts) > 0 {
		workflowOutput := fmt.Sprintf("Workflow completed: %d/%d subtasks done.\n\n", status.Done, status.Total)
		for _, part := range summaryParts {
			workflowOutput += part + "\n\n"
		}
		db.SetTaskOutput(parentID, workflowOutput)
	}

	// Log completion
	db.AppendTaskLog(parentID, "system", fmt.Sprintf("All %d subtask(s) completed. Workflow auto-completing.", status.Total))

	// Mark parent as done
	if err := db.UpdateTaskStatus(parentID, StatusDone); err != nil {
		return false, fmt.Errorf("complete parent: %w", err)
	}

	return true, nil
}
