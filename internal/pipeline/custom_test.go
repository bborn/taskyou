package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

const sampleYAML = `
name: build-and-qa
description: build then review + qa in parallel
steps:
  - name: Plan
    model: opus
    prompt: Plan {{goal}}.
  - name: Build
    deps: [Plan]
    prompt: Build it.
  - name: Security
    deps: [Build]
    prompt: Security review.
  - name: QA
    executor: codex
    deps: [Build]
    prompt: QA it.
  - name: Finalize
    deps: [Security, QA]
    prompt: Finalize.
`

func TestParseDefinition(t *testing.T) {
	def, err := ParseDefinition([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	if !def.Custom {
		t.Error("custom def should be marked Custom")
	}
	if len(def.Steps) != 5 {
		t.Fatalf("got %d steps, want 5", len(def.Steps))
	}
	// Executor defaults to claude; explicit executor honored.
	if def.Steps[0].Executor != "claude" {
		t.Errorf("Plan executor = %q, want claude default", def.Steps[0].Executor)
	}
	if s, _ := def.step("QA"); s.Executor != "codex" {
		t.Errorf("QA executor = %q, want codex", s.Executor)
	}
	if err := def.validate(); err != nil {
		t.Errorf("valid def failed validate: %v", err)
	}
}

func TestParseDefinitionVerify(t *testing.T) {
	const y = `
name: verify-flow
steps:
  - name: build
    prompt: Build it.
    verify: go build ./... && go test ./...
  - name: ship
    deps: [build]
    prompt: Ship it.
`
	def, err := ParseDefinition([]byte(y))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	build, _ := def.step("build")
	if build.Verify != "go build ./... && go test ./..." {
		t.Errorf("build.Verify = %q, want the command", build.Verify)
	}
	ship, _ := def.step("ship")
	if ship.Verify != "" {
		t.Errorf("ship.Verify = %q, want empty (no verify)", ship.Verify)
	}

	// The verify command survives a Marshal → re-parse round trip.
	out, err := Marshal(def)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	def2, err := ParseDefinition(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	build2, _ := def2.step("build")
	if build2.Verify != build.Verify {
		t.Errorf("round-trip Verify = %q, want %q", build2.Verify, build.Verify)
	}
}

func TestCreatePersistsStepVerify(t *testing.T) {
	const y = `
name: verify-create
steps:
  - name: build
    prompt: Build it.
    verify: go test ./...
  - name: ship
    deps: [build]
    prompt: Ship it.
`
	installWorkflow(t, "verify-create", y)
	database := testDB(t)
	res, err := Create(database, Options{Goal: "do a thing", Project: "test", Definition: "verify-create", Execute: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	build := taskByStep(res, "build")
	got, err := database.GetStepVerify(build.ID)
	if err != nil {
		t.Fatalf("GetStepVerify: %v", err)
	}
	if got != "go test ./..." {
		t.Errorf("build step verify = %q, want %q", got, "go test ./...")
	}

	// A step with no verify has no gate row.
	ship := taskByStep(res, "ship")
	shipVerify, err := database.GetStepVerify(ship.ID)
	if err != nil {
		t.Fatalf("GetStepVerify ship: %v", err)
	}
	if shipVerify != "" {
		t.Errorf("ship step verify = %q, want empty", shipVerify)
	}
}

func TestParseDefinitionRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"no name":     "steps:\n  - name: A\n    prompt: x\n",
		"no prompt":   "name: d\nsteps:\n  - name: A\n",
		"unknown dep": "name: d\nsteps:\n  - name: A\n    prompt: x\n  - name: B\n    deps: [Z]\n    prompt: y\n",
	}
	for name, y := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDefinition([]byte(y)); err == nil {
				t.Errorf("expected error for %s", name)
			}
		})
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	def, err := ParseDefinition([]byte(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	def2, err := ParseDefinition(out)
	if err != nil {
		t.Fatalf("re-parse marshaled: %v", err)
	}
	if len(def2.Steps) != len(def.Steps) || def2.Name != def.Name {
		t.Error("round-trip changed the definition")
	}
}

func TestRegistryMergesCustom(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TY_WORKFLOWS_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "build-and-qa.yaml"), []byte(sampleYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	// A workflow file is discovered by Get; there are no built-ins.
	if _, ok := Get("build-and-qa"); !ok {
		t.Error("custom workflow not resolved by Get")
	}
	names := DefinitionNames()
	if !contains(names, "build-and-qa") {
		t.Errorf("DefinitionNames = %v, want the custom workflow", names)
	}
}

func TestComposeDerivesHandoffFromDAG(t *testing.T) {
	def, err := ParseDefinition([]byte(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	// Security runs parallel to QA → push to its own branch, no PR.
	sec := effectiveInstruction(def, "Security")
	if !strings.Contains(sec, "{{branch}}-security") || !strings.Contains(sec, "Do NOT open a pull request") {
		t.Errorf("Security handoff wrong:\n%s", sec)
	}
	// Finalize joins the two parallel steps and is the sink → reads their branches + opens PR.
	fin := effectiveInstruction(def, "Finalize")
	if !strings.Contains(fin, "{{branch}}-security") || !strings.Contains(fin, "{{branch}}-qa") {
		t.Errorf("Finalize should read both review branches:\n%s", fin)
	}
	if !strings.Contains(fin, "gh pr create") {
		t.Errorf("Finalize (sink) should open a PR:\n%s", fin)
	}
	// Build is linear (no parallel peer, not sink) → shared-branch push, no PR.
	build := effectiveInstruction(def, "Build")
	if !strings.Contains(build, "push origin HEAD:{{branch}}") || strings.Contains(build, "gh pr create") {
		t.Errorf("Build handoff wrong:\n%s", build)
	}
	// The author's prompt is carried through.
	if !strings.Contains(build, "Build it.") {
		t.Error("composed body should include the author's prompt")
	}
}

func TestVerbatimStepUsesInstructionAsIs(t *testing.T) {
	const y = `
name: verb
steps:
  - name: Solo
    verbatim: true
    prompt: |-
      Do exactly this for {{goal}}.
`
	def, err := ParseDefinition([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	// A verbatim step's body is its prompt as-is — no DAG-derived git handoff added.
	got := effectiveInstruction(def, "Solo")
	if got != "Do exactly this for {{goal}}." {
		t.Errorf("verbatim instruction = %q, want the prompt as-is", got)
	}
}

func TestCreateHonorsCustomWorkflow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TY_WORKFLOWS_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "build-and-qa.yaml"), []byte(sampleYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	database := testDB(t)
	res, err := Create(database, Options{Goal: "do it", Project: "test", Definition: "build-and-qa"})
	if err != nil {
		t.Fatalf("Create custom: %v", err)
	}
	if len(res.Tasks) != 5 {
		t.Fatalf("got %d tasks, want 5", len(res.Tasks))
	}
	// The QA step's configured executor (codex) flows through.
	if got := taskByStep(res, "QA").Executor; got != db.ExecutorCodex {
		t.Errorf("QA executor = %q, want codex", got)
	}
}

const multiRootYAML = `
name: three-spikes
description: try three approaches at once, then pick and build
steps:
  - name: Spike A
    prompt: Approach A to {{goal}}.
  - name: Spike B
    prompt: Approach B to {{goal}}.
  - name: Spike C
    prompt: Approach C to {{goal}}.
  - name: Pick
    deps: [Spike A, Spike B, Spike C]
    prompt: Pick the best approach and build it.
`

func TestMultiRootWorkflow(t *testing.T) {
	def, err := ParseDefinition([]byte(multiRootYAML))
	if err != nil {
		t.Fatalf("ParseDefinition (multi-root should be valid): %v", err)
	}
	if len(def.Roots()) != 3 {
		t.Fatalf("got %d roots, want 3", len(def.Roots()))
	}
	// Each parallel root pushes to its own branch (they run at once).
	spike := effectiveInstruction(def, "Spike A")
	if !strings.Contains(spike, "{{branch}}-spike-a") || !strings.Contains(spike, "Do NOT open a pull request") {
		t.Errorf("parallel root handoff wrong:\n%s", spike)
	}
	// The join reads all three root branches and (as sink) opens the PR.
	pick := effectiveInstruction(def, "Pick")
	for _, want := range []string{"{{branch}}-spike-a", "{{branch}}-spike-b", "{{branch}}-spike-c", "gh pr create"} {
		if !strings.Contains(pick, want) {
			t.Errorf("Pick handoff missing %q:\n%s", want, pick)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestParseDefinitionRejectsUnknownModel covers the workflow-file half of model
// validation. A step whose model its CLI won't accept fails silently at launch
// — the agent rejects the flag inside tmux and the step stalls looking busy —
// so the file must be rejected while it is being read.
func TestParseDefinitionRejectsUnknownModel(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string // substring the error must carry
	}{
		{
			name: "typo in a claude alias",
			yaml: "name: k\nsteps:\n  - {name: Plan, model: opuss, prompt: Plan it.}\n",
			want: "opuss",
		},
		{
			name: "executor slug used as a model",
			yaml: "name: k\nsteps:\n  - {name: Plan, model: claude, prompt: Plan it.}\n",
			want: "claude",
		},
		{
			name: "model on an executor with no --model flag",
			yaml: "name: k\nsteps:\n  - {name: QA, executor: codex, model: gpt-5-codex, prompt: QA it.}\n",
			want: "codex",
		},
		{
			name: "claude model on a grok step",
			yaml: "name: k\nsteps:\n  - {name: Plan, executor: grok, model: opus, prompt: Plan it.}\n",
			want: "grok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseDefinition([]byte(tt.yaml))
			if err == nil {
				t.Fatal("ParseDefinition accepted a step with an unusable model")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q should mention %q", err, tt.want)
			}
			// The step name locates the problem in a multi-step file.
			if !strings.Contains(err.Error(), "step ") {
				t.Errorf("error %q should name the offending step", err)
			}
		})
	}
}

// TestParseDefinitionAllowsProxyModels is the escape hatch: a step routed at a
// proxy (a config_dir or an ANTHROPIC_BASE_URL env override — the ollama shape)
// names the proxy's models, which ty has no way to check.
func TestParseDefinitionAllowsProxyModels(t *testing.T) {
	viaConfigDir := "name: k\nsteps:\n  - {name: Code, model: glm-5.2:cloud, config_dir: \"~/.claude-ollama\", prompt: Do it.}\n"
	if _, err := ParseDefinition([]byte(viaConfigDir)); err != nil {
		t.Errorf("config_dir-routed step should skip model validation: %v", err)
	}

	viaEnv := "name: k\nsteps:\n  - {name: Code, model: glm-5.2:cloud, env: {ANTHROPIC_BASE_URL: \"http://127.0.0.1:11434\"}, prompt: Do it.}\n"
	if _, err := ParseDefinition([]byte(viaEnv)); err != nil {
		t.Errorf("ANTHROPIC_BASE_URL-routed step should skip model validation: %v", err)
	}

	// Same model with no routing override is still a hard error.
	bare := "name: k\nsteps:\n  - {name: Code, model: glm-5.2:cloud, prompt: Do it.}\n"
	if _, err := ParseDefinition([]byte(bare)); err == nil {
		t.Error("an unrouted step with a proxy-only model should be rejected")
	}
}

// TestParseDefinitionAcceptsRealModels guards against the check being too
// strict: full model IDs, including ones newer than this code, must pass.
func TestParseDefinitionAcceptsRealModels(t *testing.T) {
	for _, model := range []string{"opus", "sonnet", "haiku", "fable", "claude-opus-5", "claude-opus-5[1m]", "claude-opus-9"} {
		// Quoted: a bracketed variant like claude-opus-5[1m] is a YAML flow
		// sequence otherwise.
		yaml := "name: k\nsteps:\n  - {name: Plan, model: \"" + model + "\", prompt: Plan it.}\n"
		if _, err := ParseDefinition([]byte(yaml)); err != nil {
			t.Errorf("ParseDefinition rejected valid model %q: %v", model, err)
		}
	}
	// db is imported by the sample above; keep the executor-specific case honest.
	yaml := "name: k\nsteps:\n  - {name: Plan, executor: " + db.ExecutorGrok + ", model: grok-4, prompt: Plan it.}\n"
	if _, err := ParseDefinition([]byte(yaml)); err != nil {
		t.Errorf("ParseDefinition rejected a valid grok model: %v", err)
	}
}
