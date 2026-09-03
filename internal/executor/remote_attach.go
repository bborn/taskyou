package executor

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/bborn/workflow/internal/db"
)

// RemoteSessionStatus is what a local surface found when it looked for a
// remotely placed task's live tmux window.
type RemoteSessionStatus int

const (
	// RemoteSessionLive means the window is there and can be attached to.
	RemoteSessionLive RemoteSessionStatus = iota
	// RemoteSessionEnded means the host answered and the window is not there.
	RemoteSessionEnded
	// RemoteSessionUnreachable means the host did not answer, so nothing is known
	// about the window one way or the other.
	RemoteSessionUnreachable
)

// RemoteInnerPrefix is the tmux prefix the attached remote session is given, and
// the reason it needs one.
//
// Attaching a tmux session inside a tmux pane nests two servers that both listen
// for C-b. The outer one wins every time, so with the default prefix the inner
// session cannot be detached, scrolled or copied from — the pane renders but is
// half-dead. Rather than leave that as folklore, ty sets the inner session's
// prefix to C-a and says so next to the pane.
const (
	RemoteInnerPrefix      = "C-a"
	RemoteInnerPrefixHuman = "Ctrl-a"
)

// RemoteSessionState reports whether a remotely placed task still has a live
// tmux window on its host.
//
// It is the same probe the poller runs, deliberately: a surface that decides
// whether to attach must not be able to disagree with the daemon that decides
// whether the task is finished.
func (e *Executor) RemoteSessionState(ctx context.Context, task *db.Task) RemoteSessionStatus {
	loc, ok := e.RemoteLocation(task)
	if !ok || loc.Host == "" || task.DaemonSession == "" {
		return RemoteSessionEnded
	}
	ctx, cancel := context.WithTimeout(WithRunner(ctx, RemoteRunner{Host: loc.Host}), remoteProbeTimeout)
	defer cancel()

	switch probeWindow(ctx, remoteWindowTarget(task), true) {
	case windowLive:
		return RemoteSessionLive
	case windowGone:
		return RemoteSessionEnded
	default:
		return RemoteSessionUnreachable
	}
}

// remoteWindowTarget is the "session:window" the task's agent runs in on its
// host.
func remoteWindowTarget(task *db.Task) string {
	return task.DaemonSession + ":" + TmuxWindowName(task.ID)
}

// remoteViewSession is the name of the throwaway, grouped session a local pane
// attaches to.
//
// Attaching directly to the daemon session would be destructive to the daemon
// itself: tmux sizes a session to its smallest attached client, and the daemon's
// windows are shared by every task placed on that host. A grouped session
// (`new-session -t <daemon>`) shares the windows but keeps its own current
// window, its own size and its own options — so the pane can zoom, resize and
// rebind its prefix without any of that reaching the agent's real session.
func remoteViewSession(taskID int64) string {
	return fmt.Sprintf("ty-view-%d-%d", taskID, os.Getpid())
}

// RemoteAttachScript renders the LOCAL shell line a pane runs to show a remotely
// placed task's live session, and to explain itself when that session ends.
//
// The pane must never blank or hang. ssh returns whenever the session ends or
// the link drops, and what follows it — the reason, and the attach command to
// use by hand — is the whole point of wrapping it in a shell instead of running
// ssh as the pane's process directly.
func RemoteAttachScript(task *db.Task, loc RemoteTaskLocation) string {
	view := remoteViewSession(task.ID)

	attach := loc.Attach
	if attach == "" {
		attach = fmt.Sprintf("ssh %s -t tmux attach -t %s", loc.Host, remoteWindowTarget(task))
	}

	// Two levels of quoting, and no more: this script is run by the LOCAL shell,
	// which passes one argument to ssh — a `sh -lc '<chain>'` line the remote
	// shell then parses. (A login shell for the same reason every other remote
	// command uses one: ~/.profile is where a fleet host puts tmux on PATH.)
	return strings.Join([]string{
		fmt.Sprintf("%s -t %s %s", localSSHInvocation(), shellQuote(loc.Host),
			shellQuote(loginShell(remoteAttachChain(task, view)))),
		"status=$?",
		`printf '\n\033[33m── %s ──\033[0m\n' "$(if [ "$status" = 0 ]; then echo 'the remote session ended'; else echo "disconnected from the remote session (exit $status)"; fi)"`,
		fmt.Sprintf("printf 'Reattach by hand: %%s\\n' %s", shellQuote(attach)),
		"printf 'This pane stays here so you can read this. Press Enter to close it.\\n'",
		// Hold the pane open so the explanation can be read. If stdin is not a
		// terminal (nothing to press Enter on), hold it anyway rather than vanish.
		"read _ || sleep 86400",
	}, "\n")
}

// remoteAttachChain is the shell line the PLACED HOST runs: check the window is
// there, build a disposable grouped view of it, fix the nested prefix, attach.
func remoteAttachChain(task *db.Task, view string) string {
	window := TmuxWindowName(task.ID)
	q := shellQuote
	return strings.Join([]string{
		// Nothing to attach to: say so, and let the wrapper print the manual
		// command. The exit status is the ordinary "no such session" 1.
		fmt.Sprintf("tmux has-session -t %s 2>/dev/null || { echo 'the tmux session for this task is no longer running there' >&2; exit 1; }",
			q(task.DaemonSession)),
		fmt.Sprintf("tmux list-windows -t %s -F '#{window_name}' | grep -qx %s || { echo 'the window for this task is no longer running there' >&2; exit 1; }",
			q(task.DaemonSession), q(window)),
		// Grouped view session. Recreating one that already exists is not an error
		// worth failing on — a previous pane may have left it behind.
		fmt.Sprintf("tmux new-session -d -s %s -t %s 2>/dev/null || true", q(view), q(task.DaemonSession)),
		fmt.Sprintf("tmux set-option -t %s status off >/dev/null", q(view)),
		fmt.Sprintf("tmux set-option -t %s mouse on >/dev/null", q(view)),
		// The nested-prefix fix, applied to the VIEW session only so the agent's
		// real session (and anyone attached to it over plain ssh) keeps C-b.
		fmt.Sprintf("tmux set-option -t %s prefix %s >/dev/null", q(view), RemoteInnerPrefix),
		fmt.Sprintf("tmux set-option -t %s prefix2 None >/dev/null", q(view)),
		fmt.Sprintf("tmux select-window -t %s >/dev/null", q(view+":"+window)),
		// Attach, and only then mark the view session disposable: chained with
		// tmux's ";" so destroy-unattached cannot fire before this client connects
		// and take the session with it.
		fmt.Sprintf("exec tmux attach-session -t %s ';' set-option destroy-unattached on", q(view)),
	}, "\n")
}

// localSSHInvocation is the ssh command line (binary and options, no host) the
// attach pane uses.
//
// It shares BatchMode and connection multiplexing with every other remote
// command — an interactive attach that stopped to ask for a passphrase inside a
// tmux pane would just look frozen — but not the runner's command-building,
// because this one needs a TTY and runs in the foreground of a pane rather than
// being collected by the daemon.
func localSSHInvocation() string {
	parts := []string{sshBinary, "-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", int(DefaultRemoteConnectTimeout.Seconds()))}
	parts = append(parts, sshMultiplexArgs()...)
	return strings.Join(parts, " ")
}

// RemoteAttachNotice is the badge shown beside an attached remote pane. It names
// the host, so nobody mistakes the pane for a local agent, and the branch, so it
// is clear which worktree over there is being watched.
//
// It is a badge and not a sentence on purpose: it shares one right-aligned
// header line with the status, project, type and PR badges, and prose there
// wraps and strands its own tail on a line of its own. The inner tmux prefix —
// the one thing about a nested session a user cannot guess — is documented
// where the keys actually go instead: on the remote pane's border title and in
// the tmux status bar (see attachRemotePane).
func RemoteAttachNotice(loc RemoteTaskLocation) string {
	if loc.Branch != "" {
		return loc.Host + " (" + loc.Branch + ")"
	}
	return loc.Host
}

// RemoteEndedMessage is what a local surface says when the host answered and the
// task's window is not there. It keeps the attach command visible: the window
// may be gone, but the worktree, the branch and the host are still where the
// work is.
func RemoteEndedMessage(loc RemoteTaskLocation) string {
	msg := fmt.Sprintf("The session on %s has ended", loc.Host)
	if loc.WorkDir != "" {
		msg += " (worktree " + loc.WorkDir + " is still there)"
	}
	msg += ". Nothing to attach to."
	if loc.Attach != "" {
		msg += " If it comes back: " + loc.Attach
	}
	return msg
}

// RemoteUnreachableMessage is what a local surface says when it could not reach
// the host at all — which is NOT the same as the task being over, and must not
// read as if it were.
func RemoteUnreachableMessage(loc RemoteTaskLocation) string {
	msg := fmt.Sprintf("Cannot reach %s right now, so this task's session cannot be shown. It may still be running there.", loc.Host)
	if loc.Attach != "" {
		msg += " Attach with: " + loc.Attach
	}
	return msg
}
