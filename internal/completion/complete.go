// Package completion holds the one authoritative implementation of "this task is
// finished".
//
// Completing a task is not a status write — it is a decision tree with real
// consequences: an evidence gate that can REJECT the completion, a human-gate
// step that must park for review instead of advancing the workflow DAG, and a
// PR-bearing task that must wait for a human merge rather than being buried in
// Done. That logic used to live inside the taskyou_complete MCP handler, which
// made the MCP transport the only way to finish a task correctly — when it was
// unavailable, agents reached for `ty close`, which is a plain status write and
// silently skips every one of those rules.
//
// Both the MCP tool and the `ty complete` CLI now call Complete, so the two
// cannot drift and neither can bypass a gate.
package completion

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
	"github.com/bborn/workflow/internal/pipeline"
	"github.com/bborn/workflow/internal/tasksummary"
)

// Kind is what completion actually did — the caller renders its own wording, but
// the decision itself is made here, once.
type Kind string

const (
	// KindVerifyFailed means the step's `verify:` command exited non-zero, so the
	// completion was REJECTED and the task deliberately left running.
	KindVerifyFailed Kind = "verify_failed"
	// KindGateParked means a human-review gate finished and parked in 'blocked'
	// awaiting approval, holding its dependents.
	KindGateParked Kind = "gate_parked"
	// KindPRReview means the task produced a PR and parked in 'blocked' for a
	// human to merge and close. Nothing moves it to 'done' but that human.
	KindPRReview Kind = "pr_review"
	// KindReview means the task finished with no PR and parked in 'blocked' for
	// a human to review and close.
	KindReview Kind = "review"
	// KindDone means a workflow step with dependents finished and moved to
	// 'done', releasing the next steps. It is the only completion automation
	// may write.
	KindDone Kind = "done"
)

// Outcome describes what Complete did, with the details a caller needs to
// explain it.
type Outcome struct {
	Kind          Kind
	VerifyCommand string // set when Kind == KindVerifyFailed
	VerifyOutput  string // tail of the failing command's output
	PRNumber      int    // set when Kind == KindPRReview
	PRURL         string
}

// Options tunes side effects that differ between callers.
type Options struct {
	// Actor is who is completing the task — an agent through the MCP tool, or a
	// human running `ty complete`. It rides into the status log, where "the
	// agent said it was done" and "a person said it was done" are different
	// facts about the same transition.
	Actor db.Actor

	// AsyncSummary runs activity-summary generation in a background goroutine.
	// The long-lived MCP server wants this (it must not block the agent); a
	// short-lived CLI process must NOT, because the process exits and kills the
	// goroutine before it can store anything.
	AsyncSummary bool
}

// actorFor resolves the caller's actor, defaulting to the MCP tool — the
// original and still the most common caller.
func actorFor(opts Options) db.Actor {
	if opts.Actor == "" {
		return db.ActorMCP
	}
	return opts.Actor
}

// doneEvidence records what was actually observed when a task completed: the
// agent's own summary of the work, plus the verify gate it had to pass.
func doneEvidence(summary, gate string) db.Evidence {
	observed := strings.TrimSpace(summary)
	if observed == "" {
		observed = "completion signalled with no summary"
	}
	if len(observed) > 400 {
		observed = observed[:400] + "…"
	}
	return db.Evidence{Observed: observed, Gate: gate}
}

// LookupPR returns the PR number and URL for a task's branch, or (0, "").
//
// It prefers a live `gh` lookup — an agent typically opens the PR moments before
// completing, so the DB copy is stale — and persists a fresh result so the board
// and the daemon reconciler both see it. Falls back to stored PR info, including
// when GitHub can't be reached: a failed lookup must not make a PR-bearing task
// look PR-less and skip the human merge.
func LookupPR(database *db.DB, task *db.Task) (int, string) {
	if task == nil {
		return 0, ""
	}
	repoDir, branch := prLookupTarget(database, task)
	if branch != "" {
		if info, err := github.LookupPR(context.Background(), repoDir, branch); err == nil && info != nil {
			_ = database.UpdateTaskPRInfo(task.ID, info.URL, info.Number, github.MarshalPRInfo(info))
			return info.Number, info.URL
		}
	}
	return task.PRNumber, task.PRURL
}

// prLookupTarget resolves WHERE to ask about a PR and WHICH branch to ask about.
//
// A remotely placed task records its branch in remote_branch and its worktree on
// another machine, leaving branch_name and worktree_path empty. Reading only the
// local fields meant the query was skipped entirely and every placed task looked
// PR-less, which routes finished work to done instead of parking it in blocked
// where a human can see it. The branch was pushed to the shared origin, so the
// project checkout on this machine can answer for it.
//
// A worktree path that is not a directory here is another machine's path, not a
// usable one — checking rather than assuming is what keeps a remote path from
// being handed to git as if it were local.
func prLookupTarget(database *db.DB, task *db.Task) (repoDir, branch string) {
	branch = task.BranchName
	repoDir = task.WorktreePath
	if !isDir(repoDir) {
		repoDir = ""
	}
	if branch == "" {
		if _, remoteBranch, err := database.GetTaskRemoteWorktree(task.ID); err == nil {
			branch = remoteBranch
		}
	}
	if repoDir == "" {
		if proj, err := database.GetProjectByName(task.Project); err == nil && proj != nil {
			repoDir = proj.Path
		}
	}
	if repoDir == "" {
		repoDir, _ = os.Getwd()
	}
	return repoDir, branch
}

// isDir reports whether path names a directory on THIS machine.
func isDir(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// verifyDir resolves WHERE a step's `verify:` command should run on THIS
// machine, or returns deferRemote=true when the gate must NOT be run locally.
//
// A local task records its worktree in worktree_path; a worktree_path that is
// not a directory on this machine belongs to another host and is not usable
// (the same isDir guard prLookupTarget uses to keep a remote path from being
// handed to a local git as if it were local). With no usable worktree, a local
// task falls back to the daemon's project checkout, where the work was done —
// the gate runs against the tree where the commit lives.
//
// A remotely placed step keeps its worktree in remote_worktree_path on another
// machine, leaving the local worktree_path column empty by design
// (SetTaskRemoteWorktree writes only the remote columns). The daemon's local
// project checkout here is a DIFFERENT tree from where the work was done, so
// running `verify:` against it would evaluate the wrong tree — false-pass on a
// checkout the remote work never touched, or false-reject on one that does
// not satisfy it. Rather than run against the wrong tree, the gate is deferred:
// the backstop for remote+verify belongs on the remote done path, which runs
// `verify:` on the agent's host before `signal done` is honoured.
//
// (dir=="", deferRemote==false) is preserved for the rare case where a local
// task has neither a usable worktree nor a registered project: RunStepVerify
// then runs in the daemon's CWD exactly as it did before this guard, which is
// the original semantics rather than a new failure mode.
func verifyDir(database *db.DB, task *db.Task, taskID int64) (dir string, deferRemote bool) {
	dir = strings.TrimSpace(task.WorktreePath)
	if !isDir(dir) {
		// A worktree path that is not a directory here is another machine's
		// path, not a usable one — checking rather than assuming is what keeps
		// a remote path from being handed to a local command as if it were
		// local. The same guard prLookupTarget applies.
		dir = ""
	}
	if dir != "" {
		return dir, false
	}
	remote, _, rerr := database.GetTaskRemoteWorktree(taskID)
	if rerr == nil && strings.TrimSpace(remote) != "" {
		// Remote-placed step: the worktree is on another host. Defer the gate
		// rather than fall back to proj.Path, which would test the wrong tree.
		return "", true
	}
	if proj, perr := database.GetProjectByName(task.Project); perr == nil && proj != nil {
		return proj.Path, false
	}
	return "", false
}

// Complete runs the full completion decision for a task and applies its effects.
//
// The order matters and is load-bearing:
//  1. Evidence gate — a configured `verify:` command runs FIRST. Non-zero exit
//     rejects the completion and leaves the task running so the agent can fix it.
//     This is the backstop against completion-by-assertion; nothing below runs.
//  2. A non-terminal human gate parks 'blocked' (its dependents stay held).
//  3. A terminal task with a PR parks 'blocked' for the human merge.
//  4. Any other terminal task parks 'blocked' for the human to close.
//  5. A workflow step with dependents is done, which advances the workflow.
func Complete(database *db.DB, taskID int64, summary string, opts Options) (*Outcome, error) {
	task, err := database.GetTask(taskID)
	if err != nil || task == nil {
		return nil, fmt.Errorf("task #%d not found", taskID)
	}

	// 1. Evidence gate. A step that registered a `verify:` command must pass it
	// before completion is accepted — the agent's say-so alone is not trusted.
	// Tasks with no verify row fall straight through.
	verifiedGate := ""
	if verifyCmd, _ := database.GetStepVerify(taskID); strings.TrimSpace(verifyCmd) != "" {
		dir, deferRemote := verifyDir(database, task, taskID)
		if deferRemote {
			// A remotely placed step keeps its worktree on another host, so the
			// local worktree_path column is empty by design and the daemon's
			// project checkout here is a DIFFERENT tree from where the agent's
			// commit lives. Running `verify:` against that checkout would either
			// false-pass (the local checkout satisfies the command independent
			// of the agent's work) or false-reject (it does not, parking correct
			// pushed work as "Verification failed"). Defer the gate to the
			// remote done path, which runs `verify:` on the agent's host before
			// `signal done` is honoured. The no-run skip is strictly better than
			// the wrong-tree run; restoring the backstop for remote+verify
			// belongs on the remote host, not in this checkout.
			database.AppendTaskLog(taskID, "system",
				"Verify gate deferred — the step's worktree is on another host. "+
					"Run `verify: "+verifyCmd+"` there before this step is accepted.")
		} else if out, passed := pipeline.RunStepVerify(dir, verifyCmd); !passed {
			database.AppendTaskLog(taskID, "system", "Verification failed — completion rejected; the step keeps running so the agent can fix it.")
			return &Outcome{Kind: KindVerifyFailed, VerifyCommand: verifyCmd, VerifyOutput: out}, nil
		} else {
			database.AppendTaskLog(taskID, "system", "Verification passed: "+verifyCmd)
			verifiedGate = verifyCmd
		}
	}

	database.AppendTaskLog(taskID, "system", fmt.Sprintf("Task completed: %s", summary))

	// A workflow step that still has dependents must ADVANCE the DAG, not park for
	// review — only the terminal step opens a PR. The root step in particular owns
	// the shared branch, which a PR lookup would otherwise mistake for "this task
	// produced a PR", stalling the workflow at step one.
	nonTerminalStep := false
	if pipeline.IsWorkflowTask(task) {
		if deps, err := database.GetBlockedBy(task.ID); err == nil && len(deps) > 0 {
			nonTerminalStep = true
		}
	}

	// 2. Human-review gate: park rather than advance. Leaving it 'blocked' (not
	// 'done') keeps its dependents held until a human releases the chain.
	if nonTerminalStep && pipeline.IsGateStep(task) {
		if err := database.SetTaskStatus(taskID, db.StatusBlocked, actorFor(opts),
			"human-review gate finished — parked for approval, holding its dependents",
			db.Evidence{Observed: "the step signalled completion and is a gate step with dependents", Gate: "human-review-gate"}); err != nil {
			return nil, fmt.Errorf("failed to park gate step for review: %w", err)
		}
		// Logged as a "question" so it lands in the blocked/needs-input lane and the
		// daemon sweep leaves it for the human instead of auto-completing it.
		database.AppendTaskLog(taskID, "question", pipeline.GateStepParkedLog)
		tasksummary.KickoffRewrite(database, taskID)
		return &Outcome{Kind: KindGateParked}, nil
	}

	// 3. PR-bearing terminal task: park for the human merge.
	var prNumber int
	var prURL string
	if !nonTerminalStep {
		prNumber, prURL = LookupPR(database, task)
	}
	if prNumber > 0 {
		if err := database.SetTaskStatus(taskID, db.StatusBlocked, actorFor(opts),
			"work finished and a PR is open — parked for a human merge",
			db.Evidence{Observed: "the agent signalled completion", PRNumber: prNumber, PRState: "OPEN"}); err != nil {
			return nil, fmt.Errorf("failed to move task to review: %w", err)
		}
		reviewMsg := fmt.Sprintf("✅ PR #%d ready for review — merge it, then close this task.", prNumber)
		if prURL != "" {
			reviewMsg += " " + prURL
		}
		database.AppendTaskLog(taskID, "question", reviewMsg)
		tasksummary.KickoffRewrite(database, taskID)
		return &Outcome{Kind: KindPRReview, PRNumber: prNumber, PRURL: prURL}, nil
	}

	generate := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = tasksummary.GenerateAndStore(ctx, database, taskID)
	}
	summarize := func() {
		if opts.AsyncSummary {
			go generate()
		} else {
			generate()
		}
	}

	// 4. No PR, not a workflow step others wait on: park for the human. Only a
	// person moves a task to done (see the human-only gate in db.SetTaskStatus).
	if !nonTerminalStep {
		if err := database.SetTaskStatus(taskID, db.StatusBlocked, actorFor(opts),
			"work finished — parked for a human to review and close",
			doneEvidence(summary, verifiedGate)); err != nil {
			return nil, fmt.Errorf("failed to park task for review: %w", err)
		}
		database.AppendTaskLog(taskID, "question", "✅ Work finished — review it and close the task when you're happy.")
		summarize()
		return &Outcome{Kind: KindReview}, nil
	}

	// 5. A workflow step with dependents — done, so the next steps start.
	//
	// A non-terminal step can still carry a PR NUMBER: the steps of a workflow
	// share one branch, so the terminal step's PR matches all of them. That is
	// the one case where a done-write happens with an open PR on the row, and
	// the evidence has to say so out loud rather than the gate guessing.
	ev := doneEvidence(summary, verifiedGate).DisownSharedBranchPR(task.PRNumber, task.BranchName)
	if err := database.SetTaskStatus(taskID, db.StatusDone, actorFor(opts),
		"workflow step finished and every gate passed; its dependents can start",
		ev); err != nil {
		return nil, fmt.Errorf("failed to mark task done: %w", err)
	}
	summarize()

	return &Outcome{Kind: KindDone}, nil
}
