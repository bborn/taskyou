package ui

import (
	"strings"
	"testing"
)

// TestFormKindDefaultsToSingleTask: a fresh form makes an ordinary task (no
// workflow) until a workflow kind is picked.
func TestFormKindDefaultsToSingleTask(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	if m.Pipeline() != "" {
		t.Errorf("default Pipeline() = %q, want empty (single task)", m.Pipeline())
	}
	if len(m.types) < 2 || m.types[0] != "" {
		t.Errorf("kinds = %v, want first entry empty followed by types/kinds", m.types)
	}
}

// TestFormKindWorkflowDetection: Pipeline() is non-empty only when the selected
// kind is a workflow (has steps); a plain type is a single task.
func TestFormKindWorkflowDetection(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)

	m.taskType = "plan-code-review" // a built-in workflow kind
	if m.Pipeline() != "plan-code-review" {
		t.Errorf("Pipeline() = %q for a workflow kind, want plan-code-review", m.Pipeline())
	}

	m.taskType = "code" // a plain type → single task
	if m.Pipeline() != "" {
		t.Errorf("Pipeline() = %q for a plain type, want empty", m.Pipeline())
	}
}

// TestBuildKindsIncludesWorkflowsForNewTasks: the one kind list carries workflow
// kinds for a new task, but only types when editing (a task can't become a
// workflow after creation).
func TestBuildKindsIncludesWorkflowsForNewTasks(t *testing.T) {
	newKinds := buildKinds(nil, true)
	if !kindsContain(newKinds, "plan-code-review") {
		t.Errorf("new-task kinds should include workflow kinds; got %v", newKinds)
	}
	editKinds := buildKinds(nil, false)
	if kindsContain(editKinds, "plan-code-review") {
		t.Errorf("edit kinds should not offer workflows; got %v", editKinds)
	}
}

// TestFormKindRendersSelector: the unified selector renders under one "Kind" label.
func TestFormKindRendersSelector(t *testing.T) {
	m := NewFormModel(nil, 120, 50, "", nil)
	m.showAdvanced = true
	view := m.View()
	if !strings.Contains(view, "Kind") {
		t.Error("advanced form View should render the Kind selector")
	}
	if strings.Contains(view, "Workflow") {
		t.Error("the separate Workflow selector should be gone (merged into Kind)")
	}
}

func kindsContain(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
