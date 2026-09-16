package handshake

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// daemonRecord is a plausible daemon: same build, same world.
func daemonRecord() Record {
	return Record{
		Version:         "0.9.3",
		Protocol:        Protocol,
		PID:             4242,
		StartedAt:       time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC),
		Executable:      "/home/x/go/bin/ty",
		ClaudeConfigDir: "/home/x/.claude",
		TmuxSocket:      "taskyou",
		DBPath:          "/home/x/.local/share/task/tasks.db",
	}
}

// find returns the finding with the given id, failing if it is missing.
func find(t *testing.T, findings []Finding, id string) Finding {
	t.Helper()
	for _, f := range findings {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("no finding %q in %+v", id, findings)
	return Finding{}
}

// TestCompareAlwaysReturnsBothChecks pins the shape `ty doctor --json` depends
// on: the same two check ids, in the same order, whatever the verdict.
func TestCompareAlwaysReturnsBothChecks(t *testing.T) {
	d := daemonRecord()
	for name, daemon := range map[string]*Record{"match": &d, "no record": nil} {
		findings := Compare(daemon, d)
		if len(findings) != 2 {
			t.Fatalf("%s: got %d findings, want 2", name, len(findings))
		}
		if findings[0].ID != CheckBuild || findings[1].ID != CheckEnv {
			t.Errorf("%s: ids = %q, %q; want %q, %q", name, findings[0].ID, findings[1].ID, CheckBuild, CheckEnv)
		}
		for _, f := range findings {
			if f.Severity == "" {
				t.Errorf("%s: finding %q has no severity", name, f.ID)
			}
			if f.Summary == "" {
				t.Errorf("%s: finding %q has no summary", name, f.ID)
			}
		}
	}
}

// TestCompareMatch: same build, same protocol, same environment is silent.
func TestCompareMatch(t *testing.T) {
	d := daemonRecord()
	findings := Compare(&d, d)
	for _, f := range findings {
		if f.Severity != SeverityOK {
			t.Errorf("%s = %s (%s), want ok", f.ID, f.Severity, f.Summary)
		}
	}
	if got := Worst(findings[0].Severity, findings[1].Severity); got != SeverityOK {
		t.Errorf("worst = %s, want ok", got)
	}
}

// TestCompareBuildOnlyMismatch: a daemon that has not picked up this build is a
// warning, not an error — the two still understand each other.
func TestCompareBuildOnlyMismatch(t *testing.T) {
	daemon := daemonRecord()
	client := daemonRecord()
	client.Version = "0.9.4"

	f := find(t, Compare(&daemon, client), CheckBuild)
	if f.Severity != SeverityWarning {
		t.Fatalf("severity = %s, want warning: %s", f.Severity, f.Summary)
	}
	for _, want := range []string{"0.9.3", "0.9.4", "ty restart"} {
		if !strings.Contains(f.Summary, want) {
			t.Errorf("summary %q does not name %q", f.Summary, want)
		}
	}
	// The environment is identical, so it must not be dragged into the warning.
	if env := find(t, Compare(&daemon, client), CheckEnv); env.Severity != SeverityOK {
		t.Errorf("env severity = %s, want ok", env.Severity)
	}
}

// TestCompareProtocolMismatch: different protocols is an error that names both
// builds and the command that fixes it.
func TestCompareProtocolMismatch(t *testing.T) {
	daemon := daemonRecord()
	daemon.Protocol = Protocol + 1
	daemon.Version = "0.9.9"
	client := daemonRecord()

	f := find(t, Compare(&daemon, client), CheckBuild)
	if f.Severity != SeverityError {
		t.Fatalf("severity = %s, want error: %s", f.Severity, f.Summary)
	}
	for _, want := range []string{"daemon is build 0.9.9", "this is build 0.9.3", "ty restart"} {
		if !strings.Contains(f.Summary, want) {
			t.Errorf("summary %q does not contain %q", f.Summary, want)
		}
	}
}

// TestCompareProtocolMismatchBeatsSameVersion: an identical version string is
// not reassurance when the protocols differ (two dev builds, say).
func TestCompareProtocolMismatchBeatsSameVersion(t *testing.T) {
	daemon := daemonRecord()
	daemon.Protocol = Protocol + 1
	client := daemonRecord()

	if f := find(t, Compare(&daemon, client), CheckBuild); f.Severity != SeverityError {
		t.Fatalf("severity = %s, want error", f.Severity)
	}
}

// TestCompareEnvDivergence: the values that make two processes see different
// worlds are a warning that names both sides.
func TestCompareEnvDivergence(t *testing.T) {
	cases := map[string]struct {
		mutate     func(*Record)
		wantField  string
		wantDaemon string
		wantClient string
	}{
		"claude config dir": {
			mutate:     func(r *Record) { r.ClaudeConfigDir = "/home/x/.claude-ik" },
			wantField:  "claude config dir",
			wantDaemon: "/home/x/.claude-ik",
			wantClient: "/home/x/.claude",
		},
		"database": {
			mutate:     func(r *Record) { r.DBPath = "/tmp/qa/tasks.db" },
			wantField:  "database",
			wantDaemon: "/tmp/qa/tasks.db",
			wantClient: "/home/x/.local/share/task/tasks.db",
		},
		"tmux server": {
			mutate:     func(r *Record) { r.TmuxSocket = "" },
			wantField:  "tmux server",
			wantDaemon: "default",
			wantClient: "taskyou",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			daemon := daemonRecord()
			tc.mutate(&daemon)
			client := daemonRecord()

			f := find(t, Compare(&daemon, client), CheckEnv)
			if f.Severity != SeverityWarning {
				t.Fatalf("severity = %s, want warning: %s", f.Severity, f.Summary)
			}
			if !strings.Contains(f.Summary, tc.wantField) {
				t.Errorf("summary %q does not name the field %q", f.Summary, tc.wantField)
			}
			// Naming both values is the whole point: "they differ" is useless.
			if !strings.Contains(f.Detail, tc.wantDaemon) {
				t.Errorf("detail %q does not name the daemon's value %q", f.Detail, tc.wantDaemon)
			}
			if !strings.Contains(f.Detail, tc.wantClient) {
				t.Errorf("detail %q does not name this process's value %q", f.Detail, tc.wantClient)
			}
			// Same build on both sides, so the build check stays quiet.
			if b := find(t, Compare(&daemon, client), CheckBuild); b.Severity != SeverityOK {
				t.Errorf("build severity = %s, want ok", b.Severity)
			}
		})
	}
}

// TestCompareEnvDivergenceNamesEveryDifference: two diverging fields both get
// reported, not just the first.
func TestCompareEnvDivergenceNamesEveryDifference(t *testing.T) {
	daemon := daemonRecord()
	daemon.ClaudeConfigDir = "/home/x/.claude-ik"
	daemon.DBPath = "/tmp/qa/tasks.db"
	client := daemonRecord()

	f := find(t, Compare(&daemon, client), CheckEnv)
	for _, want := range []string{"claude config dir", "database", "/home/x/.claude-ik", "/tmp/qa/tasks.db"} {
		if !strings.Contains(f.Summary+f.Detail, want) {
			t.Errorf("%q missing from %q / %q", want, f.Summary, f.Detail)
		}
	}
}

// TestCompareNoRecord: a daemon from before the handshake — which is what a
// remote or placed host running a slightly older ty looks like — is info, never
// an error. Nothing about it may be described as a mismatch.
func TestCompareNoRecord(t *testing.T) {
	findings := Compare(nil, daemonRecord())
	for _, f := range findings {
		if f.Severity != SeverityInfo {
			t.Errorf("%s = %s, want info: %s", f.ID, f.Severity, f.Summary)
		}
	}
	build := find(t, findings, CheckBuild)
	if !strings.Contains(build.Detail, "ty restart") {
		t.Errorf("detail %q does not say how to fix it", build.Detail)
	}
	if strings.Contains(strings.ToLower(build.Summary), "mismatch") {
		t.Errorf("summary %q calls an unknown build a mismatch", build.Summary)
	}
}

// TestWorst folds severities the way doctor's overall status does.
func TestWorst(t *testing.T) {
	cases := []struct {
		in   []Severity
		want Severity
	}{
		{nil, SeverityOK},
		{[]Severity{SeverityOK, SeverityOK}, SeverityOK},
		{[]Severity{SeverityOK, SeverityInfo}, SeverityInfo},
		{[]Severity{SeverityInfo, SeverityWarning}, SeverityWarning},
		{[]Severity{SeverityWarning, SeverityError, SeverityOK}, SeverityError},
	}
	for _, tc := range cases {
		if got := Worst(tc.in...); got != tc.want {
			t.Errorf("Worst(%v) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// TestWriteReadRoundTrip: what the daemon writes is what a client reads.
func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.pid.info")
	want := daemonRecord()
	if err := Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, want.StartedAt)
	}
	got.StartedAt, want.StartedAt = time.Time{}, time.Time{}
	if *got != want {
		t.Errorf("round trip changed the record:\n got %+v\nwant %+v", *got, want)
	}
	// Write leaves no temp files behind for the next reader to trip over.
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want just the record", len(entries))
	}
}

// TestWriteOverwrites: a restarted daemon replaces the previous record rather
// than appending to it or leaving the old build's values in place.
func TestWriteOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.pid.info")
	first := daemonRecord()
	if err := Write(path, first); err != nil {
		t.Fatalf("Write: %v", err)
	}
	second := daemonRecord()
	second.Version = "0.9.4"
	second.PID = 5151
	if err := Write(path, second); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Version != "0.9.4" || got.PID != 5151 {
		t.Errorf("got %s/%d, want 0.9.4/5151", got.Version, got.PID)
	}
}

// TestReadMissing: no record is os.ErrNotExist, which callers map to "older
// than the handshake" rather than to a failure.
func TestReadMissing(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "absent.info"))
	if !os.IsNotExist(err) {
		t.Fatalf("err = %v, want a not-exist error", err)
	}
}

// TestReadUnknownFieldsTolerated: a newer daemon may add fields; an older
// client must still read the ones it knows instead of erroring out.
func TestReadUnknownFieldsTolerated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.pid.info")
	raw := map[string]any{
		"version":           "1.0.0",
		"protocol":          Protocol,
		"db_path":           "/home/x/tasks.db",
		"something_new":     "from a future build",
		"claude_config_dir": "/home/x/.claude",
	}
	data, _ := json.Marshal(raw)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Version != "1.0.0" || got.DBPath != "/home/x/tasks.db" {
		t.Errorf("got %+v", *got)
	}
}

// TestReadGarbage: a truncated or corrupt record is an error the caller can
// report, not a panic and not a silently zero Record that would read as a
// protocol mismatch.
func TestReadGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.pid.info")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("Read accepted garbage")
	} else if os.IsNotExist(err) {
		t.Fatalf("corrupt record reported as missing: %v", err)
	}
}

// TestRecordPathSitsBesideThePidFile keeps the record where `ty daemon stop`
// and the QA harness already look for daemon state.
func TestRecordPathSitsBesideThePidFile(t *testing.T) {
	got := RecordPath("/home/x/.local/share/task/daemon.pid")
	want := "/home/x/.local/share/task/daemon.pid.info"
	if got != want {
		t.Errorf("RecordPath = %q, want %q", got, want)
	}
}
