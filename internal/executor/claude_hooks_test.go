package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupClaudeHooksRegistersLifecycle pins the generated hook settings: every
// lifecycle event ty relies on is registered, each with a timeout.
//
// The two failures this guards against are silent ones. A missing event leaves a
// task's status stale on the board (a turn that ends in an error, a follow-up
// answered with no tool call), and a missing timeout leaves the agent waiting on
// us — Claude blocks on a hook until it exits.
func TestSetupClaudeHooksRegistersLifecycle(t *testing.T) {
	workDir := t.TempDir()
	e := &Executor{}
	cleanup, err := e.setupClaudeHooks(workDir, 42)
	if err != nil {
		t.Fatalf("setupClaudeHooks: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(workDir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}

	want := []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit",
		"PreToolUse", "PostToolUse", "Notification", "Stop", "StopFailure",
	}
	for _, event := range want {
		entries, ok := settings.Hooks[event]
		if !ok || len(entries) == 0 {
			t.Errorf("%s hook not registered", event)
			continue
		}
		for _, entry := range entries {
			if len(entry.Hooks) == 0 {
				t.Errorf("%s entry has no command", event)
				continue
			}
			for _, h := range entry.Hooks {
				if h.Timeout <= 0 {
					t.Errorf("%s hook has no timeout; an unbounded hook stalls the agent", event)
				}
				if want := "claude-hook --event " + event; !strings.Contains(h.Command, want) {
					t.Errorf("%s command = %q, want it to contain %q", event, h.Command, want)
				}
			}
		}
	}

	// Notification only fires for the types we act on, and the newer dialogs
	// (an elicitation, an agent asking for input) are just as much "waiting on a
	// human" as an idle prompt.
	for _, typ := range []string{"idle_prompt", "permission_prompt", "elicitation_dialog", "agent_needs_input"} {
		if !strings.Contains(settings.Hooks["Notification"][0].Matcher, typ) {
			t.Errorf("Notification matcher %q is missing %q", settings.Hooks["Notification"][0].Matcher, typ)
		}
	}

	// SessionStart is what re-keys an ownership change, so it must fire for the
	// sources that produce one.
	for _, src := range []string{"startup", "resume", "clear", "fork"} {
		if !strings.Contains(settings.Hooks["SessionStart"][0].Matcher, src) {
			t.Errorf("SessionStart matcher %q is missing %q", settings.Hooks["SessionStart"][0].Matcher, src)
		}
	}
}

// TestSetupClaudeHooksKeepsExistingSettings: the file is the user's, and we are
// a guest in it. Merging must leave unrelated keys alone.
func TestSetupClaudeHooksKeepsExistingSettings(t *testing.T) {
	workDir := t.TempDir()
	claudeDir := filepath.Join(workDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"model":"opus","permissions":{"allow":["Bash(ls:*)"]}}`
	settingsPath := filepath.Join(claudeDir, "settings.local.json")
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	e := &Executor{}
	cleanup, err := e.setupClaudeHooks(workDir, 42)
	if err != nil {
		t.Fatalf("setupClaudeHooks: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var merged map[string]any
	if err := json.Unmarshal(data, &merged); err != nil {
		t.Fatalf("parse merged settings: %v", err)
	}
	if merged["model"] != "opus" {
		t.Errorf("model = %v, want the user's %q preserved", merged["model"], "opus")
	}
	if _, ok := merged["hooks"]; !ok {
		t.Error("hooks were not merged in")
	}

	cleanup()
	restored, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != existing {
		t.Errorf("after cleanup settings = %q, want the original %q", restored, existing)
	}
}
