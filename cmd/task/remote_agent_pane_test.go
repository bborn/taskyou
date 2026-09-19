package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/agentsend"
	"github.com/bborn/workflow/internal/db"
)

// fakeRemoteHost is a stand-in ssh + tmux on another machine.
//
// It does not just record what was asked of it: it keeps the pane's contents in
// a file, so a test can assert that the text `ty input` sent actually landed in
// the pane `ty output` reads — the thing a zero exit code does not prove.
type fakeRemoteHost struct {
	dir string
}

// installFakeSSH puts a fake ssh first on PATH. windowAlive says whether the
// task's tmux window still exists on the far side.
func installFakeSSH(t *testing.T, windowAlive bool) fakeRemoteHost {
	t.Helper()
	dir := t.TempDir()
	alive := "1"
	if !windowAlive {
		alive = ""
	}
	script := `#!/bin/sh
dir="` + dir + `"
alive="` + alive + `"
for arg; do last="$arg"; done
printf '%s\n' "$*" >> "$dir/commands"
# The remote command is the LAST argument, and RemoteRunner wraps it as
# sh -lc '<script>'. Unwrap it the way the remote login shell would: once to
# drop the outer quoting, once to split the tmux command into its arguments.
inner=${last#sh -lc }
eval "set -- $inner"
eval "set -- $1"
shift
sub="$1"
shift
case "$sub" in
  list-panes)
      case "$*" in *-shell*) exit 1 ;; esac
      [ -n "$alive" ] || exit 1
      echo '%91' ;;
  set-buffer)
      while [ $# -gt 0 ]; do
        if [ "$1" = "--" ]; then shift; printf '%s' "$1" > "$dir/buffer"; break; fi
        shift
      done ;;
  paste-buffer)
      cat "$dir/buffer" >> "$dir/pane" ;;
  send-keys)
      case "$*" in
        *Enter*) printf '\n[submitted]\n' >> "$dir/pane" ;;
        *) printf '[key]\n' >> "$dir/pane" ;;
      esac ;;
  capture-pane)
      [ -n "$alive" ] || exit 1
      cat "$dir/pane" 2>/dev/null ;;
  delete-buffer) : ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fakeRemoteHost{dir: dir}
}

func (h fakeRemoteHost) pane(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(h.dir, "pane"))
	if err != nil {
		return ""
	}
	return string(content)
}

func (h fakeRemoteHost) commands(t *testing.T) string {
	t.Helper()
	content, _ := os.ReadFile(filepath.Join(h.dir, "commands"))
	return string(content)
}

// placedTask makes a task the placement resolver put on host, with a daemon
// session recorded the way a real remote run records it.
func placedTask(t *testing.T, host string) (*db.DB, *db.Task) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	task := &db.Task{Title: "remote task", Status: db.StatusBlocked, Type: db.TypeCode}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if host != "" {
		if err := database.SetTaskPlacement(task.ID, host, "only host serving this project"); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.UpdateTaskDaemonSession(task.ID, "task-daemon-remote-abc123"); err != nil {
		t.Fatal(err)
	}
	fresh, err := database.GetTask(task.ID)
	if err != nil || fresh == nil {
		t.Fatalf("reload task: %v", err)
	}
	return database, fresh
}

// A task placed on another host has its agent pane over there. `ty input` used
// to look only at this machine's tmux and report "no live agent pane" about a
// pane that was alive and working.
func TestInputAndOutputReachARemotelyPlacedTask(t *testing.T) {
	host := installFakeSSH(t, true)
	database, task := placedTask(t, "ik-agents")

	var out bytes.Buffer
	if err := runTaskInput(context.Background(), database, task, inputOptions{message: "ping"}, &out); err != nil {
		t.Fatalf("input: %v", err)
	}
	if !strings.Contains(out.String(), "ik-agents") {
		t.Errorf("input said nothing about where it went: %q", out.String())
	}

	out.Reset()
	if err := runTaskOutput(context.Background(), task, 50, &out); err != nil {
		t.Fatalf("output: %v", err)
	}
	if !strings.Contains(out.String(), "ping") {
		t.Fatalf("output did not return the remote pane's contents: %q", out.String())
	}
	if pane := host.pane(t); !strings.Contains(pane, "ping") || !strings.Contains(pane, "[submitted]") {
		t.Fatalf("prompt did not land in the remote pane: %q", pane)
	}
	commands := host.commands(t)
	for _, want := range []string{"ik-agents", "%91", "capture-pane"} {
		if !strings.Contains(commands, want) {
			t.Errorf("remote commands missing %q: %s", want, commands)
		}
	}
}

// Multi-line text arrives as ONE message: the whole prompt is pasted, and only
// then is Enter pressed. Sending it line by line submits the first line and
// leaves the rest as a separate turn.
func TestRemoteInputDeliversMultiLineTextAsOneMessage(t *testing.T) {
	host := installFakeSSH(t, true)
	database, task := placedTask(t, "ik-agents")

	message := "first line\nsecond line\nthird line"
	if err := runTaskInput(context.Background(), database, task,
		inputOptions{message: message}, &bytes.Buffer{}); err != nil {
		t.Fatalf("input: %v", err)
	}

	pane := host.pane(t)
	if want := message + "\n[submitted]\n"; pane != want {
		t.Fatalf("pane contents = %q, want %q", pane, want)
	}
	// The text and its Enter are separate calls, in that order — combining them
	// submits the prompt half-typed.
	commands := host.commands(t)
	paste := strings.Index(commands, "paste-buffer")
	enter := strings.Index(commands, "send-keys")
	if paste < 0 || enter < 0 || enter < paste {
		t.Fatalf("expected a paste followed by a separate Enter: %s", commands)
	}
	if strings.Contains(commands[:enter], "Enter") {
		t.Fatalf("Enter was sent with the text rather than after it: %s", commands)
	}
}

// A remote agent that really has exited still fails — and says which machine and
// which tmux session ty looked in, so "the task is dead" reads differently from
// "ty looked on the wrong machine".
func TestRemoteInputAndOutputNameWhereTheyLookedWhenThePaneIsGone(t *testing.T) {
	installFakeSSH(t, false)
	database, task := placedTask(t, "ik-agents")

	inputErr := runTaskInput(context.Background(), database, task, inputOptions{message: "ping"}, &bytes.Buffer{})
	outputErr := runTaskOutput(context.Background(), task, 50, &bytes.Buffer{})
	for name, err := range map[string]error{"input": inputErr, "output": outputErr} {
		if err == nil {
			t.Fatalf("%s: expected a failure for a dead remote pane", name)
		}
		if !errors.Is(err, agentsend.ErrNoPane) {
			t.Errorf("%s error %q is not a no-pane error", name, err)
		}
		for _, want := range []string{"ik-agents", "task-daemon-remote-abc123"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s error %q does not name %q", name, err, want)
			}
		}
	}
}

// A local task with no pane fails the same way, naming this machine and the
// session it checked rather than the bare "not running?" that used to be the
// only clue.
func TestLocalInputAndOutputNameWhereTheyLookedWhenThePaneIsGone(t *testing.T) {
	database, task := placedTask(t, "")

	inputErr := runTaskInput(context.Background(), database, task, inputOptions{message: "ping"}, &bytes.Buffer{})
	outputErr := runTaskOutput(context.Background(), task, 50, &bytes.Buffer{})
	for name, err := range map[string]error{"input": inputErr, "output": outputErr} {
		if err == nil {
			t.Fatalf("%s: expected a failure when no pane carries the task's tag", name)
		}
		for _, want := range []string{"this machine", "task-daemon-remote-abc123"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s error %q does not name %q", name, err, want)
			}
		}
	}
}
