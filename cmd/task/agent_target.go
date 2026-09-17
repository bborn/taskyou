package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	osexec "os/exec"
	"time"

	"github.com/bborn/workflow/internal/agentsend"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/tmuxctl"
)

// A task's agent pane is not necessarily on this machine. `ty place` and the
// task.placement plugins put tasks on other hosts, and their tmux windows live
// in a tmux server over there — which `ty show` already prints an `ssh … tmux
// attach` line for. `ty input` and `ty output` used to look only at this
// machine's agent server, so for every remotely placed task they reported "no
// executor pane" about a pane that was alive and working.
//
// agentTarget is the one answer to "where is this task's agent, and how do I
// talk to it": a runner pointed at the right tmux server, the pane on it, and
// the words to say where ty looked when there is nothing there.

// remoteTmuxTimeout bounds one tmux call made over ssh. Long enough for a slow
// link, short enough that `ty input` to an unreachable host fails instead of
// hanging at the prompt.
const remoteTmuxTimeout = 20 * time.Second

// agentTarget is a task's live agent pane and the tmux server it lives on.
type agentTarget struct {
	taskID int64
	// host is the ssh destination the task was placed on; empty means this
	// machine.
	host string
	// session is the daemon tmux session the window lives in, carried for the
	// error message rather than for addressing the pane.
	session string
	// pane is the resolved pane id on host's tmux server. Empty for a local
	// target, whose pane is found by tmux tag at send time (see agentPane).
	pane   string
	runner agentsend.Runner
}

// resolveAgentTarget finds the tmux server a task's agent is on.
//
// A remote task's pane can only be resolved by the machine holding the ssh
// connection, so that lookup happens here; a local task's is left to the tag
// lookup agentsend already does, which refuses a stale pane id rather than
// typing into whatever pane inherited it.
func resolveAgentTarget(ctx context.Context, task *db.Task) (agentTarget, error) {
	t := agentTarget{taskID: task.ID, session: task.DaemonSession}
	if !executor.IsRemotePlacement(task.PlacementTarget) {
		t.runner = &execCommandRunner{}
		return t, nil
	}

	t.host = task.PlacementTarget
	t.runner = remoteTmuxRunner{ctx: ctx, host: t.host}
	terminal, err := executor.InspectRemoteTerminal(ctx, task, "", false)
	if err != nil {
		if errors.Is(err, executor.ErrRemoteTerminalEnded) {
			return t, t.noPaneError()
		}
		return t, fmt.Errorf("cannot reach task #%d's agent %s: %w", task.ID, t.where(), err)
	}
	t.pane = terminal.AgentPaneID
	if t.pane == "" {
		return t, t.noPaneError()
	}
	return t, nil
}

// sender delivers prompts to this target's pane. The host namespaces the
// per-pane lock inside agentsend: pane ids are only unique within one tmux
// server, so a remote "%3" must not queue behind this machine's.
func (t agentTarget) sender(store agentsend.Store) *agentsend.Sender {
	if t.host == "" {
		return agentsend.New(t.runner, store)
	}
	return agentsend.NewForHost(t.runner, store, t.host)
}

// where names the tmux server ty looked at, so "the agent has exited" reads
// differently from "ty looked on the wrong machine".
func (t agentTarget) where() string {
	host := t.host
	if host == "" {
		host = "this machine"
	}
	if t.session == "" {
		return "on " + host
	}
	return fmt.Sprintf("on %s in tmux session %s", host, t.session)
}

func (t agentTarget) noPaneError() error {
	return &noPaneError{taskID: t.taskID, where: t.where()}
}

// noPaneError is "there is no agent pane to reach", with the machine and tmux
// session ty looked in. The old message said only "not running?", which is
// exactly wrong for a task placed on another host: the agent was running, ty
// was looking in the wrong place. It still matches agentsend.ErrNoPane, so
// callers that tell a dead session from a busy one keep working.
type noPaneError struct {
	taskID int64
	where  string
}

func (e *noPaneError) Error() string {
	return fmt.Sprintf("task #%d has no live agent pane %s", e.taskID, e.where)
}

func (e *noPaneError) Is(target error) bool { return target == agentsend.ErrNoPane }

// agentPane returns the pane to type into. A remote pane was resolved on its
// host; a local one is found by the task's tmux tag, never by the pane id on the
// task row — tmux reuses pane ids, and a stale one names another task's pane.
func (t agentTarget) agentPane() (string, error) {
	if t.pane != "" {
		return t.pane, nil
	}
	pane, err := agentsend.TaggedPane(t.runner, t.taskID, tmuxctl.RoleAgent)
	if err != nil {
		return "", t.noPaneError()
	}
	return pane, nil
}

// outputPane is agentPane with the pane id on the task row as a last resort.
// Reading a pane cannot cross-wire two tasks the way typing into one can, and
// windows made before panes were tagged have no tag to find.
func (t agentTarget) outputPane(task *db.Task) (string, error) {
	if pane, err := t.agentPane(); err == nil {
		return pane, nil
	}
	if t.host == "" && task.ClaudePaneID != "" {
		return task.ClaudePaneID, nil
	}
	return "", t.noPaneError()
}

// capture reads the last lines of a pane on this target's tmux server.
func (t agentTarget) capture(pane string, lines int) ([]byte, error) {
	return t.runner.Output("tmux", "capture-pane", "-t", pane, "-p", "-S", fmt.Sprintf("-%d", lines))
}

// remoteTmuxRunner runs tmux on the host a task was placed on.
//
// It is the agentsend.Runner seam pointed at another machine, and deliberately
// does NOT apply tmuxctl.AgentArgs the way this machine's runner does: the
// remote daemon's sessions live on that host's default tmux server, which is
// where every other remote operation (the poll, the terminal, the attach line
// `ty show` prints) already looks.
type remoteTmuxRunner struct {
	ctx  context.Context
	host string
}

func (r remoteTmuxRunner) Run(_ string, args ...string) error {
	ctx, cancel := context.WithTimeout(r.ctx, remoteTmuxTimeout)
	defer cancel()
	return r.command(ctx, args).Run()
}

func (r remoteTmuxRunner) Output(_ string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(r.ctx, remoteTmuxTimeout)
	defer cancel()
	return r.command(ctx, args).Output()
}

func (r remoteTmuxRunner) command(ctx context.Context, args []string) *osexec.Cmd {
	return executor.RemoteRunner{Host: r.host}.Command(ctx, "", "tmux", args...)
}

// inputOptions is one `ty input` invocation.
type inputOptions struct {
	message string
	// key is a tmux key name pressed before the message ("Down", "Escape").
	key       string
	justEnter bool
	noSubmit  bool
	force     bool
}

// runTaskInput types into a task's agent, wherever that agent is running.
func runTaskInput(ctx context.Context, database *db.DB, task *db.Task, opts inputOptions, out io.Writer) error {
	target, err := resolveAgentTarget(ctx, task)
	if err != nil {
		return err
	}
	pane, err := target.agentPane()
	if err != nil {
		return err
	}
	sender := target.sender(database)

	if opts.key != "" {
		if err := sender.SendKeysToPane(pane, opts.key); err != nil {
			return fmt.Errorf("send key: %w", err)
		}
	}

	submit := shouldSubmitInput(opts.message, opts.justEnter, opts.noSubmit)
	switch {
	case opts.message != "":
		err = sender.SendToPane(pane, agentsend.Prompt{
			TaskID: task.ID,
			Text:   opts.message,
			Force:  opts.force,
			Submit: submit,
		})
	case submit:
		err = sender.SendKeysToPane(pane, "Enter")
	}
	if err != nil {
		return err
	}

	suffix := ""
	if opts.message != "" && !submit {
		suffix = " (not submitted)"
	}
	on := ""
	if target.host != "" {
		on = " on " + target.host
	}
	fmt.Fprintln(out, successStyle.Render(fmt.Sprintf("Sent input to task #%d%s%s", task.ID, on, suffix)))
	return nil
}

// runTaskOutput prints what a task's agent pane is showing, wherever it runs.
func runTaskOutput(ctx context.Context, task *db.Task, lines int, out io.Writer) error {
	target, err := resolveAgentTarget(ctx, task)
	if err != nil {
		return err
	}
	pane, err := target.outputPane(task)
	if err != nil {
		return err
	}
	content, err := target.capture(pane, lines)
	if err != nil {
		return fmt.Errorf("cannot read task #%d's agent pane %s: %w", task.ID, target.where(), err)
	}
	_, err = out.Write(content)
	return err
}
