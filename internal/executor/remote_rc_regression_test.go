package executor

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// TestRemoteLaunchScriptHonorsRemoteControl guards the remote Claude launch
// path against silently dropping Remote Control. remoteLaunchScript must mirror
// the local fresh-launch and resume paths: emit --remote-control <name> via
// rcFlag AND suppress the staged prompt arg ("$(cat ...)") for RemoteControl
// tasks. The two pieces are separately owned (rcFlag emits the flag; prompt
// suppression lives at the call site) and both were missing from the remote
// path when it was added, so this locks the integration of both.
func TestRemoteLaunchScriptHonorsRemoteControl(t *testing.T) {
	t.Setenv("WORKTREE_SESSION_ID", "777")

	task := &db.Task{ID: 42, Port: 3042, Title: "fix the login bug", RemoteControl: true}

	t.Run("non-empty prompt emits --remote-control and suppresses prompt arg", func(t *testing.T) {
		script, err := remoteLaunchScript(task, "claude", "/home/agent/projects/x", "do the thing", "run1")
		if err != nil {
			t.Fatalf("remoteLaunchScript: %v", err)
		}
		if !strings.Contains(script, "--remote-control 'fix the login bug'") {
			t.Errorf("script is missing --remote-control <title>: %q", script)
		}
		if strings.Contains(script, `"$(cat `) {
			t.Errorf("script must NOT stage the prompt arg for RemoteControl: %q", script)
		}
		if strings.Contains(script, "rm -f") {
			t.Errorf("script must NOT stage the prompt cleanup for RemoteControl: %q", script)
		}
	})

	t.Run("empty prompt emits --remote-control", func(t *testing.T) {
		// The empty-prompt branch early-returns; rcFlag must be threaded into
		// flags before that return, so the flag survives here too. The bug
		// omitted rcFlag uniformly across both branches, not just this one.
		script, err := remoteLaunchScript(task, "claude", "/home/agent/projects/x", "", "run1")
		if err != nil {
			t.Fatalf("remoteLaunchScript: %v", err)
		}
		if !strings.Contains(script, "--remote-control") {
			t.Errorf("script is missing --remote-control: %q", script)
		}
	})

	t.Run("codex ignores RemoteControl (RC is Claude-only)", func(t *testing.T) {
		// The codex branch resets flags=""; rcFlag must not leak --remote-control
		// into a codex launch line, matching the local path.
		script, err := remoteLaunchScript(task, "codex", "/srv/app", "work", "r1")
		if err != nil {
			t.Fatalf("remoteLaunchScript: %v", err)
		}
		if strings.Contains(script, "--remote-control") {
			t.Errorf("codex script must not carry --remote-control: %q", script)
		}
		if !strings.Contains(script, "codex ") {
			t.Errorf("codex script lost its executor: %q", script)
		}
	})
}
