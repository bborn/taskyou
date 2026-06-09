package executor

import (
	"encoding/json"
	"testing"
)

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
		{"redirect to /tmp asks", "echo data > /tmp/scratch", "ask"},
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
