package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/db"
)

// RemoteTerminal identifies panes on the placed host. These IDs must never be
// stored in the task's local pane fields or passed to a local tmux server.
type RemoteTerminal struct {
	WindowTarget string
	AgentPaneID  string
	ShellPaneID  string
}

// ErrRemoteTerminalEnded distinguishes an absent agent from an unreachable host.
var ErrRemoteTerminalEnded = errors.New("no remote executor session running for this task")

// InspectRemoteTerminal discovers the live agent and optionally ensures a
// persistent workdir shell. Both the TUI and HTTP terminal use this operation.
func InspectRemoteTerminal(ctx context.Context, task *db.Task, workdir string, ensureShell bool) (RemoteTerminal, error) {
	if task == nil || !isRemotePlacement(task.PlacementTarget) || task.DaemonSession == "" {
		return RemoteTerminal{}, ErrRemoteTerminalEnded
	}
	ctx = WithRunner(ctx, RemoteRunner{Host: task.PlacementTarget})
	return inspectRemoteTerminal(ctx, task, workdir, ensureShell)
}

func inspectRemoteTerminal(ctx context.Context, task *db.Task, workdir string, ensureShell bool) (RemoteTerminal, error) {
	info := RemoteTerminal{WindowTarget: remoteWindowTarget(task)}
	// Exact window names avoid tmux's prefix matching selecting another task.
	pane := func(window string) (string, error) {
		out, err := tmuxCmd(ctx, "list-panes", "-t", task.DaemonSession+":="+window, "-F", "#{pane_id}").Output()
		if err != nil {
			return "", err
		}
		ids := strings.Fields(string(out))
		if len(ids) == 0 {
			return "", fmt.Errorf("no panes in remote window %s", window)
		}
		return ids[0], nil
	}
	var err error
	info.AgentPaneID, err = pane(TmuxWindowName(task.ID))
	if err != nil {
		if classifyRemoteProbeFailure(ctx.Err(), err) == windowGone {
			return info, ErrRemoteTerminalEnded
		}
		return info, fmt.Errorf("cannot inspect remote executor: %w", err)
	}
	window := TmuxWindowName(task.ID) + "-shell"
	info.ShellPaneID, err = pane(window)
	if err == nil || !ensureShell {
		return info, nil
	}
	if workdir == "" {
		return info, fmt.Errorf("remote workdir is not recorded for this task")
	}
	// Separate windows let the TUI show agent and shell in separate local SSH
	// panes without changing the agent's layout or stealing its keyboard focus.
	// Set context before exec rather than typing an export into an active shell.
	script := fmt.Sprintf("cd %s && export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=\"$PWD\" && exec \"${SHELL:-/bin/sh}\" -l",
		shellQuoteRemotePath(workdir), task.ID, task.Port)
	// TUI and HTTP may live in different processes. Serialize on the host and
	// recheck under the lock so opening both surfaces cannot spawn two shells.
	lock := shellQuote(fmt.Sprintf("ty-shell-%d", task.ID))
	createArgs := []string{"tmux", "new-window", "-d", "-P", "-F", "#{pane_id}",
		"-t", task.DaemonSession, "-n", window, "sh", "-lc", script}
	for i := range createArgs {
		createArgs[i] = shellQuote(createArgs[i])
	}
	chain := "tmux wait-for -L " + lock + " || exit 1\n" +
		"trap " + shellQuote("tmux wait-for -U "+lock) + " EXIT\n" +
		"trap 'exit 1' HUP INT TERM\n" +
		"tmux list-panes -t " + shellQuote(task.DaemonSession+":="+window) +
		" -F '#{pane_id}' 2>/dev/null || " + strings.Join(createArgs, " ")
	out, err := command(ctx, "", "sh", "-c", chain).Output()
	if err != nil {
		return info, fmt.Errorf("cannot create remote shell: %w", err)
	}
	info.ShellPaneID = strings.TrimSpace(string(out))
	if info.ShellPaneID == "" {
		return info, fmt.Errorf("remote shell returned no pane ID")
	}
	return info, nil
}
