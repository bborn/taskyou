package db

import (
	"database/sql"
	"fmt"
)

// Routine run statuses.
const (
	RoutineRunStatusRunning = "running"
	RoutineRunStatusOK      = "ok"
	RoutineRunStatusFailed  = "failed"
)

// RoutineRun is one recorded execution of a routine (see internal/routine).
// Routines themselves live on disk; only run history lives in the database.
type RoutineRun struct {
	ID         int64      `json:"id"`
	Routine    string     `json:"routine"`
	Status     string     `json:"status"` // running, ok, failed
	ExitCode   int        `json:"exit_code"`
	Output     string     `json:"output"` // tail of combined output; full log on disk
	LogPath    string     `json:"log_path"`
	StartedAt  LocalTime  `json:"started_at"`
	FinishedAt *LocalTime `json:"finished_at,omitempty"`
}

// CreateRoutineRun inserts a new run in the running state and returns its ID.
func (db *DB) CreateRoutineRun(routine string) (int64, error) {
	result, err := db.Exec(`
		INSERT INTO routine_runs (routine, status)
		VALUES (?, 'running')
	`, routine)
	if err != nil {
		return 0, fmt.Errorf("create routine run: %w", err)
	}
	return result.LastInsertId()
}

// SetRoutineRunLogPath records where the run's full log lives.
func (db *DB) SetRoutineRunLogPath(id int64, logPath string) error {
	_, err := db.Exec(`UPDATE routine_runs SET log_path = ? WHERE id = ?`, logPath, id)
	if err != nil {
		return fmt.Errorf("set routine run log path: %w", err)
	}
	return nil
}

// FinishRoutineRun records a run's terminal state.
func (db *DB) FinishRoutineRun(id int64, status string, exitCode int, output string) error {
	_, err := db.Exec(`
		UPDATE routine_runs
		SET status = ?, exit_code = ?, output = ?, finished_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, exitCode, output, id)
	if err != nil {
		return fmt.Errorf("finish routine run: %w", err)
	}
	return nil
}

// GetRoutineRun fetches a single run by ID.
func (db *DB) GetRoutineRun(id int64) (*RoutineRun, error) {
	row := db.QueryRow(`
		SELECT id, routine, status, exit_code, output, log_path, started_at, finished_at
		FROM routine_runs WHERE id = ?
	`, id)
	run, err := scanRoutineRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return run, err
}

// ListRoutineRuns returns the most recent runs for a routine, newest first.
func (db *DB) ListRoutineRuns(routine string, limit int) ([]*RoutineRun, error) {
	rows, err := db.Query(`
		SELECT id, routine, status, exit_code, output, log_path, started_at, finished_at
		FROM routine_runs
		WHERE routine = ?
		ORDER BY id DESC
		LIMIT ?
	`, routine, limit)
	if err != nil {
		return nil, fmt.Errorf("list routine runs: %w", err)
	}
	defer rows.Close()
	return collectRoutineRuns(rows)
}

// LatestRoutineRuns returns the most recent run per routine, keyed by name.
func (db *DB) LatestRoutineRuns() (map[string]*RoutineRun, error) {
	rows, err := db.Query(`
		SELECT id, routine, status, exit_code, output, log_path, started_at, finished_at
		FROM routine_runs
		WHERE id IN (SELECT MAX(id) FROM routine_runs GROUP BY routine)
	`)
	if err != nil {
		return nil, fmt.Errorf("latest routine runs: %w", err)
	}
	defer rows.Close()

	runs, err := collectRoutineRuns(rows)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]*RoutineRun, len(runs))
	for _, run := range runs {
		latest[run.Routine] = run
	}
	return latest, nil
}

// DeleteRoutineRuns removes all recorded runs for a routine. Used when the
// routine itself is deleted so run history doesn't orphan.
func (db *DB) DeleteRoutineRuns(routine string) error {
	_, err := db.Exec(`DELETE FROM routine_runs WHERE routine = ?`, routine)
	if err != nil {
		return fmt.Errorf("delete routine runs: %w", err)
	}
	return nil
}

// HasOpenTaskWithTitle reports whether a non-done, non-archived task with the
// exact title exists. Used to dedupe automatically created alert tasks.
func (db *DB) HasOpenTaskWithTitle(title string) (bool, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM tasks
		WHERE title = ? AND status NOT IN ('done', 'archived')
	`, title).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check open task by title: %w", err)
	}
	return count > 0, nil
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanRoutineRun(row rowScanner) (*RoutineRun, error) {
	var run RoutineRun
	err := row.Scan(&run.ID, &run.Routine, &run.Status, &run.ExitCode,
		&run.Output, &run.LogPath, &run.StartedAt, &run.FinishedAt)
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func collectRoutineRuns(rows *sql.Rows) ([]*RoutineRun, error) {
	var runs []*RoutineRun
	for rows.Next() {
		run, err := scanRoutineRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan routine run: %w", err)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}
