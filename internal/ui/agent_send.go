package ui

import (
	"context"
	"os/exec"
	"time"

	"github.com/bborn/workflow/internal/agentsend"
	"github.com/bborn/workflow/internal/db"
)

// agentRunner runs tmux commands on the agent server for agentsend, which takes
// the same runner shape the web server uses. Each call is bounded: a wedged tmux
// must not park a Bubble Tea command goroutine forever.
type agentRunner struct{}

const agentRunnerTimeout = 10 * time.Second

func (agentRunner) Run(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), agentRunnerTimeout)
	defer cancel()
	return agentCommand(ctx, name, args...).Run()
}

func (agentRunner) Output(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentRunnerTimeout)
	defer cancel()
	return agentCommand(ctx, name, args...).Output()
}

func agentCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	if name == "tmux" {
		return agentTmux(ctx, args...)
	}
	return exec.CommandContext(ctx, name, args...)
}

// agentSender is the TUI's prompt delivery path: the same one the web API and
// `ty input` use, so a prompt typed here is resolved, serialized and
// busy-checked exactly as one typed anywhere else.
func agentSender(database *db.DB) *agentsend.Sender {
	return agentsend.New(agentRunner{}, database)
}
