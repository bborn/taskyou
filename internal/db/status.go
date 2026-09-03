package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// Task status is LOG-FIRST.
//
// A status change is not a field assignment, it is an appended fact. Every
// transition writes a row to task_status_events — task_id, from, to, actor,
// reason, evidence — in the SAME transaction as the tasks.status update, so the
// row can never disagree with the log. tasks.status remains, but only as the
// cached fold over those facts (see FoldStatus and CheckStatusConsistency); the
// board reads that cache, and this file is the only thing allowed to write it.
//
// This shape exists because the previous one — a bare `UPDATE tasks SET status =
// ?` reachable from anywhere — produced the same incident over and over:
//
//   - the reconcile sweep marking steps done it had merely failed to see a tmux
//     window for (completion inferred from absence);
//   - completed_at stamped on steps that never started, which made never-run
//     work look finished on the board;
//   - `ty close` / `ty status done` / `ty bulk close` burying tasks whose PR was
//     still open, because a plain status write skips every completion rule;
//   - daemon status changes leaving no trace at all, so debugging meant reading
//     task_logs and guessing.
//
// Each was patched where it surfaced, and each came back somewhere else. They
// come back because a plain status write answers none of the questions that
// matter: who changed it, from what, why, and on what evidence. So the write
// signature now demands all four, the gates live here rather than in each
// caller, and a refusal is itself appended to the log.
//
// Adding a caller? Use SetTaskStatus. There is deliberately no shortcut.

// Actor names what made a status change. It is not decoration: "the sweep did
// it" versus "a human did it" is the difference between a bug and a decision,
// and after the fact the log is the only place that distinction survives.
type Actor string

const (
	// ActorTUI is the interactive kanban UI, driven by a human at a keyboard.
	ActorTUI Actor = "tui"
	// ActorCLI is a `ty` subcommand, run by a human in a shell.
	ActorCLI Actor = "cli"
	// ActorWeb is the HTTP API (web UI, desktop app, remote control).
	ActorWeb Actor = "web"
	// ActorMCP is an agent calling a taskyou_* MCP tool about its own task.
	ActorMCP Actor = "mcp"
	// ActorDaemon is the executor's own lifecycle bookkeeping — queueing,
	// starting, and parking the tasks it runs.
	ActorDaemon Actor = "daemon"
	// ActorSweep is a periodic reconciler acting on a task no one asked about.
	// The most dangerous actor in the system, and the one with the strictest
	// evidence requirement.
	ActorSweep Actor = "sweep"
	// ActorHook is an executor hook (Claude's Stop/Notification/PreToolUse)
	// reporting what the agent process just did.
	ActorHook Actor = "hook"
	// ActorPipeline is workflow construction wiring a DAG's steps into their
	// initial states.
	ActorPipeline Actor = "pipeline"
	// ActorSystem is ty itself doing structural bookkeeping — creating a task,
	// releasing a dependent whose blockers all finished.
	ActorSystem Actor = "system"
	// ActorMigration is the one-time backfill that gave pre-log tasks a genesis
	// event. It never appears on a transition made after this feature shipped.
	ActorMigration Actor = "migration"
)

// KnownActors lists every actor, for validation and for `ty debug`.
func KnownActors() []Actor {
	return []Actor{ActorTUI, ActorCLI, ActorWeb, ActorMCP, ActorDaemon,
		ActorSweep, ActorHook, ActorPipeline, ActorSystem, ActorMigration}
}

func validActor(a Actor) bool {
	for _, k := range KnownActors() {
		if a == k {
			return true
		}
	}
	return false
}

// Evidence is the positive fact a transition rests on.
//
// The rule the sweep broke was "never infer completion from absence": no tmux
// window and no diff against origin look exactly like a step that has not
// started yet. Evidence is how a caller says what it actually OBSERVED, and a
// transition into a terminal state is refused without it — which is precisely
// the thing a caller reasoning from absence cannot produce.
type Evidence struct {
	// Observed is what the caller saw, in its own words. "the agent called
	// taskyou_complete", "HEAD is 3 commits past base and pushed".
	Observed string `json:"observed,omitempty"`
	// Human records that a person asked for this, and how. A human closing a
	// backlog item is legitimate even with nothing to point at; automation
	// closing one is the bug.
	Human string `json:"human,omitempty"`
	// BaseCommit / HeadCommit are the step's starting commit and where it ended
	// up. Different values are the only proof a step produced work.
	BaseCommit string `json:"base_commit,omitempty"`
	HeadCommit string `json:"head_commit,omitempty"`
	// PRNumber / PRState carry a pull request's identity and observed state.
	// PRState of MERGED or CLOSED is what lets a done-write past the open-PR gate.
	PRNumber int    `json:"pr_number,omitempty"`
	PRState  string `json:"pr_state,omitempty"`
	// Gate names the completion gate that was run and passed (e.g. a step's
	// `verify:` command).
	Gate string `json:"gate,omitempty"`
}

// NoEvidence is the explicit "nothing observed" value. It is fine for ordinary
// transitions (queueing, starting, parking) and is refused for terminal ones.
var NoEvidence = Evidence{}

// Observedf builds Evidence from what the caller saw.
func Observedf(format string, args ...interface{}) Evidence {
	return Evidence{Observed: fmt.Sprintf(format, args...)}
}

// ByHuman builds Evidence recording that a person asked for this transition.
func ByHuman(format string, args ...interface{}) Evidence {
	return Evidence{Human: fmt.Sprintf(format, args...)}
}

// IsEmpty reports whether the evidence says nothing at all.
func (e Evidence) IsEmpty() bool {
	return strings.TrimSpace(e.Observed) == "" &&
		strings.TrimSpace(e.Human) == "" &&
		strings.TrimSpace(e.HeadCommit) == "" &&
		strings.TrimSpace(e.Gate) == "" &&
		e.PRNumber == 0 &&
		strings.TrimSpace(e.PRState) == ""
}

// String renders evidence for a human reading the audit trail.
func (e Evidence) String() string {
	var parts []string
	if e.Human != "" {
		parts = append(parts, "human: "+e.Human)
	}
	if e.Observed != "" {
		parts = append(parts, "observed: "+e.Observed)
	}
	if e.HeadCommit != "" {
		head := e.HeadCommit
		if len(head) > 8 {
			head = head[:8]
		}
		if e.BaseCommit != "" {
			base := e.BaseCommit
			if len(base) > 8 {
				base = base[:8]
			}
			parts = append(parts, "commit: "+base+"→"+head)
		} else {
			parts = append(parts, "commit: "+head)
		}
	}
	if e.PRNumber > 0 {
		pr := fmt.Sprintf("PR #%d", e.PRNumber)
		if e.PRState != "" {
			pr += " " + e.PRState
		}
		parts = append(parts, pr)
	}
	if e.Gate != "" {
		parts = append(parts, "gate: "+e.Gate)
	}
	return strings.Join(parts, "; ")
}

func (e Evidence) marshal() string {
	b, err := json.Marshal(e)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Transition outcomes recorded in the log.
const (
	// OutcomeApplied means the transition happened. Only these are folded.
	OutcomeApplied = "applied"
	// OutcomeRefused means a gate rejected it. Recorded so an attempt to bury a
	// task leaves a trace instead of a silent no-op.
	OutcomeRefused = "refused"
)

// Gate names. A refusal always carries one, so the reason a write bounced is a
// value you can grep for and not prose you have to parse.
const (
	// GateEvidenceRequired: a terminal transition arrived with nothing observed.
	GateEvidenceRequired = "evidence-required"
	// GateNeverStarted: automation tried to complete a task that never ran.
	GateNeverStarted = "never-started"
	// GateOpenPR: a done-write for a task whose pull request is still open.
	GateOpenPR = "open-pr"
	// GateUnknownStatus: the target status is not a status.
	GateUnknownStatus = "unknown-status"
	// GateMissingActor / GateMissingReason: the write did not say who or why.
	GateMissingActor  = "missing-actor"
	GateMissingReason = "missing-reason"
)

// RefusedError is returned when a gate rejects a transition. Callers that want
// to render the refusal (the TUI notice, the MCP tool result, an HTTP 409)
// check for it with IsRefused.
type RefusedError struct {
	TaskID int64
	From   string
	To     string
	Gate   string
	Detail string
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("refused %s→%s for task #%d [%s]: %s", e.From, e.To, e.TaskID, e.Gate, e.Detail)
}

// IsRefused reports whether err is a gate refusal (as opposed to an I/O error).
func IsRefused(err error) bool {
	_, ok := err.(*RefusedError)
	return ok
}

// RefusalGate returns the gate that refused err, or "".
func RefusalGate(err error) string {
	if r, ok := err.(*RefusedError); ok {
		return r.Gate
	}
	return ""
}

// StatusEvent is one appended fact about a task's status.
type StatusEvent struct {
	ID        int64
	TaskID    int64
	From      string
	To        string
	Actor     Actor
	Reason    string
	Evidence  Evidence
	Outcome   string
	Gate      string
	CreatedAt LocalTime
}

// FoldStatus projects a task's status from its event log: the target of the
// last APPLIED transition. Refused attempts are in the log for the audit trail
// and are deliberately not folded — a refusal changed nothing.
//
// Events must be in append order (ascending id), which is what
// GetStatusEvents returns.
func FoldStatus(events []StatusEvent) string {
	status := ""
	for _, e := range events {
		if e.Outcome != OutcomeApplied {
			continue
		}
		status = e.To
	}
	return status
}

// FoldStatusAt projects the status as of event throughID — the same fold,
// rewound. This is what makes the log a source of truth rather than a
// changelog: any past state can be reproduced from it.
func FoldStatusAt(events []StatusEvent, throughID int64) string {
	status := ""
	for _, e := range events {
		if e.ID > throughID {
			break
		}
		if e.Outcome != OutcomeApplied {
			continue
		}
		status = e.To
	}
	return status
}

// SetTaskStatus is the ONE path that may change a task's status.
//
// actor and reason are positional and required precisely so a future caller
// cannot take the old shortcut: there is no signature that compiles without
// answering "who" and "why". evidence answers "on what grounds" and is checked
// by the gates below — pass NoEvidence for an ordinary transition and mean it.
//
// It appends the transition and updates the cached row in one transaction, so
// the two can never diverge. Gates run here rather than in callers, so the CLI,
// the web API, the MCP tool and the TUI all get them without each remembering
// to ask.
func (db *DB) SetTaskStatus(id int64, to string, actor Actor, reason string, evidence Evidence) error {
	reason = strings.TrimSpace(reason)
	if !validActor(actor) {
		return &RefusedError{TaskID: id, To: to, Gate: GateMissingActor,
			Detail: fmt.Sprintf("actor %q is not a known actor (%v)", actor, KnownActors())}
	}
	if reason == "" {
		return &RefusedError{TaskID: id, To: to, Gate: GateMissingReason,
			Detail: "a status change must say why it happened"}
	}
	if !isKnownStatus(to) {
		return &RefusedError{TaskID: id, To: to, Gate: GateUnknownStatus,
			Detail: fmt.Sprintf("%q is not a task status", to)}
	}

	task, err := db.GetTask(id)
	if err != nil {
		return fmt.Errorf("load task %d: %w", id, err)
	}
	if task == nil {
		return fmt.Errorf("task #%d not found", id)
	}
	from := task.Status

	// A no-op write changes nothing, so it is neither gated nor logged: the
	// hooks re-assert 'processing' on every tool call, and an audit trail
	// drowned in "processing→processing" is an audit trail nobody reads.
	if from == to {
		_, err := db.Exec(`UPDATE tasks SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
		return err
	}

	if refusal := db.gate(task, to, evidence); refusal != nil {
		refusal.From = from
		// Record the refusal. An attempt to bury a task with an open PR is
		// exactly the thing that used to leave no trace anywhere.
		db.appendRefusal(id, from, to, actor, reason, evidence, refusal)
		return refusal
	}

	if err := db.applyTransition(task, to, actor, reason, evidence); err != nil {
		return err
	}
	db.afterTransition(task, from, to)
	return nil
}

func isKnownStatus(s string) bool {
	switch s {
	case StatusBacklog, StatusQueued, StatusProcessing, StatusBlocked, StatusDone, StatusArchived:
		return true
	}
	return false
}

// gate runs the completion rules. It returns nil to allow the transition.
//
// Only terminal transitions are gated. Everything else (queueing, starting,
// parking for review, dropping back to backlog) is reversible and cheap to get
// wrong; burying a task is neither.
func (db *DB) gate(task *Task, to string, ev Evidence) *RefusedError {
	terminal := to == StatusDone || to == StatusArchived

	// Gate 1 — evidence required. "A transition to a terminal state must carry
	// evidence." A caller reasoning from absence (no window, no diff) has
	// nothing to put here, which is the point.
	if terminal && ev.IsEmpty() {
		return &RefusedError{TaskID: task.ID, To: to, Gate: GateEvidenceRequired,
			Detail: "a terminal status must say what was observed — no completion by inference"}
	}

	if to != StatusDone {
		return nil
	}

	// Gate 2 — never started. This is the zombie-step bug, stated as a rule: a
	// task with no started_at has produced nothing, so nothing can have been
	// observed finishing. A human may still close an item they have decided not
	// to do; automation may not decide that for them.
	if task.StartedAt == nil && strings.TrimSpace(ev.Human) == "" {
		return &RefusedError{TaskID: task.ID, To: to, Gate: GateNeverStarted,
			Detail: "this task never started, so it cannot have finished; only a human may close unstarted work"}
	}

	// Gate 3 — open PR. `ty close`, `ty status done`, `ty bulk close`, the web
	// API and the MCP tool all used to bury a task whose PR was still open.
	// The way past this gate is to observe the PR reaching a terminal state
	// (which is what the daemon's review reconciler does), not to assert it.
	if open, number := db.prIsOpen(task); open {
		switch strings.ToUpper(strings.TrimSpace(ev.PRState)) {
		case "MERGED", "CLOSED":
			// The caller looked at the PR and saw it finish. Allowed.
		default:
			return &RefusedError{TaskID: task.ID, To: to, Gate: GateOpenPR,
				Detail: fmt.Sprintf("PR #%d is still open — merge or close it (the daemon then completes this task), or use `ty complete` to park it for review", number)}
		}
	}

	return nil
}

// prIsOpen reports whether the task's pull request is still awaiting a human.
//
// A PR number with no cached state is treated as OPEN: the failure we are
// guarding against is burying live work, so an unknown state must not be a way
// through. Cached state is the daemon's, refreshed by refreshActivePRInfo.
func (db *DB) prIsOpen(task *Task) (bool, int) {
	if task.PRNumber <= 0 {
		return false, 0
	}
	if strings.TrimSpace(task.PRInfoJSON) != "" {
		var info struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal([]byte(task.PRInfoJSON), &info); err == nil {
			switch strings.ToUpper(strings.TrimSpace(info.State)) {
			case "MERGED", "CLOSED":
				return false, task.PRNumber
			}
		}
	}
	return true, task.PRNumber
}

// applyTransition writes the row update and the log row in one transaction.
//
// Everything it needs is read before BEGIN: the pool is capped at a single
// connection, so any query issued while the transaction is open would deadlock
// against it.
func (db *DB) applyTransition(task *Task, to string, actor Actor, reason string, ev Evidence) error {
	query := "UPDATE tasks SET status = ?, updated_at = CURRENT_TIMESTAMP"
	args := []interface{}{to}

	switch to {
	case StatusProcessing:
		query += ", started_at = CURRENT_TIMESTAMP"
	case StatusDone, StatusArchived, StatusBlocked:
		// completed_at means "this task ran and then stopped". Stamping it on
		// something that never started is how never-run work came to look
		// finished on the board and fed false "done" signals to the sweeps that
		// key off it. A task can only complete if it started.
		if task.StartedAt != nil {
			query += ", completed_at = CURRENT_TIMESTAMP"
		}
	}
	query += " WHERE id = ?"
	args = append(args, task.ID)

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin status transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO task_status_events (task_id, from_status, to_status, actor, reason, evidence, outcome, gate)
		VALUES (?, ?, ?, ?, ?, ?, ?, '')
	`, task.ID, task.Status, to, string(actor), reason, ev.marshal(), OutcomeApplied); err != nil {
		return fmt.Errorf("append status event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit status transaction: %w", err)
	}
	return nil
}

// appendRefusal records a rejected transition. Best-effort: failing to log a
// refusal must not turn into a second, different error for the caller.
func (db *DB) appendRefusal(id int64, from, to string, actor Actor, reason string, ev Evidence, refusal *RefusedError) {
	_, err := db.Exec(`
		INSERT INTO task_status_events (task_id, from_status, to_status, actor, reason, evidence, outcome, gate)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, id, from, to, string(actor), reason, ev.marshal(), OutcomeRefused, refusal.Gate)
	if err != nil {
		log.Printf("append refused status event for task %d: %v", id, err)
	}
	db.recordEvent("task.status_refused", id, refusal.Detail)
}

// afterTransition runs the side effects that must NOT be inside the
// transaction: they re-enter the database (and the pool holds one connection),
// and ProcessCompletedBlocker recurses back into SetTaskStatus.
func (db *DB) afterTransition(task *Task, from, to string) {
	// A finished task's executor pane is torn down; its tmux pane ID then
	// becomes free for tmux to recycle onto another task. Drop the stale pane
	// pointers so they can never resolve to a different task's live pane.
	switch to {
	case StatusDone, StatusArchived:
		if err := db.ClearTaskPaneIDs(task.ID); err != nil {
			log.Printf("ClearTaskPaneIDs(%d): %v", task.ID, err)
		}
	}

	updated, err := db.GetTask(task.ID)
	if err == nil && updated != nil {
		db.emitTaskUpdated(updated, map[string]interface{}{
			"status": map[string]string{"old": from, "new": to},
		})
		// Lifecycle events so external watchers can react to blocked/completed
		// transitions without parsing update metadata.
		switch to {
		case StatusBlocked:
			db.emitTaskBlocked(updated, "status change")
		case StatusDone:
			db.emitTaskCompleted(updated)
		}
	}

	// Release dependents when a blocker finishes. Best-effort: a dropped write
	// is recovered by the daemon's RequeueReadyTasks sweep, but log it so a
	// stalled workflow isn't a silent mystery.
	if to == StatusDone || to == StatusArchived {
		if _, err := db.ProcessCompletedBlocker(task.ID); err != nil {
			log.Printf("ProcessCompletedBlocker(%d): %v", task.ID, err)
		}
	}
}

// appendGenesisEvent records a task's initial status, so every task's log folds
// to its row from the very first event. Called by CreateTask (and by the
// backfill for tasks that predate the log).
func (db *DB) appendGenesisEvent(taskID int64, status string, actor Actor, reason string, at *time.Time) {
	ev := Evidence{Observed: "task created with status " + status}
	if actor == ActorMigration {
		ev = Evidence{Observed: "row state at migration time; no transition history existed before this point"}
	}
	var err error
	if at != nil {
		_, err = db.Exec(`
			INSERT INTO task_status_events (task_id, from_status, to_status, actor, reason, evidence, outcome, gate, created_at)
			VALUES (?, '', ?, ?, ?, ?, ?, '', ?)
		`, taskID, status, string(actor), reason, ev.marshal(), OutcomeApplied, *at)
	} else {
		_, err = db.Exec(`
			INSERT INTO task_status_events (task_id, from_status, to_status, actor, reason, evidence, outcome, gate)
			VALUES (?, '', ?, ?, ?, ?, ?, '')
		`, taskID, status, string(actor), reason, ev.marshal(), OutcomeApplied)
	}
	if err != nil {
		log.Printf("append genesis status event for task %d: %v", taskID, err)
	}
}

// GetStatusEvents returns a task's full transition history in append order,
// refusals included.
func (db *DB) GetStatusEvents(taskID int64) ([]StatusEvent, error) {
	rows, err := db.Query(`
		SELECT id, task_id, from_status, to_status, actor, reason, evidence, outcome, gate, created_at
		FROM task_status_events WHERE task_id = ? ORDER BY id ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("read status events: %w", err)
	}
	defer rows.Close()
	return scanStatusEvents(rows)
}

// GetAppliedStatusEvents returns only the transitions that actually happened —
// the sequence FoldStatus projects over.
func (db *DB) GetAppliedStatusEvents(taskID int64) ([]StatusEvent, error) {
	all, err := db.GetStatusEvents(taskID)
	if err != nil {
		return nil, err
	}
	var applied []StatusEvent
	for _, e := range all {
		if e.Outcome == OutcomeApplied {
			applied = append(applied, e)
		}
	}
	return applied, nil
}

func scanStatusEvents(rows *sql.Rows) ([]StatusEvent, error) {
	var out []StatusEvent
	for rows.Next() {
		var e StatusEvent
		var actor, evidenceJSON string
		if err := rows.Scan(&e.ID, &e.TaskID, &e.From, &e.To, &actor, &e.Reason,
			&evidenceJSON, &e.Outcome, &e.Gate, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan status event: %w", err)
		}
		e.Actor = Actor(actor)
		if evidenceJSON != "" {
			_ = json.Unmarshal([]byte(evidenceJSON), &e.Evidence)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// StatusMismatch is one task whose cached status disagrees with its log.
type StatusMismatch struct {
	TaskID   int64
	Title    string
	Cached   string
	Folded   string
	NumEvent int
}

func (m StatusMismatch) String() string {
	folded := m.Folded
	if folded == "" {
		folded = "<no events>"
	}
	return fmt.Sprintf("task #%d %q: row says %q, log folds to %q (%d events)",
		m.TaskID, m.Title, m.Cached, folded, m.NumEvent)
}

// CheckStatusConsistency asserts the invariant this whole design rests on: for
// every task, the cached tasks.status equals the fold over its status events.
//
// If this ever reports a mismatch, something wrote the status column outside
// SetTaskStatus — which is the bug class this package exists to make
// impossible. `ty debug status-consistency` runs it against a real DB.
func (db *DB) CheckStatusConsistency() ([]StatusMismatch, error) {
	rows, err := db.Query(`SELECT id, title, status FROM tasks WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	type row struct {
		id     int64
		title  string
		status string
	}
	var tasks []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.title, &r.status); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var mismatches []StatusMismatch
	for _, t := range tasks {
		events, err := db.GetStatusEvents(t.id)
		if err != nil {
			return nil, err
		}
		folded := FoldStatus(events)
		if folded != t.status {
			mismatches = append(mismatches, StatusMismatch{
				TaskID: t.id, Title: t.title, Cached: t.status,
				Folded: folded, NumEvent: len(events),
			})
		}
	}
	return mismatches, nil
}

// statusEventBackfillKey guards the one-time genesis backfill.
const statusEventBackfillKey = "migration:task_status_events_genesis_v1"

// backfillStatusEvents gives every pre-existing task a synthetic genesis event
// so CheckStatusConsistency passes on a real database.
//
// It only ever INSERTS, and only for tasks that have no events at all, so it
// cannot lose or reorder history that already exists. The genesis event is
// timestamped with the task's own clock — completed_at, else started_at, else
// created_at — so the backfilled log sorts in the order the work actually
// happened rather than all at once at migration time. Its evidence says
// plainly that nothing before it was recorded, rather than inventing a story.
func (db *DB) backfillStatusEvents() error {
	if done, _ := db.GetSetting(statusEventBackfillKey); done == "1" {
		return nil
	}

	rows, err := db.Query(`
		SELECT t.id, t.status, COALESCE(t.completed_at, t.started_at, t.created_at)
		FROM tasks t
		WHERE NOT EXISTS (SELECT 1 FROM task_status_events e WHERE e.task_id = t.id)
		ORDER BY t.id
	`)
	if err != nil {
		return fmt.Errorf("find tasks needing a genesis event: %w", err)
	}
	type seed struct {
		id     int64
		status string
		at     sql.NullTime
	}
	var seeds []seed
	for rows.Next() {
		var s seed
		if err := rows.Scan(&s.id, &s.status, &s.at); err != nil {
			rows.Close()
			return fmt.Errorf("scan task for backfill: %w", err)
		}
		seeds = append(seeds, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, s := range seeds {
		var at *time.Time
		if s.at.Valid {
			t := s.at.Time
			at = &t
		}
		db.appendGenesisEvent(s.id, s.status, ActorMigration,
			"backfilled: this task predates the status log", at)
	}

	if len(seeds) > 0 {
		log.Printf("status log: backfilled a genesis event for %d task(s)", len(seeds))
	}
	return db.SetSetting(statusEventBackfillKey, "1")
}
