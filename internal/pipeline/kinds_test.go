package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// writeKind drops a kind YAML file into the (test-scoped) global kinds dir.
func writeKind(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tasksByTitlePrefix collects tasks whose title starts with "[<name>]".
func hasTaskTitled(res *Result, stepName string) bool {
	for _, tk := range res.Tasks {
		if strings.HasPrefix(tk.Title, "["+stepName+"]") {
			return true
		}
	}
	return false
}

// TestCreateSingleTaskFileKind: a steps-less kind file produces ONE ordinary task
// typed with the kind — no workflow branch, no pipeline tag.
func TestCreateSingleTaskFileKind(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TY_WORKFLOWS_DIR", dir)
	writeKind(t, dir, "quickfix", "name: quickfix\ninstructions: Fix the thing. Keep it minimal.\n")
	database := testDB(t)

	res, err := Create(database, Options{Goal: "fix the bug", Project: "test", Definition: "quickfix"})
	if err != nil {
		t.Fatalf("Create single-task kind: %v", err)
	}
	if len(res.Tasks) != 1 {
		t.Fatalf("single-task kind should make 1 task, got %d", len(res.Tasks))
	}
	task := res.Tasks[0]
	if task.Type != "quickfix" {
		t.Errorf("task Type = %q, want quickfix (so the kind's instructions apply)", task.Type)
	}
	if strings.Contains(task.Tags, "pipeline") {
		t.Errorf("single task should not carry the pipeline tag, got tags %q", task.Tags)
	}
	if res.Branch != "" {
		t.Errorf("single task should have no shared branch, got %q", res.Branch)
	}
}

// TestCreateSingleTaskDBType: a kind name that is a DB task type (not a file) also
// resolves to a single task — the convention bridge, no file required.
func TestCreateSingleTaskDBType(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TY_WORKFLOWS_DIR", dir)
	database := testDB(t)
	if err := database.CreateTaskType(&db.TaskType{Name: "triage", Label: "Triage", Instructions: "triage it"}); err != nil {
		t.Fatalf("create task type: %v", err)
	}

	res, err := Create(database, Options{Goal: "look at this", Project: "test", Definition: "triage"})
	if err != nil {
		t.Fatalf("Create from DB type: %v", err)
	}
	if len(res.Tasks) != 1 || res.Tasks[0].Type != "triage" {
		t.Fatalf("DB-type kind should make 1 task typed 'triage', got %d tasks type=%q", len(res.Tasks), res.Tasks[0].Type)
	}
}

// TestCreateNestedWorkflowFlattens: a workflow step that runs another workflow kind
// is inlined into the built DAG.
func TestCreateNestedWorkflowFlattens(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TY_WORKFLOWS_DIR", dir)
	writeKind(t, dir, "review-pair", "name: review-pair\nsteps:\n  - {name: R1, prompt: review one}\n  - {name: R2, prompt: review two}\n")
	writeKind(t, dir, "code-and-review", "name: code-and-review\nsteps:\n  - {name: Code, prompt: write code}\n  - {name: Review, kind: review-pair, deps: [Code]}\n")
	database := testDB(t)

	res, err := Create(database, Options{Goal: "add feature", Project: "test", Definition: "code-and-review"})
	if err != nil {
		t.Fatalf("Create nested: %v", err)
	}
	// Review inlined into Review/R1 + Review/R2 → 3 tasks, not 2.
	if len(res.Tasks) != 3 {
		t.Fatalf("nested workflow should flatten to 3 tasks, got %d", len(res.Tasks))
	}
	for _, want := range []string{"Code", "Review/R1", "Review/R2"} {
		if !hasTaskTitled(res, want) {
			t.Errorf("missing flattened step %q; titles=%v", want, taskTitles(res))
		}
	}
	if hasTaskTitled(res, "Review") {
		t.Errorf("the Review reference should have been inlined away")
	}
}

// TestCreateStepKindSetsTaskType: a leaf step that references a single-task kind
// carries that kind as the task's Type (so its instructions apply at run time).
func TestCreateStepKindSetsTaskType(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TY_WORKFLOWS_DIR", dir)
	writeKind(t, dir, "polish", "name: polish\ninstructions: polish the prose\n")
	writeKind(t, dir, "draft-flow", "name: draft-flow\nsteps:\n  - {name: Draft, prompt: draft it}\n  - {name: Polish, kind: polish, deps: [Draft]}\n")
	database := testDB(t)

	res, err := Create(database, Options{Goal: "write a post", Project: "test", Definition: "draft-flow"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	polish := taskByStep(res, "Polish")
	if polish == nil {
		t.Fatalf("no Polish step task; steps=%v", res.Definition.Steps)
	}
	if polish.Type != "polish" {
		t.Errorf("Polish task Type = %q, want polish", polish.Type)
	}
	draft := taskByStep(res, "Draft")
	if draft == nil || draft.Type != db.TypeCode {
		t.Errorf("plain prompt step should default to type 'code', got %v", draft)
	}
}

func taskTitles(res *Result) []string {
	out := make([]string, len(res.Tasks))
	for i, tk := range res.Tasks {
		out[i] = tk.Title
	}
	return out
}

// TestLookupKindInstructions: a file single-task kind resolves as a type; a
// workflow kind or unknown name does not.
func TestLookupKindInstructions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TY_WORKFLOWS_DIR", dir)
	writeKind(t, dir, "polish", "name: polish\ninstructions: polish the prose\n")
	writeKind(t, dir, "flow", "name: flow\nsteps:\n  - {name: A, prompt: do a}\n")

	if got := LookupKindInstructions("polish"); got != "polish the prose" {
		t.Errorf("single-task kind instructions = %q, want 'polish the prose'", got)
	}
	if got := LookupKindInstructions("flow"); got != "" {
		t.Errorf("workflow kind should not resolve as a single-task type, got %q", got)
	}
	if got := LookupKindInstructions("nope"); got != "" {
		t.Errorf("unknown kind should resolve to empty, got %q", got)
	}
}

// TestParseKindValidation: a steps-less kind needs instructions, and every step
// needs a prompt or a kind.
func TestParseKindValidation(t *testing.T) {
	if _, err := ParseDefinition([]byte("name: empty\n")); err == nil {
		t.Error("a kind with no steps and no instructions should be rejected")
	}
	if _, err := ParseDefinition([]byte("name: bad\nsteps:\n  - {name: X}\n")); err == nil {
		t.Error("a step with neither prompt nor kind should be rejected")
	}
	// A step that only references a kind (no prompt) is valid.
	if _, err := ParseDefinition([]byte("name: ok\nsteps:\n  - {name: X, kind: code}\n")); err != nil {
		t.Errorf("a kind-only step should be valid, got %v", err)
	}
	// An instructions-only kind is valid (a single-task kind).
	if _, err := ParseDefinition([]byte("name: solo\ninstructions: do the thing\n")); err != nil {
		t.Errorf("an instructions-only kind should be valid, got %v", err)
	}
}
