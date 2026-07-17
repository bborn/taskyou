package pipeline

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// gateYAML is a two-step workflow whose first step is a human-in-the-loop gate.
const gateYAML = `
name: gated
description: a gated first step, then a normal one
steps:
  - {name: design, gate: true, prompt: "Design {{goal}}."}
  - {name: build, deps: [design], prompt: "Build it."}
`

// TestGateStepTaggedAndDetected proves the gate declaration round-trips: a step
// marked gate:true parses into a Step with Gate set, its created task carries the
// "gate" tag token (alongside "pipeline"), and IsGateStep reports true for it and
// false for a non-gate sibling.
func TestGateStepTaggedAndDetected(t *testing.T) {
	installWorkflow(t, "gated", gateYAML)
	database := testDB(t)

	// The parsed definition carries Gate through to the Step.
	def, ok := KindResolver(database)("gated")
	if !ok {
		t.Fatal("KindResolver did not resolve \"gated\"")
	}
	for _, s := range def.Steps {
		switch s.Name {
		case "design":
			if !s.Gate {
				t.Error("design step Gate = false, want true")
			}
		case "build":
			if s.Gate {
				t.Error("build step Gate = true, want false")
			}
		}
	}

	res, err := Create(database, Options{Goal: "a thing", Project: "test", Definition: "gated", Execute: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	design := taskByStep(res, "design")
	build := taskByStep(res, "build")
	if design == nil || build == nil {
		t.Fatalf("missing expected steps in %+v", res.Definition.Steps)
	}

	// Reload so persisted tags/branches are reflected.
	design, _ = database.GetTask(design.ID)
	build, _ = database.GetTask(build.ID)

	if design.Tags != "pipeline,gate" {
		t.Errorf("gate step tags = %q, want %q", design.Tags, "pipeline,gate")
	}
	if build.Tags != "pipeline" {
		t.Errorf("non-gate step tags = %q, want %q", build.Tags, "pipeline")
	}

	if !IsGateStep(design) {
		t.Error("IsGateStep(design) = false, want true")
	}
	if IsGateStep(build) {
		t.Error("IsGateStep(build) = true, want false")
	}

	// A plain (non-workflow) task is never a gate step, even if mis-tagged.
	plain := &db.Task{Title: "plain", Tags: "gate"}
	if IsGateStep(plain) {
		t.Error("IsGateStep(non-workflow task) = true, want false")
	}
}
