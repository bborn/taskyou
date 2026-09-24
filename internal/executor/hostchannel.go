package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
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
	host        string
	database    *db.DB
	coordinator string
	lastRead    time.Time

	mu   sync.RWMutex
	snap hostSnapshot
	// events holds the latest signal each task sent, until a poller takes it.
	// Keyed by task, not queued, because only the most recent one can be true:
	// an agent that said "needs-input" and then "done" is done.
	events map[int64]hostEvent

	// relay answers the MCP requests placed tasks send up this connection (see
	// mcpproxy.go). nil answers every one with an error.
	relay relayFunc
	// wch queues what ty sends DOWN the connection — pings, and relay acks and
	// answers — for the current connection's writer. nil between connections.
	// Sends never block: a wedged connection drops them, and the host's stubs
	// then see a stale ping and fail fast, which is the point.
	wch   chan []byte
	wdone chan struct{}

	stop context.CancelFunc
	done chan struct{}
}

// relayFunc answers one relayed MCP request from host.
type relayFunc func(host, name string, payload []byte) []byte

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
# The MCP relay (mcpproxy.go). Stubs spool requests in $relay; this agent
# carries them up as M lines, and what ty sends DOWN arrives on stdin: P (still
# here), A (received), R (the answer). A background job would read /dev/null,
# so the reader is handed the channel explicitly — and not stdout, which it never
# writes and must not hold open once this agent is gone.
relay="$spool/mcp"
mkdir -p "$relay" 2>/dev/null || true
named() { case "$1" in ''|.*|*[!A-Za-z0-9._-]*) return 1 ;; esac; }
stamp() { printf '%s\n' "$1" >"$relay/.alive.$$" && mv -f "$relay/.alive.$$" "$relay/alive"; }
exec 3<&0
(
  last=
  while IFS=' ' read -r op name body; do
    case "$op" in
      P) now=$(date +%s); printf '%s\n' "$now" >"$state/alive"; stamp "$now"; last=$now ;;
      A) named "$name" && : >"$relay/$name.ack" ;;
      R) named "$name" && printf '%s' "$body" | base64 -d >"$relay/.$name.part" 2>/dev/null &&
           mv -f "$relay/.$name.part" "$relay/$name.resp" ;;
    esac
  done
  # ty hung up: say so now rather than letting stubs learn it from a stale
  # stamp — unless a newer agent has stamped since. A connection that died
  # silently (the Mac asleep) can reach EOF hours after its replacement is up.
  [ "$(cat "$relay/alive" 2>/dev/null)" != "$last" ] || stamp 0
) <&3 >/dev/null &
exec 3<&-
fresh() {
  a=$(cat "$state/alive" 2>/dev/null) || return 1
  case "$a" in ''|*[!0-9]*) return 1 ;; esac
  [ $(( $(date +%s) - a )) -le @@STALE@@ ]
}
# A request is claimed by renaming it, so of two agents (a redial overlapping a
# dying one) exactly one sends it — and only one that has heard from ty lately,
# so a request is not handed to a connection nobody is reading.
forward() {
  for f in "$relay"/*.req; do
    [ -f "$f" ] || continue
    fresh || return 0
    r=${f##*/}; r=${r%.req}
    named "$r" || { rm -f "$f"; continue; }
    mv "$f" "$relay/$r.sent" 2>/dev/null || continue
    printf 'M %s %s\n' "$r" "$(base64 <"$relay/$r.sent" | tr -d '\n')"
    rm -f "$relay/$r.sent"
  done
}
if sleep 0.2 2>/dev/null; then slice=0.2 per=5; else slice=1 per=1; fi
sweep=0
while :; do
  printf 'S\n'
  if tmux list-windows -a -F '#{session_name}:#{window_name}' >"$state/windows" 2>/dev/null; then
  while IFS= read -r w; do
    case "$w" in *:task-[0-9]*) ;; *) continue ;; esac
    c=$(tmux capture-pane -p -t "$w" 2>/dev/null) || { printf 'U %s\n' "$w"; continue; }
    h=$(printf '%s' "$c" | sum)
    f="$state/$(printf '%s' "$w" | tr -c 'A-Za-z0-9' '_')"
    if [ "$h" = "$(cat "$f" 2>/dev/null)" ]; then
      printf 'W %s %s\n' "$w" "$h"
    else
      printf '%s' "$h" >"$f"
      printf 'W %s %s %s\n' "$w" "$h" "$(printf '%s' "$c" | base64 | tr -d '\n')"
    fi
  done <"$state/windows"
  printf '.\n'
  else
    printf '!\n'
  fi
  for f in "$spool"/*.evt; do
    [ -f "$f" ] || continue
    name=${f##*/}
    while IFS=' ' read -r id run kind detail || [ -n "$kind" ]; do
      [ -n "$kind" ] && printf 'E %s %s %s %s %s\n' "$id" "$run" "$name" "$kind" "$detail"
    done <"$f"
  done
  printf 'H\n'
  # Answers nobody collected (a stub that gave up, or died) are swept eventually.
  sweep=$((sweep + 1))
  if [ "$sweep" -ge 60 ]; then
    sweep=0
    find "$relay" -type f -mmin +20 ! -name alive -exec rm -f {} + 2>/dev/null
  fi
  i=0
  while [ "$i" -lt $((tick * per)) ]; do
    forward
    sleep "$slice"
    i=$((i + 1))
  done
done
`

// hostAgentProgram is the script with ty's tick baked in, so the interval has one
// definition on this side rather than a default hidden in the shell.
func hostAgentProgram(coordinators ...string) string {
	coordinator := "legacy"
	if len(coordinators) > 0 {
		coordinator = coordinators[0]
	}
	script := strings.ReplaceAll(hostAgentScript, spoolToken, remoteSpoolDir)
	script = strings.ReplaceAll(script, "@@STALE@@", strconv.Itoa(int(mcpRelayStale.Seconds())))
	return "WORKTREE_COORDINATOR_ID=" + shellQuote(coordinator) + "\n" + fmt.Sprintf("TY_HOST_TICK=%d\n", int(hostAgentTick.Seconds())) + script
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
		c.mu.Lock()
		c.lastRead = time.Now()
		c.mu.Unlock()
		switch {
		case line == "!":
			c.mu.Lock()
			c.snap = hostSnapshot{}
			c.mu.Unlock()
			if c.database != nil {
				_ = c.database.RecordHostHealth(c.host, "Could not enumerate remote windows", false)
			}
		case strings.HasPrefix(line, "U "):
			pending[strings.TrimPrefix(line, "U ")] = hostWindow{}
		case line == "S":
			pending = map[string]hostWindow{}
		case line == ".":
			c.mu.Lock()
			c.snap = hostSnapshot{At: time.Now(), Windows: pending}
			c.mu.Unlock()
			if c.database != nil {
				_ = c.database.RecordHostHealth(c.host, "", true)
			}
			pending = map[string]hostWindow{}
		case strings.HasPrefix(line, "W "):
			target, win := parseHostWindow(line, carry)
			if target != "" {
				pending[target] = win
			}
		case strings.HasPrefix(line, "M "):
			c.relayRequest(line)
		case strings.HasPrefix(line, "E "):
			// Signals are published as they arrive rather than held for the end of
			// the tick. A window snapshot is only meaningful complete; a signal is
			// meaningful on its own, and holding it back would add a tick of delay
			// to the one thing this exists to make immediate.
			if ev, ok := parseHostEvent(line); ok {
				if c.database != nil {
					c.persistSignal(ev)
					continue
				}
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
	delay := hostChannelRetryDelay
	for {
		if ctx.Err() != nil {
			return
		}
		connectionCtx, cancel := context.WithCancel(ctx)
		c.mu.Lock()
		c.lastRead = time.Now()
		c.mu.Unlock()
		cmd, out, err := dial(connectionCtx)
		if err == nil {
			stopped := make(chan struct{})
			go func() {
				// The ping is what a stub on the host reads as "ty is there": it
				// goes first, then every mcpRelayPingInterval, so a relayed call
				// made as soon as the channel is up does not read as offline.
				c.send([]byte("P\n"))
				ticker := time.NewTicker(mcpRelayPingInterval)
				defer ticker.Stop()
				for {
					select {
					case <-stopped:
						return
					case <-connectionCtx.Done():
						return
					case <-ticker.C:
						c.send([]byte("P\n"))
						c.mu.RLock()
						stale := time.Since(c.lastRead) > hostSnapshotTTL
						c.mu.RUnlock()
						if stale {
							cancel()
							_ = cmd.Process.Kill()
							return
						}
					}
				}
			}()
			c.consume(out, map[string]string{})
			close(stopped)
			_ = cmd.Wait()
		}
		c.detach()
		cancel()
		c.mu.Lock()
		healthy := !c.snap.At.IsZero() && time.Since(c.snap.At) < hostSnapshotTTL
		c.snap = hostSnapshot{}
		c.mu.Unlock()
		if healthy {
			delay = hostChannelRetryDelay
		}
		if c.database != nil {
			if ctx.Err() != nil {
				// Stopped on purpose. Nothing will redial, and the row outlives
				// this process, so it must not claim a reconnect is under way.
				_ = c.database.RecordHostHealth(c.host, "", false)
			} else {
				_ = c.database.RecordHostHealth(c.host, "Connection interrupted; reconnecting", false)
			}
		}
		// The agent exited: the host rebooted, the link dropped, tmux went away.
		// Snapshots go stale on their own (hostSnapshotTTL), so callers fall back
		// to direct probes in the gap rather than believing a frozen picture.
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
			if delay < 30*time.Second {
				delay *= 2
				if delay > 30*time.Second {
					delay = 30 * time.Second
				}
			}
		}
	}
}

// startHostChannel dials a host and starts consuming its agent's stream.
func startHostChannel(host string, relay relayFunc, databases ...*db.DB) *hostChannel {
	ctx, cancel := context.WithCancel(context.Background())
	c := &hostChannel{host: host, stop: cancel, done: make(chan struct{}), coordinator: "legacy", relay: relay}
	if len(databases) > 0 && databases[0] != nil {
		c.database = databases[0]
		id, err := c.database.CoordinatorID()
		if err != nil {
			cancel()
			close(c.done)
			return c
		}
		c.coordinator = id
	}
	go c.run(ctx, func(ctx context.Context) (*exec.Cmd, io.Reader, error) {
		r := RemoteRunner{Host: host}
		cmd := r.Command(ctx, "", "sh", "-c", hostAgentProgram(c.coordinator))
		cmd.WaitDelay = 2 * time.Second
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, err
		}
		in, err := cmd.StdinPipe()
		if err != nil {
			return nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		c.attach(in)
		return cmd, out, nil
	})
	return c
}

// attach gives the channel a writer for the connection just dialled. Writes go
// through one goroutine so a connection that stops draining can never block
// the reader, the ping ticker or a relay answer.
func (c *hostChannel) attach(w io.WriteCloser) {
	ch := make(chan []byte, 256)
	done := make(chan struct{})
	c.mu.Lock()
	if c.wdone != nil {
		close(c.wdone)
	}
	c.wch, c.wdone = ch, done
	c.mu.Unlock()
	go func() {
		defer w.Close()
		for {
			select {
			case <-done:
				return
			case b := <-ch:
				if _, err := w.Write(b); err != nil {
					<-done
					return
				}
			}
		}
	}()
}

// detach drops the writer when its connection has ended.
func (c *hostChannel) detach() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.wdone != nil {
		close(c.wdone)
	}
	c.wch, c.wdone = nil, nil
}

// send queues a line for the host, dropping it when there is no connection or
// the connection is not keeping up. It reports whether the line was queued.
func (c *hostChannel) send(line []byte) bool {
	c.mu.RLock()
	ch := c.wch
	c.mu.RUnlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- line:
		return true
	default:
		return false
	}
}

// relayRequest acknowledges an "M <name> <base64 request>" line at once, then
// answers it when the relay has. The acknowledgement is what lets the stub tell
// "this machine is away" (the request is withdrawn, nothing happened) from
// "the answer was lost" (it may have taken effect).
func (c *hostChannel) relayRequest(line string) {
	fields := strings.SplitN(strings.TrimPrefix(line, "M "), " ", 2)
	if len(fields) != 2 || !validRelayName(fields[0]) {
		return
	}
	name := fields[0]
	payload, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return
	}
	c.send([]byte("A " + name + "\n"))
	go func() {
		var out []byte
		if c.relay != nil {
			out = c.relay(c.host, name, payload)
		}
		if len(out) == 0 {
			out = rpcErrorLine(rpcIDOf(payload), -32000, "unavailable: this machine relays no MCP servers")
		}
		out = append(bytes.TrimRight(out, "\n"), '\n')
		c.send([]byte("R " + name + " " + base64.StdEncoding.EncodeToString(out) + "\n"))
	}()
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
func (h *hostChannels) get(host string, relay relayFunc, databases ...*db.DB) *hostChannel {
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
	c := startHostChannel(host, relay, databases...)
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
	return e.hostChans.get(host, e.mcpRelayFor().handle, e.db)
}
