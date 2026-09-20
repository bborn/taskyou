package pipeline

import (
	"strings"
	"testing"
	"time"
)

// These tests exercise RunStepVerify's deadline path. The production
// StepVerifyTimeout (15 min) is far too long to wait for a regression test, so
// the timeout tests call the unexported runStepVerify helper with a SHORT
// deadline — runStepVerify is the exact body RunStepVerify runs with the
// production timeout, so this exercises the same cancellation path.

// TestRunStepVerifyDeadlineBindsWallClock is the regression test for the
// "deadline does not bound wall-clock return" bug. The recipe launches two
// background `sleep`s that inherit the stdout/stderr pipe and then `wait`s. The
// pre-fix Cancel SIGKILLed only the direct `sh`, leaving the grandchildren holding
// the pipe; CombinedOutput blocked on its internal io.Copy until the grandchildren
// exited on their own, so RunStepVerify returned at the grandchildren's lifetime
// (30s), not at the deadline (2s). After the fix the deadline SIGKILLs the whole
// process group, the pipe closes, and the function returns promptly at the deadline.
//
// Discrimination: with deadline=2s and grandchildren=30s, the fixed path returns
// at ~2s (well under the 15s bar); the buggy path returns at ~30s, cleanly above.
func TestRunStepVerifyDeadlineBindsWallClock(t *testing.T) {
	const deadline = 2 * time.Second
	start := time.Now()
	out, ok := runStepVerify("", "sleep 30 & sleep 30 & wait", deadline)
	elapsed := time.Since(start)

	if ok {
		t.Errorf("RunStepVerify ok = true, want false (a timed-out gate must fail closed)")
	}
	if !strings.Contains(out, "[verify timed out after") {
		t.Errorf("output missing the timeout marker; got: %q", out)
	}
	if elapsed > 15*time.Second {
		t.Errorf("deadline did not bind wall-clock return: elapsed %v >> deadline %v "+
			"(bug: descendants kept the pipe open past the deadline)", elapsed, deadline)
	}
	t.Logf("RunStepVerify returned after %v (deadline=%v); ok=%v", elapsed, deadline, ok)
}

// TestRunStepVerifyFailsClosedOnTimeout asserts the gate's documented fail-closed
// contract on a plain timeout: a command that exceeds the deadline returns ok=false
// and the message names the bound that fired. Guards against a future change that
// makes a timed-out gate pass or drops the diagnostic marker.
func TestRunStepVerifyFailsClosedOnTimeout(t *testing.T) {
	out, ok := runStepVerify("", "sleep 30", 1500*time.Millisecond)
	if ok {
		t.Error("RunStepVerify ok = true, want false on timeout")
	}
	want := "[verify timed out after 1.5s"
	if !strings.Contains(out, want) {
		t.Errorf("output missing %q; got: %q", want, out)
	}
}
