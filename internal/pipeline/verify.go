package pipeline

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// StepVerifyTimeout bounds a step's evidence-gate command. Generous because the
// common case is a full build + test suite; a run that exceeds it fails closed.
const StepVerifyTimeout = 15 * time.Minute

// stepVerifyOutputMax caps how much command output is handed back on failure, so a
// noisy red build doesn't flood the agent's context or the task log.
const stepVerifyOutputMax = 4000

// RunStepVerify runs a workflow step's `verify:` command in dir via `sh -c` and
// reports whether it passed (exit 0). On failure it returns a tail of the combined
// stdout+stderr. The gate fails CLOSED: a command that can't start, or times out,
// counts as a failure — a broken environment must never rubber-stamp a step as
// complete. Shared by the MCP taskyou_complete handler and the daemon's git-based
// completion sweep so both enforce the gate identically.
func RunStepVerify(dir, command string) (output string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), StepVerifyTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()

	text := string(out)
	if ctx.Err() == context.DeadlineExceeded {
		text += "\n[verify timed out after " + StepVerifyTimeout.String() + "]"
	}
	return tailString(text, stepVerifyOutputMax), err == nil
}

// tailString returns the last max bytes of s, marking a truncation.
func tailString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "…(truncated)…\n" + s[len(s)-max:]
}
