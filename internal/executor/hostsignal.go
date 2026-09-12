package executor

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// How a remotely placed agent says it is finished.
//
// It could not say anything at all before. A local agent calls taskyou_complete
// over MCP, but MCP is stdio and a remote agent's server would talk to its own
// host's database rather than to ty. So the remote path guessed: it watched the
// pane, and after two minutes of no repainting called the task finished.
//
// Guessing gets it wrong in both directions. An agent that pauses to think looks
// finished; an agent that finished in six seconds is parked two minutes later and
// labelled "needs input" when nothing was ever asked. At a handful of tasks that
// is an oddity, at hundreds it means a meaningful fraction of the board is wrong
// at any moment and there is no way to tell which part.
//
// So the agent is given a voice: a small script in its worktree that drops a file
// in a spool directory, which the host agent drains onto the connection ty is
// already holding open. No MCP, no port, no reverse tunnel — the signal travels
// out through the same stdout everything else does.

// hostEventKind is what an agent can say about itself.
type hostEventKind string

const (
	// eventDone means the work is finished.
	eventDone hostEventKind = "done"
	// eventNeedsInput means the agent is waiting on a human and will not proceed.
	eventNeedsInput hostEventKind = "needs-input"
	// eventFailed means the agent stopped because it could not continue.
	eventFailed hostEventKind = "failed"
)

// hostEvent is one signal from one task.
type hostEvent struct {
	TaskID int64
	RunID  string
	ID     string
	Kind   hostEventKind
	Detail string
	At     time.Time
}

// remoteSpoolDir is where signals wait on the host until the next drain. It is
// per-user rather than per-worktree so the host agent has one place to watch,
// and it is outside the worktree so a signal is not something the agent can
// accidentally commit.
const remoteSpoolDir = "$HOME/.ty-events/${WORKTREE_COORDINATOR_ID:?}"

// signalScriptPath is where the script is installed inside a task's worktree.
// Inside the worktree because that is the one directory the agent is certain to
// be standing in, so the instruction in its prompt can be a relative path.
const signalScriptPath = ".ty/signal"

// signalScript is what the agent runs.
//
// The write is staged and renamed rather than written in place: the host agent
// drains this directory on a timer, and a rename is the only way to guarantee it
// never reads half a line. Everything is POSIX sh for the same reason the host
// agent is — it must run on any box that can run an agent, with nothing
// installed.
// signalScript is the script with the spool path substituted. A token rather
// than a format verb: both of these scripts are full of printf '%s' of their
// own, and running them through Sprintf would eat those.
func signalScript() string {
	return strings.ReplaceAll(signalScriptTemplate, spoolToken, remoteSpoolDir)
}

// spoolToken marks where the spool path goes in a script.
const spoolToken = "@@SPOOL@@"

const signalScriptTemplate = `#!/bin/sh
# Written by ty. Tells the machine that placed this task what happened to it.
set -u
kind=${1:-done}
shift 2>/dev/null || true
id=${WORKTREE_TASK_ID:?}
run=${WORKTREE_RUN_ID:?}
case "$id:$run" in *[!0-9a-f:]*|:*) exit 1 ;; esac
spool=@@SPOOL@@
umask 077
mkdir -p "$spool" || exit 1
tmp=$(mktemp "$spool/.$id.$run.XXXXXXXX") || exit 1
name=${tmp##*/}; name=${name#.}
printf '%s %s %s %s\n' "$id" "$run" "$kind" "$(printf '%s' "$*" | base64 | tr -d '\n')" >"$tmp" || exit 1
mv "$tmp" "$spool/$name.evt" || exit 1
`

// signalInstructions is appended to a remotely placed agent's prompt.
//
// It has to be explicit that the usual tool is missing, or an agent that cannot
// find taskyou_complete concludes the harness is broken and improvises — editing
// the database directly, or parking itself with an apology.
func signalInstructions() string {
	return fmt.Sprintf(`

---
HOW TO FINISH THIS TASK (read this — it is different from usual)

You are running on a different machine from the one that scheduled you, so the
taskyou_* MCP tools are NOT available here. Do not look for them, and do not
edit any database. Instead, run one of these from your worktree when you stop:

    %s done "<one line saying what you did>"
    %s needs-input "<the question you need answered>"
    %s failed "<what stopped you>"

Run exactly one, as the last thing you do. Until you do, the machine that
scheduled you cannot tell whether you are thinking or finished.`,
		signalScriptPath, signalScriptPath, signalScriptPath)
}

// installSignalScript writes the signal script into a task's remote worktree.
func (e *Executor) installSignalScript(ctx context.Context, workDir string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	dir := workDir + "/.ty"
	script := shellQuote(dir + "/signal")
	cmd := command(ctx, "", "sh", "-c",
		fmt.Sprintf("mkdir -p %s && cat > %s && chmod +x %s", shellQuote(dir), script, script))
	cmd.Stdin = strings.NewReader(signalScript())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// parseHostEvent reads one "E <task-id> <kind> <base64 detail>" line.
func parseHostEvent(line string) (hostEvent, bool) {
	fields := strings.SplitN(strings.TrimPrefix(line, "E "), " ", 3)
	// Version 2 includes the run fence and spool entry ID.
	var runID, eventID string
	v2 := strings.SplitN(strings.TrimPrefix(line, "E "), " ", 5)
	if len(v2) == 5 {
		runID, eventID = v2[1], v2[2]
		if !validSignalID(runID) || !validSignalID(eventID) {
			return hostEvent{}, false
		}
		fields = []string{v2[0], v2[3], v2[4]}
	}
	if len(fields) < 2 {
		return hostEvent{}, false
	}
	id, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || id <= 0 {
		return hostEvent{}, false
	}
	ev := hostEvent{TaskID: id, RunID: runID, ID: eventID, Kind: hostEventKind(fields[1]), At: time.Now()}
	if len(fields) == 3 {
		if raw, derr := base64.StdEncoding.DecodeString(fields[2]); derr == nil {
			ev.Detail = strings.TrimSpace(string(raw))
		} else if runID != "" {
			return hostEvent{}, false
		}
	}
	switch ev.Kind {
	case eventDone, eventNeedsInput, eventFailed:
		return ev, true
	default:
		// An unknown kind is a newer ty talking to an older one, or a typo in a
		// hand-run signal. Neither should be turned into a task transition.
		return hostEvent{}, false
	}
}

// TakeEvent removes and returns the signal a task sent, if one is waiting.
//
// Taking rather than reading is deliberate: a signal is an event, not a state.
// Leaving it in place would re-deliver "done" to a task that had since been
// retried, finishing the new run the instant it started.
func (c *hostChannel) TakeEvent(taskID int64) (hostEvent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ev, ok := c.events[taskID]
	if ok {
		delete(c.events, taskID)
	}
	return ev, ok
}

// taskSignal returns the signal a placed task has sent, if any.
func (e *Executor) taskSignal(host string, taskID int64) (hostEvent, bool) {
	if host == "" {
		return hostEvent{}, false
	}
	e.hostChannelFor(host)
	ev, ok, err := e.db.RemoteSignal(taskID, host)
	if err != nil {
		e.logger.Warn("read remote signal", "error", err)
		return hostEvent{}, false
	}
	return hostEvent{TaskID: ev.TaskID, RunID: ev.RunID, ID: ev.ID, Kind: hostEventKind(ev.Kind), Detail: ev.Detail}, ok
}

func validSignalID(s string) bool {
	if s == "" || len(s) > 160 {
		return false
	}
	for _, c := range s {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-'
		if !valid {
			return false
		}
	}
	return !strings.Contains(s, "..")
}

func (c *hostChannel) persistSignal(ev hostEvent) {
	if c.database == nil || ev.ID == "" || ev.RunID == "" {
		return
	}
	err := c.database.SaveRemoteEvent(db.RemoteEvent{ID: ev.ID, TaskID: ev.TaskID, RunID: ev.RunID, Host: c.host, Kind: string(ev.Kind), Detail: ev.Detail})
	if err != nil {
		return
	} // No acknowledgement until the inbox is durable.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := "~/.ty-events/" + c.coordinator + "/" + ev.ID
	cmd := (RemoteRunner{Host: c.host}).Command(ctx, "", "sh", "-c", "rm -f -- "+shellQuoteRemotePath(path))
	_ = cmd.Run() // A failed acknowledgement is safely retried on redelivery.
}
