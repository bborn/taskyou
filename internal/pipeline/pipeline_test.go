package pipeline

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

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

func TestCreateBuildsChain(t *testing.T) {
	database := testDB(t)

	res, err := Create(database, Options{
		Goal:    "Add rate limiting to the API",
		Project: "test",
		Execute: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if res.Definition.Name != "plan-code-review" {
		t.Errorf("definition = %q, want plan-code-review", res.Definition.Name)
	}
	if len(res.Tasks) != 3 {
		t.Fatalf("got %d tasks, want 3", len(res.Tasks))
	}

	// Branch is seeded from the first phase's ID and shared by every phase.
	wantBranch := "pipeline/" + itoa(res.Tasks[0].ID) + "-add-rate-limiting-to-the-api"
	if res.Branch != wantBranch {
		t.Errorf("branch = %q, want %q", res.Branch, wantBranch)
	}

	wantExec := []string{db.ExecutorClaude, db.ExecutorClaude, db.ExecutorCodex}
	wantModel := []string{db.ModelOpus, db.ModelSonnet, ""}
	wantPhase := []string{"Plan", "Code", "Review"}
	for i, task := range res.Tasks {
		if task.Executor != wantExec[i] {
			t.Errorf("phase %d executor = %q, want %q", i, task.Executor, wantExec[i])
		}
		if task.Model != wantModel[i] {
			t.Errorf("phase %d model = %q, want %q", i, task.Model, wantModel[i])
		}
		if !strings.HasPrefix(task.Title, "["+wantPhase[i]+"]") {
			t.Errorf("phase %d title = %q, want prefix [%s]", i, task.Title, wantPhase[i])
		}
		if task.Tags != "pipeline" {
			t.Errorf("phase %d tags = %q, want pipeline", i, task.Tags)
		}
		// Every phase's body carries the goal and the shared branch.
		if !strings.Contains(task.Body, "Add rate limiting to the API") {
			t.Errorf("phase %d body missing goal", i)
		}
		if !strings.Contains(task.Body, wantBranch) {
			t.Errorf("phase %d body missing branch %q", i, wantBranch)
		}
	}

	// Phase 1 owns the branch (pinned BranchName); later phases check it out.
	if res.Tasks[0].BranchName != wantBranch {
		t.Errorf("phase 0 BranchName = %q, want %q", res.Tasks[0].BranchName, wantBranch)
	}
	if res.Tasks[0].SourceBranch != "" {
		t.Errorf("phase 0 SourceBranch = %q, want empty", res.Tasks[0].SourceBranch)
	}
	for i := 1; i < len(res.Tasks); i++ {
		if res.Tasks[i].SourceBranch != wantBranch {
			t.Errorf("phase %d SourceBranch = %q, want %q", i, res.Tasks[i].SourceBranch, wantBranch)
		}
	}

	// Execute=true queues phase 1; the rest wait blocked on their predecessor.
	if res.Tasks[0].Status != db.StatusQueued {
		t.Errorf("phase 0 status = %q, want queued", res.Tasks[0].Status)
	}
	for i := 1; i < len(res.Tasks); i++ {
		reloaded, _ := database.GetTask(res.Tasks[i].ID)
		if reloaded.Status != db.StatusBlocked {
			t.Errorf("phase %d status = %q, want blocked", i, reloaded.Status)
		}
	}

	// Dependencies form a linear chain with auto_queue enabled.
	for i := 1; i < len(res.Tasks); i++ {
		dep, err := database.GetDependency(res.Tasks[i-1].ID, res.Tasks[i].ID)
		if err != nil {
			t.Fatalf("GetDependency: %v", err)
		}
		if dep == nil {
			t.Fatalf("missing dependency %d -> %d", res.Tasks[i-1].ID, res.Tasks[i].ID)
		}
		if !dep.AutoQueue {
			t.Errorf("dependency %d -> %d not auto-queue", res.Tasks[i-1].ID, res.Tasks[i].ID)
		}
	}
}

func TestCreateAutoQueuesNextPhaseOnCompletion(t *testing.T) {
	database := testDB(t)
	res, err := Create(database, Options{Goal: "Refactor auth", Project: "test", Execute: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	plan, code, review := res.Tasks[0], res.Tasks[1], res.Tasks[2]

	// Completing the Plan phase should auto-queue Code but leave Review blocked.
	if err := database.UpdateTaskStatus(plan.ID, db.StatusDone); err != nil {
		t.Fatalf("complete plan: %v", err)
	}
	if got := statusOf(t, database, code.ID); got != db.StatusQueued {
		t.Errorf("Code status = %q, want queued after Plan done", got)
	}
	if got := statusOf(t, database, review.ID); got != db.StatusBlocked {
		t.Errorf("Review status = %q, want still blocked after Plan done", got)
	}

	// Completing Code then auto-queues Review.
	if err := database.UpdateTaskStatus(code.ID, db.StatusDone); err != nil {
		t.Fatalf("complete code: %v", err)
	}
	if got := statusOf(t, database, review.ID); got != db.StatusQueued {
		t.Errorf("Review status = %q, want queued after Code done", got)
	}
}

func TestCreateNoExecuteLeavesFirstBacklog(t *testing.T) {
	database := testDB(t)
	res, err := Create(database, Options{Goal: "Do a thing", Project: "test", Execute: false})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := statusOf(t, database, res.Tasks[0].ID); got != db.StatusBacklog {
		t.Errorf("phase 0 status = %q, want backlog when not executing", got)
	}
}

func TestCreatePermissionModeAppliesToEveryPhase(t *testing.T) {
	database := testDB(t)
	res, err := Create(database, Options{
		Goal:           "Tighten things",
		Project:        "test",
		PermissionMode: db.PermissionModeDangerous,
		Execute:        true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i, task := range res.Tasks {
		reloaded, _ := database.GetTask(task.ID)
		if reloaded.EffectivePermissionMode() != db.PermissionModeDangerous {
			t.Errorf("phase %d permission = %q, want dangerous", i, reloaded.EffectivePermissionMode())
		}
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

func TestGetDefaultsToPlanCodeReview(t *testing.T) {
	def, ok := Get("")
	if !ok {
		t.Fatal("Get(\"\") not ok")
	}
	if def.Name != DefaultDefinition {
		t.Errorf("Get(\"\") = %q, want %q", def.Name, DefaultDefinition)
	}
	if _, ok := Get("bogus"); ok {
		t.Error("Get(bogus) should not be ok")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Add rate limiting to the API": "add-rate-limiting-to-the-api",
		"  Trim & symbols!! ":          "trim-symbols",
		"":                             "pipeline",
		"---":                          "pipeline",
	}
	for in, want := range cases {
		if got := slugify(in, 40); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if got := slugify(strings.Repeat("a", 100), 40); len(got) != 40 {
		t.Errorf("slugify truncation len = %d, want 40", len(got))
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
