package executor

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// TestEveryRegisteredExecutorHasBinaries keeps the map that `ty doctor` reads
// in step with the executors this build actually registers. An executor added
// without an entry here would be reported by doctor as "unknown" — which is
// true, but only because someone forgot.
func TestEveryRegisteredExecutorHasBinaries(t *testing.T) {
	e := &Executor{executorFactory: NewExecutorFactory()}
	e.registerBuiltinExecutors()

	for _, name := range e.AllExecutors() {
		if len(ExecutorBinaries(name)) == 0 {
			t.Errorf("executor %q is registered but names no CLI binary; add it to executorBinaries", name)
		}
	}
	for _, name := range ExecutorsWithBinaries() {
		if e.GetExecutor(name) == nil {
			t.Errorf("executorBinaries lists %q, which is not a registered executor", name)
		}
	}
}

// TestClaudeSlugMatchesDB pins the one constant binaries.go duplicates rather
// than imports.
func TestClaudeSlugMatchesDB(t *testing.T) {
	if claudeSlug != db.ExecutorClaude {
		t.Errorf("claudeSlug = %q, want %q", claudeSlug, db.ExecutorClaude)
	}
}

// TestCursorAcceptsEitherName: the Cursor CLI ships as both cursor-agent and
// agent, and IsAvailable has always accepted either.
func TestCursorAcceptsEitherName(t *testing.T) {
	got := ExecutorBinaries("cursor")
	if len(got) != 2 || got[0] != "cursor-agent" || got[1] != "agent" {
		t.Errorf("cursor binaries = %v, want [cursor-agent agent]", got)
	}
}
