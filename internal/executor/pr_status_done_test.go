package executor

import (
	"context"
	"fmt"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

// Done tasks are listed newest first and capped, and a real board has thousands.
// A done task whose PR is still open must be polled however far down the list it
// sits, or its badge stays frozen at OPEN after the PR merges.
func TestRefreshPRStatus_PollsOldDoneTasksWithOpenPRs(t *testing.T) {
	exec, database := newPRTestExecutor(t)

	old := createBranchTask(t, database, "task/old-open-pr", db.StatusDone,
		&github.PRInfo{Number: 40, State: github.PRStateOpen, CheckState: github.CheckStatePassing})
	for i := 0; i < 204; i++ {
		createBranchTask(t, database, fmt.Sprintf("task/newer-%d", i), db.StatusDone, nil)
	}

	calls := 0
	exec.prPoller = github.NewPRPollerWithFetcher(stubPRFetcher(map[string]*github.PRInfo{
		"task/old-open-pr": {Number: 40, State: github.PRStateMerged},
	}, nil, &calls))
	exec.refreshPRStatus(context.Background())

	got, _ := database.GetTask(old.ID)
	if info := github.UnmarshalPRInfo(got.PRInfoJSON); info == nil || info.State != github.PRStateMerged {
		t.Fatalf("old done task PR = %s, want merged (fetch calls %d)", got.PRInfoJSON, calls)
	}
}
