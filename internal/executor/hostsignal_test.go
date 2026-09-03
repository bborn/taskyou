package executor

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseHostEvent(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		want  hostEvent
		valid bool
	}{
		{"done with detail", "E 42 done " + base64.StdEncoding.EncodeToString([]byte("shipped it")),
			hostEvent{TaskID: 42, Kind: eventDone, Detail: "shipped it"}, true},
		{"needs input", "E 7 needs-input " + base64.StdEncoding.EncodeToString([]byte("which branch?")),
			hostEvent{TaskID: 7, Kind: eventNeedsInput, Detail: "which branch?"}, true},
		{"failed", "E 9 failed " + base64.StdEncoding.EncodeToString([]byte("no disk")),
			hostEvent{TaskID: 9, Kind: eventFailed, Detail: "no disk"}, true},
		{"no detail", "E 3 done", hostEvent{TaskID: 3, Kind: eventDone}, true},

		// An unknown kind must not become a transition: it is a newer ty talking to
		// an older one, or a typo in a hand-run signal.
		{"unknown kind", "E 3 exploded " + base64.StdEncoding.EncodeToString([]byte("x")), hostEvent{}, false},
		{"no task id", "E done", hostEvent{}, false},
		{"non-numeric id", "E abc done", hostEvent{}, false},
		{"zero id", "E 0 done", hostEvent{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseHostEvent(tc.line)
			if ok != tc.valid {
				t.Fatalf("valid = %v, want %v", ok, tc.valid)
			}
			if !ok {
				return
			}
			if got.TaskID != tc.want.TaskID || got.Kind != tc.want.Kind || got.Detail != tc.want.Detail {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A signal is an event, not a state. Leaving it in place would re-deliver "done"
// to a task that had since been retried, finishing the new run as it started.
func TestTakeEventConsumesTheSignal(t *testing.T) {
	c := &hostChannel{host: "far-host"}
	c.consume(strings.NewReader("S\nE 42 done "+base64.StdEncoding.EncodeToString([]byte("ok"))+"\n.\n"),
		map[string]string{})

	ev, ok := c.TakeEvent(42)
	if !ok || ev.Kind != eventDone || ev.Detail != "ok" {
		t.Fatalf("first take = %+v ok=%v", ev, ok)
	}
	if _, ok := c.TakeEvent(42); ok {
		t.Error("the signal was delivered twice; a retry would finish instantly")
	}
}

// Only the most recent signal can be true: an agent that asked a question and
// then finished is finished.
func TestLatestSignalWins(t *testing.T) {
	c := &hostChannel{host: "far-host"}
	c.consume(strings.NewReader(
		"S\nE 42 needs-input "+base64.StdEncoding.EncodeToString([]byte("q"))+"\n.\n"+
			"S\nE 42 done "+base64.StdEncoding.EncodeToString([]byte("finished"))+"\n.\n"), map[string]string{})

	ev, ok := c.TakeEvent(42)
	if !ok || ev.Kind != eventDone {
		t.Errorf("got %+v, want the later done", ev)
	}
}

func TestApplyHostSignal(t *testing.T) {
	e, _ := reconcileTestExecutor(t)
	cases := []struct {
		kind    hostEventKind
		success bool
		needs   bool
	}{
		{eventDone, true, false},
		{eventNeedsInput, false, true},
		{eventFailed, false, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			got := e.applyHostSignal(1, hostEvent{TaskID: 1, Kind: tc.kind, Detail: "because"})
			if got.Success != tc.success || got.NeedsInput != tc.needs {
				t.Errorf("%s -> Success=%v NeedsInput=%v, want %v/%v",
					tc.kind, got.Success, got.NeedsInput, tc.success, tc.needs)
			}
			if got.Message != "because" {
				t.Errorf("detail lost: %q", got.Message)
			}
		})
	}
}

// End to end through real shells: the signal script writes a spool file, and the
// host agent drains it onto the wire in the form the parser expects. This is the
// join that unit tests on either side cannot check.
func TestSignalScriptAndHostAgentRoundTrip(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()

	// A tmux stub with no windows, so the agent's only output is the drained event.
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Install the signal script exactly as installSignalScript would.
	work := filepath.Join(home, "worktree", ".ty")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	sig := filepath.Join(work, "signal")
	if err := os.WriteFile(sig, []byte(signalScript()), 0o755); err != nil {
		t.Fatal(err)
	}

	// The agent signals.
	run := exec.Command(sig, "done", "built the thing")
	run.Env = append(os.Environ(), "HOME="+home, "WORKTREE_TASK_ID=4242")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("signal script failed: %v (%s)", err, out)
	}

	// The host agent drains it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agent := exec.CommandContext(ctx, "sh", "-c", hostAgentProgram())
	agent.Env = append(os.Environ(), "HOME="+home, "PATH="+bin+":"+os.Getenv("PATH"))
	out, err := agent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = agent.Process.Kill(); _ = agent.Wait() }()

	c := &hostChannel{host: "local"}
	go c.consume(out, map[string]string{})

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if ev, ok := c.TakeEvent(4242); ok {
			if ev.Kind != eventDone {
				t.Errorf("kind = %q, want done", ev.Kind)
			}
			if ev.Detail != "built the thing" {
				t.Errorf("detail = %q, want %q", ev.Detail, "built the thing")
			}
			// The spool entry must be consumed, or it replays every tick.
			if files, _ := filepath.Glob(filepath.Join(home, ".ty-events", "*.evt")); len(files) != 0 {
				t.Errorf("spool still holds %v", files)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the signal never arrived over the host agent")
}

// The prompt has to say the usual tool is missing, or an agent that cannot find
// taskyou_complete improvises — editing the database, or parking with an apology.
func TestSignalInstructionsNameTheScriptAndTheMissingTools(t *testing.T) {
	got := signalInstructions()
	for _, want := range []string{signalScriptPath, "done", "needs-input", "failed", "taskyou_"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions do not mention %q:\n%s", want, got)
		}
	}
}
