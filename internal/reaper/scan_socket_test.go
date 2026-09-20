package reaper

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/tmuxctl"
)

// requireTmux skips the test if tmux is not available in PATH. Mirrors
// cmd/task/sessions_test.go:17, kept local so the reaper package has no
// dependency on package main's helpers.
func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := osexec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available in PATH")
	}
}

// prependTmuxShim writes a fake `tmux` into a scratch dir and pushes that dir to
// the front of PATH, so the next LivePanePIDs() call invokes the shim instead
// of the real tmux. The shim records its own argv to recordPath and prints
// fakePanePID (a single pane PID) on stdout. This lets the routing of
// LivePanePIDs be observed without a real tmux server, so the regression test
// runs on every host — including the CI's tmux-less workers.
//
// The shim reads its record target and emitted PID from the environment so the
// one fixed script serves every subcase. The script uses /bin/sh and so is a
// unix artifact, mirroring the unix-or-skip stance of the rest of the reaper
// suite.
func prependTmuxShim(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("tmux shim is unix-only")
	}
	dir := t.TempDir()
	shim := filepath.Join(dir, "tmux")
	// `printf '%s\n' "$@"` prints each argv word on its own line; the recorded
	// file is therefore one word per line, which strings.Fields can re-split
	// safely (none of tmux's args here contain whitespace).
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"$TMUX_RECORD_ARGS\"\n" +
		"printf '%s\\n' \"$TMUX_FAKE_PID\"\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatalf("write tmux shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestLivePanePIDs_RoutesThroughAgentSocket pins the bug at the
// command-construction level: with a private socket recorded
// (tmuxctl.Socket() == "taskyou"), LivePanePIDs() must invoke
// `tmux -L taskyou list-panes -a -F #{pane_pid}` — the same routing every other
// tmux call in the cleanup/suspend flow uses. The buggy call ran bare
// `tmux list-panes -a` against the default socket and missed the agent-server
// pane PIDs, defeating RuleLivePane so an in-pane dev server of a blocked idle
// task was mis-reaped as RuleStaleIdle.
//
// This test swaps a recording shim onto PATH, so it runs without a real tmux.
// Under the buggy tree the recorded argv lacks `-L taskyou` and the assertion
// fires; after the fix the argv contains it. The default-socket subcase also
// guards against an over-fix that always prepends `-L` and breaks existing
// default-socket installs (where Socket() == "" and bare `tmux` is correct).
func TestLivePanePIDs_RoutesThroughAgentSocket(t *testing.T) {
	// Private (named) socket: agent panes live on `tmux -L taskyou`. The env
	// override at tmuxctl.Socket (line 61) wins before the underTest
	// short-circuit, so this is honored even under `go test`.
	recordNamed := filepath.Join(t.TempDir(), "argv-named.txt")
	t.Setenv("TMUX_RECORD_ARGS", recordNamed)
	t.Setenv("TMUX_FAKE_PID", "4242")
	prependTmuxShim(t)
	t.Setenv(tmuxctl.EnvSocket, "taskyou")

	named := LivePanePIDs()
	if !named[4242] {
		t.Errorf("LivePanePIDs() = %v, want the shim's pane PID 4242 parsed from stdout", named)
	}
	recordedNamed, err := os.ReadFile(recordNamed)
	if err != nil {
		t.Fatalf("read recorded argv (named socket): %v", err)
	}
	argvNamed := strings.Fields(string(recordedNamed))
	if !slices.Contains(argvNamed, "-L") || !slices.Contains(argvNamed, "taskyou") {
		t.Errorf("LivePanePIDs() ran `tmux %s`; it must prepend `-L taskyou` so the "+
			"live-pane set is read from the same server the agents live on, "+
			"matching agentTmuxCmd/tmuxctl.AgentArgs. Without it, an in-pane side "+
			"process of a blocked idle task is mis-reaped as RuleStaleIdle "+
			"instead of spared as RuleLivePane.", strings.Join(argvNamed, " "))
	}
	if !slices.Contains(argvNamed, "list-panes") || !slices.Contains(argvNamed, "-a") || !slices.Contains(argvNamed, "-F") {
		t.Errorf("LivePanePIDs() argv=`%s` missing list-panes -a -F flags", strings.Join(argvNamed, " "))
	}

	// Default socket (normalize("default") == ""): no `-L` should be prepended,
	// so existing default-socket installs behave exactly as before.
	recordDefault := filepath.Join(t.TempDir(), "argv-default.txt")
	t.Setenv("TMUX_RECORD_ARGS", recordDefault)
	t.Setenv("TMUX_FAKE_PID", "5353")
	prependTmuxShim(t)
	t.Setenv(tmuxctl.EnvSocket, "default")

	def := LivePanePIDs()
	if !def[5353] {
		t.Errorf("LivePanePIDs() on default socket = %v, want the shim's pane PID 5353", def)
	}
	recordedDefault, err := os.ReadFile(recordDefault)
	if err != nil {
		t.Fatalf("read recorded argv (default socket): %v", err)
	}
	argvDefault := strings.Fields(string(recordedDefault))
	if slices.Contains(argvDefault, "-L") {
		t.Errorf("LivePanePIDs() on the default socket ran `tmux %s`; it must NOT "+
			"prepend -L when Socket() is empty, so existing default-socket "+
			"installs keep hitting tmux's own default server exactly as before.",
			strings.Join(argvDefault, " "))
	}
}

// TestLivePanePIDs_EmptyWhenTmuxFails guards the error branch: when the recorded
// tmux exits nonzero (no server on the queried socket), LivePanePIDs() returns
// an empty set rather than panicking or fabricating PIDs. The reaper treats an
// empty live-pane set as "no live pane", so this is the safe degradation path.
func TestLivePanePIDs_EmptyWhenTmuxFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux shim is unix-only")
	}
	dir := t.TempDir()
	shim := filepath.Join(dir, "tmux")
	// Exit nonzero and produce no pane lines.
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write failing tmux shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Socket choice is irrelevant here; pin the default to keep the test
	// independent of the recorded-choice file.
	t.Setenv(tmuxctl.EnvSocket, "default")

	got := LivePanePIDs()
	if len(got) != 0 {
		t.Errorf("LivePanePIDs() = %v, want empty map when tmux exits nonzero", got)
	}
}

// TestLivePanePIDs_HonorsAgentSocket is the live, end-to-end contract: with a
// private socket recorded, a pane created on `tmux -L <socket>` MUST appear in
// the set LivePanePIDs() returns. Under the buggy tree the call hit the default
// socket and the agent-server pane PID was absent, so an in-pane dev server of
// a blocked idle task was mis-reaped as RuleStaleIdle. It is gated on tmux and
// skips cleanly where tmux is not installed.
func TestLivePanePIDs_HonorsAgentSocket(t *testing.T) {
	requireTmux(t)

	// A uniquely-named socket is its own tmux server, so the probe never
	// touches the default server or the live install. A fresh TMUX_TMPDIR keeps
	// the socket path short and means a stale server from a prior run (at a
	// different tmpdir) is unreachable. We do NOT use tmuxtest.Isolate here: it
	// pins TASKYOU_TMUX_SOCKET=default, which would defeat the whole point of
	// this test.
	const socket = "taskyou-reaper-probe"
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	// Socket() honors EnvSocket before the underTest short-circuit, so this
	// makes tmuxctl.AgentArgs(...) prepend `-L taskyou-reaper-probe`. The buggy
	// LivePanePIDs ignored Socket() entirely and kept hitting the default
	// socket — which is the divergence under test.
	t.Setenv(tmuxctl.EnvSocket, socket)

	session := "livepane-probe"
	if err := osexec.Command("tmux", "-L", socket, "new-session", "-d", "-s", session, "-x", "80", "-y", "24").Run(); err != nil {
		t.Fatalf("new-session on -L %s: %v", socket, err)
	}
	t.Cleanup(func() {
		osexec.Command("tmux", "-L", socket, "kill-server").Run()
	})

	// Establish ground truth: the pane PID on the agent server, queried through
	// the same socket-aware helper the rest of the cleanup flow uses.
	agentOut, err := osexec.Command("tmux", tmuxctl.AgentArgs("list-panes", "-a", "-F", "#{pane_pid}")...).Output()
	if err != nil {
		t.Fatalf("agent list-panes: %v", err)
	}
	var agentPID int
	for _, line := range strings.Split(strings.TrimSpace(string(agentOut)), "\n") {
		if p, perr := strconv.Atoi(strings.TrimSpace(line)); perr == nil {
			agentPID = p
		}
	}
	if agentPID == 0 {
		t.Fatal("no pane PID found on agent server")
	}

	// The production call under test. On a taskyou-socket install this is the
	// set Plan() consumes via hasLiveAncestor; it MUST contain the agent pane
	// PID or an in-pane dev server of a blocked idle task is mis-reaped as
	// RuleStaleIdle instead of spared as RuleLivePane.
	got := LivePanePIDs()
	if !got[agentPID] {
		t.Errorf("LivePanePIDs() did not include agent-server pane PID %d "+
			"(it reads bare `tmux list-panes -a` from the default socket while "+
			"TASKYOU_TMUX_SOCKET=%s routes agent panes to tmux -L %s). "+
			"An in-pane process of a blocked idle task will be mis-reaped as "+
			"RuleStaleIdle instead of spared as RuleLivePane.",
			agentPID, socket, socket)
	}
}
