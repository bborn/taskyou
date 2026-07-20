package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bborn/workflow/internal/db"
)

func wfTask(id int64, title, branch, status string) *db.Task {
	return &db.Task{ID: id, Title: title, Status: status, Tags: "pipeline", SourceBranch: branch}
}

func plainTask(id int64, title, status string) *db.Task {
	return &db.Task{ID: id, Title: title, Status: status}
}

func TestParseFilterKind(t *testing.T) {
	cases := []struct {
		query    string
		wantKind string
		wantRest string
	}{
		{"", "", ""},
		{"rate limiting", "", "rate limiting"},
		{"is:workflow", filterKindWorkflow, ""},
		{"is:wf", filterKindWorkflow, ""},
		{"is:pipeline", filterKindWorkflow, ""},
		{"is:task", filterKindTask, ""},
		{"is:normal", filterKindTask, ""},
		// The token must be stripped from the keyword, or it pollutes fuzzy scoring.
		{"is:workflow lumen", filterKindWorkflow, "lumen"},
		{"lumen is:workflow", filterKindWorkflow, "lumen"},
		{"IS:WORKFLOW lumen", filterKindWorkflow, "lumen"},
		// Project tags must survive untouched.
		{"is:task [offerlab] bump", filterKindTask, "[offerlab] bump"},
	}
	for _, c := range cases {
		kind, rest := parseFilterKind(c.query)
		if kind != c.wantKind || rest != c.wantRest {
			t.Errorf("parseFilterKind(%q) = (%q,%q), want (%q,%q)", c.query, kind, rest, c.wantKind, c.wantRest)
		}
	}
}

func TestFilterTasksByKind(t *testing.T) {
	tasks := []*db.Task{
		wfTask(1, "[design] Alpha", "pipeline/1-a", db.StatusDone),
		wfTask(2, "[plan] Alpha", "pipeline/1-a", db.StatusProcessing),
		plainTask(3, "standalone", db.StatusQueued),
	}

	if got := filterTasksByKind(tasks, ""); len(got) != 3 {
		t.Errorf("empty kind should be a no-op, got %d", len(got))
	}
	wf := filterTasksByKind(tasks, filterKindWorkflow)
	if len(wf) != 2 {
		t.Fatalf("workflow filter got %d, want 2", len(wf))
	}
	plain := filterTasksByKind(tasks, filterKindTask)
	if len(plain) != 1 || plain[0].ID != 3 {
		t.Fatalf("task filter got %v, want just #3", plain)
	}
	// Exact partition: no task in both, none dropped.
	if len(wf)+len(plain) != len(tasks) {
		t.Errorf("partition lost or duplicated tasks")
	}
}

// A workflow-tagged task with no shared branch isn't a workflow member, so it
// must fall on the "task" side rather than vanishing from both filters.
func TestFilterKindTreatsBranchlessTaggedTaskAsPlain(t *testing.T) {
	odd := &db.Task{ID: 9, Title: "tagged but no branch", Tags: "pipeline"}
	tasks := []*db.Task{odd}

	if got := filterTasksByKind(tasks, filterKindWorkflow); len(got) != 0 {
		t.Errorf("branchless task should not count as workflow, got %d", len(got))
	}
	if got := filterTasksByKind(tasks, filterKindTask); len(got) != 1 {
		t.Errorf("branchless task should count as plain, got %d", len(got))
	}
}

func TestWorkflowStepLabel(t *testing.T) {
	if got := workflowStepLabel("[structure-outline] Some goal"); got != "structure-outline" {
		t.Errorf("got %q", got)
	}
	if got := workflowStepLabel("no brackets here"); got != "no brackets here" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("x", 40)
	if got := []rune(workflowStepLabel(long)); len(got) > 22 {
		t.Errorf("long unlabeled title not truncated: %d chars", len(got))
	}
	// Multi-byte titles must not be sliced mid-character.
	multibyte := strings.Repeat("é", 40)
	got := workflowStepLabel(multibyte)
	if !utf8.ValidString(got) {
		t.Errorf("truncation produced invalid UTF-8: %q", got)
	}
	if r := []rune(got); len(r) > 22 {
		t.Errorf("multibyte title not truncated: %d runes", len(r))
	}
}

// The detail panel is render-cached; if the hash ignored sibling status the flow
// would freeze mid-run showing stale steps.
func TestWorkflowStepsHashChangesWithStatus(t *testing.T) {
	m := &DetailModel{workflowSteps: []*db.Task{
		wfTask(1, "[design] A", "pipeline/1-a", db.StatusDone),
		wfTask(2, "[plan] A", "pipeline/1-a", db.StatusProcessing),
	}}
	before := m.workflowStepsHash()

	m.workflowSteps[1].Status = db.StatusDone
	if after := m.workflowStepsHash(); after == before {
		t.Fatal("hash must change when a sibling step advances, or the cached flow goes stale")
	}

	// No steps => stable zero, so standalone tasks never thrash the cache.
	empty := &DetailModel{}
	if empty.workflowStepsHash() != 0 {
		t.Error("empty workflow should hash to 0")
	}
}

func TestRenderWorkflowFlowMarksCurrentStep(t *testing.T) {
	steps := []*db.Task{
		wfTask(1, "[research] A", "pipeline/1-a", db.StatusDone),
		wfTask(2, "[design] A", "pipeline/1-a", db.StatusProcessing),
		wfTask(3, "[plan] A", "pipeline/1-a", db.StatusBlocked),
	}
	m := &DetailModel{task: steps[1], workflowSteps: steps}

	out := m.renderWorkflowFlow(false)
	if out == "" {
		t.Fatal("expected flow output")
	}
	if !strings.Contains(out, "you are here") {
		t.Error("current step must be marked")
	}
	if !strings.Contains(out, "1/3 steps complete") {
		t.Errorf("expected progress line, got:\n%s", out)
	}
	for _, name := range []string{"research", "design", "plan"} {
		if !strings.Contains(out, name) {
			t.Errorf("missing step %q in:\n%s", name, out)
		}
	}

	// Standalone task renders nothing at all — no empty header.
	if got := (&DetailModel{task: plainTask(5, "x", db.StatusQueued)}).renderWorkflowFlow(false); got != "" {
		t.Errorf("standalone task should render no workflow section, got %q", got)
	}
}
