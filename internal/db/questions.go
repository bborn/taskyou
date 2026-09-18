package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// A pending question is what an agent asked when it called
// taskyou_needs_input and is still waiting on: the question itself, and — when
// the agent offered them — the answers a person can pick with a tap or a
// keypress instead of typing one out.
//
// It is only meaningful while the task is blocked on it. Leaving blocked clears
// it in the same transaction as the status change (see applyTransition), and
// GetPendingQuestion refuses to return one for a task that is not blocked, so a
// row that outlived its question can never be shown as live.
//
// A table of its own rather than columns on tasks: it is written once per
// question and deleted once per answer, it is empty for almost every task, and
// every board query would otherwise scan it.

// Question kinds. QuestionText is a plain question answered in the person's own
// words — what taskyou_needs_input always was.
const (
	QuestionText        = "text"
	QuestionChoice      = "choice"
	QuestionMultiChoice = "multi_choice"
	QuestionConfirm     = "confirm"
)

// QuestionOption is one answer an agent offered.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// PendingQuestion is an agent's outstanding question on a task.
type PendingQuestion struct {
	// ID changes every time the agent asks, so an answer built against one
	// question can be refused when it arrives after the agent asked another —
	// option 2 of the old question is not option 2 of the new one.
	ID       int64            `json:"id"`
	TaskID   int64            `json:"task_id"`
	Question string           `json:"question"`
	Kind     string           `json:"kind"`
	Options  []QuestionOption `json:"options,omitempty"`
	// AllowOther lets the person answer in their own words instead of (or, for
	// multi_choice, as well as) picking an option.
	AllowOther bool      `json:"allow_other"`
	CreatedAt  LocalTime `json:"created_at"`
}

// SetPendingQuestion records q as the task's outstanding question, replacing
// any earlier one, and sets q.ID and q.CreatedAt. It does not change the task's
// status; the caller moves the task to blocked.
func (db *DB) SetPendingQuestion(q *PendingQuestion) error {
	if q.Kind == "" {
		q.Kind = QuestionText
	}
	opts, err := json.Marshal(q.Options)
	if err != nil {
		return fmt.Errorf("encode question options: %w", err)
	}
	if q.Options == nil {
		opts = []byte("[]")
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin question write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Delete-then-insert rather than an upsert: a new question gets a new id,
	// which is what lets a stale answer be told apart from a current one.
	if _, err := tx.Exec(`DELETE FROM task_questions WHERE task_id = ?`, q.TaskID); err != nil {
		return fmt.Errorf("replace pending question: %w", err)
	}
	res, err := tx.Exec(`
		INSERT INTO task_questions (task_id, question, kind, options, allow_other)
		VALUES (?, ?, ?, ?, ?)
	`, q.TaskID, q.Question, q.Kind, string(opts), q.AllowOther)
	if err != nil {
		return fmt.Errorf("insert pending question: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("pending question id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pending question: %w", err)
	}
	q.ID = id

	stored, err := db.getQuestionRow(q.TaskID)
	if err == nil && stored != nil {
		q.CreatedAt = stored.CreatedAt
	}
	// The status change that usually follows is what wakes the surfaces, but
	// an agent asking again while already blocked changes no status.
	db.questionChanged(q.TaskID, "asked")
	return nil
}

// GetPendingQuestion returns the question the task is blocked on, or nil when
// there is none — including when a row exists but the task is no longer
// blocked, since whatever it asked has been overtaken.
func (db *DB) GetPendingQuestion(taskID int64) (*PendingQuestion, error) {
	var status string
	err := db.QueryRow(`SELECT status FROM tasks WHERE id = ?`, taskID).Scan(&status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read task status: %w", err)
	}
	if status != StatusBlocked {
		return nil, nil
	}
	return db.getQuestionRow(taskID)
}

// getQuestionRow reads the stored row regardless of the task's status.
func (db *DB) getQuestionRow(taskID int64) (*PendingQuestion, error) {
	q := &PendingQuestion{TaskID: taskID}
	var opts string
	err := db.QueryRow(`
		SELECT id, question, kind, options, allow_other, created_at
		FROM task_questions WHERE task_id = ?
	`, taskID).Scan(&q.ID, &q.Question, &q.Kind, &opts, &q.AllowOther, &q.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pending question: %w", err)
	}
	if opts != "" {
		if err := json.Unmarshal([]byte(opts), &q.Options); err != nil {
			return nil, fmt.Errorf("decode question options: %w", err)
		}
	}
	return q, nil
}

// PendingQuestions returns every blocked task's outstanding question, keyed by
// task id — one query for a whole board rather than one per card.
func (db *DB) PendingQuestions() (map[int64]*PendingQuestion, error) {
	rows, err := db.Query(`
		SELECT q.task_id, q.id, q.question, q.kind, q.options, q.allow_other, q.created_at
		FROM task_questions q JOIN tasks t ON t.id = q.task_id
		WHERE t.status = ?
	`, StatusBlocked)
	if err != nil {
		return nil, fmt.Errorf("list pending questions: %w", err)
	}
	defer rows.Close()

	out := map[int64]*PendingQuestion{}
	for rows.Next() {
		q := &PendingQuestion{}
		var opts string
		if err := rows.Scan(&q.TaskID, &q.ID, &q.Question, &q.Kind, &opts, &q.AllowOther, &q.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan pending question: %w", err)
		}
		if opts != "" {
			if err := json.Unmarshal([]byte(opts), &q.Options); err != nil {
				return nil, fmt.Errorf("decode question options: %w", err)
			}
		}
		out[q.TaskID] = q
	}
	return out, rows.Err()
}

// ClearPendingQuestion drops the task's outstanding question, if any.
func (db *DB) ClearPendingQuestion(taskID int64) error {
	if _, err := db.Exec(`DELETE FROM task_questions WHERE task_id = ?`, taskID); err != nil {
		return fmt.Errorf("clear pending question: %w", err)
	}
	return nil
}

// ClaimPendingQuestion takes question id off the task so exactly one answer is
// delivered for it. It reports false when the question is gone or has been
// replaced — another surface answered first, or the agent asked again.
//
// ty is several processes against one file (the TUI, the daemon's web API, a
// `ty answer` in a shell), so "one answer per question" has to be decided by
// the database, not by any one process's memory: the DELETE is the decision.
func (db *DB) ClaimPendingQuestion(taskID, id int64) (bool, error) {
	res, err := db.Exec(`DELETE FROM task_questions WHERE task_id = ? AND id = ?`, taskID, id)
	if err != nil {
		return false, fmt.Errorf("claim pending question: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim pending question: %w", err)
	}
	if n != 1 {
		return false, nil
	}
	db.questionChanged(taskID, "answered")
	return true, nil
}

// RestorePendingQuestion puts back a question whose claimed answer could not be
// delivered, so the person can try again. It is a no-op when the agent has
// asked something newer in the meantime, or when the task is no longer blocked.
func (db *DB) RestorePendingQuestion(q *PendingQuestion) error {
	opts, err := json.Marshal(q.Options)
	if err != nil {
		return fmt.Errorf("encode question options: %w", err)
	}
	if q.Options == nil {
		opts = []byte("[]")
	}
	created := q.CreatedAt
	res, err := db.Exec(`
		INSERT INTO task_questions (id, task_id, question, kind, options, allow_other, created_at)
		SELECT ?, ?, ?, ?, ?, ?, COALESCE(?, CURRENT_TIMESTAMP)
		WHERE EXISTS (SELECT 1 FROM tasks WHERE id = ? AND status = ?)
		ON CONFLICT DO NOTHING
	`, q.ID, q.TaskID, q.Question, q.Kind, string(opts), q.AllowOther, created,
		q.TaskID, StatusBlocked)
	if err != nil {
		return fmt.Errorf("restore pending question: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 1 {
		db.questionChanged(q.TaskID, "restored")
	}
	return nil
}

// questionChanged tells the surfaces a task's question moved. The GUI wakes on
// event_log growth, and asking or answering a question does not always change
// the task's status, which is the event that would otherwise carry the news.
func (db *DB) questionChanged(taskID int64, what string) {
	task, err := db.GetTask(taskID)
	if err != nil || task == nil {
		return
	}
	db.emitTaskUpdated(task, map[string]interface{}{"question": what})
}
