package db

import (
	"database/sql"
	"fmt"
)

// SetStepVerify records the opt-in evidence-gate command for a workflow step,
// keyed by the step's task id. It is written once at workflow-build time (see
// pipeline.Create) for steps whose YAML sets `verify:`. A second write overwrites.
func (db *DB) SetStepVerify(taskID int64, command string) error {
	_, err := db.Exec(`
		INSERT INTO pipeline_step_verify (task_id, command)
		VALUES (?, ?)
		ON CONFLICT(task_id) DO UPDATE SET command = excluded.command
	`, taskID, command)
	if err != nil {
		return fmt.Errorf("set step verify: %w", err)
	}
	return nil
}

// GetStepVerify returns the evidence-gate command for a step task, or "" when the
// step has no gate (the common case) — so callers treat "no row" as "no gate".
func (db *DB) GetStepVerify(taskID int64) (string, error) {
	var command string
	err := db.QueryRow(`SELECT command FROM pipeline_step_verify WHERE task_id = ?`, taskID).Scan(&command)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get step verify: %w", err)
	}
	return command, nil
}
