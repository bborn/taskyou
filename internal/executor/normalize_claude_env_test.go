package executor

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNormalizeClaudeConfigEnv is the regression guard for the silent
// session-destruction bug: a ty process that inherits CLAUDE_CONFIG_DIR resolves
// the "default" Claude config dir to that inherited value, disagreeing with a ty
// process launched from a clean shell. After normalization the ambient value is
// gone, so DefaultClaudeConfigDir falls back to ~/.claude in every process.
func TestNormalizeClaudeConfigEnv(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	wantDefault := filepath.Join(home, ".claude")

	t.Run("removes an inherited value and restores the fixed default", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", "/Users/someone/.claude-ik")

		// Before normalization the inherited value pollutes resolution.
		if got := DefaultClaudeConfigDir(); got != "/Users/someone/.claude-ik" {
			t.Fatalf("precondition: DefaultClaudeConfigDir() = %q, want the inherited value", got)
		}

		if removed := NormalizeClaudeConfigEnv(); removed != "/Users/someone/.claude-ik" {
			t.Fatalf("NormalizeClaudeConfigEnv() returned %q, want the removed value", removed)
		}
		if v, ok := os.LookupEnv("CLAUDE_CONFIG_DIR"); ok {
			t.Fatalf("CLAUDE_CONFIG_DIR still set to %q after normalization", v)
		}
		if got := DefaultClaudeConfigDir(); got != wantDefault {
			t.Fatalf("DefaultClaudeConfigDir() = %q after normalization, want %q", got, wantDefault)
		}
	})

	t.Run("no-op when unset", func(t *testing.T) {
		os.Unsetenv("CLAUDE_CONFIG_DIR")
		if removed := NormalizeClaudeConfigEnv(); removed != "" {
			t.Fatalf("NormalizeClaudeConfigEnv() = %q with no env set, want empty", removed)
		}
		if got := DefaultClaudeConfigDir(); got != wantDefault {
			t.Fatalf("DefaultClaudeConfigDir() = %q, want %q", got, wantDefault)
		}
	})

	t.Run("per-project config dir is honored independently of the ambient env", func(t *testing.T) {
		// A custom-config project keeps resolving to its DB value; only the
		// env-derived default is affected by normalization.
		t.Setenv("CLAUDE_CONFIG_DIR", "/Users/someone/.claude-ik")
		NormalizeClaudeConfigEnv()
		if got := ResolveClaudeConfigDir("~/.claude-custom"); got != filepath.Join(home, ".claude-custom") {
			t.Fatalf("ResolveClaudeConfigDir(custom) = %q, want the explicit custom dir", got)
		}
	})
}
