package completion

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

func prJSON(t *testing.T, state github.PRState, number int) string {
	t.Helper()
	return github.MarshalPRInfo(&github.PRInfo{
		Number: number,
		URL:    "https://github.com/o/r/pull/1",
		State:  state,
	})
}

func TestCheckDoneWrite(t *testing.T) {
	tests := []struct {
		name    string
		task    *db.Task
		blocked bool
	}{
		{
			name:    "nil task is allowed",
			task:    nil,
			blocked: false,
		},
		{
			name:    "no PR at all is allowed",
			task:    &db.Task{ID: 1},
			blocked: false,
		},
		{
			// The case that motivated the guard: five tasks were buried in Done
			// while their PRs sat open and unreviewed.
			name:    "open PR is refused",
			task:    &db.Task{ID: 2, PRNumber: 10, PRInfoJSON: prJSON(t, github.PRStateOpen, 10)},
			blocked: true,
		},
		{
			name:    "draft PR is refused",
			task:    &db.Task{ID: 3, PRNumber: 11, PRInfoJSON: prJSON(t, github.PRStateDraft, 11)},
			blocked: true,
		},
		{
			// The human already decided; the daemon's reconciler promotes these.
			name:    "merged PR is allowed",
			task:    &db.Task{ID: 4, PRNumber: 12, PRInfoJSON: prJSON(t, github.PRStateMerged, 12)},
			blocked: false,
		},
		{
			name:    "closed PR is allowed",
			task:    &db.Task{ID: 5, PRNumber: 13, PRInfoJSON: prJSON(t, github.PRStateClosed, 13)},
			blocked: false,
		},
		{
			// Fail closed: a PR number with no recorded state means a PR exists and
			// nothing has told us it reached a terminal state.
			name:    "PR number with no recorded state is refused",
			task:    &db.Task{ID: 6, PRNumber: 14},
			blocked: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard := CheckDoneWrite(tt.task)
			if got := guard != nil; got != tt.blocked {
				t.Fatalf("CheckDoneWrite() blocked = %v, want %v", got, tt.blocked)
			}
			if guard != nil && guard.Error() == "" {
				t.Error("guard returned an empty explanation")
			}
		})
	}
}

// A workflow gate step is released with `ty close`, which is the legitimate use of
// the command. Non-terminal steps never open a PR (only the sink step does), so
// the guard must not stand in the way of advancing a pipeline.
func TestCheckDoneWriteAllowsGateStepRelease(t *testing.T) {
	gateStep := &db.Task{ID: 7, Tags: "pipeline,gate"}
	if guard := CheckDoneWrite(gateStep); guard != nil {
		t.Fatalf("gate step release was refused: %s", guard.Error())
	}
}
