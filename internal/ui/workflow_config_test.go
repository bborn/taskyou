package ui

import "testing"

func TestStepChoiceRoundTrip(t *testing.T) {
	cases := []struct {
		exec, model, want string
	}{
		{"claude", "opus", "claude/opus"},
		{"claude", "", "claude"},
		{"codex", "", "codex"},
	}
	for _, c := range cases {
		v := stepChoiceValue(c.exec, c.model)
		if v != c.want {
			t.Errorf("stepChoiceValue(%q,%q) = %q, want %q", c.exec, c.model, v, c.want)
		}
		e, m := parseStepChoice(v)
		if e != c.exec || m != c.model {
			t.Errorf("parseStepChoice(%q) = (%q,%q), want (%q,%q)", v, e, m, c.exec, c.model)
		}
	}
}

func TestWorkflowStepChoicesFilterByAvailability(t *testing.T) {
	// Only claude available → claude model options, no codex.
	values, labels := workflowStepChoices([]string{"claude"})
	if len(values) != len(labels) {
		t.Fatalf("values/labels misaligned: %d vs %d", len(values), len(labels))
	}
	if indexOfString(values, "claude/opus") < 0 {
		t.Error("expected claude/opus option")
	}
	if indexOfString(values, "codex") >= 0 {
		t.Error("codex should be absent when not available")
	}

	// Codex available → codex present.
	values, _ = workflowStepChoices([]string{"claude", "codex"})
	if indexOfString(values, "codex") < 0 {
		t.Error("codex should be present when available")
	}
}
