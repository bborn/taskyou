package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// installFormWorkflow isolates the workflows dir and drops in one known workflow,
// so the pipeline selector tests don't depend on whatever is installed on the dev
// machine (there are no built-in workflows anymore).
func installFormWorkflow(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TY_WORKFLOWS_DIR", dir)
	yaml := "name: " + name + "\nsteps:\n  - name: Solo\n    prompt: Do {{goal}}.\n"
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("install workflow: %v", err)
	}
}

// TestFormPipelineFieldDefaultsToSingleTask verifies a fresh form creates an
// ordinary task (no pipeline) until the user selects one.
func TestFormPipelineFieldDefaultsToSingleTask(t *testing.T) {
	installFormWorkflow(t, "plan-code-review")
	m := NewFormModel(nil, 100, 50, "", nil)
	if m.Pipeline() != "" {
		t.Errorf("default Pipeline() = %q, want empty", m.Pipeline())
	}
	if len(m.pipelines) < 2 || m.pipelines[0] != "" {
		t.Errorf("pipelines = %v, want first entry empty followed by definitions", m.pipelines)
	}
}

// TestFormPipelineFieldCycles verifies the pipeline selector advances to a real
// definition and the getter reflects it.
func TestFormPipelineFieldCycles(t *testing.T) {
	installFormWorkflow(t, "plan-code-review")
	m := NewFormModel(nil, 100, 50, "", nil)
	m.showAdvanced = true
	m.focused = FieldPipeline

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	form, ok := updated.(*FormModel)
	if !ok {
		t.Fatalf("Update returned %T, want *FormModel", updated)
	}
	if form.Pipeline() == "" {
		t.Fatal("Pipeline() still empty after cycling right")
	}
	if form.Pipeline() != "plan-code-review" {
		t.Errorf("Pipeline() = %q, want plan-code-review", form.Pipeline())
	}

	// Cycling left returns to "single task".
	updated, _ = form.Update(tea.KeyMsg{Type: tea.KeyLeft})
	form = updated.(*FormModel)
	if form.Pipeline() != "" {
		t.Errorf("Pipeline() = %q after cycling back, want empty", form.Pipeline())
	}
}

// TestFormPipelineFieldHiddenWhenEditing verifies the pipeline option is only
// offered for new tasks, never when editing an existing one.
func TestFormPipelineFieldVisibility(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	m.showAdvanced = true
	if !m.isFieldVisible(FieldPipeline) {
		t.Error("FieldPipeline should be visible for a new task in advanced mode")
	}
	m.showAdvanced = false
	if m.isFieldVisible(FieldPipeline) {
		t.Error("FieldPipeline should be hidden when advanced fields are collapsed")
	}
	m.showAdvanced = true
	m.isEdit = true
	if m.isFieldVisible(FieldPipeline) {
		t.Error("FieldPipeline should be hidden when editing an existing task")
	}
}

// TestFormPipelineRendersSelector verifies the selector appears in the rendered
// form when advanced fields are shown.
func TestFormPipelineRendersSelector(t *testing.T) {
	m := NewFormModel(nil, 120, 50, "", nil)
	m.showAdvanced = true
	if !strings.Contains(m.View(), "Workflow") {
		t.Error("advanced form View should render the Workflow selector")
	}
}
