package db

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateModel covers the whole point of the check: a model the executor's
// CLI would actually reject must not be storable, while anything it accepts —
// including model IDs released after this code was written — must pass.
func TestValidateModel(t *testing.T) {
	tests := []struct {
		name     string
		executor string
		model    string
		wantErr  bool
	}{
		// No override is always fine — it means "use the agent's own default".
		{"empty is no override", ExecutorClaude, "", false},
		{"empty with no executor", "", "", false},

		// Claude aliases.
		{"opus", ExecutorClaude, ModelOpus, false},
		{"sonnet", ExecutorClaude, ModelSonnet, false},
		{"haiku", ExecutorClaude, ModelHaiku, false},
		{"fable", ExecutorClaude, ModelFable, false},
		{"opusplan", ExecutorClaude, ModelOpusPlan, false},
		{"alias with 1m variant", ExecutorClaude, "sonnet[1m]", false},
		{"alias is case-insensitive", ExecutorClaude, "Opus", false},
		{"empty executor defaults to claude", "", ModelOpus, false},

		// Full Claude IDs, including ones newer than this code.
		{"full id", ExecutorClaude, "claude-opus-5", false},
		{"full id with date", ExecutorClaude, "claude-haiku-4-5-20251001", false},
		{"full id with 1m variant", ExecutorClaude, "claude-opus-5[1m]", false},
		{"unreleased full id still passes", ExecutorClaude, "claude-opus-9", false},

		// The mistakes that actually happen.
		{"typo alias", ExecutorClaude, "opuss", true},
		{"executor slug as model", ExecutorClaude, "claude", true},
		{"another vendor's model", ExecutorClaude, "gpt-5", true},
		{"grok model on claude", ExecutorClaude, "grok-4", true},
		{"bare version", ExecutorClaude, "opus-4.5", true},
		{"shell metacharacters", ExecutorClaude, "claude-opus-5; rm -rf /", true},
		{"whitespace inside", ExecutorClaude, "claude opus", true},

		// Grok takes full IDs only, no aliases.
		{"grok id", ExecutorGrok, "grok-4", false},
		{"grok fast id", ExecutorGrok, "grok-code-fast-1", false},
		{"unreleased grok id", ExecutorGrok, "grok-9-turbo", false},
		{"claude alias on grok", ExecutorGrok, ModelOpus, true},
		{"claude id on grok", ExecutorGrok, "claude-opus-5", true},

		// Executors with no --model flag: an override there is dead config.
		{"codex takes no model", ExecutorCodex, "gpt-5-codex", true},
		{"gemini takes no model", ExecutorGemini, "gemini-2.5-pro", true},
		{"pi takes no model", ExecutorPi, ModelOpus, true},
		{"codex with no override is fine", ExecutorCodex, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateModel(tt.executor, tt.model)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateModel(%q, %q) = nil, want an error", tt.executor, tt.model)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateModel(%q, %q) = %v, want nil", tt.executor, tt.model, err)
			}
		})
	}
}

// TestValidateModelErrorNamesAlternatives checks the rejection actually tells
// the user what to type instead — the error is the whole user-facing surface.
func TestValidateModelErrorNamesAlternatives(t *testing.T) {
	err := ValidateModel(ExecutorClaude, "opuss")
	if err == nil {
		t.Fatal("expected an error for an unknown claude model")
	}
	for _, want := range []string{"opuss", ModelOpus, ModelSonnet, "claude-"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}

	err = ValidateModel(ExecutorCodex, "gpt-5-codex")
	if err == nil {
		t.Fatal("expected an error setting a model on a modelless executor")
	}
	// It must name the executors that DO take a model, or the user is stuck.
	for _, want := range []string{ExecutorCodex, ExecutorClaude, ExecutorGrok} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// TestExecutorSupportsModel pins which executors pass --model to their CLI.
// Adding a --model flag to another executor must update executorModelPrefix,
// or overrides for it stay silently ignored.
func TestExecutorSupportsModel(t *testing.T) {
	supported := map[string]bool{ExecutorClaude: true, ExecutorGrok: true}
	for _, e := range KnownExecutors() {
		if got := ExecutorSupportsModel(e); got != supported[e] {
			t.Errorf("ExecutorSupportsModel(%q) = %v, want %v", e, got, supported[e])
		}
	}
	if !ExecutorSupportsModel("") {
		t.Error("empty executor should resolve to the default (claude), which supports models")
	}
	if got := ModelCapableExecutors(); len(got) != 2 || got[0] != ExecutorClaude || got[1] != ExecutorGrok {
		t.Errorf("ModelCapableExecutors() = %v, want [claude grok]", got)
	}
}

// TestModelsForExecutor verifies the completion/help list is executor-specific
// and that every entry it advertises actually validates.
func TestModelsForExecutor(t *testing.T) {
	for _, e := range ModelCapableExecutors() {
		models := ModelsForExecutor(e)
		if len(models) == 0 {
			t.Errorf("ModelsForExecutor(%q) is empty", e)
		}
		for _, m := range models {
			if err := ValidateModel(e, m); err != nil {
				t.Errorf("ModelsForExecutor(%q) advertises %q but ValidateModel rejects it: %v", e, m, err)
			}
		}
	}
	if got := ModelsForExecutor(ExecutorCodex); got != nil {
		t.Errorf("ModelsForExecutor(codex) = %v, want nil (no --model flag)", got)
	}
	// The returned slice must not alias the package-level table.
	models := ModelsForExecutor(ExecutorClaude)
	models[0] = "mutated"
	if ModelsForExecutor(ExecutorClaude)[0] == "mutated" {
		t.Error("ModelsForExecutor returned a slice aliasing the package table")
	}
}

// TestModelOptionsAreValid guards the UI picker: every value it offers must be
// one the CLI check accepts, or the TUI and CLI disagree about what's legal.
func TestModelOptionsAreValid(t *testing.T) {
	for _, m := range ModelOptions() {
		if err := ValidateModel(ExecutorClaude, m); err != nil {
			t.Errorf("ModelOptions() offers %q but ValidateModel rejects it: %v", m, err)
		}
	}
}

// TestModelBackendIsCustom covers the escape hatch: a task routed at a proxy
// names the proxy's models, which ty cannot check against Anthropic's.
func TestModelBackendIsCustom(t *testing.T) {
	if ModelBackendIsCustom("", nil) {
		t.Error("stock backend (no config dir, no env) should not be custom")
	}
	if ModelBackendIsCustom("   ", map[string]string{"ANTHROPIC_BASE_URL": " "}) {
		t.Error("whitespace-only overrides should not count as custom")
	}
	if !ModelBackendIsCustom("~/.claude-ollama", nil) {
		t.Error("a CLAUDE_CONFIG_DIR override is a custom backend")
	}
	if !ModelBackendIsCustom("", map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:11434"}) {
		t.Error("an ANTHROPIC_BASE_URL override is a custom backend")
	}
	// The point of the hatch: an ollama model name is unknowable to ty, so it
	// must never be validated.
	if err := ValidateModel(ExecutorClaude, "glm-5.2:cloud"); err == nil {
		t.Error("a proxy model name should fail the strict check (callers must skip it, not rely on it passing)")
	}
}

// TestIsValidModel covers the looser executor-less form used by the new-task
// form's remembered per-project default.
func TestIsValidModel(t *testing.T) {
	valid := []string{"", ModelOpus, "claude-opus-5", "grok-4"}
	for _, m := range valid {
		if !IsValidModel(m) {
			t.Errorf("IsValidModel(%q) = false, want true", m)
		}
	}
	// "claude" is the executor slug an early default baked into every row; it is
	// not a model and must not survive as a remembered default.
	invalid := []string{"claude", "opuss", "gpt-5"}
	for _, m := range invalid {
		if IsValidModel(m) {
			t.Errorf("IsValidModel(%q) = true, want false", m)
		}
	}
}

// TestValidateTaskModel covers the write-path entry point: the same rules as
// ValidateModel, but with the proxy escape hatch resolved from the task's own
// config, its project's config, and the ambient environment.
func TestValidateTaskModel(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	if err := database.CreateProject(&Project{Name: "stock", Path: filepath.Join(tmpDir, "stock")}); err != nil {
		t.Fatalf("create stock project: %v", err)
	}
	if err := database.CreateProject(&Project{
		Name:            "ollama",
		Path:            filepath.Join(tmpDir, "ollama"),
		ClaudeConfigDir: "~/.claude-ollama",
	}); err != nil {
		t.Fatalf("create ollama project: %v", err)
	}

	t.Setenv("ANTHROPIC_BASE_URL", "")

	t.Run("nil and empty are no-ops", func(t *testing.T) {
		if err := database.ValidateTaskModel(nil); err != nil {
			t.Errorf("nil task: %v", err)
		}
		if err := database.ValidateTaskModel(&Task{Project: "stock"}); err != nil {
			t.Errorf("no override: %v", err)
		}
		if err := database.ValidateTaskModel(&Task{Project: "stock", Model: "   "}); err != nil {
			t.Errorf("blank override: %v", err)
		}
	})

	t.Run("stock backend is validated", func(t *testing.T) {
		if err := database.ValidateTaskModel(&Task{Project: "stock", Model: "opuss"}); err == nil {
			t.Error("expected a typo'd model to be rejected")
		}
		if err := database.ValidateTaskModel(&Task{Project: "stock", Model: ModelOpus}); err != nil {
			t.Errorf("a valid alias should pass: %v", err)
		}
		if err := database.ValidateTaskModel(&Task{Project: "stock", Executor: ExecutorCodex, Model: ModelOpus}); err == nil {
			t.Error("a modelless executor should be rejected")
		}
	})

	t.Run("per-task config dir skips validation", func(t *testing.T) {
		task := &Task{Project: "stock", Model: "glm-5.2:cloud", ClaudeConfigDir: "~/.claude-ollama"}
		if err := database.ValidateTaskModel(task); err != nil {
			t.Errorf("a task-level config dir should skip validation: %v", err)
		}
	})

	t.Run("per-task env skips validation", func(t *testing.T) {
		task := &Task{
			Project: "stock",
			Model:   "glm-5.2:cloud",
			EnvJSON: `{"ANTHROPIC_BASE_URL":"http://127.0.0.1:11434"}`,
		}
		if err := database.ValidateTaskModel(task); err != nil {
			t.Errorf("a task-level ANTHROPIC_BASE_URL should skip validation: %v", err)
		}
	})

	t.Run("project config dir skips validation", func(t *testing.T) {
		if err := database.ValidateTaskModel(&Task{Project: "ollama", Model: "glm-5.2:cloud"}); err != nil {
			t.Errorf("a project-level config dir should skip validation: %v", err)
		}
		// Same model, stock project: still rejected.
		if err := database.ValidateTaskModel(&Task{Project: "stock", Model: "glm-5.2:cloud"}); err == nil {
			t.Error("the escape hatch must not leak across projects")
		}
	})

	t.Run("unknown project is not a custom backend", func(t *testing.T) {
		if err := database.ValidateTaskModel(&Task{Project: "nope", Model: "opuss"}); err == nil {
			t.Error("an unresolvable project must not disable validation")
		}
	})

	t.Run("ambient base url skips validation", func(t *testing.T) {
		t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:11434")
		if err := database.ValidateTaskModel(&Task{Project: "stock", Model: "glm-5.2:cloud"}); err != nil {
			t.Errorf("an ambient ANTHROPIC_BASE_URL should skip validation: %v", err)
		}
	})
}
