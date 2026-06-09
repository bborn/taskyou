package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupWorktreeGuardHooks verifies that each non-Claude executor's guard setup
// writes the right pre-tool hook config into the worktree, wired to call back into
// `worktree-guard` with the executor-appropriate format, and that cleanup removes it.
func TestSetupWorktreeGuardHooks(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(e *Executor, workDir string) (func(), error)
		relPath    string // config file written, relative to the worktree
		wantEvent  string // hooks.<event> key (empty for the opencode plugin)
		wantFormat string // the --format argument the hook must invoke
		jsonHook   bool   // true for codex/gemini JSON hook configs
	}{
		{
			name:       "codex",
			setup:      func(e *Executor, wd string) (func(), error) { return e.setupCodexWorktreeGuard(wd, "") },
			relPath:    ".codex/hooks.json",
			wantEvent:  "PreToolUse",
			wantFormat: "codex",
			jsonHook:   true,
		},
		{
			name:       "gemini",
			setup:      func(e *Executor, wd string) (func(), error) { return e.setupGeminiWorktreeGuard(wd, "") },
			relPath:    ".gemini/settings.json",
			wantEvent:  "BeforeTool",
			wantFormat: "gemini",
			jsonHook:   true,
		},
		{
			name:       "opencode",
			setup:      func(e *Executor, wd string) (func(), error) { return e.setupOpenCodeWorktreeGuard(wd, "") },
			relPath:    ".opencode/plugins/taskyou-worktree-guard.js",
			wantFormat: "opencode",
			jsonHook:   false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &Executor{}
			workDir := t.TempDir()

			cleanup, err := c.setup(e, workDir)
			if err != nil {
				t.Fatalf("setup: %v", err)
			}
			path := filepath.Join(workDir, c.relPath)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("expected config at %s: %v", c.relPath, err)
			}

			if !strings.Contains(string(data), "worktree-guard") ||
				!strings.Contains(string(data), c.wantFormat) {
				t.Errorf("config does not wire `worktree-guard --format %s`:\n%s", c.wantFormat, data)
			}

			if c.jsonHook {
				command := jsonHookCommand(t, data, c.wantEvent)
				if !strings.Contains(command, "worktree-guard --format "+c.wantFormat) {
					t.Errorf("hook command = %q, want it to invoke worktree-guard --format %s", command, c.wantFormat)
				}
			} else {
				// OpenCode plugin: must register the tool.execute.before hook.
				if !strings.Contains(string(data), "tool.execute.before") {
					t.Errorf("opencode plugin missing tool.execute.before hook:\n%s", data)
				}
			}

			cleanup()
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("cleanup did not remove %s (err=%v)", c.relPath, err)
			}
		})
	}
}

// TestSetupWorktreeGuardHookMergePreservesExisting verifies that wiring the guard into
// a worktree that already has a Gemini settings.json preserves the user's existing
// config, and that cleanup restores the original file byte-for-byte.
func TestSetupWorktreeGuardHookMergePreservesExisting(t *testing.T) {
	e := &Executor{}
	workDir := t.TempDir()

	geminiDir := filepath.Join(workDir, ".gemini")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsPath := filepath.Join(geminiDir, "settings.json")
	original := []byte(`{"theme":"dark","hooks":{"BeforeTool":[{"hooks":[{"type":"command","command":"existing"}]}]}}`)
	if err := os.WriteFile(settingsPath, original, 0644); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	cleanup, err := e.setupGeminiWorktreeGuard(workDir, "")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	merged, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read merged: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(merged, &cfg); err != nil {
		t.Fatalf("merged settings not valid JSON: %v", err)
	}
	if cfg["theme"] != "dark" {
		t.Errorf("merge dropped existing top-level key: %v", cfg)
	}
	hooks := cfg["hooks"].(map[string]any)
	before := hooks["BeforeTool"].([]any)
	if len(before) != 2 {
		t.Errorf("expected existing + guard hook groups (2), got %d: %v", len(before), before)
	}
	if !strings.Contains(string(merged), "existing") || !strings.Contains(string(merged), "worktree-guard --format gemini") {
		t.Errorf("merge must keep existing hook AND add the guard hook:\n%s", merged)
	}

	cleanup()
	restored, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if string(restored) != string(original) {
		t.Errorf("cleanup did not restore original settings.json\n got: %s\nwant: %s", restored, original)
	}
}

// jsonHookCommand digs the first hook command out of a {"hooks":{<event>:[{"hooks":[{"command":...}]}]}} config.
func jsonHookCommand(t *testing.T, data []byte, event string) string {
	t.Helper()
	var cfg struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("hook config not valid JSON: %v\n%s", err, data)
	}
	groups := cfg.Hooks[event]
	if len(groups) == 0 || len(groups[0].Hooks) == 0 {
		t.Fatalf("no hook registered under %s: %s", event, data)
	}
	if groups[0].Hooks[0].Type != "command" {
		t.Errorf("hook type = %q, want command", groups[0].Hooks[0].Type)
	}
	return groups[0].Hooks[0].Command
}
