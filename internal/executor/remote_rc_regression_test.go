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

// TestRemoteLaunchScriptCodexKeepsPromptUnderRemoteControl locks the other half
// of the codex + RemoteControl contract: RemoteControl is a Claude-only behavior,
// so a codex task with RemoteControl=true must still feed codex the staged prompt
// file and still clean that file up on the placed host — exactly as a codex task
// without RemoteControl does (and as the local codex path always does, which
// never special-cases RemoteControl). The prompt-suppression guard in
// remoteLaunchScript was once unscoped and dropped both pieces for codex; this
// test sits next to the flag-side guard above so the two halves stay in sync.
func TestRemoteLaunchScriptCodexKeepsPromptUnderRemoteControl(t *testing.T) {
	t.Setenv("WORKTREE_SESSION_ID", "777")
	task := &db.Task{ID: 42, Port: 3042, Title: "investigate", RemoteControl: true}

	t.Run("non-empty prompt keeps the staged prompt arg and cleanup", func(t *testing.T) {
		script, err := remoteLaunchScript(task, "codex", "/srv/app", "do the thing", "run1")
		if err != nil {
			t.Fatalf("remoteLaunchScript: %v", err)
		}
		if !strings.Contains(script, "codex ") {
			t.Fatalf("codex script lost its executor: %q", script)
		}
		if strings.Contains(script, "--remote-control") {
			t.Errorf("codex script must not carry --remote-control: %q", script)
		}
		if !strings.Contains(script, `"$(cat `) {
			t.Errorf("codex script must keep the staged prompt arg even with RemoteControl: %q", script)
		}
		if !strings.Contains(script, "rm -f") {
			t.Errorf("codex script must keep the staged prompt cleanup even with RemoteControl: %q", script)
		}
		// The staged prompt path must point at the run-scoped prompt file
		// runRemoteSession writes, so the file is actually consumed and removed.
		if !strings.Contains(script, "/tmp/ty-task-42-run1-prompt.txt") {
			t.Errorf("codex script must reference the run-scoped prompt file: %q", script)
		}
	})

	t.Run("empty prompt still launches codex without an arg", func(t *testing.T) {
		// The empty-prompt branch early-returns and carries no promptArg at all;
		// RemoteControl must not perturb that branch for codex either.
		script, err := remoteLaunchScript(task, "codex", "/srv/app", "", "run1")
		if err != nil {
			t.Fatalf("remoteLaunchScript: %v", err)
		}
		if strings.Contains(script, `"$(cat `) {
			t.Errorf("codex empty-prompt script must not stage a prompt arg: %q", script)
		}
		if strings.Contains(script, "rm -f") {
			t.Errorf("codex empty-prompt script must not carry cleanup: %q", script)
		}
		if !strings.Contains(script, "codex") {
			t.Errorf("codex empty-prompt script lost its executor: %q", script)
		}
	})

	t.Run("RemoteControl=false codex keeps the prompt arg (no-op parity)", func(t *testing.T) {
		// A codex task without RemoteControl is the documented baseline. The
		// RemoteControl=true case above must look identical to it, otherwise the
		// "RemoteControl is a no-op for codex" contract is broken.
		offTask := &db.Task{ID: 42, Port: 3042, Title: "investigate", RemoteControl: false}
		offScript, err := remoteLaunchScript(offTask, "codex", "/srv/app", "do the thing", "run1")
		if err != nil {
			t.Fatalf("remoteLaunchScript: %v", err)
		}
		onScript, err := remoteLaunchScript(task, "codex", "/srv/app", "do the thing", "run1")
		if err != nil {
			t.Fatalf("remoteLaunchScript: %v", err)
		}
		if offScript != onScript {
			t.Errorf("codex script must be identical with RemoteControl on vs off\noff: %q\non:  %q", offScript, onScript)
		}
	})
}
