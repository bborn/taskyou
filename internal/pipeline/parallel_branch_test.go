package pipeline

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// A fan-out step must be given its OWN branch, and it must be the same branch its
// composed handoff tells the agent to push to.
//
// This is the bug that killed every workflow with parallel steps: the
// instructions said "push to {{branch}}-planreviewa", but the task carried only
// SourceBranch, so the executor tried to attach the step to the SHARED branch —
// which a sibling (or the finished root) already had checked out. git allows one
// worktree per branch, so the step died at spawn, before its agent ever ran.
func TestParallelStepsGetTheirOwnBranch(t *testing.T) {
	installWorkflow(t, "pcr", pcrYAML)
	database := testDB(t)
	res, err := Create(database, Options{Goal: "Add rate limiting to the API", Project: "test", Definition: "pcr"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	shared := res.Branch

	rvA := taskByStep(res, "Review A")
	rvB := taskByStep(res, "Review B")

	for _, tk := range []*db.Task{rvA, rvB} {
		if tk.SourceBranch != shared {
			t.Errorf("%s SourceBranch = %q, want the shared branch %q to cut from", tk.Title, tk.SourceBranch, shared)
		}
		if tk.BranchName == "" || tk.BranchName == shared {
			t.Errorf("%s BranchName = %q, want its own branch (siblings cannot share one branch)", tk.Title, tk.BranchName)
		}
		// The worktree it gets and the branch it is told to push to must agree.
		if !strings.Contains(tk.Body, tk.BranchName) {
			t.Errorf("%s is on branch %q but its handoff never names it:\n%s", tk.Title, tk.BranchName, tk.Body)
		}
	}
	if rvA.BranchName == rvB.BranchName {
		t.Errorf("both reviews got branch %q; they would contend", rvA.BranchName)
	}

	// The step that consumes them is told to read those exact branches.
	collect := taskByStep(res, "Collect")
	for _, tk := range []*db.Task{rvA, rvB} {
		if !strings.Contains(collect.Body, tk.BranchName) {
			t.Errorf("Collect is not told to read %q:\n%s", tk.BranchName, collect.Body)
		}
	}
}

// Sequential steps keep the proven behaviour: they attach to the shared branch
// itself, so work accumulates on it commit by commit.
func TestSequentialStepsStayOnTheSharedBranch(t *testing.T) {
	installWorkflow(t, "pcr", pcrYAML)
	database := testDB(t)
	res, err := Create(database, Options{Goal: "Add rate limiting to the API", Project: "test", Definition: "pcr"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	code := taskByStep(res, "Code")
	if code.BranchName != "" {
		t.Errorf("Code (sequential) got its own branch %q, want it on the shared branch", code.BranchName)
	}
	if code.SourceBranch != res.Branch {
		t.Errorf("Code SourceBranch = %q, want %q", code.SourceBranch, res.Branch)
	}
	// Collect has two deps but no peer running beside it — also sequential.
	collect := taskByStep(res, "Collect")
	if collect.BranchName != "" {
		t.Errorf("Collect got its own branch %q, want it on the shared branch", collect.BranchName)
	}
}

// StepBranch is the single source of truth for a fan-out step's branch name: the
// pinned branch and the name printed in the instructions come from it.
func TestStepBranchMatchesComposedInstructions(t *testing.T) {
	shared := "pipeline/12-goal"
	if got, want := StepBranch(shared, "Review A"), "pipeline/12-goal-review-a"; got != want {
		t.Errorf("StepBranch = %q, want %q", got, want)
	}
}
