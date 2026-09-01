package hooks

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// A consulted hook is one ty asks a question of, rather than merely notifying.
//
// Most events here are fire-and-forget: the script runs in the background and
// nothing waits for it or reads what it says. Two are not. `task.route` decides
// which Claude profile runs a task; `task.placement` decides which machine does.
// Both are answered before the executor spawns, so both block the spawn, and
// both parse the handler's stdout.
//
// They were written separately and drifted into two copies of the same loop.
// The wire formats stay different on purpose — routing answers in KEY=VALUE
// because the scripts writing it are shell, placement answers in JSON because
// it carries structured fields — but everything around the format is identical,
// and that part lives here.
//
// The shared rules, in one place so both hooks cannot disagree about them:
//
//   - Handlers are consulted in plugin-name order, never in parallel. With two
//     handlers installed, the first answer stands. Merging or last-write-wins
//     would make the effective policy depend on which script happened to finish
//     first, which is not a policy anyone chose.
//   - A handler that errors, times out, or answers unusably is skipped, and the
//     search continues. Being unable to route or place a task must never be the
//     reason it fails to run.
//   - stdout and stderr are captured separately. stdout is the answer channel;
//     stderr is free for the script to log on. Merging them would parse an
//     `echo "checking..." >&2` as part of the verdict.

// consultedWaitDelay bounds how long a killed handler's lingering children may
// hold its output pipe open.
const consultedWaitDelay = 2 * time.Second

// handlerAnswer is one handler's raw reply, before either hook's parser sees it.
type handlerAnswer struct {
	Plugin string // plugin that answered, for attribution in logs and the UI
	Stdout string
}

// consult walks the plugins declaring event, runs each one, and hands its raw
// stdout to decide.
//
// decide reports whether the answer settles the question. Returning false means
// "not decisive, keep looking" — which is how placement distinguishes a handler
// that deliberately said "run locally" from one that had nothing to say, and
// how routing skips a router that printed no usable keys.
//
// Returning the answer rather than a parsed type keeps this free of either
// hook's vocabulary; each parses what it understands.
func (r *Runner) consult(
	ctx context.Context,
	event string,
	timeout time.Duration,
	stdin []byte,
	envFor func(p Plugin) []string,
	decide func(a handlerAnswer) bool,
) {
	for _, p := range r.plugins {
		script, ok := p.ScriptFor(event)
		if !ok {
			continue
		}

		var env []string
		if envFor != nil {
			env = envFor(p)
		}

		out, err := runConsultedScript(ctx, script, p.Dir, env, stdin, timeout)
		if err != nil {
			// Logged by the caller, which knows what the failure costs — for
			// placement it means running locally, for routing it means not
			// optimizing. Both are survivable; neither is silent.
			r.logger.Warn("consulted hook failed",
				"event", event, "plugin", p.Name, "error", err)
			continue
		}

		if decide(handlerAnswer{Plugin: p.Name, Stdout: out}) {
			return
		}
	}
}

// runConsultedScript runs one handler to completion under a deadline and
// returns its stdout.
//
// stdin is optional: routing passes its request through the environment, since
// shell scripts read env more naturally than they parse stdin, while placement
// writes a JSON request. A handler that ignores what it is given still works.
func runConsultedScript(
	ctx context.Context,
	script, workDir string,
	env []string,
	stdin []byte,
	timeout time.Duration,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, script)
	cmd.Dir = workDir
	if env != nil {
		cmd.Env = env
	}
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// A handler that forks a child which outlives it would otherwise hold the
	// output pipe open past the deadline, hanging the spawn this hook blocks.
	cmd.WaitDelay = consultedWaitDelay

	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr == context.DeadlineExceeded {
		// Name the budget. "signal: killed" tells the operator nothing about why
		// their handler was killed, and this is the failure they will actually
		// hit while writing one.
		return "", fmt.Errorf("handler did not answer within %s: %w", timeout, ctxErr)
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return stdout.String(), nil
}
