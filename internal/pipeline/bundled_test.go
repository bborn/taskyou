package pipeline

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// rpiSteps is the full seven-phase RPI DAG the bundled rpi.yaml ships, in
// dependency order. Each entry is the step name and its (single) dependency.
var rpiSteps = []struct {
	name string
	dep  string // "" for the root
	gate bool
}{
	{name: "research-questions", dep: ""},
	{name: "research", dep: "research-questions"},
	{name: "design", dep: "research", gate: true},
	{name: "structure-outline", dep: "design"},
	{name: "plan", dep: "structure-outline", gate: true},
	{name: "implement", dep: "plan"},
	{name: "describe-pr", dep: "implement"},
}

// TestBundledRPIResolves proves the bundled "rpi" workflow is compiled into the
// binary and resolvable end-to-end with NO on-disk workflow file present
// (TestMain points TY_WORKFLOWS_DIR at an empty dir, and this test does not
// install one). It asserts DefinitionNames() lists it, KindResolver resolves it as
// a built-in (Custom:false), and Create builds the expected seven-phase DAG with
// the design/plan gates tagged.
func TestBundledRPIResolves(t *testing.T) {
	database := testDB(t)

	// DefinitionNames() surfaces the bundled rpi even though no file is on disk.
	found := false
	for _, n := range DefinitionNames() {
		if n == "rpi" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DefinitionNames() = %v, want it to contain \"rpi\"", DefinitionNames())
	}

	// KindResolver resolves it, and it's labeled a built-in (not custom).
	def, ok := KindResolver(database)("rpi")
	if !ok {
		t.Fatal("KindResolver did not resolve \"rpi\"")
	}
	if def.Custom {
		t.Error("bundled rpi Custom = true, want false (built-in)")
	}
	if len(def.Steps) != len(rpiSteps) {
		t.Fatalf("rpi has %d steps, want %d", len(def.Steps), len(rpiSteps))
	}

	// Create builds the DAG from the bundled def with no on-disk file present.
	res, err := Create(database, Options{Goal: "add a hello endpoint", Project: "test", Definition: "rpi", Execute: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(res.Tasks) != len(rpiSteps) {
		t.Fatalf("got %d tasks, want %d", len(res.Tasks), len(rpiSteps))
	}

	// The root pins the branch; everyone else checks it out via SourceBranch.
	root := taskByStep(res, "research-questions")
	if root == nil {
		t.Fatalf("missing root step in %+v", res.Definition.Steps)
	}
	if root.BranchName == "" || root.SourceBranch != "" {
		t.Errorf("research-questions branch=%q source=%q, want branch pinned", root.BranchName, root.SourceBranch)
	}

	for _, want := range rpiSteps {
		tk := taskByStep(res, want.name)
		if tk == nil {
			t.Fatalf("missing step %q in %+v", want.name, res.Definition.Steps)
		}
		// Reload so persisted tags/branches/status are reflected.
		tk, _ = database.GetTask(tk.ID)

		// Gate steps carry the "gate" tag token alongside "pipeline".
		wantTags := "pipeline"
		if want.gate {
			wantTags = "pipeline,gate"
		}
		if tk.Tags != wantTags {
			t.Errorf("%s tags = %q, want %q", want.name, tk.Tags, wantTags)
		}
		if IsGateStep(tk) != want.gate {
			t.Errorf("IsGateStep(%s) = %v, want %v", want.name, IsGateStep(tk), want.gate)
		}

		if want.dep == "" {
			// Root starts (queued) and owns the branch.
			if tk.Status != db.StatusQueued {
				t.Errorf("%s status = %q, want queued", want.name, tk.Status)
			}
			continue
		}
		// Non-root steps check out the shared branch, wait blocked, and depend on
		// exactly their predecessor.
		if tk.SourceBranch != root.BranchName {
			t.Errorf("%s SourceBranch = %q, want %q", want.name, tk.SourceBranch, root.BranchName)
		}
		if tk.Status != db.StatusBlocked {
			t.Errorf("%s status = %q, want blocked", want.name, tk.Status)
		}
		dep := taskByStep(res, want.dep)
		if dep == nil {
			t.Fatalf("missing dep %q for %q", want.dep, want.name)
		}
		assertDep(t, database, dep.ID, tk.ID)
	}
}

// TestBundledRPIWellFormed guards against a malformed bundled rpi.yaml shipping:
// it must parse and validate as an acyclic DAG with the expected step names and
// edges, design/plan must be gates, and every step must carry a non-empty
// instruction (its own prompt or a composed one).
func TestBundledRPIWellFormed(t *testing.T) {
	database := testDB(t)
	def, ok := KindResolver(database)("rpi")
	if !ok {
		t.Fatal("KindResolver did not resolve \"rpi\"")
	}

	// validate() enforces unique names, known deps, a root, and acyclicity.
	if err := def.validate(); err != nil {
		t.Fatalf("bundled rpi is not a valid DAG: %v", err)
	}

	byName := make(map[string]Step, len(def.Steps))
	for _, s := range def.Steps {
		byName[s.Name] = s
	}
	for _, want := range rpiSteps {
		s, ok := byName[want.name]
		if !ok {
			t.Fatalf("bundled rpi is missing step %q", want.name)
		}
		if s.Gate != want.gate {
			t.Errorf("step %q Gate = %v, want %v", want.name, s.Gate, want.gate)
		}
		// The step must actually say what to do — either a verbatim Instruction
		// (doc phases) or a Prompt (the code phases) — which effectiveInstruction
		// surfaces either way.
		if strings.TrimSpace(effectiveInstruction(def, want.name)) == "" {
			t.Errorf("step %q has an empty instruction", want.name)
		}
		// Edges: each non-root step depends on exactly its predecessor.
		if want.dep == "" {
			if len(s.Deps) != 0 {
				t.Errorf("root step %q has deps %v, want none", want.name, s.Deps)
			}
			continue
		}
		if len(s.Deps) != 1 || s.Deps[0] != want.dep {
			t.Errorf("step %q deps = %v, want [%q]", want.name, s.Deps, want.dep)
		}
	}

	// research must stay blind to the raw goal: its instruction must not carry the
	// {{goal}} placeholder (it works only from the research-questions artifact).
	if strings.Contains(effectiveInstruction(def, "research"), "{{goal}}") {
		t.Error("research step references {{goal}} — it must stay blind to the raw goal")
	}
}
