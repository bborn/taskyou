package executor

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestDetectAuthPrompt(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "empty content",
			content: "",
			want:    false,
		},
		{
			name:    "normal working output",
			content: "Editing main.go\nRunning tests...\nAll tests passed.",
			want:    false,
		},
		{
			name:    "please run /login",
			content: "Your session has ended.\nPlease run /login to continue.",
			want:    true,
		},
		{
			name:    "oauth token expired",
			content: "Error: OAuth token has expired. Please re-authenticate.",
			want:    true,
		},
		{
			name:    "invalid api key",
			content: "API Error: Invalid API key · Please run /login",
			want:    true,
		},
		{
			name:    "login screen",
			content: "Select login method:\n1. Claude account with subscription\n2. Anthropic Console account",
			want:    true,
		},
		{
			name:    "authentication error",
			content: `{"type":"error","error":{"type":"authentication_error"}}`,
			want:    true,
		},
		{
			name:    "case insensitive",
			content: "PLEASE RUN /LOGIN",
			want:    true,
		},
		{
			// A diff that merely mentions login should not trip detection.
			name:    "false positive guard - code mentioning login",
			content: "func handleLogin() {\n  // redirect to /login on failure\n}",
			want:    false,
		},
		{
			name:    "grok login prompt",
			content: "Your Grok credentials expired.\nPlease run grok login to continue.",
			want:    true,
		},
		{
			name:    "grok sign-in screen",
			content: "Sign in to Grok to continue this session.",
			want:    true,
		},
		{
			name:    "cursor agent login prompt",
			content: "Your Cursor credentials expired.\nPlease run agent login to continue.",
			want:    true,
		},
		{
			name:    "cursor sign-in screen",
			content: "Sign in to Cursor to continue this session.",
			want:    true,
		},
		// Positive coverage for needles narrowed to remove false positives.
		// Each anchored form must still match a real login/expired prompt that
		// renders it the way the executor does.
		{
			name:    "expired session prompt with your prefix",
			content: "Your session has expired. Please run /login to continue.",
			want:    true,
		},
		{
			name:    "run `/login` re-auth prompt",
			content: "Session expired. Run `/login` to re-authenticate.",
			want:    true,
		},
		{
			name:    "run `grok login` re-auth prompt",
			content: "Your Grok session expired. Run `grok login` to re-authenticate.",
			want:    true,
		},
		{
			name:    "run `agent login` re-auth prompt",
			content: "Your Cursor session expired. Run `agent login` to re-authenticate.",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, got := DetectAuthPrompt(tt.content)
			if got != tt.want {
				t.Errorf("DetectAuthPrompt() = %v, want %v (reason=%q)", got, tt.want, reason)
			}
			if got && reason == "" {
				t.Errorf("DetectAuthPrompt() returned match with empty reason")
			}
		})
	}
}

// TestDetectAuthPromptFalsePositiveOnCodeOutput is the regression test for the
// bug where loose substring matching over the whole captured pane parked
// working tasks as auth-blocked whenever their output incidentally contained
// a multi-word auth phrase. Each case is ordinary agent output (a diff, a test
// run, an OAuth client, login docs) that legitimately prints one of the
// needles; none of them is a logged-out executor session, so none may match.
func TestDetectAuthPromptFalsePositiveOnCodeOutput(t *testing.T) {
	cases := []struct{ name, content string }{
		{"writing tests for invalid API key handling",
			"Diff:\n+func TestInvalidAPIKey(t *testing.T) {\n+\terr := client.Do(req)\n+\tif !errors.Is(err, ErrInvalidAPIKey) {\n+\t\tt.Fatalf(\"expected invalid api key, got %v\", err)\n+\t}\n+}"},
		{"running tests that emit session has expired",
			"$ go test ./auth\n--- FAIL: TestSessionExpiry (0.01s)\n    session_test.go:42: session has expired before refresh\nFAIL"},
		{"implementing an OAuth client, printing errors",
			"> writing oauth.go\n  if resp.StatusCode == 401 {\n    return fmt.Errorf(\"oauth token expired: %w\", err)\n  }"},
		{"reviewing docs about login methods",
			"## Sign in\nTo sign in to grok, run `grok login` and paste the token."},
		{"reviewing cursor login docs",
			"## Sign in\nTo sign in to cursor, run `agent login` and paste the token."},
		{"docs instructing run `/login`",
			"To re-authenticate, run `/login` and paste the token from the console."},
		{"docs instructing run `agent login`",
			"Run `agent login` and paste the token printed by the dashboard."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := DetectAuthPrompt(tc.content); got {
				t.Errorf("DetectAuthPrompt falsely reported auth-required for ordinary task output.\n%s", tc.content)
			}
		})
	}
}

// A remotely placed task's window lives in a tmux server on another machine.
// Before this, the sweep captured LOCAL tmux for it, got nothing back, and
// concluded there was no login prompt — so a logged-out fleet host produced a
// task that hung and then parked with no reason attached.
func TestCheckAuthStuckTasksReadsARemotelyPlacedPane(t *testing.T) {
	exec, database := newTestExecutor(t)
	task := createProcessingTask(t, database, "a placed task")
	if err := database.SetTaskPlacement(task.ID, "mona", "fleet host"); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskDaemonSession(task.ID, "ty-daemon"); err != nil {
		t.Fatal(err)
	}

	// Stand in for ssh: whatever ty asks mona, the screen shows a login prompt.
	stubSSH(t, "#!/bin/sh\necho 'Please run /login to authenticate'\n")

	exec.checkAuthStuckTasks()

	got, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.StatusBlocked {
		t.Errorf("status = %q, want %q", got.Status, db.StatusBlocked)
	}
	logs, err := database.GetTaskLogs(task.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, l := range logs {
		if l.LineType == "error" && strings.Contains(l.Content, "/login") {
			found = l.Content
		}
	}
	if found == "" {
		t.Fatal("no error log explaining the block")
	}
	// Which host to go and log into is the whole actionable part.
	if !strings.Contains(found, "mona") {
		t.Errorf("log %q does not name the host", found)
	}
}

// The same sweep must keep leaving healthy placed tasks alone.
func TestCheckAuthStuckTasksLeavesAWorkingRemoteTaskAlone(t *testing.T) {
	exec, database := newTestExecutor(t)
	task := createProcessingTask(t, database, "a working placed task")
	if err := database.SetTaskPlacement(task.ID, "mona", "fleet host"); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskDaemonSession(task.ID, "ty-daemon"); err != nil {
		t.Fatal(err)
	}
	stubSSH(t, "#!/bin/sh\necho 'Editing remote_poll.go'\necho 'Running tests...'\n")

	exec.checkAuthStuckTasks()

	got, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.StatusProcessing {
		t.Errorf("status = %q, want it left in %q", got.Status, db.StatusProcessing)
	}
}
