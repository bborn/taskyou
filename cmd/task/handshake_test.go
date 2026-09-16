package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/handshake"
)

// isolateInstance points this process's data dir at a temp directory, so the
// pid file and daemon record it reads are the test's and never the machine's.
func isolateInstance(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("WORKTREE_DB_PATH", filepath.Join(dir, "tasks.db"))
	return dir
}

func writePid(t *testing.T, pid int) {
	t.Helper()
	if err := writePidFile(getPidFilePath(), pid); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
}

// TestReadDaemonRecordNoDaemon: with nothing running there is nothing to
// compare against, whatever files happen to be lying around.
func TestReadDaemonRecordNoDaemon(t *testing.T) {
	isolateInstance(t)
	if rec := readDaemonRecord(); rec != nil {
		t.Fatalf("got %+v, want nil with no pid file", rec)
	}

	// A record with no live process behind it must not be believed either.
	if err := handshake.Write(daemonRecordPath(), currentRecord()); err != nil {
		t.Fatal(err)
	}
	if rec := readDaemonRecord(); rec != nil {
		t.Fatalf("got %+v, want nil: the record has no daemon", rec)
	}
}

// TestReadDaemonRecordStale is the important one. A record left by a daemon
// that has been replaced would otherwise be read as a live build mismatch, and
// send the user to `ty restart` for a daemon that already restarted.
func TestReadDaemonRecordStale(t *testing.T) {
	isolateInstance(t)
	writePid(t, os.Getpid()) // this process stands in for a live daemon

	rec := currentRecord()
	rec.PID = os.Getpid() + 1 // ...but the record is from a different process
	rec.Version = "0.0.1"
	if err := handshake.Write(daemonRecordPath(), rec); err != nil {
		t.Fatal(err)
	}

	if got := readDaemonRecord(); got != nil {
		t.Fatalf("got %+v, want nil for a record whose pid is not the live daemon", got)
	}
}

// TestReadDaemonRecordLive: a record written by the live process is trusted.
func TestReadDaemonRecordLive(t *testing.T) {
	isolateInstance(t)
	writePid(t, os.Getpid())
	if err := writeDaemonRecord(); err != nil {
		t.Fatalf("writeDaemonRecord: %v", err)
	}

	got := readDaemonRecord()
	if got == nil {
		t.Fatal("got nil, want the record just written")
	}
	if got.Protocol != handshake.Protocol || got.PID != os.Getpid() {
		t.Errorf("got %+v", *got)
	}
	if got.StartedAt.IsZero() {
		t.Error("the daemon record does not say when the daemon started")
	}

	removeDaemonRecord()
	if readDaemonRecord() != nil {
		t.Error("record survived removeDaemonRecord")
	}
}

// TestReadDaemonRecordCorrupt: an unreadable record is "unknown build", not a
// mismatch — a half-written file must not send anyone chasing a phantom.
func TestReadDaemonRecordCorrupt(t *testing.T) {
	isolateInstance(t)
	writePid(t, os.Getpid())
	if err := os.WriteFile(daemonRecordPath(), []byte("{truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readDaemonRecord(); got != nil {
		t.Fatalf("got %+v, want nil for a corrupt record", got)
	}
}

// TestReportHandshakeSilentWhenAgreed: the common case prints nothing. A check
// that chirps on every command is a check people learn to ignore.
func TestReportHandshakeSilentWhenAgreed(t *testing.T) {
	isolateInstance(t)
	writePid(t, os.Getpid())
	if err := writeDaemonRecord(); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	reportHandshake(&out)
	if out.String() != "" {
		t.Errorf("printed %q, want nothing when the builds agree", out.String())
	}
	if notice := handshakeNotice(); notice != "" {
		t.Errorf("notice = %q, want empty", notice)
	}
}

// TestReportHandshakeSilentWithoutDaemon: no daemon, nothing to say. Starting
// one is another code path's job, and complaining here would fire on every CLI
// command run on a machine that simply has no queued work.
func TestReportHandshakeSilentWithoutDaemon(t *testing.T) {
	isolateInstance(t)
	var out strings.Builder
	reportHandshake(&out)
	if out.String() != "" {
		t.Errorf("printed %q, want nothing with no daemon running", out.String())
	}
}

// TestReportHandshakeSpeaksUp: a mismatch reaches stderr and the TUI banner,
// naming both builds and the command that fixes it.
func TestReportHandshakeSpeaksUp(t *testing.T) {
	isolateInstance(t)
	writePid(t, os.Getpid())

	rec := currentRecord()
	rec.PID = os.Getpid()
	rec.Version = "0.0.1"
	rec.Protocol = handshake.Protocol + 1
	if err := handshake.Write(daemonRecordPath(), rec); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	reportHandshake(&out)
	for _, want := range []string{"0.0.1", "ty restart"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output %q does not mention %q", out.String(), want)
		}
	}
	notice := handshakeNotice()
	if !strings.Contains(notice, "ty restart") {
		t.Errorf("notice %q does not say what to do", notice)
	}
	if !strings.HasPrefix(notice, "✗") {
		t.Errorf("notice %q does not read as an error", notice)
	}
}

// TestHandshakeNoticePrefersTheError: when the environment has also diverged,
// the banner shows the protocol break, not the milder of the two.
func TestHandshakeNoticePrefersTheError(t *testing.T) {
	isolateInstance(t)
	writePid(t, os.Getpid())

	rec := currentRecord()
	rec.PID = os.Getpid()
	rec.Protocol = handshake.Protocol + 1
	rec.ClaudeConfigDir = "/somewhere/else"
	if err := handshake.Write(daemonRecordPath(), rec); err != nil {
		t.Fatal(err)
	}
	if notice := handshakeNotice(); !strings.HasPrefix(notice, "✗") {
		t.Errorf("notice = %q, want the protocol error to win", notice)
	}
}

// TestHandshakeSilentCommands keeps the handshake out of the entrypoints whose
// output something other than a human parses, and in the ones a person reads.
func TestHandshakeSilentCommands(t *testing.T) {
	// Internal plumbing: hidden, and spoken to by another program.
	for _, name := range []string{"claude-hook", "worktree-guard", "mcp-server"} {
		if shouldReportHandshake(&cobra.Command{Use: name, Hidden: true}) {
			t.Errorf("hidden command %q should not print handshake warnings", name)
		}
	}
	// Visible, but still not the place for it.
	for _, name := range []string{"completion", "daemon", "doctor"} {
		if shouldReportHandshake(&cobra.Command{Use: name}) {
			t.Errorf("%q should not print handshake warnings", name)
		}
	}
	// Everything a person types.
	for _, name := range []string{"list", "create", "restart", "place"} {
		if !shouldReportHandshake(&cobra.Command{Use: name}) {
			t.Errorf("%q should print handshake warnings", name)
		}
	}
}

// TestCurrentRecordDescribesThisProcess: the record is only useful if both
// sides fill it from the same resolution, so every field must be populated.
func TestCurrentRecordDescribesThisProcess(t *testing.T) {
	dir := isolateInstance(t)
	rec := currentRecord()
	if rec.Protocol != handshake.Protocol {
		t.Errorf("Protocol = %d, want %d", rec.Protocol, handshake.Protocol)
	}
	if rec.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", rec.PID, os.Getpid())
	}
	if rec.DBPath != filepath.Join(dir, "tasks.db") {
		t.Errorf("DBPath = %q, want the isolated database", rec.DBPath)
	}
	if rec.ClaudeConfigDir == "" {
		t.Error("ClaudeConfigDir is empty; a divergence in it could never be reported")
	}
	if rec.Version == "" {
		t.Error("Version is empty")
	}
}

// TestDaemonRecordSitsBesideThePidFile: `ty daemon stop` and the QA harness
// clean up around the pid file, and the record has to be where they look.
func TestDaemonRecordSitsBesideThePidFile(t *testing.T) {
	isolateInstance(t)
	if got, want := daemonRecordPath(), getPidFilePath()+".info"; got != want {
		t.Errorf("daemonRecordPath = %q, want %q", got, want)
	}
	if filepath.Dir(daemonRecordPath()) != filepath.Dir(getPidFilePath()) {
		t.Error("the record is not beside the pid file")
	}
}
