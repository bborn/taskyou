package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/handshake"
	"github.com/bborn/workflow/internal/tmuxctl"
)

// currentRecord describes this process: its build, the contract it speaks, and
// the three pieces of environment that decide whether it and the daemon are
// looking at the same world.
//
// The daemon writes one of these at startup; every other ty process builds one
// for itself and compares. Both sides call this exact function, so a divergence
// in the record can only mean a real divergence in the processes.
func currentRecord() handshake.Record {
	exe, _ := os.Executable()
	// Peek, not Socket: building a record must never be the act that decides
	// which tmux server this install uses. `ty doctor` builds one on every run.
	socket, _ := tmuxctl.Peek()
	return handshake.Record{
		Version:         version,
		Protocol:        handshake.Protocol,
		PID:             os.Getpid(),
		Executable:      exe,
		ClaudeConfigDir: executor.DefaultClaudeConfigDir(),
		TmuxSocket:      socket,
		DBPath:          db.DefaultPath(),
	}
}

// daemonRecordPath is where this install's daemon leaves its record: beside the
// pid file, so an isolated instance (WORKTREE_DB_PATH) gets its own.
func daemonRecordPath() string { return handshake.RecordPath(getPidFilePath()) }

// readDaemonRecord returns the running daemon's record, or nil when there is
// none to trust.
//
// nil covers three cases that all mean the same thing to a client — "the
// daemon's build is unknown, carry on" — and none of which is an error:
//
//   - the daemon predates the handshake and writes no record (an older ty, on
//     this machine or a placed host)
//   - the record is left over from a daemon that is gone, identified by a PID
//     that no longer matches the live one
//   - the record is unreadable
//
// A stale record is worse than no record: it would report a build mismatch
// against a daemon that is not running, so the PID check is not optional.
func readDaemonRecord() *handshake.Record {
	pid, err := readPidFile(getPidFilePath())
	if err != nil || !processExists(pid) {
		return nil
	}
	rec, err := handshake.Read(daemonRecordPath())
	if err != nil || rec == nil {
		return nil
	}
	if rec.PID != 0 && rec.PID != pid {
		return nil // written by a daemon that has since been replaced
	}
	return rec
}

// daemonRunning reports whether this install's daemon process is alive.
func daemonRunning() (int, bool) {
	pid, err := readPidFile(getPidFilePath())
	if err != nil || !processExists(pid) {
		return 0, false
	}
	return pid, true
}

// writeDaemonRecord is called by the daemon once it is up. A failure is logged
// by the caller and never fatal: a daemon that cannot describe itself is still
// a daemon that runs tasks, and clients treat the missing record as "unknown".
func writeDaemonRecord() error {
	rec := currentRecord()
	rec.StartedAt = time.Now()
	return handshake.Write(daemonRecordPath(), rec)
}

// removeDaemonRecord clears the record when the daemon exits, so the next
// client does not compare itself against a build that stopped running.
func removeDaemonRecord() { os.Remove(daemonRecordPath()) }

// handshakeSilentCommands are the entrypoints that must stay quiet no matter
// what:
//
//   - completion feeds the shell
//   - daemon is the thing being compared against
//   - doctor renders the same findings itself, in full
//
// Every hidden command is silent too (see shouldReportHandshake): they are ty's
// internal plumbing — claude-hook, worktree-guard, mcp-server — spoken to by
// another program rather than read by a person. Listing the visible exceptions
// here and deriving the rest means a new internal command is quiet by default.
var handshakeSilentCommands = map[string]bool{
	"completion": true,
	"daemon":     true,
	"doctor":     true,
}

// shouldReportHandshake reports whether a command may print handshake warnings.
func shouldReportHandshake(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	return !cmd.Hidden && !handshakeSilentCommands[cmd.Name()]
}

// reportHandshake prints a client's disagreement with the running daemon.
//
// It stays silent when the two agree, when no daemon is running, and when the
// daemon left no record — the last being what a slightly older ty looks like,
// which must not nag on every command. Everything else is one line per finding,
// on stderr, and never changes an exit code: a mismatched daemon is something
// you fix with `ty restart`, not a reason to refuse the command the user typed.
func reportHandshake(w io.Writer) {
	rec := readDaemonRecord()
	if rec == nil {
		return
	}
	for _, f := range handshake.Compare(rec, currentRecord()) {
		switch f.Severity {
		case handshake.SeverityError:
			fmt.Fprintln(w, errorStyle.Render("✗ "+f.Summary))
			if f.Detail != "" {
				fmt.Fprintln(w, dimStyle.Render("  "+f.Detail))
			}
		case handshake.SeverityWarning:
			fmt.Fprintln(w, warnStyle.Render("⚠ "+f.Summary))
		}
	}
}

// handshakeNotice is the same comparison folded into one line for the TUI's
// notification banner, or "" when there is nothing to say. stderr is no use to
// a full-screen TUI: it scrolls away under the alt screen before anyone reads
// it, and this is exactly the class of problem someone stares past for an hour.
func handshakeNotice() string {
	rec := readDaemonRecord()
	if rec == nil {
		return ""
	}
	findings := handshake.Compare(rec, currentRecord())
	// Severity order, not list order: an env warning must not be what the user
	// sees when the protocols also disagree.
	for _, f := range findings {
		if f.Severity == handshake.SeverityError {
			return "✗ " + f.Summary
		}
	}
	for _, f := range findings {
		if f.Severity == handshake.SeverityWarning {
			return "⚠ " + f.Summary
		}
	}
	return ""
}
