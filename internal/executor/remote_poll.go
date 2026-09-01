package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// How often a REMOTELY placed task's tmux window is checked, and how long one
// check may take.
//
// A local poll is a syscall to a tmux server on this machine, so it runs every
// second and finishes in milliseconds. A remote poll is an ssh round trip: at
// one per second, a fleet of ten placed tasks would open ten ssh connections a
// second, all day, to learn nothing. Fifteen seconds still notices a finished
// agent well inside the time it takes a human to look at the board, and the
// probe timeout is generous enough that a slow handshake is not mistaken for a
// dead window.
// They are vars, not consts, only so tests can shorten them; nothing
// user-facing changes them.
var (
	remotePollInterval = 15 * time.Second
	remoteProbeTimeout = 20 * time.Second
)

// windowProbe is what one look at a task's tmux window found.
//
// The third state is the point of the type. Locally there are only two answers —
// the window is there or it is not — because the tmux server is on this machine
// and asking it cannot fail for reasons unrelated to the window. Over ssh it
// can: the host reboots, the VPN drops, the network blips. A failed LOOK is not
// a finished TASK, and collapsing the two (which is what a bare
// `list-panes ... .Run() == nil` does) parks a task as "needs review" while its
// agent is still working perfectly well on a host we merely could not see.
type windowProbe int

const (
	windowLive windowProbe = iota
	windowGone
	windowUnreachable
)

func (w windowProbe) String() string {
	switch w {
	case windowLive:
		return "live"
	case windowGone:
		return "gone"
	default:
		return "unreachable"
	}
}

// probeWindow asks the runner behind ctx whether target still exists.
//
// remote selects the classification, not the command: the command is the same
// `tmux list-panes -t <target>` the local path has always run, built through the
// context's runner so it lands on whichever machine the task was placed on. Only
// a remote probe can answer "unreachable"; a local one keeps exactly the two
// outcomes it has always had, so an unplaced task's polling is unchanged.
func probeWindow(ctx context.Context, target string, remote bool) windowProbe {
	cmd := tmuxCmd(ctx, "list-panes", "-t", target)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if !remote {
			return windowGone
		}
		return classifyRemoteProbeFailure(ctx.Err(), err)
	}
	return windowLive
}

// classifyRemoteProbeFailure separates "the window is gone" from "we could not
// look", using the one signal ssh gives us for free: its exit status.
//
// ssh exits 255 when SSH ITSELF failed — connection refused, host unreachable,
// handshake timed out, auth declined. Any other status is the remote command's
// own, relayed verbatim, so a 1 here is tmux on the far side saying the window
// is not there. A probe that never produced a status at all (context deadline,
// or the ssh binary failing to start) is likewise a failure to look.
func classifyRemoteProbeFailure(ctxErr, err error) windowProbe {
	if ctxErr != nil {
		return windowUnreachable
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() == 255 {
			return windowUnreachable
		}
		return windowGone
	}
	return windowUnreachable
}

// hostReachability tracks, and narrates, a placed host we cannot currently see.
//
// Silence is the failure mode this exists to prevent: a task whose host has gone
// away must not be transitioned, but it must also not sit in 'processing' with
// nothing anywhere saying why. So the first outage writes a task log line, long
// outages repeat it on a slow cadence, and recovery says so too.
type hostReachability struct {
	host    string
	since   time.Time
	lastLog time.Time
}

// remoteOutageReminder is how often a continuing outage re-announces itself.
const remoteOutageReminder = 10 * time.Minute

// unreachable records a failed look and returns the line to log, or "" when the
// outage has already been announced recently enough.
func (h *hostReachability) unreachable(now time.Time) string {
	if h.since.IsZero() {
		h.since, h.lastLog = now, now
		return fmt.Sprintf(
			"Cannot reach %s to check on this task's session. Leaving the task as it is — the agent may still be running there — and will keep checking.",
			h.host)
	}
	if now.Sub(h.lastLog) < remoteOutageReminder {
		return ""
	}
	h.lastLog = now
	return fmt.Sprintf("Still cannot reach %s (%s). The task is left untouched until its session can be checked again.",
		h.host, now.Sub(h.since).Round(time.Second))
}

// reachable records a successful look and returns the recovery line, or "" when
// there was no outage to recover from.
func (h *hostReachability) reachable(now time.Time) string {
	if h.since.IsZero() {
		return ""
	}
	outage := now.Sub(h.since).Round(time.Second)
	h.since, h.lastLog = time.Time{}, time.Time{}
	return fmt.Sprintf("Reached %s again after %s.", h.host, outage)
}
