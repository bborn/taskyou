package completion

import (
	"fmt"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

// OpenPRGuard is the still-open PR that makes a plain "mark it done" write wrong.
//
// Why this exists: Complete() routes a PR-bearing task to 'blocked' so a human
// decides when it ships. But `ty close`, `ty status <id> done` and `ty bulk close`
// are plain status writes that skip that decision entirely, and agents reach for
// them constantly — `ty close` is on PATH, needs no MCP session, and the gate-step
// log line advertises it ("Approve it with `ty close <id>` to release the next
// phase"). The result is work with an open PR buried in Done where the human who
// was supposed to review it never sees it again.
//
// The guard is deliberately narrow: it fires only when a PR exists AND is still
// open or draft. Merged and closed PRs pass — the human already decided. Tasks
// with no PR pass, which is what keeps legitimate workflow-gate releases working,
// since non-terminal steps never open a PR (only the sink step does).
type OpenPRGuard struct {
	Number int
	URL    string
	Draft  bool
}

// Reason renders the refusal shown to whoever tried the write. This is
// deliberately not an Error() method: OpenPRGuard is a verdict, not an error
// value, and is never returned through the error interface.
func (g *OpenPRGuard) Reason() string {
	kind := "open"
	if g.Draft {
		kind = "draft"
	}
	msg := fmt.Sprintf("PR #%d is still %s — this task is waiting on a human review, not finished", g.Number, kind)
	if g.URL != "" {
		msg += "\n  " + g.URL
	}
	return msg
}

// CheckDoneWrite reports the open PR blocking a plain write to 'done', or nil if
// the write is allowed.
//
// It reads the PR state already persisted on the task rather than shelling out to
// `gh`. That is a deliberate trade: the daemon's reconciler and Complete() both
// keep this field fresh, and a guard that made a network call on every `ty close`
// would add latency to the common path and fail open whenever GitHub was slow —
// exactly when a wrong answer is most costly. A task whose PR info was never
// recorded falls back to PRNumber, which is set whenever a PR is known at all.
func CheckDoneWrite(task *db.Task) *OpenPRGuard {
	if task == nil {
		return nil
	}

	if info := github.UnmarshalPRInfo(task.PRInfoJSON); info != nil {
		switch info.State {
		case github.PRStateMerged, github.PRStateClosed:
			// The human already merged or closed it — nothing left to protect.
			return nil
		case github.PRStateOpen, github.PRStateDraft:
			return &OpenPRGuard{
				Number: info.Number,
				URL:    info.URL,
				Draft:  info.State == github.PRStateDraft,
			}
		}
	}

	// No usable PR state recorded. A known PR number still means a PR was opened
	// for this task and nothing has told us it reached a terminal state, so treat
	// it as open: failing closed here costs one `--force`, while failing open
	// costs a silently buried task.
	if task.PRNumber > 0 {
		return &OpenPRGuard{Number: task.PRNumber, URL: task.PRURL}
	}
	return nil
}

// RecordStatusWrite leaves an audit line naming the surface that changed a task's
// status. Plain status writes previously landed with no trace at all — no task
// log, no PR-info update — which made "who marked this done?" unanswerable after
// the fact and turned a simple diagnosis into an archaeology dig through daemon
// logs and event timestamps. Every non-Complete path that reaches 'done' should
// call this.
func RecordStatusWrite(database *db.DB, taskID int64, status, source string) {
	if database == nil {
		return
	}
	database.AppendTaskLog(taskID, "system",
		fmt.Sprintf("Status set to %q via %s (plain status write — completion rules not evaluated).", status, source))
}
