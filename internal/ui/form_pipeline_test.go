package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installFlow writes a workflow file to a fresh global kinds dir for the test, so
// a same-named kind runs as a workflow.
func installFlow(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TY_WORKFLOWS_DIR", dir)
	body := "name: " + name + "\nsteps:\n  - {name: A, prompt: do a}\n  - {name: B, deps: [A], prompt: do b}\n"
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

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
// kind has a same-named workflow file; a plain type is a single task.
func TestFormKindWorkflowDetection(t *testing.T) {
	installFlow(t, "myflow")
	m := NewFormModel(nil, 100, 50, "", nil)

	m.taskType = "myflow" // a workflow kind (has a same-named file)
	if m.Pipeline() != "myflow" {
		t.Errorf("Pipeline() = %q for a workflow kind, want myflow", m.Pipeline())
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
	installFlow(t, "myflow")
	newKinds := buildKinds(nil, true)
	if !kindsContain(newKinds, "myflow") {
		t.Errorf("new-task kinds should include workflow kinds; got %v", newKinds)
	}
	editKinds := buildKinds(nil, false)
	if kindsContain(editKinds, "myflow") {
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
