// Package handshake is the daemon↔client version contract.
//
// A ty install is several processes of the same program: the daemon that runs
// agents, the TUI, and every short-lived CLI invocation. They are supposed to
// be the same build reading the same database, talking to the same tmux
// server, resolving the same Claude config dir. When they are not, the symptom
// is never "version mismatch" — it is an agent that resumes into an empty
// session, a hook that writes a status nobody reads, or a fix that is built,
// installed and apparently ignored because the daemon from twenty minutes ago
// is still the one executing tasks.
//
// So the daemon writes down what it is and where it lives, and clients read
// that record at startup and say so out loud.
package handshake

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Protocol is the version of the daemon↔client contract this build speaks.
//
// It is NOT the release version: it moves only when the daemon and a client
// must agree on something that changed underneath them. Bump it by one — never
// reusing a number — when any of these change:
//
//   - db.SchemaVersion: the daemon writes rows an older client cannot read, or
//     reads columns a newer client has stopped writing.
//   - executor.ClaudeHookEvents: the set of Claude hook events ty installs into
//     a task worktree, or the payload those hooks send back to `ty claude-hook`.
//     A daemon that installs hooks a client does not understand silently loses
//     status transitions.
//   - the tmux conventions in internal/tmuxctl: the agent server socket name,
//     the pane tags, the daemon session and window naming. Two builds that
//     disagree here cannot find each other's agents.
//
// Those three inputs are fingerprinted by TestContractFingerprint. Change one
// without bumping Protocol and the test fails, printing the fingerprint to
// paste back in — so the bump is a deliberate act, not a thing you remember.
// 2: the Claude hook set gained SessionStart/UserPromptSubmit/StopFailure/
// SessionEnd, and hook payloads are now checked against the session that owns
// the task. A daemon that installs those hooks against a client too old to
// handle them loses the transitions they carry.
//
// 3: per-task turn counters (task_turns, db.SchemaVersion 2). The hooks that
// advance them run the DAEMON's binary, so a daemon from before this writes no
// counters — and a newer client that sends a prompt and waits for the reply then
// waits for something that cannot happen. It has to be able to say so.
//
// 4: attachment bytes moved to local files (db.SchemaVersion 3). Older
// executors would read the empty compatibility blob and silently lose files.
const Protocol = 4

// ContractFingerprint pins the contract inputs Protocol covers. See
// contract_test.go; it prints the replacement value when the inputs move.
const ContractFingerprint = "955cf702ca9431fd"

// Severity ranks a finding. The zero value is intentionally invalid so a
// finding always carries an explicit one.
type Severity string

const (
	// SeverityOK means the two agree.
	SeverityOK Severity = "ok"
	// SeverityInfo is worth printing but is not a problem — chiefly a daemon
	// too old to have left a record at all.
	SeverityInfo Severity = "info"
	// SeverityWarning is a divergence that can bite but does not break the
	// contract: same protocol, different build; or a differing environment.
	SeverityWarning Severity = "warning"
	// SeverityError is a broken contract: different protocols.
	SeverityError Severity = "error"
)

// rank orders severities so callers can fold many findings into one verdict.
func rank(s Severity) int {
	switch s {
	case SeverityError:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// Worst returns the most severe of the given severities, SeverityOK for none.
func Worst(sevs ...Severity) Severity {
	worst := SeverityOK
	for _, s := range sevs {
		if rank(s) > rank(worst) {
			worst = s
		}
	}
	return worst
}

// Record is what a daemon writes down about itself, and what a client compares
// itself against. Every field is stable JSON: an older ty must be able to read
// a newer record without choking, so fields are only ever added.
type Record struct {
	Version    string    `json:"version"`  // build, e.g. "0.9.3" or "dev"
	Protocol   int       `json:"protocol"` // Protocol of the build that wrote this
	PID        int       `json:"pid"`      // the daemon process
	StartedAt  time.Time `json:"started_at"`
	Executable string    `json:"executable"` // resolved path of the running binary

	// The environment two processes must agree on to see the same world.
	ClaudeConfigDir string `json:"claude_config_dir"`
	TmuxSocket      string `json:"tmux_socket"` // "" means tmux's default server
	DBPath          string `json:"db_path"`
}

// Finding is one comparison result, shaped so `ty doctor` can render it
// directly as a check.
type Finding struct {
	ID       string
	Severity Severity
	Summary  string
	Detail   string
}

// Check ids produced by Compare. They are part of `ty doctor --json`'s stable
// shape, so they are constants rather than literals scattered around.
const (
	CheckBuild = "daemon-handshake"
	CheckEnv   = "daemon-env"
)

// RecordPath is where a daemon whose pid file is pidFile writes its record —
// beside the pid file, alongside the existing .mode and .lock siblings.
func RecordPath(pidFile string) string { return pidFile + ".info" }

// Write saves r to path, atomically: a client must never read a half-written
// record and conclude the daemon is a build that does not exist.
func Write(path string, r Record) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".daemon-info-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// Read loads a record. A missing file is reported as os.ErrNotExist, which
// callers treat as "the daemon predates the handshake" rather than an error.
func Read(path string) (*Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &r, nil
}

// Compare reports how a client lines up with the running daemon.
//
// daemon == nil means the daemon left no readable record. That is not an
// error: it is what every ty from before this handshake looks like, and what a
// remote or placed host running a slightly older ty looks like from here. Such
// a daemon is reported as info and is never blocked — see the package docs on
// mixed fleets.
//
// It always returns exactly two findings, CheckBuild then CheckEnv, so
// `ty doctor --json` has the same check ids on every machine.
func Compare(daemon *Record, client Record) []Finding {
	return []Finding{buildFinding(daemon, client), envFinding(daemon, client)}
}

func buildFinding(daemon *Record, client Record) Finding {
	if daemon == nil {
		return Finding{
			ID:       CheckBuild,
			Severity: SeverityInfo,
			Summary:  "daemon left no build record — it predates the version handshake",
			Detail: fmt.Sprintf("This is build %s (protocol %d). The running daemon is older than the handshake, "+
				"so its build cannot be checked. Run `ty restart` to put it on this build.",
				describe(client.Version), client.Protocol),
		}
	}
	switch {
	case daemon.Protocol != client.Protocol:
		return Finding{
			ID:       CheckBuild,
			Severity: SeverityError,
			Summary: fmt.Sprintf("daemon is build %s, this is build %s — run `ty restart`",
				describe(daemon.Version), describe(client.Version)),
			Detail: fmt.Sprintf("The daemon speaks protocol %d and this build speaks %d. They disagree about the "+
				"database schema, the hook payloads or the tmux conventions, so anything they do together is "+
				"unreliable until the daemon is restarted on this build.", daemon.Protocol, client.Protocol),
		}
	case daemon.Version != client.Version:
		return Finding{
			ID:       CheckBuild,
			Severity: SeverityWarning,
			Summary: fmt.Sprintf("daemon is build %s, this is build %s — run `ty restart` when convenient",
				describe(daemon.Version), describe(client.Version)),
			Detail: fmt.Sprintf("Both speak protocol %d, so they still understand each other. The daemon simply "+
				"has not picked up this build yet: a fix built here is not running there.", client.Protocol),
		}
	default:
		return Finding{
			ID:       CheckBuild,
			Severity: SeverityOK,
			Summary:  fmt.Sprintf("daemon and this binary are both build %s (protocol %d)", describe(client.Version), client.Protocol),
		}
	}
}

// envDiff is one environment field the daemon and client resolved differently.
type envDiff struct {
	field  string
	daemon string
	client string
}

func envFinding(daemon *Record, client Record) Finding {
	if daemon == nil {
		return Finding{
			ID:       CheckEnv,
			Severity: SeverityInfo,
			Summary:  "daemon environment not comparable — no build record",
			Detail: fmt.Sprintf("This process: claude config dir %s, tmux server %s, database %s.",
				describe(client.ClaudeConfigDir), describeSocket(client.TmuxSocket), describe(client.DBPath)),
		}
	}

	var diffs []envDiff
	for _, d := range []envDiff{
		{"claude config dir", daemon.ClaudeConfigDir, client.ClaudeConfigDir},
		{"database", daemon.DBPath, client.DBPath},
		{"tmux server", describeSocket(daemon.TmuxSocket), describeSocket(client.TmuxSocket)},
	} {
		if d.daemon != d.client {
			diffs = append(diffs, d)
		}
	}
	if len(diffs) == 0 {
		return Finding{
			ID:       CheckEnv,
			Severity: SeverityOK,
			Summary:  "daemon and this binary resolve the same config dir, database and tmux server",
			Detail: fmt.Sprintf("claude config dir %s, tmux server %s, database %s.",
				describe(client.ClaudeConfigDir), describeSocket(client.TmuxSocket), describe(client.DBPath)),
		}
	}
	summary := "daemon environment differs from this process: "
	detail := ""
	for i, d := range diffs {
		if i > 0 {
			summary += ", "
		}
		summary += d.field
		detail += fmt.Sprintf("%s: daemon %s, this process %s. ", d.field, describe(d.daemon), describe(d.client))
	}
	return Finding{
		ID:       CheckEnv,
		Severity: SeverityWarning,
		Summary:  summary,
		Detail:   detail + "The two are looking at different worlds; a task started by one may be invisible to the other.",
	}
}

func describe(v string) string {
	if v == "" {
		return "(unset)"
	}
	return v
}

// describeSocket names a tmux server the way a human would: "" is not "unset",
// it is tmux's own default server.
func describeSocket(s string) string {
	if s == "" {
		return "default"
	}
	return s
}
