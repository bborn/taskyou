package pipeline

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestGroupWorkflowsCollapsesMembers(t *testing.T) {
	installWorkflow(t, "pcr", pcrYAML)
	database := testDB(t)
	res, err := Create(database, Options{Goal: "Build a widget", Project: "test", Definition: "pcr", Execute: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Reload from DB so SourceBranch/BranchName reflect what's persisted.
	all := make([]*db.Task, 0, len(res.Tasks)+1)
	for _, tk := range res.Tasks {
		reloaded, _ := database.GetTask(tk.ID)
		all = append(all, reloaded)
	}
	// A plain, unrelated task should not be grouped.
	plain := &db.Task{Title: "unrelated", Status: db.StatusBacklog, Type: db.TypeCode, Project: "test"}
	must(t, database.CreateTask(plain))
	plain, _ = database.GetTask(plain.ID)
	all = append(all, plain)

	groups, rest := GroupWorkflows(all)
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	if len(rest) != 1 || rest[0].Title != "unrelated" {
		t.Errorf("rest = %v, want [unrelated]", rest)
	}
	g := groups[0]
	if g.Total() != 5 {
		t.Errorf("group total = %d, want 5", g.Total())
	}
	if g.Goal() != "Build a widget" {
		t.Errorf("group goal = %q, want 'Build a widget'", g.Goal())
	}
	// Right after Create, the root (Plan) is queued → it's the lead.
	if lead := g.Lead(); lead == nil || stepName(lead.Title) != "Plan" {
		t.Errorf("lead = %v, want Plan", lead)
	}
	if g.DoneCount() != 0 {
		t.Errorf("done = %d, want 0", g.DoneCount())
	}
}

func TestGroupLeadPrefersMostLiveStep(t *testing.T) {
	// Plan done, Code processing, reviews blocked → lead is Code (most live).
	members := []*db.Task{
		{ID: 1, Title: "[Plan] g", Status: db.StatusDone, BranchName: "pipeline/1-g"},
		{ID: 2, Title: "[Code] g", Status: db.StatusProcessing, SourceBranch: "pipeline/1-g"},
		{ID: 3, Title: "[Review A] g", Status: db.StatusBlocked, SourceBranch: "pipeline/1-g"},
		{ID: 4, Title: "[Review B] g", Status: db.StatusBlocked, SourceBranch: "pipeline/1-g"},
	}
	for i := range members {
		members[i].Tags = "pipeline"
	}
	groups, _ := GroupWorkflows(members)
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	g := groups[0]
	if lead := g.Lead(); stepName(lead.Title) != "Code" {
		t.Errorf("lead = %q, want Code", lead.Title)
	}
	if g.DoneCount() != 1 {
		t.Errorf("done = %d, want 1", g.DoneCount())
	}
}

func TestGroupParallelReviewActiveSteps(t *testing.T) {
	members := []*db.Task{
		{ID: 1, Title: "[Plan] g", Status: db.StatusDone, Tags: "pipeline", BranchName: "pipeline/1-g"},
		{ID: 2, Title: "[Code] g", Status: db.StatusDone, Tags: "pipeline", SourceBranch: "pipeline/1-g"},
		{ID: 3, Title: "[Review A] g", Status: db.StatusProcessing, Tags: "pipeline", SourceBranch: "pipeline/1-g"},
		{ID: 4, Title: "[Review B] g", Status: db.StatusProcessing, Tags: "pipeline", SourceBranch: "pipeline/1-g"},
		{ID: 5, Title: "[Collect] g", Status: db.StatusBlocked, Tags: "pipeline", SourceBranch: "pipeline/1-g"},
	}
	groups, _ := GroupWorkflows(members)
	g := groups[0]
	active := g.ActiveSteps()
	if len(active) != 2 {
		t.Fatalf("active steps = %v, want 2 (both reviewers)", active)
	}
}

func TestIsWorkflowTaskRequiresTagAndBranch(t *testing.T) {
	if IsWorkflowTask(&db.Task{Tags: "pipeline"}) {
		t.Error("no branch → not a workflow task")
	}
	if IsWorkflowTask(&db.Task{BranchName: "pipeline/1-x"}) {
		t.Error("no pipeline tag → not a workflow task")
	}
	if !IsWorkflowTask(&db.Task{Tags: "a,pipeline,b", SourceBranch: "pipeline/1-x"}) {
		t.Error("tagged + branch → should be a workflow task")
	}
}

func TestIsTerminalStep(t *testing.T) {
	database := testDB(t)
	branch := "pipeline/1-x"

	// Two workflow steps on a shared branch, wired first → last (last depends on first).
	first := &db.Task{Title: "[Plan] x", Status: db.StatusDone, Type: db.TypeCode, Project: "test", Tags: "pipeline", BranchName: branch}
	must(t, database.CreateTask(first))
	last := &db.Task{Title: "[Verify] x", Status: db.StatusProcessing, Type: db.TypeCode, Project: "test", Tags: "pipeline", SourceBranch: branch}
	must(t, database.CreateTask(last))
	must(t, database.AddDependency(first.ID, last.ID, true)) // first blocks last

	first, _ = database.GetTask(first.ID)
	last, _ = database.GetTask(last.ID)

	// The sink (nothing depends on it) is the terminal step; the earlier step isn't.
	if IsTerminalStep(database, first) {
		t.Error("first step has a dependent (last) — should NOT be terminal")
	}
	if !IsTerminalStep(database, last) {
		t.Error("last step has no dependents — should be terminal")
	}

	// A non-workflow task is never a terminal step, even with no dependents.
	plain := &db.Task{Title: "plain", Status: db.StatusProcessing, Type: db.TypeCode, Project: "test"}
	must(t, database.CreateTask(plain))
	plain, _ = database.GetTask(plain.ID)
	if IsTerminalStep(database, plain) {
		t.Error("non-workflow task should never be a terminal step")
	}
}
