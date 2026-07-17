package pipeline

import (
	"sort"
	"strings"
	"testing"
)

// resolverFrom builds a ResolveFunc backed by a map of kinds.
func resolverFrom(kinds map[string]Definition) ResolveFunc {
	return func(name string) (Definition, bool) {
		d, ok := kinds[name]
		return d, ok
	}
}

func stepByName(steps []Step, name string) (Step, bool) {
	for _, s := range steps {
		if s.Name == name {
			return s, true
		}
	}
	return Step{}, false
}

func sortedDeps(s Step) []string {
	d := append([]string{}, s.Deps...)
	sort.Strings(d)
	return d
}

// TestFlattenSingleTaskKindUnchanged: a steps-less kind flattens to itself.
func TestFlattenSingleTaskKindUnchanged(t *testing.T) {
	code := Definition{Name: "code", Instructions: "write code"}
	got, err := Flatten(code, resolverFrom(map[string]Definition{"code": code}))
	if err != nil {
		t.Fatalf("flatten single-task kind: %v", err)
	}
	if !got.IsSingle() || got.Instructions != "write code" {
		t.Fatalf("expected the single-task kind unchanged, got %+v", got)
	}
}

// TestFlattenLeafKindReferenceStaysLeaf: a step referencing a single-task kind is
// a leaf that keeps its Kind (so the task's Type applies that kind's instructions).
func TestFlattenLeafKindReferenceStaysLeaf(t *testing.T) {
	kinds := map[string]Definition{
		"code": {Name: "code", Instructions: "write code"},
	}
	def := Definition{Name: "solo", Steps: []Step{
		{Name: "Do", Kind: "code"},
	}}
	got, err := Flatten(def, resolverFrom(kinds))
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("expected 1 leaf step, got %d", len(got.Steps))
	}
	if got.Steps[0].Kind != "code" {
		t.Errorf("leaf step should keep Kind=code, got %q", got.Steps[0].Kind)
	}
}

// TestFlattenNestedWorkflow: a step referencing a multi-step kind is inlined, with
// names namespaced and deps rewired into the parent DAG.
func TestFlattenNestedWorkflow(t *testing.T) {
	kinds := map[string]Definition{
		// pcr: A → B
		"pcr": {Name: "pcr", Steps: []Step{
			{Name: "A", Prompt: "plan"},
			{Name: "B", Prompt: "code", Deps: []string{"A"}},
		}},
	}
	ship := Definition{Name: "ship", Steps: []Step{
		{Name: "Build", Kind: "pcr"},
		{Name: "QA", Prompt: "qa", Deps: []string{"Build"}},
		{Name: "Deploy", Prompt: "deploy", Deps: []string{"QA"}},
	}}

	got, err := Flatten(ship, resolverFrom(kinds))
	if err != nil {
		t.Fatalf("flatten nested: %v", err)
	}

	// Build should be gone, replaced by Build/A and Build/B.
	names := map[string]bool{}
	for _, s := range got.Steps {
		names[s.Name] = true
	}
	for _, want := range []string{"Build/A", "Build/B", "QA", "Deploy"} {
		if !names[want] {
			t.Errorf("expected inlined step %q; steps=%v", want, stepNames(got.Steps))
		}
	}
	if names["Build"] {
		t.Errorf("original Build step should have been inlined away")
	}

	// Build/A is the sub-root: it inherited Build's (empty) deps → a root.
	a, _ := stepByName(got.Steps, "Build/A")
	if len(a.Deps) != 0 {
		t.Errorf("Build/A should be a root, got deps %v", a.Deps)
	}
	// Build/B keeps its internal dep on Build/A (prefixed).
	b, _ := stepByName(got.Steps, "Build/B")
	if got := sortedDeps(b); len(got) != 1 || got[0] != "Build/A" {
		t.Errorf("Build/B deps = %v, want [Build/A]", got)
	}
	// QA depended on Build → now depends on the sub-sink Build/B.
	qa, _ := stepByName(got.Steps, "QA")
	if got := sortedDeps(qa); len(got) != 1 || got[0] != "Build/B" {
		t.Errorf("QA deps = %v, want [Build/B] (sub-sink)", got)
	}
	// Deploy still depends on QA.
	dep, _ := stepByName(got.Steps, "Deploy")
	if got := sortedDeps(dep); len(got) != 1 || got[0] != "QA" {
		t.Errorf("Deploy deps = %v, want [QA]", got)
	}
}

// TestFlattenSubRootInheritsParentDeps: when the inlined step had deps, the
// sub-workflow's roots inherit them.
func TestFlattenSubRootInheritsParentDeps(t *testing.T) {
	kinds := map[string]Definition{
		"review": {Name: "review", Steps: []Step{
			{Name: "R1", Prompt: "r1"},
			{Name: "R2", Prompt: "r2"}, // two roots, two sinks
		}},
	}
	def := Definition{Name: "flow", Steps: []Step{
		{Name: "Code", Prompt: "code"},
		{Name: "Review", Kind: "review", Deps: []string{"Code"}},
		{Name: "Ship", Prompt: "ship", Deps: []string{"Review"}},
	}}
	got, err := Flatten(def, resolverFrom(kinds))
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}
	// Both sub-roots inherit Code as their dep.
	for _, n := range []string{"Review/R1", "Review/R2"} {
		s, ok := stepByName(got.Steps, n)
		if !ok {
			t.Fatalf("missing %s in %v", n, stepNames(got.Steps))
		}
		if got := sortedDeps(s); len(got) != 1 || got[0] != "Code" {
			t.Errorf("%s deps = %v, want [Code]", n, got)
		}
	}
	// Ship depended on Review → now depends on BOTH sub-sinks.
	ship, _ := stepByName(got.Steps, "Ship")
	if got := sortedDeps(ship); len(got) != 2 || got[0] != "Review/R1" || got[1] != "Review/R2" {
		t.Errorf("Ship deps = %v, want [Review/R1 Review/R2]", got)
	}
}

// TestFlattenCycle: a kind cycle is rejected, not expanded forever.
func TestFlattenCycle(t *testing.T) {
	kinds := map[string]Definition{
		"a": {Name: "a", Steps: []Step{{Name: "s", Kind: "b"}}},
		"b": {Name: "b", Steps: []Step{{Name: "s", Kind: "a"}}},
	}
	_, err := Flatten(kinds["a"], resolverFrom(kinds))
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected a cycle error, got %v", err)
	}
}

// TestFlattenSelfReference: a kind referencing itself is a cycle.
func TestFlattenSelfReference(t *testing.T) {
	kinds := map[string]Definition{
		"loop": {Name: "loop", Steps: []Step{{Name: "again", Kind: "loop"}}},
	}
	_, err := Flatten(kinds["loop"], resolverFrom(kinds))
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected a self-reference cycle error, got %v", err)
	}
}

func stepNames(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Name
	}
	return out
}
