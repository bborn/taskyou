package compute

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExecAdapter_Name(t *testing.T) {
	adapter := NewExecAdapter(ExecConfig{})
	if adapter.Name() != "exec" {
		t.Errorf("expected name 'exec', got '%s'", adapter.Name())
	}
}

func TestExecAdapter_IsAvailable(t *testing.T) {
	adapter := NewExecAdapter(ExecConfig{})
	if !adapter.IsAvailable() {
		t.Error("exec adapter should always be available")
	}
}

func TestExecAdapter_Deploy(t *testing.T) {
	tmpDir := t.TempDir()
	adapter := NewExecAdapter(ExecConfig{WorkDir: tmpDir})

	workflow := &WorkflowDefinition{
		ID:      "test-workflow",
		Name:    "Test Workflow",
		Version: "1.0.0",
		Runtime: "node",
		Code: `
async function workflow(input, { step }) {
  return await step("greet", () => "Hello, " + input.name);
}
`,
	}

	err := adapter.Deploy(context.Background(), workflow)
	if err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	// Check workflow files exist
	workflowDir := filepath.Join(tmpDir, "workflows", "test-workflow")
	if _, err := os.Stat(filepath.Join(workflowDir, "workflow.js")); os.IsNotExist(err) {
		t.Error("workflow.js was not created")
	}
	if _, err := os.Stat(filepath.Join(workflowDir, "workflow.json")); os.IsNotExist(err) {
		t.Error("workflow.json was not created")
	}
}

func TestExecAdapter_DeployPython(t *testing.T) {
	tmpDir := t.TempDir()
	adapter := NewExecAdapter(ExecConfig{WorkDir: tmpDir})

	workflow := &WorkflowDefinition{
		ID:      "python-workflow",
		Name:    "Python Workflow",
		Version: "1.0.0",
		Runtime: "python",
		Code: `
def workflow(input, ctx):
    return ctx.step("greet", lambda: "Hello, " + input["name"])
`,
	}

	err := adapter.Deploy(context.Background(), workflow)
	if err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	// Check workflow files exist
	workflowDir := filepath.Join(tmpDir, "workflows", "python-workflow")
	if _, err := os.Stat(filepath.Join(workflowDir, "workflow.py")); os.IsNotExist(err) {
		t.Error("workflow.py was not created")
	}
}

func TestExecAdapter_StartAndStatus(t *testing.T) {
	// Skip if node is not installed
	if _, err := os.Stat("/usr/bin/node"); os.IsNotExist(err) {
		if _, err := os.Stat("/usr/local/bin/node"); os.IsNotExist(err) {
			t.Skip("node not installed")
		}
	}

	tmpDir := t.TempDir()
	adapter := NewExecAdapter(ExecConfig{WorkDir: tmpDir})

	// Deploy a simple workflow
	workflow := &WorkflowDefinition{
		ID:      "quick-workflow",
		Name:    "Quick Workflow",
		Version: "1.0.0",
		Runtime: "node",
		Code: `
async function workflow(input, { step }) {
  return await step("result", () => ({ greeting: "Hello, " + input.name }));
}
`,
	}

	err := adapter.Deploy(context.Background(), workflow)
	if err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	// Start the workflow
	input := map[string]any{"name": "World"}
	run, err := adapter.Start(context.Background(), "quick-workflow", input)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	if run.ID == "" {
		t.Error("run ID should not be empty")
	}
	if run.WorkflowID != "quick-workflow" {
		t.Errorf("expected workflow ID 'quick-workflow', got '%s'", run.WorkflowID)
	}

	// Wait for completion
	time.Sleep(2 * time.Second)

	// Check status
	status, err := adapter.Status(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	if status.Status != StatusCompleted {
		t.Errorf("expected status 'completed', got '%s'", status.Status)
	}

	if status.Output == nil {
		t.Error("output should not be nil")
	}
}

func TestExecAdapter_ListRuns(t *testing.T) {
	tmpDir := t.TempDir()
	adapter := NewExecAdapter(ExecConfig{WorkDir: tmpDir})

	runs, err := adapter.ListRuns(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("list runs failed: %v", err)
	}

	// Should be empty initially
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestWorkflowDefinition(t *testing.T) {
	workflow := &WorkflowDefinition{
		ID:          "test",
		Name:        "Test",
		Description: "A test workflow",
		Version:     "1.0.0",
		Runtime:     "node",
		Code:        "function workflow() {}",
		Timeout:     5 * time.Minute,
		MaxRetries:  3,
	}

	if workflow.ID != "test" {
		t.Errorf("expected ID 'test', got '%s'", workflow.ID)
	}
	if workflow.Timeout != 5*time.Minute {
		t.Errorf("expected timeout 5m, got %v", workflow.Timeout)
	}
}

func TestWorkflowRun(t *testing.T) {
	now := time.Now()
	run := &WorkflowRun{
		ID:         "run-123",
		WorkflowID: "workflow-1",
		Status:     StatusRunning,
		Input:      map[string]any{"key": "value"},
		StartedAt:  now,
	}

	if run.Status != StatusRunning {
		t.Errorf("expected status running, got %s", run.Status)
	}
	if run.CompletedAt != nil {
		t.Error("completed_at should be nil for running workflow")
	}
}

func TestStepAttempt(t *testing.T) {
	now := time.Now()
	step := StepAttempt{
		ID:        "step-1",
		StepName:  "fetch-data",
		StepType:  StepTypeRun,
		Status:    StatusCompleted,
		Input:     map[string]any{"url": "https://example.com"},
		Output:    map[string]any{"data": "result"},
		StartedAt: now,
	}

	if step.StepType != StepTypeRun {
		t.Errorf("expected step type 'run', got '%s'", step.StepType)
	}
}
