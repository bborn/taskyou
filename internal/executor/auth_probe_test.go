package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// The probe runs wherever the context's runner points, which is the whole
// point: the question is never "is claude logged in" but "is claude logged in
// on mona".
func TestCheckExecutorAuthClassifiesTheHostsAnswer(t *testing.T) {
	tests := []struct {
		name     string
		executor string
		stub     string
		want     authState
	}{
		{
			name:     "claude says it is logged in",
			executor: db.ExecutorClaude,
			stub:     `echo '{ "loggedIn": true, "authMethod": "claude.ai" }'`,
			want:     authOK,
		},
		{
			name:     "claude says it is not",
			executor: db.ExecutorClaude,
			stub:     `echo '{ "loggedIn": false }'`,
			want:     authLoggedOut,
		},
		{
			name:     "an older CLI with no auth status subcommand",
			executor: db.ExecutorClaude,
			stub:     `echo "error: unknown command 'auth'" >&2; exit 1`,
			want:     authUnknown,
		},
		{
			name:     "the CLI is not installed on that host",
			executor: db.ExecutorClaude,
			stub:     `echo 'sh: 1: claude: command not found' >&2; exit 127`,
			want:     authUnknown,
		},
		{
			name:     "codex reports a failure",
			executor: db.ExecutorCodex,
			stub:     `echo 'not logged in' >&2; exit 1`,
			want:     authLoggedOut,
		},
		{
			name:     "codex is happy",
			executor: db.ExecutorCodex,
			stub:     `echo 'Logged in'`,
			want:     authOK,
		},
		{
			name:     "an executor with no probe is never blocked",
			executor: db.ExecutorGrok,
			stub:     `exit 1`,
			want:     authUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubSSH(t, "#!/bin/sh\n"+tt.stub+"\n")
			ctx := WithRunner(context.Background(), RemoteRunner{Host: "mona"})
			got, _ := checkExecutorAuth(ctx, tt.executor)
			if got != tt.want {
				t.Errorf("state = %v, want %v", got, tt.want)
			}
		})
	}
}

// A logged-out host must produce something a human can act on without reading
// code: which executor, which host, and the command to run there.
func TestRemoteAuthFailureNamesTheFix(t *testing.T) {
	msg := remoteAuthFailure("claude", "mona", "claude auth login")
	for _, want := range []string{"claude", "mona", "ssh mona -t claude auth login"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q is missing %q", msg, want)
		}
	}
}

func TestTaskExecutorNameFallsBackToTheDefault(t *testing.T) {
	if got := taskExecutorName(&db.Task{Executor: "codex"}); got != "codex" {
		t.Errorf("got %q", got)
	}
	if got := taskExecutorName(&db.Task{}); got != db.DefaultExecutor() {
		t.Errorf("got %q, want %q", got, db.DefaultExecutor())
	}
}
