package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// exitingSSH writes an executable that exits with the given status. It stands in
// for ssh: 255 is ssh's own "I could not connect", anything else is the remote
// command's status relayed back.
func exitingSSH(t *testing.T, exitCode int) string {
	t.Helper()
	return stubSSH(t, "#!/bin/sh\nexit "+strconv.Itoa(exitCode)+"\n")
}

// sigKilledSSH writes a stub that stands in for an ssh process the kernel or an
// operator has SIGKILLed mid-probe (OOM, `pkill -9`, cgroup pressure). It kills
// itself with SIGKILL before doing anything else, so cmd.Run() reports an
// *exec.ExitError whose ExitCode() is -1 while the probe's own context is still
// live — the exact shape an externally-signalled probe arrives at the classifier
// in, distinct from a tripped context deadline.
//
// exitingSSH cannot represent this: `sh -c 'exit N'` always exits with a status
// and never yields ExitCode()==-1.
func sigKilledSSH(t *testing.T) string {
	t.Helper()
	return stubSSH(t, "#!/bin/sh\nkill -9 $$\n")
}

// The whole point of the third probe state: ssh failing to connect must not look
// like a window that has exited.
func TestClassifyRemoteProbeFailureSeparatesUnreachableFromGone(t *testing.T) {
	tests := []struct {
		name     string
		ctxErr   error
		exitCode int
		want     windowProbe
	}{
		{"ssh could not connect (255)", nil, 255, windowUnreachable},
		{"tmux says no such window (1)", nil, 1, windowGone},
		{"the probe timed out", context.DeadlineExceeded, 1, windowUnreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(exitingSSH(t, tt.exitCode))
			err := cmd.Run()
			if err == nil {
				t.Fatal("stub exited 0; the test needs a failure to classify")
			}
			if got := classifyRemoteProbeFailure(tt.ctxErr, err); got != tt.want {
				t.Errorf("classifyRemoteProbeFailure = %v, want %v", got, tt.want)
			}
		})
	}
}

// A probe that never produced an exit status at all (no ssh binary) is a failure
// to LOOK, not evidence the window is gone.
func TestClassifyRemoteProbeFailureTreatsAStartFailureAsUnreachable(t *testing.T) {
	if got := classifyRemoteProbeFailure(nil, errors.New("exec: not found")); got != windowUnreachable {
		t.Errorf("got %v, want unreachable", got)
	}
}

// A probe whose ssh process was killed by a signal — an OOM, an operator's
// `pkill -9`, cgroup pressure — never produced an exit status at all. Go's
// (*exec.ExitError).ExitCode returns -1, the documented sentinel for a
// signal-terminated process, and a signalled ssh never relayed tmux's answer, so
// the classifier must read that as a failure to look rather than a gone window.
//
// exitingSSH stubs ssh as `sh -c 'exit N'`, which can only ever produce an
// exit-CODE error; a signal-terminated error has a different shape, so this case
// runs a real process and signals it (the way the bug is produced in the wild).
func TestClassifyRemoteProbeFailureTreatsASignalledProbeAsUnreachable(t *testing.T) {
	signals := []struct {
		name string
		sig  os.Signal
	}{
		{"SIGKILL (OOM, pkill -9)", syscall.SIGKILL},
		{"SIGTERM (cgroup graceful shutdown)", syscall.SIGTERM},
	}
	for _, s := range signals {
		t.Run(s.name, func(t *testing.T) {
			// Mirror the production wire shape: RemoteRunner wraps every remote
			// command in `sh -lc '...'`, so the process ty waits on is the sh
			// below, not a bare executable.
			cmd := exec.Command("sh", "-c", "sleep 30")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Process.Signal(s.sig); err != nil {
				t.Fatal(err)
			}
			err := cmd.Wait()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("signal produced err=%T, want *exec.ExitError", err)
			}
			if got := exitErr.ExitCode(); got != -1 {
				t.Fatalf("ExitCode()=%d, want -1 (Go's sentinel for signal termination)", got)
			}
			// ctxErr is nil here: the probe's own context did not time out, the
			// ssh process was killed from outside it. This is the exact shape that
			// separates this bug from the (already correct) context-deadline branch.
			if got := classifyRemoteProbeFailure(nil, err); got != windowUnreachable {
				t.Errorf("signalled probe classified as %v, want unreachable (a failed look, not a gone window)", got)
			}
		})
	}
}

// The local path keeps exactly two outcomes. A local tmux that fails for any
// reason means "gone", as it always has — an unplaced task must poll
// byte-for-byte the way it did before remote polling existed.
func TestProbeWindowLocalNeverReportsUnreachable(t *testing.T) {
	got := probeWindow(context.Background(), "no-such-session-5247", false)
	if got != windowGone {
		t.Errorf("local probe of a missing session = %v, want gone", got)
	}
}

func TestProbeWindowRemoteReportsUnreachableWhenSSHCannotConnect(t *testing.T) {
	ctx := WithRunner(context.Background(), RemoteRunner{Host: "h", SSHBin: exitingSSH(t, 255)})
	if got := probeWindow(ctx, "task-daemon-1:task-5247", true); got != windowUnreachable {
		t.Errorf("probe with a failing ssh = %v, want unreachable", got)
	}
}

func TestProbeWindowRemoteReportsGoneWhenTmuxSaysSo(t *testing.T) {
	ctx := WithRunner(context.Background(), RemoteRunner{Host: "h", SSHBin: exitingSSH(t, 1)})
	if got := probeWindow(ctx, "task-daemon-1:task-5247", true); got != windowGone {
		t.Errorf("probe with tmux exit 1 = %v, want gone", got)
	}
}

// A probe whose ssh is externally SIGKILLed (OOM, pkill -9, cgroup pressure) must
// read as unreachable, not gone: a signalled ssh never relayed tmux's status, so
// it tells us nothing about the window. This mirrors the ssh-exits-255 case but
// exercises the -1 exit-code shape that an external kill produces.
func TestProbeWindowRemoteReportsUnreachableWhenSSHIsSignalled(t *testing.T) {
	ctx := WithRunner(context.Background(), RemoteRunner{Host: "h", SSHBin: sigKilledSSH(t)})
	if got := probeWindow(ctx, "task-daemon-1:task-5247", true); got != windowUnreachable {
		t.Errorf("probe with a signalled ssh = %v, want unreachable", got)
	}
}

// An unreachable host must NOT transition the task. The work may still be
// running perfectly well on a machine we temporarily cannot see, and parking it
// "needs review" would be a lie the human then has to undo.
func TestPollTmuxSessionDoesNotTransitionAnUnreachableHost(t *testing.T) {
	defer restorePollTimings(t)()
	remotePollInterval, remoteProbeTimeout = 30*time.Millisecond, 2*time.Second

	exec, database := newTestExecutor(t)
	task := &db.Task{Title: "unreachable host", Type: "task", Project: "test"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTaskStatus(task.ID, db.StatusProcessing, db.ActorCLI, "test fixture", db.ByHuman("test fixture")); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	ctx = WithRunner(ctx, RemoteRunner{Host: "ghost", SSHBin: exitingSSH(t, 255)})

	// ~20 ticks: far past the 3-miss threshold that parks a local task.
	result := exec.pollTmuxSession(ctx, task.ID, "task-daemon-1:task-1")

	if result.NeedsInput {
		t.Fatal("an unreachable host parked the task for review; a failed look is not a finished task")
	}
	if !result.Interrupted {
		t.Fatalf("poll returned %+v; it should still have been polling when the context expired", result)
	}

	// And it must not be silent about it.
	logs, err := database.GetTaskLogs(task.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var said bool
	for _, l := range logs {
		if strings.Contains(l.Content, "Cannot reach ghost") {
			said = true
		}
	}
	if !said {
		t.Error("nothing in the task log says the host could not be reached")
	}
}

// The other half of the same contract: when the host DOES answer and the window
// is gone, a remote task parks for review exactly as a local one does — blocked,
// never done.
func TestPollTmuxSessionParksARemoteTaskWhenItsWindowIsGone(t *testing.T) {
	defer restorePollTimings(t)()
	remotePollInterval, remoteProbeTimeout = 30*time.Millisecond, 2*time.Second

	exec, database := newTestExecutor(t)
	task := &db.Task{Title: "finished remotely", Type: "task", Project: "test"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTaskStatus(task.ID, db.StatusProcessing, db.ActorCLI, "test fixture", db.ByHuman("test fixture")); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = WithRunner(ctx, RemoteRunner{Host: "ol-agents", SSHBin: exitingSSH(t, 1)})

	result := exec.pollTmuxSession(ctx, task.ID, "task-daemon-1:task-1")
	if !result.NeedsInput {
		t.Fatalf("poll returned %+v, want NeedsInput (the task parks for a human)", result)
	}
	if result.Success {
		t.Fatal("a remote session ending marked the task done; only a human may do that")
	}
}

func restorePollTimings(t *testing.T) func() {
	t.Helper()
	interval, timeout := remotePollInterval, remoteProbeTimeout
	return func() { remotePollInterval, remoteProbeTimeout = interval, timeout }
}

// The outage narration: announce once, repeat rarely, and say when it clears.
func TestHostReachabilityNarratesAnOutageOnceAndItsRecovery(t *testing.T) {
	h := hostReachability{host: "ol-agents"}
	now := time.Now()

	first := h.unreachable(now)
	if !strings.Contains(first, "Cannot reach ol-agents") {
		t.Fatalf("first outage line = %q", first)
	}
	if repeat := h.unreachable(now.Add(time.Minute)); repeat != "" {
		t.Errorf("outage re-announced after a minute: %q", repeat)
	}
	if repeat := h.unreachable(now.Add(remoteOutageReminder + time.Second)); repeat == "" {
		t.Error("a long outage never repeated itself")
	}
	if back := h.reachable(now.Add(20 * time.Minute)); !strings.Contains(back, "Reached ol-agents again") {
		t.Errorf("recovery line = %q", back)
	}
	if again := h.reachable(now.Add(21 * time.Minute)); again != "" {
		t.Errorf("recovery announced with no outage to recover from: %q", again)
	}
}

// Multiplexing is what makes polling over ssh affordable; every remote command
// must carry it.
func TestRemoteCommandsShareOneSSHConnection(t *testing.T) {
	args := strings.Join(RemoteRunner{Host: "ol-agents"}.Command(context.Background(), "", "tmux", "list-panes").Args, " ")
	for _, want := range []string{"ControlMaster=auto", "ControlPath=", "ControlPersist="} {
		if !strings.Contains(args, want) {
			t.Errorf("ssh invocation is missing %q: %s", want, args)
		}
	}
}

func TestIdleTrackerNeedsSustainedStillness(t *testing.T) {
	tr := idleTracker{threshold: 3}

	// A changing screen is an agent still working, however long we watch.
	for i, sum := range []string{"a", "b", "c", "d", "e"} {
		if tr.record(sum, true) {
			t.Fatalf("changing pane reported idle at step %d", i)
		}
	}

	// Stillness only counts once it is sustained.
	if tr.record("x", true) {
		t.Error("first sighting of a new state is not stillness")
	}
	if tr.record("x", true) {
		t.Error("two identical captures is below the threshold")
	}
	if !tr.record("x", true) {
		t.Error("threshold consecutive identical captures should read as idle")
	}
}

func TestIdleTrackerResetsWhenTheAgentMovesAgain(t *testing.T) {
	tr := idleTracker{threshold: 3}
	tr.record("x", true)
	tr.record("x", true)
	// An agent that was thinking and starts printing again is not finished.
	if tr.record("y", true) {
		t.Fatal("a changed pane must reset the run")
	}
	if tr.record("y", true) {
		t.Error("run should have restarted from the change")
	}
}

func TestIdleTrackerTreatsAnUnreadablePaneAsNotIdle(t *testing.T) {
	tr := idleTracker{threshold: 2}
	tr.record("x", true)
	// A failed capture teaches us nothing, and nothing is not stillness —
	// the same rule the window probe follows for an unreachable host.
	if tr.record("", false) {
		t.Fatal("an unreadable pane must not count toward idleness")
	}
	if tr.record("x", true) {
		t.Error("the run should have restarted after the failed read")
	}
}
