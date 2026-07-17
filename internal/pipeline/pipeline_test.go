package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// installPCR installs the plan-code-review workflow (the former in-code built-in,
// now shipped as a plugin) from testdata into the workflows dir, so tests that
// exercise its plan→code→2-reviewers→collect shape can request it by name.
func installPCR(t *testing.T) {
	t.Helper()
	b, err := os.ReadFile("testdata/plan-code-review.yaml")
	if err != nil {
		t.Fatalf("read pcr testdata: %v", err)
	}
	dir := t.TempDir()
	t.Setenv("TY_WORKFLOWS_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "plan-code-review.yaml"), b, 0o644); err != nil {
		t.Fatalf("install pcr: %v", err)
	}
}

func testDB(t *testing.T) *db.DB {
	t.Helper()
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "test", Path: tmpDir}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return database
}

// taskByStep finds a created step task by its step name.
func taskByStep(res *Result, step string) *db.Task {
	for i, s := range res.Definition.Steps {
		if s.Name == step {
			return res.Tasks[i]
		}
	}
	return nil
}

func TestNoDefaultWorkflow(t *testing.T) {
	// There is no built-in default: an empty definition name resolves to nothing.
	if _, ok := Get(""); ok {
		t.Error("Get(\"\") resolved a definition; want none (no built-in default)")
	}
	database := testDB(t)
	if _, err := Create(database, Options{Goal: "x", Project: "test"}); err == nil {
		t.Error("Create with no definition should error (no default workflow)")
	}
}

func TestCreateBuildsDAG(t *testing.T) {
	installPCR(t)
	database := testDB(t)
	res, err := Create(database, Options{Goal: "Add rate limiting to the API", Project: "test", Definition: "plan-code-review", Execute: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(res.Tasks) != 5 {
		t.Fatalf("got %d tasks, want 5", len(res.Tasks))
	}

	wantBranch := "pipeline/" + itoa(taskByStep(res, "Plan").ID) + "-add-rate-limiting-to-the-api"
	if res.Branch != wantBranch {
		t.Errorf("branch = %q, want %q", res.Branch, wantBranch)
	}

	plan := taskByStep(res, "Plan")
	code := taskByStep(res, "Code")
	rvA := taskByStep(res, "Review A")
	rvB := taskByStep(res, "Review B")
	collect := taskByStep(res, "Collect")

	// Root owns the branch; everyone else checks it out via SourceBranch.
	if plan.BranchName != wantBranch || plan.SourceBranch != "" {
		t.Errorf("Plan branch=%q source=%q, want branch pinned", plan.BranchName, plan.SourceBranch)
	}
	for _, tk := range []*db.Task{code, rvA, rvB, collect} {
		if tk.SourceBranch != wantBranch {
			t.Errorf("%s SourceBranch = %q, want %q", tk.Title, tk.SourceBranch, wantBranch)
		}
	}

	// Root starts; everything else waits blocked.
	if plan.Status != db.StatusQueued {
		t.Errorf("Plan status = %q, want queued", plan.Status)
	}
	for _, tk := range []*db.Task{code, rvA, rvB, collect} {
		reloaded, _ := database.GetTask(tk.ID)
		if reloaded.Status != db.StatusBlocked {
			t.Errorf("%s status = %q, want blocked", tk.Title, reloaded.Status)
		}
	}

	// Dependency edges: Code←Plan, ReviewA←Code, ReviewB←Code, Collect←ReviewA+ReviewB.
	assertDep(t, database, plan.ID, code.ID)
	assertDep(t, database, code.ID, rvA.ID)
	assertDep(t, database, code.ID, rvB.ID)
	assertDep(t, database, rvA.ID, collect.ID)
	assertDep(t, database, rvB.ID, collect.ID)

	// Every step task is tagged for grouping and carries the goal + branch in its prompt.
	for _, tk := range res.Tasks {
		if tk.Tags != "pipeline" {
			t.Errorf("%s tags = %q, want pipeline", tk.Title, tk.Tags)
		}
		if !strings.Contains(tk.Body, "Add rate limiting to the API") || !strings.Contains(tk.Body, wantBranch) {
			t.Errorf("%s body missing goal/branch", tk.Title)
		}
	}
	// Parallel reviewers write distinct review files and push to their OWN branch
	// (not the shared branch) so a weak agent can't clobber the other reviewer.
	if !strings.Contains(rvA.Body, "review-review-a.md") || !strings.Contains(rvA.Body, wantBranch+"-review-a") {
		t.Errorf("Review A body missing its review file / own branch: %s", rvA.Body)
	}
	if !strings.Contains(rvB.Body, wantBranch+"-review-b") {
		t.Errorf("Review B body missing its own review branch")
	}
	// Collect is told exactly which review branches to read.
	if !strings.Contains(collect.Body, wantBranch+"-review-a") || !strings.Contains(collect.Body, wantBranch+"-review-b") {
		t.Errorf("Collect body missing review branch references: %s", collect.Body)
	}
}

func TestCreateAutoAdvancesDAGWithParallelJoin(t *testing.T) {
	installPCR(t)
	database := testDB(t)
	res, err := Create(database, Options{Goal: "Ship it", Project: "test", Definition: "plan-code-review", Execute: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	plan := taskByStep(res, "Plan")
	code := taskByStep(res, "Code")
	rvA := taskByStep(res, "Review A")
	rvB := taskByStep(res, "Review B")
	collect := taskByStep(res, "Collect")

	// Plan done → Code queues.
	must(t, database.UpdateTaskStatus(plan.ID, db.StatusDone))
	if s := statusOf(t, database, code.ID); s != db.StatusQueued {
		t.Errorf("Code = %q after Plan done, want queued", s)
	}

	// Code done → BOTH reviewers queue at once (fan-out).
	must(t, database.UpdateTaskStatus(code.ID, db.StatusDone))
	if s := statusOf(t, database, rvA.ID); s != db.StatusQueued {
		t.Errorf("Review A = %q after Code done, want queued", s)
	}
	if s := statusOf(t, database, rvB.ID); s != db.StatusQueued {
		t.Errorf("Review B = %q after Code done, want queued", s)
	}
	// Collect still waits — both reviews outstanding.
	if s := statusOf(t, database, collect.ID); s != db.StatusBlocked {
		t.Errorf("Collect = %q with reviews pending, want blocked", s)
	}

	// One reviewer done → Collect still blocked (join needs both).
	must(t, database.UpdateTaskStatus(rvA.ID, db.StatusDone))
	if s := statusOf(t, database, collect.ID); s != db.StatusBlocked {
		t.Errorf("Collect = %q after one review, want still blocked", s)
	}

	// Second reviewer done → Collect queues (join satisfied).
	must(t, database.UpdateTaskStatus(rvB.ID, db.StatusDone))
	if s := statusOf(t, database, collect.ID); s != db.StatusQueued {
		t.Errorf("Collect = %q after both reviews, want queued", s)
	}
}

func TestCreateValidation(t *testing.T) {
	database := testDB(t)
	cases := []struct {
		name string
		opts Options
	}{
		{"empty goal", Options{Goal: "  ", Project: "test"}},
		{"empty project", Options{Goal: "x", Project: ""}},
		{"unknown definition", Options{Goal: "x", Project: "test", Definition: "nope"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Create(database, tc.opts); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestValidateRejectsMalformedDAGs(t *testing.T) {
	cases := map[string]Definition{
		"dup name":    {Name: "d", Steps: []Step{{Name: "A"}, {Name: "A"}}},
		"unknown dep": {Name: "d", Steps: []Step{{Name: "A"}, {Name: "B", Deps: []string{"Z"}}}},
		"no root":     {Name: "d", Steps: []Step{{Name: "A", Deps: []string{"B"}}, {Name: "B", Deps: []string{"A"}}}},
		"empty":       {Name: "d"},
	}
	for name, def := range cases {
		t.Run(name, func(t *testing.T) {
			if err := def.validate(); err == nil {
				t.Errorf("expected validate() error for %s", name)
			}
		})
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Add rate limiting to the API": "add-rate-limiting-to-the-api",
		"  Trim & symbols!! ":          "trim-symbols",
		"":                             "pipeline",
		"Review B":                     "review-b",
	}
	for in, want := range cases {
		if got := slugify(in, 40); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func assertDep(t *testing.T, database *db.DB, blocker, blocked int64) {
	t.Helper()
	dep, err := database.GetDependency(blocker, blocked)
	if err != nil {
		t.Fatalf("GetDependency(%d,%d): %v", blocker, blocked, err)
	}
	if dep == nil {
		t.Fatalf("missing dependency %d → %d", blocker, blocked)
	}
	if !dep.AutoQueue {
		t.Errorf("dependency %d → %d not auto-queue", blocker, blocked)
	}
}

func statusOf(t *testing.T, database *db.DB, id int64) string {
	t.Helper()
	task, err := database.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask(%d): %v", id, err)
	}
	return task.Status
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// itoa avoids pulling strconv into the test just for one conversion.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
