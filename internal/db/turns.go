package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// A turn is one exchange with an agent: a prompt goes in, the agent works, the
// agent stops. Both ends are reported by the agent's own hooks —
// UserPromptSubmit starts a turn, Stop finishes it — so the counters say what
// the agent did, not what ty hoped it would do.
//
// This exists for one reason: a caller that sends a prompt and waits for the
// answer cannot tell "the agent replied to me" from "the agent finished what it
// was already doing" by watching status alone. Both land on the same Stop hook
// and the same processing → blocked transition. Counting turns makes the
// difference explicit — the answer to MY prompt is a Stop that comes after a
// Started that comes after my send.

// AgentTurn is a task's turn counters: how many turns the agent has begun, and
// how many it has finished. Started is always >= Completed while the agent is
// working.
type AgentTurn struct {
	Started   int64
	Completed int64
}

// RepliedAfter reports whether t is a completed reply to a prompt sent when the
// counters read prev — a turn that both started after the send and has since
// finished.
//
// Completed >= Started is what keeps the PREVIOUS turn's Stop from being read as
// this turn's answer: a stale Stop can only raise Completed to a value Started
// has already passed.
func (t AgentTurn) RepliedAfter(prev AgentTurn) bool {
	return t.Started > prev.Started && t.Completed >= t.Started
}

// ErrReplyTimeout is returned by WaitForAgentReply when no new turn completed
// inside the timeout. It says nothing about the agent's health: a long task is
// indistinguishable here from a dead one.
var ErrReplyTimeout = errors.New("timed out waiting for the agent to reply")

// agentTurnPollInterval is how often WaitForAgentReply re-reads the counters.
// The hooks that advance them run in a separate process (`ty claude-hook`), so
// SQLite is the only channel between the two and there is nothing to subscribe
// to. A variable so tests do not wait in real time.
var agentTurnPollInterval = 200 * time.Millisecond

// BeginAgentTurn records that the agent started a turn (its UserPromptSubmit
// hook fired) and returns the counters as they now stand.
func (db *DB) BeginAgentTurn(taskID int64) (AgentTurn, error) {
	_, err := db.Exec(`
		INSERT INTO task_turns (task_id, started, completed, updated_at)
		VALUES (?, 1, 0, CURRENT_TIMESTAMP)
		ON CONFLICT(task_id) DO UPDATE SET
			started = task_turns.started + 1,
			updated_at = CURRENT_TIMESTAMP
	`, taskID)
	if err != nil {
		return AgentTurn{}, fmt.Errorf("begin agent turn: %w", err)
	}
	return db.AgentTurnState(taskID)
}

// CompleteAgentTurn records that the agent finished the turn it was on (its Stop
// hook fired).
//
// Completed is set to Started rather than incremented: a Stop with no turn
// behind it — an agent whose prompt predates turn tracking, or a hook that fired
// twice — must not push Completed past Started and make the next wait return on
// a turn that never began.
func (db *DB) CompleteAgentTurn(taskID int64) (AgentTurn, error) {
	_, err := db.Exec(`
		INSERT INTO task_turns (task_id, started, completed, updated_at)
		VALUES (?, 0, 0, CURRENT_TIMESTAMP)
		ON CONFLICT(task_id) DO UPDATE SET
			completed = task_turns.started,
			updated_at = CURRENT_TIMESTAMP
	`, taskID)
	if err != nil {
		return AgentTurn{}, fmt.Errorf("complete agent turn: %w", err)
	}
	return db.AgentTurnState(taskID)
}

// AgentTurnState returns a task's turn counters. A task nothing has been sent to
// yet reads as zero, which is the right answer: nothing has started and nothing
// has finished.
func (db *DB) AgentTurnState(taskID int64) (AgentTurn, error) {
	var t AgentTurn
	err := db.QueryRow(`SELECT started, completed FROM task_turns WHERE task_id = ?`, taskID).
		Scan(&t.Started, &t.Completed)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentTurn{}, nil
	}
	if err != nil {
		return AgentTurn{}, fmt.Errorf("read agent turn: %w", err)
	}
	return t, nil
}

// WaitForAgentReply blocks until a turn that began after since has finished, and
// returns the counters at that moment. The waiting lives here so no caller has
// to write its own polling loop, and so there is one definition of what counts
// as "the agent answered me".
//
// since is the counters read BEFORE the prompt was sent. Returns ErrReplyTimeout
// if nothing qualifies within timeout, or ctx.Err() if the caller gives up
// first.
func (db *DB) WaitForAgentReply(ctx context.Context, taskID int64, since AgentTurn, timeout time.Duration) (AgentTurn, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(agentTurnPollInterval)
	defer ticker.Stop()
	for {
		cur, err := db.AgentTurnState(taskID)
		if err != nil {
			return AgentTurn{}, err
		}
		if cur.RepliedAfter(since) {
			return cur, nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return cur, ErrReplyTimeout
			}
			return cur, ctx.Err()
		case <-ticker.C:
		}
	}
}
