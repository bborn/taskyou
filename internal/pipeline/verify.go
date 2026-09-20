package pipeline

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
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
	return runStepVerify(dir, command, StepVerifyTimeout)
}

// runStepVerify is the runner body, parameterized on the timeout so the deadline
// path can be exercised in a CI test budget (StepVerifyTimeout is 15 min). The
// public RunStepVerify calls this with the production constant, so behaviour is
// identical at runtime — the parameter exists for testability, not for callers.
func runStepVerify(dir, command string, timeout time.Duration) (output string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	// Run in its own process group so the deadline can kill sh AND any descendants
	// (a test runner it spawned) with one signal. CombinedOutput blocks on its
	// internal io.Copy until every pipe write-end closes; without a group kill a
	// descendant that inherited the pipe keeps it open past the deadline, so the
	// timeout would be advisory on RunStepVerify's wall-clock return, not a bound.
	// WaitDelay is a belt-and-suspenders fallback that force-reaps the copy
	// goroutine if a child escapes the group (e.g. re-sets its own pgid).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()

	text := string(out)
	if ctx.Err() == context.DeadlineExceeded {
		text += "\n[verify timed out after " + timeout.String() + "]"
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
