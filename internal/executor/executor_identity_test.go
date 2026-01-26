package executor

import "testing"

func TestDetectExecutorIdentityDefault(t *testing.T) {
	t.Setenv("TASK_EXECUTOR", "")
	t.Setenv("WORKFLOW_EXECUTOR", "")
	t.Setenv("TASKYOU_EXECUTOR", "")
	t.Setenv("WORKTREE_EXECUTOR", "")

	slug, display := detectExecutorIdentity()
	if slug != defaultExecutorSlug {
		t.Fatalf("expected slug %q, got %q", defaultExecutorSlug, slug)
	}
	if display != defaultExecutorName {
		t.Fatalf("expected display %q, got %q", defaultExecutorName, display)
	}
}

func TestDetectExecutorIdentityCodex(t *testing.T) {
	t.Setenv("TASK_EXECUTOR", "codex")
	slug, display := detectExecutorIdentity()
	if slug != "codex" {
		t.Fatalf("expected slug codex, got %q", slug)
	}
	if display != "Codex" {
		t.Fatalf("expected display Codex, got %q", display)
	}
}

func TestDetectExecutorIdentityGemini(t *testing.T) {
	t.Setenv("TASK_EXECUTOR", "gemini")
	slug, display := detectExecutorIdentity()
	if slug != "gemini" {
		t.Fatalf("expected slug gemini, got %q", slug)
	}
	if display != "Gemini" {
		t.Fatalf("expected display Gemini, got %q", display)
	}
}

func TestDetectExecutorIdentityUnknown(t *testing.T) {
	t.Setenv("TASK_EXECUTOR", "beta")
	slug, display := detectExecutorIdentity()
	if slug != "beta" {
		t.Fatalf("expected slug beta, got %q", slug)
	}
	if display != "Beta" {
		t.Fatalf("expected display Beta, got %q", display)
	}
}
