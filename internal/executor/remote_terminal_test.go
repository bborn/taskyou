package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestRemoteTerminalShellUsesPlacedHostAndReusesPane(t *testing.T) {
	dir := t.TempDir()
	logPath, marker := filepath.Join(dir, "commands"), filepath.Join(dir, "shell")
	stubSSH(t, `#!/bin/sh
for arg do last="$arg"; done
printf '%s\n' "$last" >> `+shellQuote(logPath)+`
case "$last" in
  *new-window*) touch `+shellQuote(marker)+`; echo %92 ;;
  *list-panes*task-42-shell*) test -f `+shellQuote(marker)+` || exit 1; echo %92 ;;
  *list-panes*) echo %91 ;;
  *) exit 1 ;;
esac
`)
	task := &db.Task{ID: 42, PlacementTarget: "placed-host", DaemonSession: "task-daemon-7", Port: 3142, ClaudePaneID: "%1", ShellPaneID: "%2"}
	for i := 0; i < 2; i++ {
		info, err := InspectRemoteTerminal(context.Background(), task, "~/work/my task", true)
		if err != nil {
			t.Fatal(err)
		}
		if info.AgentPaneID != "%91" || info.ShellPaneID != "%92" {
			t.Fatalf("wrong remote panes: %+v", info)
		}
	}
	if task.ClaudePaneID != "%1" || task.ShellPaneID != "%2" {
		t.Fatal("remote pane IDs overwrote local pane IDs")
	}
	commands, _ := os.ReadFile(logPath)
	if strings.Count(string(commands), "new-window") != 1 {
		t.Fatalf("shell was not reused: %s", commands)
	}
	for _, want := range []string{"WORKTREE_TASK_ID=42", "WORKTREE_PORT=3142", "WORKTREE_PATH", "SHELL:-/bin/sh", "my task"} {
		if !strings.Contains(string(commands), want) {
			t.Errorf("missing shell context %s: %s", want, commands)
		}
	}
}

func TestRemoteTerminalFailureDoesNotCreateShell(t *testing.T) {
	stubSSH(t, "#!/bin/sh\nexit 255\n")
	task := &db.Task{ID: 42, PlacementTarget: "unreachable", DaemonSession: "task-daemon-7"}
	if _, err := InspectRemoteTerminal(context.Background(), task, "/remote", true); err == nil {
		t.Fatal("unreachable host was reported as a live terminal")
	}
}

func TestRemoteTerminalDistinguishesEndedFromUnreachable(t *testing.T) {
	for _, code := range []int{1, 255} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			exitingSSH(t, code)
			task := &db.Task{ID: 42, PlacementTarget: "remote", DaemonSession: "task-daemon-7"}
			_, err := InspectRemoteTerminal(context.Background(), task, "", false)
			if errors.Is(err, ErrRemoteTerminalEnded) != (code == 1) {
				t.Fatalf("exit %d classified as %v", code, err)
			}
		})
	}
}
