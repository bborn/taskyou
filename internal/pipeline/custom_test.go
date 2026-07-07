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

func TestParseDefinitionRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"no name":     "steps:\n  - name: A\n    prompt: x\n",
		"no prompt":   "name: d\nsteps:\n  - name: A\n",
		"two roots":   "name: d\nsteps:\n  - name: A\n    prompt: x\n  - name: B\n    prompt: y\n",
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
	// Custom appears alongside the built-in.
	if _, ok := Get("build-and-qa"); !ok {
		t.Error("custom workflow not resolved by Get")
	}
	if _, ok := Get(DefaultDefinition); !ok {
		t.Error("built-in should still resolve")
	}
	names := DefinitionNames()
	if !contains(names, "build-and-qa") || !contains(names, DefaultDefinition) {
		t.Errorf("DefinitionNames = %v, want built-in + custom", names)
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

func TestBuiltinUsesInstructionVerbatim(t *testing.T) {
	def, _ := Get(DefaultDefinition)
	got := effectiveInstruction(def, "Plan")
	if got != planInstruction {
		t.Error("built-in step should use its Instruction verbatim")
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

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
