package panel

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/tmuxctl"
)

// TerminalView is a snapshot source, not an attached tmux client. Reading it
// cannot select a pane, zoom a window, or change another client's dimensions.
type TerminalView struct {
	PaneID string
	Host   string
}

func (v TerminalView) command(ctx context.Context, args ...string) *exec.Cmd {
	if v.Host != "" {
		return (executor.RemoteRunner{Host: v.Host}).Command(ctx, "", "tmux", args...)
	}
	return tmuxctl.Agent(ctx, args...)
}
func (s *Service) terminal(ctx context.Context, taskID int64) (TerminalView, error) {
	t, err := s.task(taskID)
	if err != nil {
		return TerminalView{}, err
	}
	v := TerminalView{PaneID: t.ShellPaneID}
	if t.PlacementTarget != "" && t.PlacementTarget != "local" {
		root, err := s.root(t)
		if err != nil {
			return v, err
		}
		info, err := executor.InspectRemoteTerminal(ctx, t, root, true)
		if err != nil {
			return v, err
		}
		v.Host, v.PaneID = t.PlacementTarget, info.ShellPaneID
	} else {
		if v.PaneID == "" {
			return v, fmt.Errorf("no shell session; open the task's shell first")
		}
		// Fail closed when stale IDs have been recycled for another task.
		out, err := v.command(ctx, "display-message", "-t", v.PaneID, "-p", "#{pane_id}\t#{"+tmuxctl.PaneTaskOption+"}\t#{"+tmuxctl.PaneRoleOption+"}").Output()
		if err != nil || strings.TrimSpace(string(out)) != v.PaneID+"\t"+strconv.FormatInt(taskID, 10)+"\t"+tmuxctl.RoleShell {
			return v, fmt.Errorf("shell session is unavailable or its ownership changed")
		}
	}
	return v, nil
}
func (s *Service) ShellFrame(ctx context.Context, taskID int64) (string, error) {
	v, err := s.terminal(ctx, taskID)
	if err != nil {
		return "", err
	}
	out, err := v.command(ctx, "capture-pane", "-p", "-e", "-t", v.PaneID).Output()
	return strings.TrimRight(string(out), "\n"), err
}

// SendShell sends one terminal key or a literal paste, never a shell command.
func (s *Service) SendShell(ctx context.Context, taskID int64, text string, literal bool) error {
	if len(text) > 65536 {
		return fmt.Errorf("terminal input too large")
	}
	v, err := s.terminal(ctx, taskID)
	if err != nil {
		return err
	}
	args := []string{"send-keys", "-t", v.PaneID}
	if literal {
		args = append(args, "-l")
	}
	args = append(args, "--", text)
	return v.command(ctx, args...).Run()
}
