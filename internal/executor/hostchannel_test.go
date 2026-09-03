package executor

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// feed runs one stream through a channel and returns it once consumed.
func feed(t *testing.T, stream string) *hostChannel {
	t.Helper()
	c := &hostChannel{host: "far-host"}
	c.consume(strings.NewReader(stream), map[string]string{})
	return c
}

func TestHostChannelReadsASnapshot(t *testing.T) {
	c := feed(t, "S\nW d:task-1 aaa "+b64("hello")+"\nW d:task-2 bbb "+b64("world")+"\n.\n")

	win, live, known := c.Window("d:task-1")
	if !known || !live {
		t.Fatalf("task-1: live=%v known=%v, want both true", live, known)
	}
	if win.Sum != "aaa" || win.Content != "hello" {
		t.Errorf("task-1 = %+v, want sum aaa content hello", win)
	}
	if _, live, known := c.Window("d:task-3"); !known || live {
		t.Errorf("task-3: live=%v known=%v — a window absent from a complete snapshot is gone", live, known)
	}
}

// The pane text is sent only when it changes, so a window whose fingerprint is
// unchanged must keep the text the channel already has. Without this an idle
// agent's screen would read as empty every tick after the first.
func TestHostChannelCarriesUnchangedPaneText(t *testing.T) {
	c := &hostChannel{host: "far-host"}
	c.consume(strings.NewReader(
		"S\nW d:task-1 aaa "+b64("working")+"\n.\n"+
			"S\nW d:task-1 aaa\n.\n"+
			"S\nW d:task-1 aaa\n.\n"), map[string]string{})

	win, live, known := c.Window("d:task-1")
	if !known || !live {
		t.Fatal("task-1 should still be live")
	}
	if win.Content != "working" {
		t.Errorf("content = %q, want the carried-forward %q", win.Content, "working")
	}
}

// A tick that never terminated is not evidence about anything. Publishing it
// would say every window not yet parsed had vanished.
func TestHostChannelIgnoresAnIncompleteTick(t *testing.T) {
	c := &hostChannel{host: "far-host"}
	c.consume(strings.NewReader(
		"S\nW d:task-1 aaa "+b64("x")+"\nW d:task-2 bbb "+b64("y")+"\n.\n"+
			"S\nW d:task-1 ccc "+b64("z")+"\n"), map[string]string{}) // truncated: no "."

	if _, live, known := c.Window("d:task-2"); !known || !live {
		t.Errorf("task-2 live=%v known=%v — a truncated tick erased a live window", live, known)
	}
}

// The critical safety property: with no fresh snapshot the channel must say "I
// don't know", never "gone". Answering "gone" from a dead agent would park every
// task on the host.
func TestHostChannelWithoutAFreshSnapshotKnowsNothing(t *testing.T) {
	t.Run("never connected", func(t *testing.T) {
		c := &hostChannel{host: "far-host"}
		if _, _, known := c.Window("d:task-1"); known {
			t.Error("a channel that never connected claimed to know")
		}
	})

	t.Run("snapshot expired", func(t *testing.T) {
		c := feed(t, "S\nW d:task-1 aaa "+b64("x")+"\n.\n")
		c.mu.Lock()
		c.snap.At = time.Now().Add(-hostSnapshotTTL - time.Second)
		c.mu.Unlock()
		if _, _, known := c.Window("d:task-1"); known {
			t.Error("a stale snapshot was still trusted")
		}
	})
}

// channelProbe is what the poll loop actually calls; ok must be false in every
// case where the caller has to fall back to a direct round trip.
func TestChannelProbeFallsBackWhenItCannotSpeak(t *testing.T) {
	e := &Executor{}

	if _, _, ok := e.channelProbe("", "d:task-1"); ok {
		t.Error("a local task was answered from a host channel")
	}

	// A started-but-silent channel: registered, no snapshot yet.
	e.hostChans.mu.Lock()
	e.hostChans.byHost = map[string]*hostChannel{"far-host": {host: "far-host"}}
	e.hostChans.mu.Unlock()
	if _, _, ok := e.channelProbe("far-host", "d:task-1"); ok {
		t.Error("a channel with no snapshot answered instead of deferring to a probe")
	}
}

func TestChannelProbeReportsLiveAndGone(t *testing.T) {
	e := &Executor{}
	c := feed(t, "S\nW d:task-1 aaa "+b64("screen")+"\n.\n")
	e.hostChans.mu.Lock()
	e.hostChans.byHost = map[string]*hostChannel{"far-host": c}
	e.hostChans.mu.Unlock()

	probe, win, ok := e.channelProbe("far-host", "d:task-1")
	if !ok || probe != windowLive || win.Content != "screen" {
		t.Errorf("live window: probe=%v content=%q ok=%v", probe, win.Content, ok)
	}

	probe, _, ok = e.channelProbe("far-host", "d:task-9")
	if !ok || probe != windowGone {
		t.Errorf("absent window: probe=%v ok=%v, want windowGone/true", probe, ok)
	}
}

// The dial loop must survive an agent that exits, and must stop promptly when the
// channel is closed rather than leaving an ssh behind.
func TestHostChannelRedialsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &hostChannel{host: "far-host", stop: cancel, done: make(chan struct{})}

	dials := make(chan struct{}, 16)
	go c.run(ctx, func(ctx context.Context) (*exec.Cmd, io.Reader, error) {
		select {
		case dials <- struct{}{}:
		default:
		}
		// An agent that immediately ends its stream.
		return exec.Command("true"), strings.NewReader("S\n.\n"), nil
	})

	select {
	case <-dials:
	case <-time.After(2 * time.Second):
		t.Fatal("never dialled")
	}

	c.Close() // must return: run() observes ctx and closes done
	select {
	case <-c.done:
	default:
		t.Error("Close returned with the run loop still going")
	}
}

// A shell agent is only useful if it runs on a plain POSIX shell. This exercises
// the real script against the local shell with a stub tmux, so a bashism or a
// quoting slip fails here rather than silently on a fleet host.
func TestHostAgentScriptRunsUnderPlainSh(t *testing.T) {
	dir := t.TempDir()
	stub := dir + "/tmux"
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"+
		"case \"$1\" in\n"+
		"  list-windows) echo 'daemon:task-42'; echo 'daemon:_placeholder' ;;\n"+
		"  capture-pane) echo 'agent screen' ;;\n"+
		"esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", hostAgentProgram())
	cmd.Env = append(cmd.Environ(), "PATH="+dir+":"+pathEnv())
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	c := &hostChannel{host: "local"}
	go c.consume(out, map[string]string{})

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if win, live, known := c.Window("daemon:task-42"); known && live {
			if win.Content != "agent screen" {
				t.Errorf("pane content = %q, want %q", win.Content, "agent screen")
			}
			// A non-task window must not be reported at all.
			if _, live, _ := c.Window("daemon:_placeholder"); live {
				t.Error("the agent reported a window that is not a ty task")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the script produced no usable snapshot within the deadline")
}

func pathEnv() string { return os.Getenv("PATH") }
