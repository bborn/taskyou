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
