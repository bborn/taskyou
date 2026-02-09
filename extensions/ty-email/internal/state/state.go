// Package state manages ty-email's persistent state (email threads, processed emails).
package state

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB manages ty-email state.
type DB struct {
	db *sql.DB
}

// EmailThread tracks the mapping between email threads and tasks.
type EmailThread struct {
	EmailThreadID string    // Message-ID or thread reference
	TaskID        int64     // TaskYou task ID
	CreatedAt     time.Time
}

// ProcessedEmail tracks which emails have been handled.
type ProcessedEmail struct {
	EmailID     string    // Message-ID
	TaskID      *int64    // Associated task (if any)
	Action      string    // What action was taken
	ProcessedAt time.Time
}

// OutboundEmail represents a queued outbound email.
type OutboundEmail struct {
	ID        int64
	To        string
	From      string // Reply-from address (e.g., the +ty alias)
	Subject   string
	Body      string
	TaskID    *int64
	InReplyTo string
	Attempts  int
	LastError string
	CreatedAt time.Time
}

// DefaultPath returns the default state database path.
func DefaultPath() string {
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataDir, "ty-email", "state.db")
}

// Open opens or creates the state database.
func Open(path string) (*DB, error) {
	if path == "" {
		path = DefaultPath()
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("failed to open state database: %w", err)
	}

	s := &DB{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate state database: %w", err)
	}

	return s, nil
}

// Close closes the database.
func (s *DB) Close() error {
	return s.db.Close()
}

func (s *DB) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS email_threads (
			email_thread_id TEXT PRIMARY KEY,
			task_id INTEGER NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_email_threads_task_id ON email_threads(task_id);

		CREATE TABLE IF NOT EXISTS processed_emails (
			email_id TEXT PRIMARY KEY,
			task_id INTEGER,
			action TEXT NOT NULL,
			processed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS outbound_queue (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			to_addr TEXT NOT NULL,
			subject TEXT NOT NULL,
			body TEXT NOT NULL,
			task_id INTEGER,
			in_reply_to TEXT,
			attempts INTEGER DEFAULT 0,
			last_error TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_outbound_queue_attempts ON outbound_queue(attempts);
	`)
	if err != nil {
		return err
	}

	// Add from_addr column if it doesn't exist (migration for existing DBs)
	_, _ = s.db.Exec(`ALTER TABLE outbound_queue ADD COLUMN from_addr TEXT DEFAULT ''`)

	return nil
}

// LinkThread associates an email thread with a task.
func (s *DB) LinkThread(emailThreadID string, taskID int64) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO email_threads (email_thread_id, task_id, created_at) VALUES (?, ?, ?)`,
		emailThreadID, taskID, time.Now(),
	)
	return err
}

// GetThreadTask returns the task ID associated with an email thread.
func (s *DB) GetThreadTask(emailThreadID string) (*int64, error) {
	var taskID int64
	err := s.db.QueryRow(
		`SELECT task_id FROM email_threads WHERE email_thread_id = ?`,
		emailThreadID,
	).Scan(&taskID)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &taskID, nil
}

// GetTaskThread returns the email thread ID for a task.
func (s *DB) GetTaskThread(taskID int64) (string, error) {
	var threadID string
	err := s.db.QueryRow(
		`SELECT email_thread_id FROM email_threads WHERE task_id = ?`,
		taskID,
	).Scan(&threadID)

	if err == sql.ErrNoRows {
		return "", nil
	}
	return threadID, err
}

// IsProcessed checks if an email has already been processed.
func (s *DB) IsProcessed(emailID string) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM processed_emails WHERE email_id = ?`,
		emailID,
	).Scan(&count)
	return count > 0, err
}

// MarkProcessed marks an email as processed.
func (s *DB) MarkProcessed(emailID string, taskID *int64, action string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO processed_emails (email_id, task_id, action, processed_at) VALUES (?, ?, ?, ?)`,
		emailID, taskID, action, time.Now(),
	)
	return err
}

// QueueOutbound queues an email for sending.
// The from parameter specifies the reply-from address (e.g., the +ty alias).
// If empty, the adapter's default SMTP from address is used.
func (s *DB) QueueOutbound(to, from, subject, body string, taskID *int64, inReplyTo string) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO outbound_queue (to_addr, from_addr, subject, body, task_id, in_reply_to) VALUES (?, ?, ?, ?, ?, ?)`,
		to, from, subject, body, taskID, inReplyTo,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetPendingOutbound returns emails that need to be sent.
func (s *DB) GetPendingOutbound(maxAttempts int) ([]OutboundEmail, error) {
	rows, err := s.db.Query(
		`SELECT id, to_addr, from_addr, subject, body, task_id, in_reply_to, attempts, last_error, created_at
		 FROM outbound_queue WHERE attempts < ? ORDER BY created_at ASC`,
		maxAttempts,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var emails []OutboundEmail
	for rows.Next() {
		var e OutboundEmail
		var lastError sql.NullString
		var fromAddr sql.NullString
		err := rows.Scan(&e.ID, &e.To, &fromAddr, &e.Subject, &e.Body, &e.TaskID, &e.InReplyTo, &e.Attempts, &lastError, &e.CreatedAt)
		if err != nil {
			return nil, err
		}
		if lastError.Valid {
			e.LastError = lastError.String
		}
		if fromAddr.Valid {
			e.From = fromAddr.String
		}
		emails = append(emails, e)
	}
	return emails, rows.Err()
}

// MarkOutboundSent removes a sent email from the queue.
func (s *DB) MarkOutboundSent(id int64) error {
	_, err := s.db.Exec(`DELETE FROM outbound_queue WHERE id = ?`, id)
	return err
}

// MarkOutboundFailed increments the attempt counter and records the error.
func (s *DB) MarkOutboundFailed(id int64, errMsg string) error {
	_, err := s.db.Exec(
		`UPDATE outbound_queue SET attempts = attempts + 1, last_error = ? WHERE id = ?`,
		errMsg, id,
	)
	return err
}
