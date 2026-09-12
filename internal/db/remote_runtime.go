package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

var remoteMigrations = []string{
	`CREATE TABLE IF NOT EXISTS remote_runs (task_id INTEGER PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE, run_id TEXT NOT NULL, host TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS remote_events (event_id TEXT PRIMARY KEY, task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, run_id TEXT NOT NULL, host TEXT NOT NULL, kind TEXT NOT NULL, detail TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS remote_events_run ON remote_events(task_id, run_id)`,
	`CREATE TABLE IF NOT EXISTS remote_hosts (host TEXT PRIMARY KEY, last_seen TEXT NOT NULL DEFAULT '', problem TEXT NOT NULL DEFAULT '')`,
}

func remoteID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// CoordinatorID is stable across daemon restarts and scoped to this database.
func (db *DB) CoordinatorID() (string, error) {
	id, err := remoteID()
	if err != nil {
		return "", err
	}
	if _, err = db.Exec(`INSERT OR IGNORE INTO settings(key,value) VALUES('remote_coordinator_id',?)`, id); err != nil {
		return "", err
	}
	err = db.QueryRow(`SELECT value FROM settings WHERE key='remote_coordinator_id'`).Scan(&id)
	return id, err
}

// BeginRemoteRun fences signals from every earlier attempt of the same task.
func (db *DB) BeginRemoteRun(taskID int64, host string) (string, error) {
	id, err := remoteID()
	if err != nil {
		return "", err
	}
	_, err = db.Exec(`INSERT INTO remote_runs(task_id,run_id,host) VALUES(?,?,?) ON CONFLICT(task_id) DO UPDATE SET run_id=excluded.run_id,host=excluded.host`, taskID, id, host)
	return id, err
}

// RemoteEvent is a durable inbox entry. The sender retains it until this DB
// accepts it; reads are repeatable until the task's next run replaces its fence.
type RemoteEvent struct {
	ID     string
	TaskID int64
	RunID  string
	Host   string
	Kind   string
	Detail string
}

// SaveRemoteEvent deduplicates delivery and discards signals from old runs or
// other hosts. Success is permission to acknowledge the remote spool file.
func (db *DB) SaveRemoteEvent(ev RemoteEvent) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO remote_events(event_id,task_id,run_id,host,kind,detail)
 SELECT ?,task_id,run_id,host,?,? FROM remote_runs WHERE task_id=? AND run_id=? AND host=?`, ev.ID, ev.Kind, ev.Detail, ev.TaskID, ev.RunID, ev.Host)
	return err
}

// RemoteSignal returns the latest persisted event for the current host and run.
// It is deliberately not deleted: a crash before finalization must replay it.
func (db *DB) RemoteSignal(taskID int64, host string) (RemoteEvent, bool, error) {
	var ev RemoteEvent
	err := db.QueryRow(`SELECT e.event_id,e.task_id,e.run_id,e.host,e.kind,e.detail FROM remote_events e JOIN remote_runs r ON r.task_id=e.task_id AND r.run_id=e.run_id AND r.host=e.host WHERE e.task_id=? AND e.host=? ORDER BY e.rowid DESC LIMIT 1`, taskID, host).Scan(&ev.ID, &ev.TaskID, &ev.RunID, &ev.Host, &ev.Kind, &ev.Detail)
	if errors.Is(err, sql.ErrNoRows) {
		return ev, false, nil
	}
	return ev, err == nil, err
}

// HostHealth keeps transport health separate from task state and is shared by
// the daemon, HTTP server and TUI through the coordinator database.
type HostHealth struct {
	State    string `json:"state"`
	LastSeen string `json:"last_seen,omitempty"`
	Problem  string `json:"problem,omitempty"`
}

func (db *DB) RecordHostHealth(host, problem string, observed bool) error {
	seen := ""
	if observed {
		seen = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := db.Exec(`INSERT INTO remote_hosts(host,last_seen,problem) VALUES(?,?,?) ON CONFLICT(host) DO UPDATE SET last_seen=CASE WHEN excluded.last_seen='' THEN remote_hosts.last_seen ELSE excluded.last_seen END,problem=excluded.problem`, host, seen, problem)
	return err
}

func (db *DB) RemoteHostHealth(host string) (HostHealth, error) {
	h := HostHealth{State: "unknown"}
	if host == "" {
		h.State = "local"
		return h, nil
	}
	err := db.QueryRow(`SELECT last_seen,problem FROM remote_hosts WHERE host=?`, host).Scan(&h.LastSeen, &h.Problem)
	if errors.Is(err, sql.ErrNoRows) {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	seen, _ := time.Parse(time.RFC3339Nano, h.LastSeen)
	if h.Problem != "" || time.Since(seen) > 30*time.Second {
		h.State = "reconnecting"
	} else {
		h.State = "online"
	}
	return h, nil
}

// CommitTaskPlacement atomically publishes a carried branch and its destination,
// clears stale remote references, and fences the outgoing run's late signals.
func (db *DB) CommitTaskPlacement(taskID int64, target, reason, workDir, branch string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE tasks SET placement_target=?,placement_reason=?,placement_workdir=?,placement_decided_at=CURRENT_TIMESTAMP,
 remote_worktree_path='',remote_branch='',
 source_branch=CASE WHEN ?='' THEN source_branch ELSE ? END,
 branch_name=CASE WHEN ?='' THEN branch_name ELSE ? END
 WHERE id=?`, target, reason, workDir, branch, branch, branch, branch, taskID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM remote_runs WHERE task_id=?`, taskID); err != nil {
		return err
	}
	return tx.Commit()
}
