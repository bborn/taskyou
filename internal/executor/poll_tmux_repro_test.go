//go:build qa_repro

// QA repro for PR #558 — "Don't false-block tasks on a transient tmux check failure".
//
// This is NOT part of the normal unit suite (build tag `qa_repro`). It is a live,
// deterministic demonstration that:
//
//  1. The exact window-check command pollTmuxSession runs (`tmux list-panes -t
//     <session>` with a 3s timeout) succeeds against a real, alive tmux session.
//  2. A realistic *transient* failure — the command's context deadline tripping
//     "under load" — makes that same check fail even though the window is alive.
//     This is the failure mode that caused the mass false-block.
//  3. The real windowMissTracker debounces that transient failure (alive →
//     transient-fail → alive never blocks), yet still reports "gone" after the
//     session is genuinely killed (threshold consecutive real failures).
//
// Isolation: runs on a DEDICATED tmux server (`tmux -L <socket>`) so it can never
// disturb a live ty instance on the default socket. The ONLY deviation from the
// production command is that `-L <socket>` flag; flags, 3s timeout, and the
// windowMissTracker decision logic are identical to executor.go.
//
// Run: go test ./internal/executor/ -tags=qa_repro -run TestQAReproTransientTmux -v

package executor

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"
)

func TestQAReproTransientTmux(t *testing.T) {
	const socket = "taskyou-qa-558"
	const session = "pr558-repro"

	tmux := func(timeout time.Duration, args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		full := append([]string{"-L", socket}, args...)
		return exec.CommandContext(ctx, "tmux", full...).Run()
	}

	// windowCheck mirrors executor.go:pollTmuxSession exactly: `tmux list-panes
	// -t <session>` under a 3s timeout, windowExists := (err == nil).
	windowCheck := func(timeout time.Duration) bool {
		return tmux(timeout, "list-panes", "-t", session) == nil
	}

	// Clean slate + guaranteed teardown of the isolated server.
	_ = tmux(3*time.Second, "kill-session", "-t", session)
	t.Cleanup(func() { _ = tmux(3*time.Second, "kill-server") })

	if err := tmux(5*time.Second, "new-session", "-d", "-s", session, "-x", "80", "-y", "24"); err != nil {
		t.Skipf("cannot start isolated tmux server (tmux required for this repro): %v", err)
	}
	t.Logf("[setup] started isolated tmux session %q on socket %q", session, socket)

	misses := windowMissTracker{threshold: 3}

	// helper to run one poll "tick" and report the decision the way pollTmuxSession would.
	tick := func(label string, exists bool) (blocked bool) {
		gone := misses.record(exists)
		state := "ALIVE  ✓ (counter reset)"
		if !exists {
			state = fmt.Sprintf("MISS   ✗ (consecutive=%d/%d)", misses.consecutive, misses.threshold)
		}
		decision := "keep polling"
		if gone {
			decision = ">>> BLOCK: \"Task needs review\""
		}
		t.Logf("[tick] %-28s window=%s  -> %s", label, state, decision)
		return gone
	}

	// 1) Real alive check — the genuine command against the live session.
	if got := windowCheck(3 * time.Second); !got {
		t.Fatalf("alive session: real `tmux list-panes` failed; expected success")
	}
	if tick("healthy check", true) {
		t.Fatalf("healthy check blocked the task")
	}

	// 2) Transient failure: SAME command, but the deadline trips (simulating the
	//    3s timeout failing under load). The session is still alive.
	if got := windowCheck(1 * time.Nanosecond); got {
		t.Fatalf("expected transient timeout to fail the check, but it succeeded")
	}
	t.Logf("[note] real `tmux list-panes` returned an ERROR under a tripped deadline while the window is ALIVE")
	if tick("transient timeout (load)", false) {
		t.Fatalf("OLD BEHAVIOR REGRESSION: a single transient failure blocked the task")
	}

	// 3) Recovery — server responsive again. The streak must reset.
	if got := windowCheck(3 * time.Second); !got {
		t.Fatalf("post-blip alive check failed unexpectedly")
	}
	if tick("recovered", true) {
		t.Fatalf("recovered check blocked the task")
	}
	if misses.consecutive != 0 {
		t.Fatalf("counter not reset after recovery: consecutive=%d", misses.consecutive)
	}
	t.Logf("[PASS] transient blip did NOT false-block the healthy task")

	// 4) Genuine death — kill the session; now the real check fails for real.
	if err := tmux(3*time.Second, "kill-session", "-t", session); err != nil {
		t.Fatalf("failed to kill session: %v", err)
	}
	t.Logf("[setup] killed session %q — window is now genuinely gone", session)

	blockedAt := 0
	for i := 1; i <= 3; i++ {
		got := windowCheck(3 * time.Second) // real failure: session no longer exists
		if got {
			t.Fatalf("killed session still reported as alive on check %d", i)
		}
		if tick(fmt.Sprintf("genuine death #%d", i), false) {
			blockedAt = i
			break
		}
	}
	if blockedAt != 3 {
		t.Fatalf("genuine death should block on the 3rd consecutive miss; blocked at %d", blockedAt)
	}
	t.Logf("[PASS] genuinely-closed window still detected after %d consecutive real failures", blockedAt)
}
