package main

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/pipeline"
)

// wfGroups builds groups the same way the commands do, from raw tasks.
func wfGroups(tasks ...*db.Task) []*pipeline.Group {
	groups, _ := pipeline.GroupWorkflows(tasks)
	return groups
}

func wfStep(id int64, title, branch, status string) *db.Task {
	return &db.Task{ID: id, Title: title, Status: status, Tags: "pipeline", SourceBranch: branch, Project: "p"}
}

func TestFindWorkflowGroup(t *testing.T) {
	groups := wfGroups(
		wfStep(10, "[research] Alpha goal", "pipeline/10-alpha", db.StatusDone),
		wfStep(11, "[design] Alpha goal", "pipeline/10-alpha", db.StatusProcessing),
		wfStep(20, "[research] Beta goal", "pipeline/20-beta", db.StatusDone),
		wfStep(21, "[design] Beta goal", "pipeline/20-beta", db.StatusBlocked),
	)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	t.Run("by member task id", func(t *testing.T) {
		g := findWorkflowGroup(groups, "11")
		if g == nil || g.Branch != "pipeline/10-alpha" {
			t.Fatalf("id lookup got %v", g)
		}
		// A non-root member must resolve to its whole workflow.
		if g2 := findWorkflowGroup(groups, "21"); g2 == nil || g2.Branch != "pipeline/20-beta" {
			t.Fatalf("id lookup for non-root got %v", g2)
		}
	})

	t.Run("accepts a #-prefixed id", func(t *testing.T) {
		if g := findWorkflowGroup(groups, "#20"); g == nil || g.Branch != "pipeline/20-beta" {
			t.Fatalf("#id lookup got %v", g)
		}
	})

	t.Run("by exact branch", func(t *testing.T) {
		if g := findWorkflowGroup(groups, "pipeline/20-beta"); g == nil || g.Branch != "pipeline/20-beta" {
			t.Fatalf("branch lookup failed")
		}
	})

	t.Run("by branch substring", func(t *testing.T) {
		if g := findWorkflowGroup(groups, "alpha"); g == nil || g.Branch != "pipeline/10-alpha" {
			t.Fatalf("substring lookup failed")
		}
	})

	t.Run("unknown ref returns nil", func(t *testing.T) {
		if g := findWorkflowGroup(groups, "nope-not-here"); g != nil {
			t.Fatalf("expected nil, got %v", g)
		}
		if g := findWorkflowGroup(groups, ""); g != nil {
			t.Fatalf("expected nil for empty ref")
		}
	})
}

func TestWorkflowGroupProgressAndLead(t *testing.T) {
	groups := wfGroups(
		wfStep(1, "[research] G", "pipeline/1-g", db.StatusDone),
		wfStep(2, "[design] G", "pipeline/1-g", db.StatusDone),
		wfStep(3, "[implement] G", "pipeline/1-g", db.StatusProcessing),
		wfStep(4, "[ship] G", "pipeline/1-g", db.StatusBlocked),
	)
	g := groups[0]
	if g.Total() != 4 || g.DoneCount() != 2 {
		t.Fatalf("progress = %d/%d, want 2/4", g.DoneCount(), g.Total())
	}
	// The frontier must be the live step, not the last one.
	if lead := g.Lead(); lead == nil || lead.ID != 3 {
		t.Fatalf("lead = %v, want #3 (the processing step)", lead)
	}
}

func TestWorkflowStepMarkDistinguishesStatuses(t *testing.T) {
	seen := map[string]string{}
	for _, s := range []string{db.StatusDone, db.StatusProcessing, db.StatusQueued, db.StatusBlocked, db.StatusBacklog} {
		m := workflowStepMark(&db.Task{Status: s})
		if m == "" {
			t.Fatalf("no mark for status %q", s)
		}
		if prev, ok := seen[m]; ok && prev != s {
			// done/processing/blocked must be visually distinct — that's the whole
			// point of the column.
			if s != db.StatusBacklog && prev != db.StatusBacklog {
				t.Errorf("statuses %q and %q share mark %q", prev, s, m)
			}
		}
		seen[m] = s
	}
}

func TestFirstLineCollapsesMultiParagraphGoal(t *testing.T) {
	goal := "RubyLLM + Rails Per Account CSM\n\nOkay, I want you to brainstorm this idea..."
	if got := firstLine(goal); got != "RubyLLM + Rails Per Account CSM" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine("\n\n  leading blanks  \nnext"); got != "leading blanks" {
		t.Errorf("firstLine trimmed = %q", got)
	}
	if got := firstLine(""); got != "" {
		t.Errorf("firstLine empty = %q", got)
	}
}

func TestStepDisplayName(t *testing.T) {
	if got := stepDisplayName("[structure-outline] Some goal"); got != "structure-outline" {
		t.Errorf("got %q", got)
	}
	// A step with no bracketed label still renders something.
	if got := stepDisplayName("plain title"); got == "" {
		t.Errorf("expected fallback for unlabeled title")
	}
}

func TestHumanBytes(t *testing.T) {
	if got := humanBytes(512); got != "512B" {
		t.Errorf("got %q", got)
	}
	if got := humanBytes(2048); !strings.HasPrefix(got, "2.0KB") {
		t.Errorf("got %q", got)
	}
}

// The --workflows/--no-workflows split must partition the list exactly: every
// task lands in one side or the other, never both, never neither.
func TestWorkflowFilterPartitionsTasks(t *testing.T) {
	tasks := []*db.Task{
		wfStep(1, "[research] G", "pipeline/1-g", db.StatusDone),
		wfStep(2, "[design] G", "pipeline/1-g", db.StatusProcessing),
		{ID: 3, Title: "standalone task", Status: db.StatusQueued},
		{ID: 4, Title: "another standalone", Status: db.StatusBlocked},
	}

	var wf, plain []*db.Task
	for _, tk := range tasks {
		if pipeline.IsWorkflowTask(tk) {
			wf = append(wf, tk)
		} else {
			plain = append(plain, tk)
		}
	}
	if len(wf) != 2 || len(plain) != 2 {
		t.Fatalf("partition = %d workflow / %d plain, want 2/2", len(wf), len(plain))
	}
	if len(wf)+len(plain) != len(tasks) {
		t.Fatalf("partition lost tasks")
	}
}
