package pipeline

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// TestBundledRPIResolves proves the bundled "rpi" workflow is compiled into the
// binary and resolvable end-to-end with NO on-disk workflow file present
// (TestMain points TY_WORKFLOWS_DIR at an empty dir, and this test does not
// install one). It asserts DefinitionNames() lists it, KindResolver resolves it as
// a built-in (Custom:false), and Create builds the expected research → implement
// DAG.
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
	if len(def.Steps) != 2 {
		t.Fatalf("rpi has %d steps, want 2", len(def.Steps))
	}

	// Create builds the DAG from the bundled def with no on-disk file present.
	res, err := Create(database, Options{Goal: "add a hello endpoint", Project: "test", Definition: "rpi", Execute: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(res.Tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(res.Tasks))
	}

	research := taskByStep(res, "research")
	implement := taskByStep(res, "implement")
	if research == nil || implement == nil {
		t.Fatalf("missing expected steps in %+v", res.Definition.Steps)
	}

	// Root pins the branch; implement checks it out and depends on research.
	if research.BranchName == "" || research.SourceBranch != "" {
		t.Errorf("research branch=%q source=%q, want branch pinned", research.BranchName, research.SourceBranch)
	}
	if implement.SourceBranch != research.BranchName {
		t.Errorf("implement SourceBranch = %q, want %q", implement.SourceBranch, research.BranchName)
	}
	assertDep(t, database, research.ID, implement.ID)

	// Both step tasks carry the pipeline tag for grouping.
	for _, tk := range res.Tasks {
		if tk.Tags != "pipeline" {
			t.Errorf("%s tags = %q, want pipeline", tk.Title, tk.Tags)
		}
	}

	// Root starts; implement waits blocked on its dependency.
	if research.Status != db.StatusQueued {
		t.Errorf("research status = %q, want queued", research.Status)
	}
	reloaded, _ := database.GetTask(implement.ID)
	if reloaded.Status != db.StatusBlocked {
		t.Errorf("implement status = %q, want blocked", reloaded.Status)
	}
}
