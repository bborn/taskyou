package executor

import (
	"context"
	"strings"
	"testing"
	"time"
)

func remoteArgs(t *testing.T, r RemoteRunner, workDir, name string, args ...string) []string {
	t.Helper()
	return r.Command(context.Background(), workDir, name, args...).Args
}

// unwrapLoginShell returns the script inside the "sh -lc '...'" wrapper every
// remote command carries, with the wrapper's quoting undone — i.e. exactly the
// line the remote LOGIN shell ends up parsing.
func unwrapLoginShell(t *testing.T, line string) string {
	t.Helper()
	const prefix = "sh -lc '"
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, "'") {
		t.Fatalf("remote command %q is not wrapped in a login shell; a non-login shell never reads ~/.profile, so version-managed tools are invisible", line)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(line, prefix), "'")
	return strings.ReplaceAll(body, `'\''`, "'")
}

func TestRemoteRunnerRunsOverSSHAgainstTheNamedHost(t *testing.T) {
	r := RemoteRunner{Host: "ol-agents", WorkDir: "~/projects/engineering"}

	got := remoteArgs(t, r, "", "tmux", "new-window", "-d")
	if got[0] != "ssh" {
		t.Fatalf("argv[0] = %q, want ssh", got[0])
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "ol-agents") {
		t.Errorf("ssh invocation does not name the host: %v", got)
	}
	// BatchMode matters: without it ssh can drop into an interactive prompt
	// inside the daemon, where nothing can answer it, and the task hangs.
	if !strings.Contains(joined, "BatchMode=yes") {
		t.Errorf("ssh invocation is not batch-mode: %v", got)
	}
	remote := unwrapLoginShell(t, got[len(got)-1])
	if !strings.Contains(remote, "tmux") || !strings.Contains(remote, "new-window") {
		t.Errorf("remote command = %q, want the tmux command", remote)
	}
}

// The workdir is a path on the REMOTE machine, so it must travel inside the
// remote shell line, never as cmd.Dir — which would point at this machine.
func TestRemoteRunnerCdsOnTheRemoteAndNeverSetsLocalDir(t *testing.T) {
	r := RemoteRunner{Host: "mona", WorkDir: "~/Projects/taskyou"}
	cmd := r.Command(context.Background(), "", "git", "status")

	if cmd.Dir != "" {
		t.Errorf("cmd.Dir = %q, want empty: a remote workdir is not a local one", cmd.Dir)
	}
	remote := unwrapLoginShell(t, cmd.Args[len(cmd.Args)-1])
	if !strings.HasPrefix(remote, "cd ~/") {
		t.Errorf("remote command = %q, want a cd into the remote workdir with the ~ left for the remote shell", remote)
	}
	if !strings.Contains(remote, "'Projects/taskyou'") {
		t.Errorf("remote command = %q, want the rest of the path quoted", remote)
	}
}

func TestRemoteRunnerPerCommandWorkDirWinsOverTheDefault(t *testing.T) {
	r := RemoteRunner{Host: "mona", WorkDir: "/home/bruno/Projects/taskyou"}
	remote := remoteArgs(t, r, "/srv/other", "git", "status")
	line := unwrapLoginShell(t, remote[len(remote)-1])
	if !strings.Contains(line, "cd '/srv/other'") {
		t.Errorf("remote command = %q, want the per-command workdir", line)
	}
	if strings.Contains(line, "taskyou") {
		t.Errorf("remote command = %q, still carries the default workdir", line)
	}
}

// A tmux command addresses a server, not a directory. With no workdir on either
// the command or the runner there must be no cd at all.
func TestRemoteRunnerOmitsCdWhenThereIsNoWorkDir(t *testing.T) {
	r := RemoteRunner{Host: "mona"}
	args := remoteArgs(t, r, "", "tmux", "list-sessions")
	line := unwrapLoginShell(t, args[len(args)-1])
	if strings.Contains(line, "cd ") {
		t.Errorf("remote command = %q, want no cd", line)
	}
}

func TestRemoteRunnerQuotesArguments(t *testing.T) {
	r := RemoteRunner{Host: "mona"}
	remote := remoteArgs(t, r, "", "tmux", "new-window", "-n", "task-5228", "sh", "-c", "echo 'hi there'; cat /tmp/x")
	line := unwrapLoginShell(t, remote[len(remote)-1])
	// The nested single quotes must survive as data, not become shell syntax.
	if !strings.Contains(line, `'echo '\''hi there'\''; cat /tmp/x'`) {
		t.Errorf("remote command = %q, want the script passed as one quoted argument", line)
	}
}

func TestRemoteRunnerTargetIsTheHost(t *testing.T) {
	if got := (RemoteRunner{Host: "ol-agents"}).Target(); got != "ol-agents" {
		t.Errorf("Target() = %q, want ol-agents", got)
	}
}

// The context-scoped runner is how a placement decision reaches the command
// builders without threading a parameter through every call site.
func TestContextRunnerSelection(t *testing.T) {
	ctx := context.Background()
	if _, ok := RunnerFrom(ctx).(LocalRunner); !ok {
		t.Fatalf("RunnerFrom(background) = %T, want LocalRunner", RunnerFrom(ctx))
	}

	remote := RemoteRunner{Host: "mona", WorkDir: "~/Projects/taskyou"}
	rctx := WithRunner(ctx, remote)
	if RunnerFrom(rctx).Target() != "mona" {
		t.Errorf("RunnerFrom(placed ctx).Target() = %q, want mona", RunnerFrom(rctx).Target())
	}
	// tmuxCmd must follow the context's runner — that is the whole mechanism.
	if got := tmuxCmd(rctx, "list-windows").Args[0]; got != "ssh" {
		t.Errorf("tmuxCmd in a placed context ran %q, want ssh", got)
	}
	if got := tmuxCmd(ctx, "list-windows").Args[0]; strings.HasSuffix(got, "ssh") {
		t.Errorf("tmuxCmd in an unplaced context ran %q, want local tmux", got)
	}

	// A detached probe context keeps the placement but drops the cancellation.
	dctx := detachedRunnerCtx(rctx)
	if RunnerFrom(dctx).Target() != "mona" {
		t.Error("detachedRunnerCtx lost the placement")
	}
}

func TestRemoteRunnerPreflightFailsForAnUnreachableHost(t *testing.T) {
	r := RemoteRunner{
		// RFC 5737 TEST-NET-1: guaranteed not to route anywhere.
		Host:           "192.0.2.1",
		WorkDir:        "/tmp",
		ConnectTimeout: 1 * time.Second,
	}
	dir, err := r.Preflight(context.Background())
	if err == nil {
		t.Fatal("Preflight() = nil for an unroutable host; an unreachable placement must fail visibly")
	}
	if dir != "" {
		t.Errorf("Preflight() returned workdir %q alongside an error", dir)
	}
	if !strings.Contains(err.Error(), "192.0.2.1") {
		t.Errorf("error %q does not name the host that could not be reached", err)
	}
}

func TestRemoteRunnerPreflightRejectsAnEmptyHost(t *testing.T) {
	if _, err := (RemoteRunner{}).Preflight(context.Background()); err == nil {
		t.Fatal("Preflight() = nil for an empty host")
	}
}
