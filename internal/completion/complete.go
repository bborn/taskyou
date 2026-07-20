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
	// KindPRReview means the task produced a PR and parked in 'blocked' awaiting a
	// human merge (the daemon promotes it to 'done' once the PR closes).
	KindPRReview Kind = "pr_review"
	// KindDone means the task is genuinely finished and moved to 'done'.
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
	// AsyncSummary runs activity-summary generation in a background goroutine.
	// The long-lived MCP server wants this (it must not block the agent); a
	// short-lived CLI process must NOT, because the process exits and kills the
	// goroutine before it can store anything.
	AsyncSummary bool
}

// LookupPR returns the PR number and URL for a task's branch, or (0, "").
//
// It prefers a live `gh` lookup — an agent typically opens the PR moments before
// completing, so the DB copy is stale — and persists a fresh result so the board
// and the daemon reconciler both see it. Falls back to stored PR info.
func LookupPR(database *db.DB, task *db.Task) (int, string) {
	if task == nil {
		return 0, ""
	}
	if task.BranchName != "" {
		repoDir := task.WorktreePath
		if repoDir == "" {
			repoDir, _ = os.Getwd()
		}
		if info := github.NewPRCache().GetPRForBranch(repoDir, task.BranchName); info != nil {
			_ = database.UpdateTaskPRInfo(task.ID, info.URL, info.Number, github.MarshalPRInfo(info))
			return info.Number, info.URL
		}
	}
	return task.PRNumber, task.PRURL
}

// Complete runs the full completion decision for a task and applies its effects.
//
// The order matters and is load-bearing:
//  1. Evidence gate — a configured `verify:` command runs FIRST. Non-zero exit
//     rejects the completion and leaves the task running so the agent can fix it.
//     This is the backstop against completion-by-assertion; nothing below runs.
//  2. A non-terminal human gate parks 'blocked' (its dependents stay held).
//  3. A terminal task with a PR parks 'blocked' for the human merge.
//  4. Otherwise the task is done.
func Complete(database *db.DB, taskID int64, summary string, opts Options) (*Outcome, error) {
	task, err := database.GetTask(taskID)
	if err != nil || task == nil {
		return nil, fmt.Errorf("task #%d not found", taskID)
	}

	// 1. Evidence gate. A step that registered a `verify:` command must pass it
	// before completion is accepted — the agent's say-so alone is not trusted.
	// Tasks with no verify row fall straight through.
	if verifyCmd, _ := database.GetStepVerify(taskID); strings.TrimSpace(verifyCmd) != "" {
		dir := strings.TrimSpace(task.WorktreePath)
		if dir == "" {
			if proj, err := database.GetProjectByName(task.Project); err == nil && proj != nil {
				dir = proj.Path
			}
		}
		if out, passed := pipeline.RunStepVerify(dir, verifyCmd); !passed {
			database.AppendTaskLog(taskID, "system", "Verification failed — completion rejected; the step keeps running so the agent can fix it.")
			return &Outcome{Kind: KindVerifyFailed, VerifyCommand: verifyCmd, VerifyOutput: out}, nil
		}
		database.AppendTaskLog(taskID, "system", "Verification passed: "+verifyCmd)
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
		if err := database.UpdateTaskStatus(taskID, db.StatusBlocked); err != nil {
			return nil, fmt.Errorf("failed to park gate step for review: %w", err)
		}
		// Logged as a "question" so it lands in the blocked/needs-input lane and the
		// daemon sweep leaves it for the human instead of auto-completing it.
		database.AppendTaskLog(taskID, "question", pipeline.GateStepParkedLog)
		return &Outcome{Kind: KindGateParked}, nil
	}

	// 3. PR-bearing terminal task: park for the human merge.
	var prNumber int
	var prURL string
	if !nonTerminalStep {
		prNumber, prURL = LookupPR(database, task)
	}
	if prNumber > 0 {
		if err := database.UpdateTaskStatus(taskID, db.StatusBlocked); err != nil {
			return nil, fmt.Errorf("failed to move task to review: %w", err)
		}
		reviewMsg := fmt.Sprintf("✅ PR #%d ready for review — merge or close it to complete this task.", prNumber)
		if prURL != "" {
			reviewMsg += " " + prURL
		}
		database.AppendTaskLog(taskID, "question", reviewMsg)
		return &Outcome{Kind: KindPRReview, PRNumber: prNumber, PRURL: prURL}, nil
	}

	// 4. No PR — genuinely done.
	if err := database.UpdateTaskStatus(taskID, db.StatusDone); err != nil {
		return nil, fmt.Errorf("failed to mark task done: %w", err)
	}

	generate := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = tasksummary.GenerateAndStore(ctx, database, taskID)
	}
	if opts.AsyncSummary {
		go generate()
	} else {
		generate()
	}

	return &Outcome{Kind: KindDone}, nil
}
