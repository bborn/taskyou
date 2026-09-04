package executor

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Everything else about the carry is tested against LocalRunner, which proves
// the git logic and nothing about ssh. This is the other half: the same
// CarryWork, driven over a real ssh connection to a real host, which is the only
// way to catch quoting that survives a local shell and dies in a login shell,
// stdout that arrives interleaved with stderr, and a `cd` that silently resolves
// on the wrong machine.
//
// Opt-in, because it needs a host:
//
//	TY_REMOTE_TEST_HOST=mona go test ./internal/executor -run TestCarryWorkOverSSH -v
func TestCarryWorkOverSSH(t *testing.T) {
	host := os.Getenv("TY_REMOTE_TEST_HOST")
	if host == "" {
		t.Skip("set TY_REMOTE_TEST_HOST to run the real-ssh carry test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	remote := func(script string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", host, script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ssh %s: %v\n%s", script, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	root := remote("mktemp -d")
	t.Cleanup(func() {
		exec.Command("ssh", "-o", "BatchMode=yes", host, "rm -rf "+root).Run()
	})

	// A bare origin and a working clone, both on the far side — exactly the shape
	// of a placed task's worktree and the repo it pushes to.
	remote(strings.Join([]string{
		"set -e",
		"cd " + root,
		"git init -q --bare --initial-branch=main origin.git",
		"git clone -q origin.git work",
		"cd work",
		"echo seed > README",
		"git add -A && git commit -q -m seed && git push -q -u origin main",
		"git checkout -q -b task/9999",
		// The thing a move exists to rescue: work the agent never committed.
		"printf 'package x\\n' > feature.go",
		"printf 'SECRET=1\\n' > .env",
		"printf '.env\\n' > .gitignore",
	}, "\n"))

	src := WorkSource{
		Runner:  RemoteRunner{Host: host, WorkDir: root + "/work"},
		Host:    host,
		WorkDir: root + "/work",
	}

	// A handoff with the characters most likely to die in a shell round trip.
	handoff := "# Handoff\n\nI was on the exporter; `git log --oneline -1` shows it.\n" +
		"Careful: it's \"half-done\" & $HOME is not what you think — 100% not.\n"

	rep, err := CarryWork(ctx, src, handoff, "this machine")
	if err != nil {
		t.Fatalf("CarryWork over ssh: %v", err)
	}
	if rep.Branch != "task/9999" {
		t.Errorf("Branch = %q, want task/9999", rep.Branch)
	}
	if !rep.WIPCommit {
		t.Error("uncommitted work was carried without reporting the wip commit")
	}

	// The only copy that matters is the one in origin, which is what the target
	// host would clone.
	if got := remote("git --git-dir " + root + "/origin.git show task/9999:feature.go"); got != "package x" {
		t.Errorf("origin has %q; the uncommitted file did not travel", got)
	}
	gotHandoff := remote("git --git-dir " + root + "/origin.git show task/9999:" + HandoffPath)
	if !strings.Contains(gotHandoff, `it's "half-done" & $HOME is not what you think — 100% not.`) {
		t.Errorf("the handoff was mangled crossing the shell:\n%s", gotHandoff)
	}

	var sawEnv bool
	for _, f := range rep.LeftBehind {
		if strings.Contains(f, ".env") {
			sawEnv = true
		}
	}
	if !sawEnv {
		t.Errorf("LeftBehind = %v, want .env named as not travelling", rep.LeftBehind)
	}
}

// The handoff request has to reach a real agent through a real remote tmux, and
// — more importantly — must FAIL fast against a session that is not there, or a
// move waits out its whole timeout for an answer that cannot come.
func TestAgentSenderOverSSH(t *testing.T) {
	host := os.Getenv("TY_REMOTE_TEST_HOST")
	if host == "" {
		t.Skip("set TY_REMOTE_TEST_HOST to run the real-ssh sender test")
	}

	session := "ty-sender-test"
	run := func(script string) { exec.Command("ssh", "-o", "BatchMode=yes", host, script).Run() }
	t.Cleanup(func() { run("tmux kill-session -t " + session + " 2>/dev/null") })

	src := WorkSource{Runner: RemoteRunner{Host: host}, Host: host}

	// No session yet: this must be an error, not a silent success.
	if err := AgentSender(src, session, 9999)("hello"); err == nil {
		t.Error("sending to a session that does not exist reported success; a move would wait out its timeout")
	}

	// Now make the window ty would have made, running a shell that records input.
	run("tmux kill-session -t " + session + " 2>/dev/null; " +
		"tmux new-session -d -s " + session + " -n " + TmuxWindowName(9999) + " 'cat > /tmp/ty-sender-test.txt'")
	time.Sleep(2 * time.Second)

	if err := AgentSender(src, session, 9999)("write the handoff please"); err != nil {
		t.Fatalf("sending to a live window failed: %v", err)
	}
	time.Sleep(2 * time.Second)

	out, err := exec.Command("ssh", "-o", "BatchMode=yes", host, "cat /tmp/ty-sender-test.txt").CombinedOutput()
	if err != nil {
		t.Fatalf("could not read what the window received: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "write the handoff please") {
		t.Errorf("the agent window received %q", out)
	}
}
