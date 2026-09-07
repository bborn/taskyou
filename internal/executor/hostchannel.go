package executor

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// One long-lived connection per HOST, replacing one poller per TASK.
//
// Polling used to cost two ssh round trips per remote task per tick: one to ask
// whether its tmux window still existed, one to capture its pane for the idle
// check. That is fine for five tasks and ruinous for five hundred — 500 tasks is
// a thousand ssh process spawns every fifteen seconds, each forking a tmux on the
// far side to answer one question about one window.
//
// A host knows the answer for every task on it at once. So ty asks once: a small
// POSIX shell agent runs there, walks every ty window each tick, and streams back
// one snapshot covering all of them. Cost becomes O(hosts) — three connections,
// not five hundred — and stops growing with the fleet.
//
// The direction is the other reason this shape was chosen. ty holds the
// connection OUTBOUND, exactly as it already does for every other remote command.
// Nothing listens on the user's machine, no port is opened, no reverse tunnel is
// established, and no host is given a way to reach back on its own initiative.
// The agent speaks only by writing to the stdout of a process ty started.

const (
	// hostAgentTick is how often the remote agent walks its windows. It is faster
	// than the old per-task interval because it now costs one local process on the
	// host rather than a round trip per task.
	hostAgentTick = 5 * time.Second

	// hostSnapshotTTL is how long a snapshot is trusted. Past this the channel
	// reports "I don't know" and callers fall back to probing directly, so a
	// wedged or dead agent degrades to the old behaviour instead of freezing every
	// task's view of itself.
	hostSnapshotTTL = 30 * time.Second

	// hostChannelRetryDelay backs off before redialling a host whose agent exited.
	hostChannelRetryDelay = 10 * time.Second
)

// hostWindow is what one tick learned about one task's window.
type hostWindow struct {
	// Sum fingerprints the pane, for the idle check.
	Sum string
	// Content is the pane text. The agent sends it only when the fingerprint
	// changed, so a screen that is not repainting costs nothing on the wire; the
	// channel carries the previous text forward.
	Content string
}

// hostSnapshot is one complete tick: every ty window the host could see.
//
// Completeness is what makes absence meaningful. A window missing from a snapshot
// that the agent successfully produced really is gone, which is exactly the
// question the miss tracker asks.
type hostSnapshot struct {
	At      time.Time
	Windows map[string]hostWindow
}

// hostChannel is the single connection to one host.
type hostChannel struct {
	host string

	mu   sync.RWMutex
	snap hostSnapshot
	// events holds the latest signal each task sent, until a poller takes it.
	// Keyed by task, not queued, because only the most recent one can be true:
	// an agent that said "needs-input" and then "done" is done.
	events map[int64]hostEvent

	stop context.CancelFunc
	done chan struct{}
}

// hostAgentScript is the POSIX shell the host runs.
//
// Shell, not a ty binary: a shell script needs no install, no cross-compilation
// for the host's architecture, and no version agreement between the two ends. It
// needs tmux, which any host that can run an agent already has. (ty is present on
// some fleet hosts and absent on others; requiring it would make placement depend
// on which boxes happen to have been updated.)
//
// The pane text is sent only when its fingerprint changes. An agent sitting idle
// at its prompt — the common case, and the one that used to cost the most — then
// costs one short line per tick instead of a screenful.
//
// How it dies matters as much as what it does, because nothing on a fleet host
// reaps a stray loop. Closing the channel kills the local ssh, which closes this
// shell's stdout; the next tick's printf then takes SIGPIPE and the agent exits
// on its own, within one tick and without ty having to reach over and clean up.
// That death is abrupt enough that the EXIT trap does not run, so each agent also
// sweeps the leftovers of its dead predecessors on the way in — which bounds the
// mess at one stale directory per host rather than one per redial, forever.
const hostAgentScript = `
set -u
tick=${TY_HOST_TICK:-5}
spool=@@SPOOL@@
mkdir -p "$spool" 2>/dev/null || true
state=${TMPDIR:-/tmp}/ty-hostagent-$$
mkdir -p "$state" 2>/dev/null || exit 1
trap 'rm -rf "$state"' EXIT INT TERM
# Sweep the state of previous agents whose process is gone. The trap above only
# runs when this shell exits cleanly, and it usually does not: ty closing the
# channel kills the ssh, which drops the remote shell without giving it a chance.
# Redialling every few seconds then leaves one directory per attempt, forever.
for d in ${TMPDIR:-/tmp}/ty-hostagent-*; do
  [ -d "$d" ] || continue
  p=${d##*-}
  [ "$d" = "$state" ] || kill -0 "$p" 2>/dev/null || rm -rf "$d"
done
sum() { if command -v sha256sum >/dev/null 2>&1; then sha256sum | cut -c1-16; else cksum | cut -d" " -f1; fi; }
while :; do
  printf 'S\n'
  tmux list-windows -a -F '#{session_name}:#{window_name}' 2>/dev/null | while IFS= read -r w; do
    case "$w" in *:task-[0-9]*) ;; *) continue ;; esac
    c=$(tmux capture-pane -p -t "$w" 2>/dev/null) || continue
    h=$(printf '%s' "$c" | sum)
    f="$state/$(printf '%s' "$w" | tr -c 'A-Za-z0-9' '_')"
    if [ "$h" = "$(cat "$f" 2>/dev/null)" ]; then
      printf 'W %s %s\n' "$w" "$h"
    else
      printf '%s' "$h" >"$f"
      printf 'W %s %s %s\n' "$w" "$h" "$(printf '%s' "$c" | base64 | tr -d '\n')"
    fi
  done
  for f in "$spool"/*.evt; do
    [ -f "$f" ] || continue
    id=${f##*/}; id=${id%%.*}
    while IFS=' ' read -r kind detail || [ -n "$kind" ]; do
      [ -n "$kind" ] && printf 'E %s %s %s\n' "$id" "$kind" "$detail"
    done <"$f"
    rm -f "$f"
  done
  printf '.\n'
  sleep "$tick"
done
`

// hostAgentProgram is the script with ty's tick baked in, so the interval has one
// definition on this side rather than a default hidden in the shell.
func hostAgentProgram() string {
	return fmt.Sprintf("TY_HOST_TICK=%d\n", int(hostAgentTick.Seconds())) +
		strings.ReplaceAll(hostAgentScript, spoolToken, remoteSpoolDir)
}

// Window reports what the host last said about a target ("session:window").
//
// known is false when there is no fresh snapshot — no agent yet, a dead one, or
// one whose last word is older than hostSnapshotTTL. Callers must treat that as
// "ask the host directly", never as "the window is gone": concluding absence from
// a channel that simply is not running would park every task on the host.
func (c *hostChannel) Window(target string) (w hostWindow, live bool, known bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.snap.At.IsZero() || time.Since(c.snap.At) > hostSnapshotTTL {
		return hostWindow{}, false, false
	}
	win, ok := c.snap.Windows[target]
	return win, ok, true
}

// consume reads the agent's stream, publishing a snapshot per complete tick.
//
// A snapshot is published only on the terminating "." so a half-read tick can
// never be mistaken for a complete one — which would read as "every window not
// yet parsed is gone".
func (c *hostChannel) consume(r io.Reader, carry map[string]string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	pending := map[string]hostWindow{}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "S":
			pending = map[string]hostWindow{}
		case line == ".":
			c.mu.Lock()
			c.snap = hostSnapshot{At: time.Now(), Windows: pending}
			c.mu.Unlock()
			pending = map[string]hostWindow{}
		case strings.HasPrefix(line, "W "):
			target, win := parseHostWindow(line, carry)
			if target != "" {
				pending[target] = win
			}
		case strings.HasPrefix(line, "E "):
			// Signals are published as they arrive rather than held for the end of
			// the tick. A window snapshot is only meaningful complete; a signal is
			// meaningful on its own, and holding it back would add a tick of delay
			// to the one thing this exists to make immediate.
			if ev, ok := parseHostEvent(line); ok {
				c.mu.Lock()
				if c.events == nil {
					c.events = map[int64]hostEvent{}
				}
				c.events[ev.TaskID] = ev
				c.mu.Unlock()
			}
		}
	}
}

// parseHostWindow reads one "W <target> <sum> [<base64 pane>]" line, using carry
// to supply the pane text for a window whose fingerprint has not changed.
func parseHostWindow(line string, carry map[string]string) (string, hostWindow) {
	fields := strings.SplitN(strings.TrimPrefix(line, "W "), " ", 3)
	if len(fields) < 2 {
		return "", hostWindow{}
	}
	target, sum := fields[0], fields[1]
	if len(fields) == 3 {
		if raw, err := base64.StdEncoding.DecodeString(fields[2]); err == nil {
			carry[target] = string(raw)
		}
	}
	return target, hostWindow{Sum: sum, Content: carry[target]}
}

// run keeps the agent alive on the host, redialling until the channel is stopped.
func (c *hostChannel) run(ctx context.Context, dial func(context.Context) (*exec.Cmd, io.Reader, error)) {
	defer close(c.done)
	for {
		if ctx.Err() != nil {
			return
		}
		cmd, out, err := dial(ctx)
		if err == nil {
			c.consume(out, map[string]string{})
			_ = cmd.Wait()
		}
		// The agent exited: the host rebooted, the link dropped, tmux went away.
		// Snapshots go stale on their own (hostSnapshotTTL), so callers fall back
		// to direct probes in the gap rather than believing a frozen picture.
		select {
		case <-ctx.Done():
			return
		case <-time.After(hostChannelRetryDelay):
		}
	}
}

// startHostChannel dials a host and starts consuming its agent's stream.
func startHostChannel(host string) *hostChannel {
	ctx, cancel := context.WithCancel(context.Background())
	c := &hostChannel{host: host, stop: cancel, done: make(chan struct{})}
	go c.run(ctx, func(ctx context.Context) (*exec.Cmd, io.Reader, error) {
		r := RemoteRunner{Host: host}
		cmd := r.Command(ctx, "", "sh", "-c", hostAgentProgram())
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		return cmd, out, nil
	})
	return c
}

// Close stops the channel and waits for its goroutine to finish, so a shutting
// down daemon leaves no ssh behind.
func (c *hostChannel) Close() {
	c.stop()
	<-c.done
}

// hostChannels is the per-Executor registry, one channel per host.
type hostChannels struct {
	mu     sync.Mutex
	byHost map[string]*hostChannel
	closed bool
}

// get returns the channel for a host, starting one on first use. It returns nil
// for an empty host (a local task) and after Close.
func (h *hostChannels) get(host string) *hostChannel {
	if strings.TrimSpace(host) == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	if h.byHost == nil {
		h.byHost = map[string]*hostChannel{}
	}
	if c, ok := h.byHost[host]; ok {
		return c
	}
	c := startHostChannel(host)
	h.byHost[host] = c
	return c
}

// Close stops every channel.
func (h *hostChannels) Close() {
	h.mu.Lock()
	h.closed = true
	channels := make([]*hostChannel, 0, len(h.byHost))
	for _, c := range h.byHost {
		channels = append(channels, c)
	}
	h.byHost = nil
	h.mu.Unlock()

	for _, c := range channels {
		c.Close()
	}
}

// hostChannelFor returns the executor's channel to a placed host.
func (e *Executor) hostChannelFor(host string) *hostChannel {
	return e.hostChans.get(host)
}
