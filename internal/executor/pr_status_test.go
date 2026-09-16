package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

// stubPRFetcher answers PR lookups from a fixed table, or fails with err.
func stubPRFetcher(prs map[string]*github.PRInfo, err error, calls *int) github.BranchFetcher {
	return func(ctx context.Context, repoDir string, branches []string) (*github.BranchPRs, error) {
		*calls++
		if err != nil {
			return nil, err
		}
		res := &github.BranchPRs{PRs: map[string]*github.PRInfo{}, RateRemaining: -1}
		for _, b := range branches {
			if pr := prs[b]; pr != nil {
				res.PRs[b] = pr
			}
		}
		return res, nil
	}
}

// newPRTestExecutor is newTestExecutor with a config that can resolve the test
// project's directory, which PR targets are grouped by.
func newPRTestExecutor(t *testing.T) (*Executor, *db.DB) {
	t.Helper()
	exec, database := newTestExecutor(t)
	exec.config = config.New(database)
	return exec, database
}

func createBranchTask(t *testing.T, database *db.DB, branch, status string, stored *github.PRInfo) *db.Task {
	t.Helper()
	task := &db.Task{Title: branch, Type: "task", Project: "test"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	// CreateTask doesn't store a branch; the executor sets it once a worktree exists.
	task.BranchName = branch
	if err := database.UpdateTask(task); err != nil {
		t.Fatal(err)
	}
	// Status is append-only. Everything here has a branch and so has really run;
	// routing through 'processing' is what gives it the started_at the completion
	// gate requires, and keeps the fixture honest about what happened.
	if err := database.SetTaskStatus(task.ID, db.StatusProcessing, db.ActorDaemon,
		"test fixture: the task started and got a worktree", db.NoEvidence); err != nil {
		t.Fatal(err)
	}
	if status != db.StatusProcessing {
		if err := database.SetTaskStatus(task.ID, status, db.ActorDaemon,
			"test fixture: the task reached "+status,
			db.Observedf("the agent finished its turn on branch %s", branch)); err != nil {
			t.Fatal(err)
		}
	}
	if stored != nil {
		if err := database.UpdateTaskPRInfo(task.ID, stored.URL, stored.Number, github.MarshalPRInfo(stored)); err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func TestRefreshPRStatus_StoresNewPRsAndPromotesMergedReviews(t *testing.T) {
	exec, database := newPRTestExecutor(t)

	review := createBranchTask(t, database, "task/review", db.StatusBlocked,
		&github.PRInfo{Number: 10, State: github.PRStateOpen, CheckState: github.CheckStatePassing})
	fresh := createBranchTask(t, database, "task/fresh", db.StatusProcessing, nil)
	noPR := createBranchTask(t, database, "task/no-pr", db.StatusProcessing, nil)

	calls := 0
	exec.prPoller = github.NewPRPollerWithFetcher(stubPRFetcher(map[string]*github.PRInfo{
		"task/review": {Number: 10, State: github.PRStateMerged},
		"task/fresh":  {Number: 11, State: github.PRStateOpen, CheckState: github.CheckStatePending},
	}, nil, &calls))

	exec.refreshPRStatus(context.Background())

	if calls != 1 {
		t.Errorf("fetch calls = %d, want 1 for the single project repo", calls)
	}

	got, _ := database.GetTask(review.ID)
	if got.Status != db.StatusDone {
		t.Errorf("merged review task status = %q, want done", got.Status)
	}
	if info := github.UnmarshalPRInfo(got.PRInfoJSON); info == nil || info.State != github.PRStateMerged {
		t.Errorf("review task PR info = %s, want merged", got.PRInfoJSON)
	}

	got, _ = database.GetTask(fresh.ID)
	if got.PRNumber != 11 || github.UnmarshalPRInfo(got.PRInfoJSON).CheckState != github.CheckStatePending {
		t.Errorf("fresh task PR = #%d %s, want #11 with pending checks", got.PRNumber, got.PRInfoJSON)
	}

	got, _ = database.GetTask(noPR.ID)
	if got.PRInfoJSON != "" {
		t.Errorf("task without a PR got PR info %s", got.PRInfoJSON)
	}
}

// The flakiness this replaces: a failed lookup looked like "no PR", blanking
// badges and firing a fetch per task. A failure must leave everything as it was.
func TestRefreshPRStatus_FailedLookupKeepsLastKnownState(t *testing.T) {
	exec, database := newPRTestExecutor(t)

	stored := &github.PRInfo{Number: 20, State: github.PRStateOpen, CheckState: github.CheckStateFailing, Mergeable: "MERGEABLE"}
	task := createBranchTask(t, database, "task/flaky", db.StatusBlocked, stored)

	calls := 0
	exec.prPoller = github.NewPRPollerWithFetcher(stubPRFetcher(nil, errors.New("gh api graphql timed out"), &calls))
	exec.refreshPRStatus(context.Background())
	exec.refreshPRStatus(context.Background()) // inside backoff: no second call

	if calls != 1 {
		t.Errorf("fetch calls = %d, want 1 (backoff after failure)", calls)
	}
	got, _ := database.GetTask(task.ID)
	if got.PRInfoJSON != github.MarshalPRInfo(stored) || got.Status != db.StatusBlocked {
		t.Errorf("failed lookup changed the task: status %q, pr %s", got.Status, got.PRInfoJSON)
	}
}

// A merge stored by someone else (or before a restart) is never polled again,
// so promotion can't depend on a fresh lookup.
func TestRefreshPRStatus_PromotesStoredMergeWithoutAskingGitHub(t *testing.T) {
	exec, database := newPRTestExecutor(t)
	task := createBranchTask(t, database, "task/merged", db.StatusBlocked,
		&github.PRInfo{Number: 30, State: github.PRStateMerged})

	calls := 0
	exec.prPoller = github.NewPRPollerWithFetcher(stubPRFetcher(nil, nil, &calls))
	exec.refreshPRStatus(context.Background())

	if calls != 0 {
		t.Errorf("asked GitHub about a merged PR (%d calls)", calls)
	}
	got, _ := database.GetTask(task.ID)
	if got.Status != db.StatusDone {
		t.Errorf("status = %q, want done", got.Status)
	}

	// Reopening against the same merged PR must not bounce it back to done.
	if err := database.SetTaskStatus(task.ID, db.StatusBlocked, db.ActorDaemon,
		"test fixture: the PR was reopened, so the task is parked again",
		db.Observedf("PR reopened after merge")); err != nil {
		t.Fatal(err)
	}
	exec.refreshPRStatus(context.Background())
	got, _ = database.GetTask(task.ID)
	if got.Status != db.StatusBlocked {
		t.Errorf("reopened task auto-completed again: %q", got.Status)
	}
	logs, _ := database.GetTaskLogs(task.ID, 100)
	n := 0
	for _, l := range logs {
		if strings.Contains(l.Content, "task auto-completed") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("auto-complete log lines = %d, want 1", n)
	}
}
