package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPathWithinResolvesSymlinks guards the fix for the guard falsely denying writes
// when the worktree lives under a symlinked path (macOS /tmp -> /private/tmp): the
// worktree root is /tmp/… but a tool resolves a write to /private/tmp/…, which without
// symlink resolution reads as an escape and traps the agent in a retry loop.
func TestPathWithinResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "wtlink")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	// Root is the symlink path; the write target is the resolved real path (and a
	// not-yet-created file, as a real write target is).
	if !pathWithin(link, filepath.Join(real, "REVIEW.md")) {
		t.Error("a write to the resolved real path should be WITHIN a symlinked worktree root")
	}
	// And the reverse: root is the real path, the write goes through the symlink.
	if !pathWithin(real, filepath.Join(link, "REVIEW.md")) {
		t.Error("a write via the symlinked path should be WITHIN the real worktree root")
	}
	// A genuine escape is still outside.
	if pathWithin(link, filepath.Join(t.TempDir(), "x")) {
		t.Error("a path outside the worktree must NOT be reported within")
	}
}

// wt is a representative managed-worktree root (contains the .task-worktrees segment).
const wt = "/home/u/proj/.task-worktrees/4244-speed"

func toolInput(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal tool input: %v", err)
	}
	return b
}

// applyPatch wraps one or more apply_patch marker lines in a full patch envelope,
// mirroring what Codex passes as tool_input.command for the apply_patch tool.
func applyPatch(markers ...string) string {
	body := "*** Begin Patch\n"
	for _, m := range markers {
		body += m + "\n"
	}
	body += "*** End Patch\n"
	return body
}

func TestEvaluateWorktreeWriteGuard(t *testing.T) {
	cases := []struct {
		name  string
		root  string
		allow []string
		in    WorktreeGuardInput
		want  string // "", "ask", or "deny"
	}{
		{
			name: "edit inside worktree (absolute) allowed",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "Edit", Cwd: wt},
		},
		{
			name: "edit inside worktree (relative to cwd) allowed",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "Edit", Cwd: wt},
		},
		{
			name: "edit to main repo absolute path asks (the incident)",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "Edit", Cwd: wt},
			want: "ask",
		},
		{
			name: "write to home asks",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "Write", Cwd: wt},
			want: "ask",
		},
		{
			name: "write outside in bypass mode denies",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "Write", Cwd: wt, PermissionMode: "bypassPermissions"},
			want: "deny",
		},
		{
			name: "notebook edit outside asks",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "NotebookEdit", Cwd: wt},
			want: "ask",
		},
		{
			name: "read tool anywhere allowed",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "Read", Cwd: wt},
		},
		{
			name: "multiedit outside asks",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "MultiEdit", Cwd: wt},
			want: "ask",
		},
		{
			name: "shared dir project (no .task-worktrees) is inert",
			root: "/home/u/proj",
			in:   WorktreeGuardInput{ToolName: "Edit", Cwd: "/home/u/proj"},
		},
		{
			name:  "allowlisted external path allowed",
			root:  wt,
			allow: []string{"/home/u/shared"},
			in:    WorktreeGuardInput{ToolName: "Write", Cwd: wt},
		},
		// --- non-Claude executor tool vocabularies (same policy, one source of truth) ---
		{
			name: "gemini write_file outside asks",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "write_file", Cwd: wt},
			want: "ask",
		},
		{
			name: "gemini write_file inside worktree allowed",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "write_file", Cwd: wt},
		},
		{
			name: "gemini replace outside asks",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "replace", Cwd: wt},
			want: "ask",
		},
		{
			name: "gemini run_shell_command redirect outside asks",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "run_shell_command", Cwd: wt},
			want: "ask",
		},
		{
			name: "codex apply_patch update outside asks",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "apply_patch", Cwd: wt},
			want: "ask",
		},
		{
			name: "codex apply_patch update inside worktree allowed",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "apply_patch", Cwd: wt},
		},
		{
			name: "codex apply_patch outside in bypass mode denies",
			root: wt,
			in:   WorktreeGuardInput{ToolName: "apply_patch", Cwd: wt, PermissionMode: "bypassPermissions"},
			want: "deny",
		},
	}

	// Per-case tool inputs (kept out of the struct so we can use the helper).
	inputs := map[string]map[string]any{
		"edit inside worktree (absolute) allowed":             {"file_path": wt + "/app/x.rb"},
		"edit inside worktree (relative to cwd) allowed":      {"file_path": "app/x.rb"},
		"edit to main repo absolute path asks (the incident)": {"file_path": "/home/u/proj/app/models/event.rb"},
		"write to home asks":                                  {"file_path": "~/notes.txt"},
		"write outside in bypass mode denies":                 {"file_path": "/home/u/proj/app/x.rb"},
		"notebook edit outside asks":                          {"notebook_path": "/home/u/proj/nb.ipynb"},
		"read tool anywhere allowed":                          {"file_path": "/etc/hosts"},
		"multiedit outside asks":                              {"file_path": "/home/u/proj/Gemfile"},
		"shared dir project (no .task-worktrees) is inert":    {"file_path": "/home/u/proj/app/x.rb"},
		"allowlisted external path allowed":                   {"file_path": "/home/u/shared/cache.db"},
		"gemini write_file outside asks":                      {"file_path": "/home/u/proj/config.yaml"},
		"gemini write_file inside worktree allowed":           {"file_path": wt + "/config.yaml"},
		"gemini replace outside asks":                         {"file_path": "/home/u/proj/app/models/event.rb"},
		"gemini run_shell_command redirect outside asks":      {"command": "echo data > /home/u/proj/out.txt"},
		"codex apply_patch update outside asks":               {"command": applyPatch("*** Update File: /home/u/proj/app/models/event.rb")},
		"codex apply_patch update inside worktree allowed":    {"command": applyPatch("*** Update File: app/models/event.rb")},
		"codex apply_patch outside in bypass mode denies":     {"command": applyPatch("*** Add File: /home/u/proj/new.rb")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			in.ToolInput = toolInput(t, inputs[c.name])
			got := EvaluateWorktreeWriteGuard(c.root, c.allow, in)
			gotDecision := ""
			if got != nil {
				gotDecision = got.Decision
			}
			if gotDecision != c.want {
				t.Errorf("decision = %q, want %q (reason: %v)", gotDecision, c.want, reasonOf(got))
			}
		})
	}
}

func TestWorktreeWriteGuardBash(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"read-only grep allowed", "grep -r foo .", ""},
		{"read-only cat allowed", "cat /etc/hosts", ""},
		{"redirect to /tmp allowed", "echo data > /tmp/scratch", ""},
		{"redirect to /private/tmp allowed", "echo data > /private/tmp/scratch", ""},
		{"redirect to /var/tmp allowed", "echo data > /var/tmp/scratch", ""},
		{"redirect to /dev/null allowed", "ls -la > /dev/null", ""},
		{"stderr to /dev/null allowed", "rails test 2>/dev/null", ""},
		{"redirect inside worktree allowed", "echo hi > out.txt", ""},
		{"cd to main then git checkout asks", "cd /home/u/proj && git checkout main", "ask"},
		{"git -C main reset asks", "git -C /home/u/proj reset --hard", "ask"},
		{"rm absolute outside asks", "rm -rf /home/u/other/build", "ask"},
		{"cd inside worktree then write allowed", "cd ./sub && echo x > y.txt", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := WorktreeGuardInput{
				ToolName:  "Bash",
				Cwd:       wt,
				ToolInput: toolInput(t, map[string]any{"command": c.command}),
			}
			got := EvaluateWorktreeWriteGuard(wt, nil, in)
			gotDecision := ""
			if got != nil {
				gotDecision = got.Decision
			}
			if gotDecision != c.want {
				t.Errorf("command %q: decision = %q, want %q", c.command, gotDecision, c.want)
			}
		})
	}
}

// System temp directories are shared, ephemeral scratch space — writing there
// can't corrupt the main checkout or a sibling worktree (the boundary the guard
// exists to protect), and agents legitimately stage temp files there. So writes
// under /tmp, /private/tmp, /var/tmp, and the OS temp dir are always allowed,
// like /dev/null — never a permission prompt.
func TestWorktreeWriteGuardAllowsTempDirs(t *testing.T) {
	tmpFile := filepath.Join(os.TempDir(), "review_a.md")
	cases := []struct {
		name string
		in   WorktreeGuardInput
	}{
		{"Write to /tmp", WorktreeGuardInput{ToolName: "Write", Cwd: wt,
			ToolInput: toolInput(t, map[string]any{"file_path": "/tmp/review_a.md"})}},
		{"Write to os.TempDir", WorktreeGuardInput{ToolName: "Write", Cwd: wt,
			ToolInput: toolInput(t, map[string]any{"file_path": tmpFile})}},
		{"Bash redirect to /tmp in bypass mode", WorktreeGuardInput{ToolName: "Bash", Cwd: wt,
			PermissionMode: "bypassPermissions",
			ToolInput:      toolInput(t, map[string]any{"command": "echo x > /tmp/scratch"})}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EvaluateWorktreeWriteGuard(wt, nil, c.in); got != nil {
				t.Errorf("temp-dir write should be allowed, got %q (%s)", got.Decision, got.Reason)
			}
		})
	}
}

// TestWorktreeWriteGuardApplyPatch covers the Codex/OpenCode apply_patch envelope
// parser: multi-file patches, Move destinations, and mixed in/out targets.
func TestWorktreeWriteGuardApplyPatch(t *testing.T) {
	cases := []struct {
		name    string
		markers []string
		want    string
	}{
		{"single update inside allowed", []string{"*** Update File: app/x.rb"}, ""},
		{"add inside allowed", []string{"*** Add File: pkg/new.go"}, ""},
		{"update outside asks", []string{"*** Update File: /home/u/proj/app/x.rb"}, "ask"},
		{"delete outside asks", []string{"*** Delete File: ../sibling/file.txt"}, "ask"},
		{"move destination outside asks", []string{"*** Update File: app/x.rb", "*** Move to: /home/u/proj/x.rb"}, "ask"},
		{
			"multi-file one escapes asks",
			[]string{"*** Update File: app/a.rb", "*** Add File: /etc/evil.conf", "*** Update File: app/b.rb"},
			"ask",
		},
		{
			"multi-file all inside allowed",
			[]string{"*** Update File: app/a.rb", "*** Add File: app/b.rb"},
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := WorktreeGuardInput{
				ToolName:  "apply_patch",
				Cwd:       wt,
				ToolInput: toolInput(t, map[string]any{"command": applyPatch(c.markers...)}),
			}
			got := EvaluateWorktreeWriteGuard(wt, nil, in)
			gotDecision := ""
			if got != nil {
				gotDecision = got.Decision
			}
			if gotDecision != c.want {
				t.Errorf("decision = %q, want %q (reason: %v)", gotDecision, c.want, reasonOf(got))
			}
		})
	}
}

func TestIsManagedWorktree(t *testing.T) {
	if !IsManagedWorktree(wt) {
		t.Errorf("expected %q to be a managed worktree", wt)
	}
	if IsManagedWorktree("/home/u/proj") {
		t.Errorf("expected plain project dir to NOT be a managed worktree")
	}
}

func TestManagedWorktreeProjectDir(t *testing.T) {
	if got := ManagedWorktreeProjectDir(wt); got != "/home/u/proj" {
		t.Errorf("ManagedWorktreeProjectDir(%q) = %q, want /home/u/proj", wt, got)
	}
	if got := ManagedWorktreeProjectDir("/home/u/proj"); got != "" {
		t.Errorf("expected empty project dir for non-worktree path, got %q", got)
	}
}

func reasonOf(d *WorktreeGuardDecision) string {
	if d == nil {
		return "<nil>"
	}
	return d.Reason
}
