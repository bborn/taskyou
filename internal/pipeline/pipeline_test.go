package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// TestMain isolates the whole package's tests from the developer's real workflows
// dir (~/.config/task/workflows). With no built-in workflows (kinds live in the DB;
// a workflow is a same-named file), a stray local workflow file is the only source
// registry() would pick up — so pointing TY_WORKFLOWS_DIR at an empty dir keeps every
// test deterministic. Tests that need a specific workflow install one (see
// installWorkflow), which overrides this per-test via t.Setenv.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pipeline-workflows-empty-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("TY_WORKFLOWS_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// pcrYAML is a plan → code → 2 parallel reviews → collect workflow, as a custom
// file (there are no built-in workflows). Tests install it to exercise the DAG.
const pcrYAML = `
name: pcr
description: plan, code, two parallel reviews, collect
steps:
  - {name: Plan, model: opus, prompt: "Plan {{goal}}."}
  - {name: Code, deps: [Plan], prompt: "Implement it."}
  - {name: Review A, deps: [Code], prompt: "Review it."}
  - {name: Review B, deps: [Code], prompt: "Review it."}
  - {name: Collect, deps: [Review A, Review B], prompt: "Collect and open a PR."}
`

// installWorkflow writes a workflow file to a fresh global kinds dir for the test.
func installWorkflow(t *testing.T, name, yaml string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TY_WORKFLOWS_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
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
	installWorkflow(t, "pcr", pcrYAML)
	database := testDB(t)
	res, err := Create(database, Options{Goal: "Add rate limiting to the API", Project: "test", Definition: "pcr", Execute: true})
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
	// Parallel reviewers push to their OWN branch (not the shared branch) so a weak
	// agent can't clobber the other reviewer.
	if !strings.Contains(rvA.Body, wantBranch+"-review-a") {
		t.Errorf("Review A body missing its own branch: %s", rvA.Body)
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
	installWorkflow(t, "pcr", pcrYAML)
	database := testDB(t)
	res, err := Create(database, Options{Goal: "Ship it", Project: "test", Definition: "pcr", Execute: true})
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

// configDirYAML has a per-step config_dir override on Code and Review only,
// exercising the route-some-steps-through-a-different-Claude-config feature
// (e.g. ollama) without changing the project.
const configDirYAML = `
name: cfgdir
description: plan on default claude, code+review on an ollama config
steps:
  - {name: Plan, model: opus, prompt: "Plan {{goal}}."}
  - {name: Code, deps: [Plan], model: glm-5.2:cloud, config_dir: "~/.claude-ollama", prompt: "Implement it."}
  - {name: Review, deps: [Code], model: glm-5.2:cloud, config_dir: "~/.claude-ollama", prompt: "Review it."}
`

// TestCreateConfigDirPropagation verifies a step's config_dir override lands on
// the created task's ClaudeConfigDir (and that steps without it stay empty), so
// the executor routes only those steps through the alternate Claude config.
func TestCreateConfigDirPropagation(t *testing.T) {
	installWorkflow(t, "cfgdir", configDirYAML)
	database := testDB(t)
	res, err := Create(database, Options{Goal: "ship the thing", Project: "test", Definition: "cfgdir"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	plan := taskByStep(res, "Plan")
	code := taskByStep(res, "Code")
	review := taskByStep(res, "Review")

	if plan.ClaudeConfigDir != "" {
		t.Errorf("Plan ClaudeConfigDir = %q, want empty (no override)", plan.ClaudeConfigDir)
	}
	for _, tk := range []*db.Task{code, review} {
		if tk.ClaudeConfigDir != "~/.claude-ollama" {
			t.Errorf("%s ClaudeConfigDir = %q, want ~/.claude-ollama", tk.Title, tk.ClaudeConfigDir)
		}
		if tk.Model != "glm-5.2:cloud" {
			t.Errorf("%s Model = %q, want glm-5.2:cloud", tk.Title, tk.Model)
		}
	}

	// The override persists across a reload (the column is saved, not just in-memory).
	reloaded, _ := database.GetTask(code.ID)
	if reloaded == nil || reloaded.ClaudeConfigDir != "~/.claude-ollama" {
		got := ""
		if reloaded != nil {
			got = reloaded.ClaudeConfigDir
		}
		t.Errorf("reloaded Code ClaudeConfigDir = %q, want ~/.claude-ollama", got)
	}
}

// TestParseConfigDirRoundtrip verifies config_dir survives the yaml parse +
// Marshal roundtrip so `ty pipeline edit`/`new` preserve it.
func TestParseConfigDirRoundtrip(t *testing.T) {
	def, err := ParseDefinition([]byte(configDirYAML))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	if s, ok := stepByName(def.Steps, "Code"); !ok || s.ConfigDir != "~/.claude-ollama" {
		t.Errorf("Code ConfigDir = %q, want ~/.claude-ollama", s.ConfigDir)
	}
	out, err := Marshal(def)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	def2, err := ParseDefinition(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if s, ok := stepByName(def2.Steps, "Review"); !ok || s.ConfigDir != "~/.claude-ollama" {
		t.Errorf("Review ConfigDir after roundtrip = %q, want ~/.claude-ollama", s.ConfigDir)
	}
}

// envYAML routes Code+Review through ollama via a per-step env map (the
// mechanism that actually reaches a token-auth proxy), while Plan stays on the
// default Anthropic backend.
const envYAML = `
name: envroute
description: plan on default claude, code+review routed to ollama via env
steps:
  - {name: Plan, model: opus, prompt: "Plan {{goal}}."}
  - {name: Code, deps: [Plan], model: glm-5.2:cloud, env: {ANTHROPIC_BASE_URL: "http://127.0.0.1:11434", ANTHROPIC_AUTH_TOKEN: ollama, ANTHROPIC_API_KEY: ""}, prompt: "Implement it."}
  - {name: Review, deps: [Code], model: glm-5.2:cloud, env: {ANTHROPIC_BASE_URL: "http://127.0.0.1:11434", ANTHROPIC_AUTH_TOKEN: ollama}, prompt: "Review it."}
`

// TestCreateEnvPropagation verifies a step's env map is serialized into the
// created task's EnvJSON (and steps without it stay empty), so the executor
// injects those vars only on the routed steps.
func TestCreateEnvPropagation(t *testing.T) {
	installWorkflow(t, "envroute", envYAML)
	database := testDB(t)
	res, err := Create(database, Options{Goal: "ship the thing", Project: "test", Definition: "envroute"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	plan := taskByStep(res, "Plan")
	code := taskByStep(res, "Code")
	review := taskByStep(res, "Review")

	if plan.EnvJSON != "" {
		t.Errorf("Plan EnvJSON = %q, want empty (no override)", plan.EnvJSON)
	}
	for _, tk := range []*db.Task{code, review} {
		if tk.EnvJSON == "" {
			t.Errorf("%s EnvJSON empty, want a JSON blob", tk.Title)
		}
		if tk.Model != "glm-5.2:cloud" {
			t.Errorf("%s Model = %q, want glm-5.2:cloud", tk.Title, tk.Model)
		}
	}
	m := code.EnvMap()
	if m["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:11434" || m["ANTHROPIC_AUTH_TOKEN"] != "ollama" {
		t.Errorf("Code EnvMap = %v, want ollama routing vars", m)
	}
	if _, ok := m["ANTHROPIC_API_KEY"]; !ok {
		t.Errorf("Code EnvMap missing explicit empty ANTHROPIC_API_KEY (needed to clear inherited creds): %v", m)
	}

	// The override persists across a reload (the column is saved, not just in-memory).
	reloaded, _ := database.GetTask(code.ID)
	if reloaded == nil || reloaded.EnvMap()["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:11434" {
		got := ""
		if reloaded != nil {
			got = reloaded.EnvJSON
		}
		t.Errorf("reloaded Code EnvJSON = %q, want persisted ollama routing blob", got)
	}
}

// TestParseEnvRoundtrip verifies the env map survives the yaml parse + Marshal
// roundtrip so `ty pipeline edit`/`new` preserve it.
func TestParseEnvRoundtrip(t *testing.T) {
	def, err := ParseDefinition([]byte(envYAML))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	if s, ok := stepByName(def.Steps, "Code"); !ok || s.Env["ANTHROPIC_AUTH_TOKEN"] != "ollama" {
		t.Errorf("Code Env = %v, want ANTHROPIC_AUTH_TOKEN=ollama", s.Env)
	}
	out, err := Marshal(def)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	def2, err := ParseDefinition(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if s, ok := stepByName(def2.Steps, "Review"); !ok || s.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:11434" {
		t.Errorf("Review Env after roundtrip = %v, want ANTHROPIC_BASE_URL preserved", s.Env)
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
