package reaper

import (
	"os"
	osexec "os/exec"
	"strings"
	"testing"
	"time"
)

// TestParsePS_TruncatedCommandKeepsMatchersHonest pins the
// matcher-to-truncation interaction as a pure-function property. It feeds
// ParsePS the command columns `ps` would emit at several COLUMNS widths
// (reproduced from the procps-ng truncation table in the bug report) and
// asserts TaskIDFor and IsDevServer return the values predicted for each
// truncation depth.
//
// This is NOT a regression guard for dropping `-ww` — by construction ParsePS
// and Plan are decoupled from ScanProcesses, so its pass/fail is independent
// of the `-ww` flag. It guards a narrower, different regression: a future
// change to the matchers that would silently make truncated input still reap
// (for example loosening IsDevServer from an exact `node_modules/.bin/<name>`
// contains-test to a prefix match).
func TestParsePS_TruncatedCommandKeepsMatchersHonest(t *testing.T) {
	// ps header prefix for pid 900, ppid 1, no tty, elapsed 4d20h15m30s.
	const pidLine = "  900     1 ??       4-20:15:30 "
	// The command column is what the medium test-fixture path truncates to at
	// each COLUMNS width. Thresholds come from the bug report's Evidence 1
	// table for /Users/b/Projects/ik/.task-worktrees/... paths.
	cases := []struct {
		name     string
		command  string // command column after truncation
		wantTask int    // expected TaskIDFor
		wantDev  bool   // expected IsDevServer
	}{
		{
			// COLUMNS=80: line ends mid-worktree-id before the dash; worktreeRe
			// requires `\d+-` so TaskIDFor returns 0. Process is invisible to
			// every matcher (no Decision emitted).
			name:     "COLUMNS_80_total_invisibility",
			command:  "node /Users/b/Projects/ik/.task-worktrees/5",
			wantTask: 0,
			wantDev:  false,
		},
		{
			// COLUMNS=100: worktree ID + slug survive but node_modules is gone;
			// TaskIDFor matches, IsDevServer does not (orphan heuristic dead).
			name:     "COLUMNS_100_orphan_heuristic_defeated",
			command:  "node /Users/b/Projects/ik/.task-worktrees/5119-creator-referral-v2",
			wantTask: 5119,
			wantDev:  false,
		},
		{
			// COLUMNS=120: node_modules/.bin/ survives but the binary name is
			// gone; both substrings past the worktree id are clipped.
			name:     "COLUMNS_120_clipped_before_binary_name",
			command:  "node /Users/b/Projects/ik/.task-worktrees/5119-creator-referral-v2/node_modules/.bin/",
			wantTask: 5119,
			wantDev:  false,
		},
		{
			// COLUMNS=139: binary name clipped mid-token; IsDevServer requires an
			// exact strings.Contains match against the literal name, so the
			// truncated `webpack-dev-serv` matches no entry.
			name:     "COLUMNS_139_clipped_mid_binary_name",
			command:  "node /Users/b/Projects/ik/.task-worktrees/5119-creator-referral-v2/node_modules/.bin/webpack-dev-serv",
			wantTask: 5119,
			wantDev:  false,
		},
		{
			// COLUMNS>=140: both substrings survive; both matchers succeed — the
			// behavior the reaper relies on.
			name:     "COLUMNS_140_full_command",
			command:  "node /Users/b/Projects/ik/.task-worktrees/5119-creator-referral-v2/node_modules/.bin/webpack-dev-server --port 3035",
			wantTask: 5119,
			wantDev:  true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			procs := ParsePS(pidLine + c.command + "\n")
			if len(procs) != 1 {
				t.Fatalf("expected 1 parsed row, got %d: %+v", len(procs), procs)
			}
			if got := TaskIDFor(procs[0].Command); got != c.wantTask {
				t.Errorf("TaskIDFor(%q) = %d, want %d", procs[0].Command, got, c.wantTask)
			}
			if got := IsDevServer(procs[0].Command); got != c.wantDev {
				t.Errorf("IsDevServer(%q) = %v, want %v", procs[0].Command, got, c.wantDev)
			}
		})
	}
}

// TestScanProcesses_DisablesColumnsTruncation is the load-bearing regression
// guard for the `-ww` flag on ScanProcesses. It spawns a real long-running
// process whose ps-visible command line exceeds 80 columns and carries both the
// `.task-worktrees/<id>-<slug>` and `node_modules/.bin/<name>` substrings the
// reaper matches on, exports COLUMNS=80 into the ps child's environment, and
// asserts ScanProcesses returns the full command line for that PID.
//
// Without `-ww`, procps-ng's `ps` truncates the command column to COLUMNS even
// when its stdout is a pipe, clipping both substrings and silently defeating
// TaskIDFor and IsDevServer for orphans that were already eligible for reaping
// — the reaper becomes more conservative (keep instead of reap).
//
// This test runs the real `ps` binary, so it is gated behind REAPER_REAL_PS=1.
// Linux CI runners ship procps-ng and should set REAPER_REAL_PS=1; platforms
// without a usable ps or bash skip it.
func TestScanProcesses_DisablesColumnsTruncation(t *testing.T) {
	if os.Getenv("REAPER_REAL_PS") != "1" {
		t.Skip("requires the real ps binary; set REAPER_REAL_PS=1 to run")
	}
	if _, err := osexec.LookPath("ps"); err != nil {
		t.Skip("ps binary not found")
	}
	if _, err := osexec.LookPath("bash"); err != nil {
		t.Skip("bash not found (needed for exec -a argv control)")
	}

	// Use bash's `exec -a NAME COMMAND` to rename argv[0] of a long-running
	// `sleep` so the reaper's ps view shows a realistic dev-server command line
	// well over 80 columns. The PID bash holds is the PID sleep inherits via
	// exec — that is what ps reports.
	const longArgv0 = "node /Users/b/Projects/ik/.task-worktrees/5119-creator-referral-v2/node_modules/.bin/webpack-dev-server --port 3035 --hot --config webpack.config.js"
	cmd := osexec.Command("bash", "-c", "exec -a "+shellQuote(longArgv0)+" sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	childPID := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// Give the shell a moment to exec into sleep so ps observes the renamed argv.
	time.Sleep(300 * time.Millisecond)

	// Export COLUMNS=80 into ps's environment. ScanProcesses uses
	// osexec.Command(...).Output() with a nil Cmd.Env, so ps inherits this
	// process's environment; t.Setenv mutates it for the test and restores it
	// afterward. Without -ww this is exactly what triggers the truncation.
	t.Setenv("COLUMNS", "80")

	procs, err := ScanProcesses()
	if err != nil {
		t.Fatalf("ScanProcesses: %v", err)
	}
	var proc *Process
	for i := range procs {
		if procs[i].PID == childPID {
			proc = &procs[i]
			break
		}
	}
	if proc == nil {
		t.Fatalf("child pid %d not found in ps output (%d rows)", childPID, len(procs))
	}
	if !strings.Contains(proc.Command, "5119-creator-referral-v2") {
		t.Errorf("command lost worktree slug (truncated?): %q", proc.Command)
	}
	if !strings.Contains(proc.Command, "node_modules/.bin/webpack-dev-server") {
		t.Errorf("command lost dev-server substring (truncated?): %q", proc.Command)
	}
	if got := TaskIDFor(proc.Command); got != 5119 {
		t.Errorf("TaskIDFor = %d, want 5119 (command: %q)", got, proc.Command)
	}
	if !IsDevServer(proc.Command) {
		t.Errorf("IsDevServer = false, want true (command: %q)", proc.Command)
	}
}

// shellQuote wraps s in single quotes so it survives a bash -c argument.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
