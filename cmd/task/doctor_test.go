package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/github"
	"github.com/bborn/workflow/internal/handshake"
)

// wantCheckIDs is the report `ty doctor --json` promises. A check may be added
// to the end of this list; renaming or dropping one breaks every fleet script
// that greps for it, so the list is spelled out rather than derived.
var wantCheckIDs = []string{
	"github-auth",
	"daemon",
	"daemon-handshake",
	"daemon-env",
	"tmux",
	"tmux-agent-server",
	"database",
	"db-schema",
	"status-consistency",
	"claude-hooks",
	"claude-mcp-config",
	"executors",
}

var validDoctorStatuses = map[string]bool{"ok": true, "info": true, "warning": true, "error": true}

// healthyAuth is a gh that a fleet operator would be happy with: a bot identity
// with its own full GraphQL bucket.
func healthyAuth() github.AuthStatus {
	return github.AuthStatus{
		GHInstalled:      true,
		LoggedIn:         true,
		TokenValid:       true,
		Account:          "taskyou-agents[bot]",
		AccountType:      github.AccountTypeApp,
		GraphQLRemaining: 5000,
		GraphQLLimit:     5000,
	}
}

// testEnv is a doctorEnv that touches nothing outside the test: a temp
// database, a stubbed gh, a stubbed PATH and a stubbed tmux.
func testEnv(t *testing.T) doctorEnv {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tasks.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("seed database: %v", err)
	}
	database.Close()

	client := handshake.Record{
		Version:         "0.9.3",
		Protocol:        handshake.Protocol,
		ClaudeConfigDir: filepath.Join(dir, ".claude"),
		TmuxSocket:      "taskyou",
		DBPath:          dbPath,
	}
	return doctorEnv{
		ctx:          context.Background(),
		client:       client,
		dbPath:       dbPath,
		pidFile:      filepath.Join(dir, "daemon.pid"),
		daemonRecord: func() *handshake.Record { return nil },
		daemonPID:    func() (int, bool) { return 0, false },
		checkAuth:    func(context.Context) github.AuthStatus { return healthyAuth() },
		lookPath: func(name string) (string, error) {
			return "/usr/bin/" + name, nil
		},
		runTmux: func(ctx context.Context, args ...string) ([]byte, error) {
			if len(args) > 0 && args[0] == "-V" {
				return []byte("tmux 3.4\n"), nil
			}
			return []byte("task-daemon-1234\n"), nil
		},
	}
}

// byID indexes a report for assertions.
func byID(t *testing.T, report doctorReport) map[string]doctorCheck {
	t.Helper()
	out := map[string]doctorCheck{}
	for _, c := range report.Checks {
		if _, dup := out[c.ID]; dup {
			t.Errorf("duplicate check id %q", c.ID)
		}
		out[c.ID] = c
	}
	return out
}

// TestDoctorJSONShape is the contract test for --json: the documented
// top-level keys, the documented per-check keys, every id present exactly
// once, and every status from the fixed vocabulary.
func TestDoctorJSONShape(t *testing.T) {
	report := runDoctor(testEnv(t))

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Decode into raw maps rather than back into doctorReport, so a renamed or
	// dropped JSON tag actually fails instead of round-tripping silently.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw) != 2 {
		t.Errorf("top level has %d keys (%v), want exactly status and checks", len(raw), keysOf(raw))
	}
	for _, key := range []string{"status", "checks"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("top level is missing %q: %s", key, data)
		}
	}

	var status string
	if err := json.Unmarshal(raw["status"], &status); err != nil {
		t.Fatalf("status is not a string: %v", err)
	}
	if !validDoctorStatuses[status] {
		t.Errorf("status = %q, want one of ok/info/warning/error", status)
	}

	var checks []map[string]json.RawMessage
	if err := json.Unmarshal(raw["checks"], &checks); err != nil {
		t.Fatalf("checks is not an array of objects: %v", err)
	}
	var gotIDs []string
	for i, c := range checks {
		if len(c) != 4 {
			t.Errorf("check %d has %d keys (%v), want exactly id/status/summary/details", i, len(c), keysOf(c))
		}
		var id, cstatus, summary, details string
		mustString(t, c, "id", &id)
		mustString(t, c, "status", &cstatus)
		mustString(t, c, "summary", &summary)
		mustString(t, c, "details", &details) // present even when empty
		if id == "" {
			t.Errorf("check %d has an empty id", i)
		}
		if summary == "" {
			t.Errorf("check %q has an empty summary", id)
		}
		if !validDoctorStatuses[cstatus] {
			t.Errorf("check %q has status %q, want one of ok/info/warning/error", id, cstatus)
		}
		gotIDs = append(gotIDs, id)
	}

	sortedGot, sortedWant := append([]string(nil), gotIDs...), append([]string(nil), wantCheckIDs...)
	sort.Strings(sortedGot)
	sort.Strings(sortedWant)
	if strings.Join(sortedGot, ",") != strings.Join(sortedWant, ",") {
		t.Errorf("check ids = %v\nwant       %v", sortedGot, sortedWant)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mustString(t *testing.T, m map[string]json.RawMessage, key string, into *string) {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		t.Errorf("check is missing %q", key)
		return
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Errorf("%q is not a string: %v", key, err)
	}
}

// TestDoctorOverallStatusIsTheWorstCheck: the top-level status must never be
// rosier than the checks underneath it.
func TestDoctorOverallStatusIsTheWorstCheck(t *testing.T) {
	cases := map[string]struct {
		mutate func(*doctorEnv)
		want   string
	}{
		"healthy": {
			mutate: func(env *doctorEnv) {
				env.daemonPID = func() (int, bool) { return 999, true }
				rec := env.client
				rec.PID = 999
				env.daemonRecord = func() *handshake.Record { return &rec }
			},
			want: "ok",
		},
		"daemon stopped is a warning": {
			mutate: func(*doctorEnv) {},
			want:   "warning",
		},
		"missing tmux is an error": {
			mutate: func(env *doctorEnv) {
				env.daemonPID = func() (int, bool) { return 999, true }
				rec := env.client
				rec.PID = 999
				env.daemonRecord = func() *handshake.Record { return &rec }
				env.lookPath = func(name string) (string, error) {
					if name == "tmux" {
						return "", errors.New("not found")
					}
					return "/usr/bin/" + name, nil
				}
			},
			want: "error",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env := testEnv(t)
			tc.mutate(&env)
			report := runDoctor(env)
			if report.Status != tc.want {
				t.Errorf("status = %q, want %q\n%s", report.Status, tc.want, renderForFailure(report))
			}
			// info checks are notes, not problems: they must never be what a
			// top-level status reports.
			if report.Status == "info" {
				t.Errorf("top-level status is %q; info is never a verdict", report.Status)
			}
			worst := handshake.SeverityOK
			for _, c := range report.Checks {
				if s := handshake.Severity(c.Status); s == handshake.SeverityWarning || s == handshake.SeverityError {
					worst = handshake.Worst(worst, s)
				}
			}
			if string(worst) != report.Status {
				t.Errorf("status %q is not the worst actionable check (%q)", report.Status, worst)
			}
		})
	}
}

func renderForFailure(report doctorReport) string {
	var b strings.Builder
	for _, c := range report.Checks {
		fmt.Fprintf(&b, "  %-18s %-8s %s\n", c.ID, c.Status, c.Summary)
	}
	return b.String()
}

// TestDoctorExitCode: errors always fail, warnings fail only under --strict.
// This is what a fleet sweep keys off, so it is pinned.
func TestDoctorExitCode(t *testing.T) {
	cases := []struct {
		status       string
		want, strict int
	}{
		{"ok", 0, 0},
		{"info", 0, 0},
		{"warning", 0, 1},
		{"error", 1, 1},
	}
	for _, tc := range cases {
		report := doctorReport{Status: tc.status}
		if got := doctorExitCode(report, false); got != tc.want {
			t.Errorf("exit(%s) = %d, want %d", tc.status, got, tc.want)
		}
		if got := doctorExitCode(report, true); got != tc.strict {
			t.Errorf("exit(%s, strict) = %d, want %d", tc.status, got, tc.strict)
		}
	}
}

// TestDoctorReportsHandshakeFindings walks the four handshake decisions through
// the report, so the wiring between Compare and the checks cannot rot.
func TestDoctorReportsHandshakeFindings(t *testing.T) {
	cases := map[string]struct {
		daemon        func(client handshake.Record) handshake.Record
		wantHandshake string
		wantEnv       string
	}{
		"match":             {func(c handshake.Record) handshake.Record { return c }, "ok", "ok"},
		"build mismatch":    {func(c handshake.Record) handshake.Record { c.Version = "0.9.1"; return c }, "warning", "ok"},
		"protocol mismatch": {func(c handshake.Record) handshake.Record { c.Protocol = handshake.Protocol + 1; return c }, "error", "ok"},
		"env divergence": {func(c handshake.Record) handshake.Record {
			c.ClaudeConfigDir = "/home/x/.claude-ik"
			return c
		}, "ok", "warning"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env := testEnv(t)
			env.daemonPID = func() (int, bool) { return 999, true }
			rec := tc.daemon(env.client)
			rec.PID = 999
			env.daemonRecord = func() *handshake.Record { return &rec }

			checks := byID(t, runDoctor(env))
			if got := checks["daemon-handshake"].Status; got != tc.wantHandshake {
				t.Errorf("daemon-handshake = %q, want %q (%s)", got, tc.wantHandshake, checks["daemon-handshake"].Summary)
			}
			if got := checks["daemon-env"].Status; got != tc.wantEnv {
				t.Errorf("daemon-env = %q, want %q (%s)", got, tc.wantEnv, checks["daemon-env"].Summary)
			}
		})
	}
}

// TestDoctorWithNoDaemonRecord: a daemon too old to leave a record — which is
// also what a placed host running a slightly older ty looks like — is reported
// as info, never as a mismatch and never as an error.
func TestDoctorWithNoDaemonRecord(t *testing.T) {
	env := testEnv(t)
	env.daemonPID = func() (int, bool) { return 999, true }
	env.daemonRecord = func() *handshake.Record { return nil }

	checks := byID(t, runDoctor(env))
	if got := checks["daemon"].Status; got != "ok" {
		t.Errorf("daemon = %q, want ok — the process is running", got)
	}
	for _, id := range []string{"daemon-handshake", "daemon-env"} {
		if got := checks[id].Status; got != "info" {
			t.Errorf("%s = %q, want info: %s", id, got, checks[id].Summary)
		}
	}
}

// TestDoctorIsReadOnly is the load-bearing promise: doctor must not create a
// database, must not migrate one, and must not start a daemon.
func TestDoctorIsReadOnly(t *testing.T) {
	t.Run("does not create a missing database", func(t *testing.T) {
		env := testEnv(t)
		env.dbPath = filepath.Join(t.TempDir(), "absent", "tasks.db")

		checks := byID(t, runDoctor(env))
		if got := checks["database"].Status; got != "info" {
			t.Errorf("database = %q, want info for a fresh install: %s", got, checks["database"].Summary)
		}
		if _, err := os.Stat(env.dbPath); !os.IsNotExist(err) {
			t.Fatalf("doctor created %s", env.dbPath)
		}
	})

	t.Run("does not migrate an unstamped database", func(t *testing.T) {
		env := testEnv(t)
		// An older ty's database: schema present, stamp absent.
		database, err := db.Open(env.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`DELETE FROM settings WHERE key = ?`, db.SchemaVersionKey); err != nil {
			t.Fatal(err)
		}
		database.Close()

		checks := byID(t, runDoctor(env))
		if got := checks["db-schema"].Status; got != "info" {
			t.Errorf("db-schema = %q, want info: %s", got, checks["db-schema"].Summary)
		}

		reopened, err := db.OpenReadOnly(env.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		if _, stamped := reopened.ReadSchemaVersion(); stamped {
			t.Error("doctor stamped the schema version — it migrated the database")
		}
	})
}

// TestDoctorSchemaVersionVerdicts covers the three ways a database can disagree
// with the running build.
func TestDoctorSchemaVersionVerdicts(t *testing.T) {
	cases := map[string]struct {
		stamp string
		want  string
	}{
		"current": {fmt.Sprint(db.SchemaVersion), "ok"},
		"older":   {fmt.Sprint(db.SchemaVersion - 1), "warning"},
		"newer":   {fmt.Sprint(db.SchemaVersion + 1), "error"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env := testEnv(t)
			database, err := db.Open(env.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := database.SetSetting(db.SchemaVersionKey, tc.stamp); err != nil {
				t.Fatal(err)
			}
			database.Close()

			checks := byID(t, runDoctor(env))
			if got := checks["db-schema"].Status; got != tc.want {
				t.Errorf("db-schema = %q, want %q: %s", got, tc.want, checks["db-schema"].Summary)
			}
		})
	}
}

// TestDoctorChecksLiveTaskHooks: a running task whose generated settings are
// missing a hook event is an error naming the event, because that is exactly a
// status transition that will never fire.
func TestDoctorChecksLiveTaskHooks(t *testing.T) {
	env := testEnv(t)
	worktree := t.TempDir()
	taskID := seedProcessingTask(t, env.dbPath, worktree)

	writeSettings := func(events []string) {
		hooks := map[string]any{}
		for _, e := range events {
			hooks[e] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "ty claude-hook --event " + e}}}}
		}
		data, _ := json.Marshal(map[string]any{"hooks": hooks})
		path := executor.ClaudeSettingsPath(worktree)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("every event present", func(t *testing.T) {
		writeSettings(executor.ClaudeHookEvents)
		c := byID(t, runDoctor(env))["claude-hooks"]
		if c.Status != "ok" {
			t.Errorf("claude-hooks = %q, want ok: %s", c.Status, c.Summary)
		}
		if !strings.Contains(c.Summary, fmt.Sprint(taskID)) {
			t.Errorf("summary %q does not name the task it checked", c.Summary)
		}
	})

	t.Run("a missing event is an error naming it", func(t *testing.T) {
		writeSettings(executor.ClaudeHookEvents[:len(executor.ClaudeHookEvents)-1])
		dropped := executor.ClaudeHookEvents[len(executor.ClaudeHookEvents)-1]
		c := byID(t, runDoctor(env))["claude-hooks"]
		if c.Status != "error" {
			t.Fatalf("claude-hooks = %q, want error: %s", c.Status, c.Summary)
		}
		if !strings.Contains(c.Summary, dropped) {
			t.Errorf("summary %q does not name the missing event %q", c.Summary, dropped)
		}
	})

	t.Run("unparseable settings are an error", func(t *testing.T) {
		if err := os.WriteFile(executor.ClaudeSettingsPath(worktree), []byte("{oops"), 0o644); err != nil {
			t.Fatal(err)
		}
		if c := byID(t, runDoctor(env))["claude-hooks"]; c.Status != "error" {
			t.Errorf("claude-hooks = %q, want error: %s", c.Status, c.Summary)
		}
	})
}

// TestDoctorChecksLiveTaskMCPConfig: without this file the task's agent has no
// taskyou_* tools, so a missing or malformed one is an error.
func TestDoctorChecksLiveTaskMCPConfig(t *testing.T) {
	env := testEnv(t)
	worktree := t.TempDir()
	taskID := seedProcessingTask(t, env.dbPath, worktree)

	path := executor.WorktreeMCPConfigPath(taskID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })

	t.Run("missing", func(t *testing.T) {
		os.Remove(path)
		if c := byID(t, runDoctor(env))["claude-mcp-config"]; c.Status != "error" {
			t.Errorf("claude-mcp-config = %q, want error: %s", c.Status, c.Summary)
		}
	})

	t.Run("present and wired", func(t *testing.T) {
		body := fmt.Sprintf(`{"mcpServers":{"taskyou":{"type":"stdio","command":"ty","args":["mcp-server","--task-id","%d"]}}}`, taskID)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if c := byID(t, runDoctor(env))["claude-mcp-config"]; c.Status != "ok" {
			t.Errorf("claude-mcp-config = %q, want ok: %s", c.Status, c.Summary)
		}
	})

	t.Run("parses but has no taskyou server", func(t *testing.T) {
		if err := os.WriteFile(path, []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if c := byID(t, runDoctor(env))["claude-mcp-config"]; c.Status != "error" {
			t.Errorf("claude-mcp-config = %q, want error: %s", c.Status, c.Summary)
		}
	})
}

// TestDoctorExecutorBinaries: an executor a live task is assigned to, whose CLI
// is not installed, is an error — that task fails the moment it is picked up.
func TestDoctorExecutorBinaries(t *testing.T) {
	env := testEnv(t)
	worktree := t.TempDir()
	seedTask(t, env.dbPath, &db.Task{
		Title:        "codex work",
		Status:       db.StatusQueued,
		Executor:     "codex",
		WorktreePath: worktree,
	})
	env.lookPath = func(name string) (string, error) {
		if name == "codex" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}

	c := byID(t, runDoctor(env))["executors"]
	if c.Status != "error" {
		t.Fatalf("executors = %q, want error: %s", c.Status, c.Summary)
	}
	if !strings.Contains(c.Summary, "codex") {
		t.Errorf("summary %q does not name the missing executor", c.Summary)
	}
	if !strings.Contains(c.Details, "claude") {
		t.Errorf("details %q does not list the default executor as configured", c.Details)
	}
}

// TestDoctorIgnoresPlacedTasks: a task running on another host keeps its hooks
// and MCP config over there, so it must not be sampled here.
func TestDoctorIgnoresPlacedTasks(t *testing.T) {
	env := testEnv(t)
	seedTask(t, env.dbPath, &db.Task{
		Title:           "runs elsewhere",
		Status:          db.StatusProcessing,
		Executor:        db.ExecutorClaude,
		WorktreePath:    t.TempDir(),
		PlacementTarget: "ik-agents",
	})
	checks := byID(t, runDoctor(env))
	for _, id := range []string{"claude-hooks", "claude-mcp-config"} {
		if got := checks[id].Status; got != "info" {
			t.Errorf("%s = %q, want info — a placed task's files are on another host: %s", id, got, checks[id].Summary)
		}
	}
}

// TestDoctorAgentServerNotRunning: a tmux server nothing has started yet is
// info. Doctor will not start one, and must not call that a failure.
func TestDoctorAgentServerNotRunning(t *testing.T) {
	env := testEnv(t)
	env.runTmux = func(ctx context.Context, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "-V" {
			return []byte("tmux 3.4\n"), nil
		}
		return nil, errors.New("no server running on /tmp/tmux-1000/taskyou")
	}
	c := byID(t, runDoctor(env))["tmux-agent-server"]
	if c.Status != "info" {
		t.Errorf("tmux-agent-server = %q, want info: %s", c.Status, c.Summary)
	}
	if !strings.Contains(c.Summary, "taskyou") {
		t.Errorf("summary %q does not name the server it asked", c.Summary)
	}
}

// TestDoctorWarnsOnTwoDaemonSessions: two task-daemon-* sessions on the agent
// server means two ty instances have been running agents there, which is the
// shape of the "old daemon is still alive" incident.
func TestDoctorWarnsOnTwoDaemonSessions(t *testing.T) {
	env := testEnv(t)
	env.runTmux = func(ctx context.Context, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "-V" {
			return []byte("tmux 3.4\n"), nil
		}
		return []byte("task-daemon-111\ntask-daemon-222\ntask-ui-333\n"), nil
	}
	c := byID(t, runDoctor(env))["tmux-agent-server"]
	if c.Status != "warning" {
		t.Fatalf("tmux-agent-server = %q, want warning: %s", c.Status, c.Summary)
	}
	for _, want := range []string{"task-daemon-111", "task-daemon-222"} {
		if !strings.Contains(c.Details, want) {
			t.Errorf("details %q does not name %q", c.Details, want)
		}
	}
}

// TestDoctorGitHubCheckSurvives: the original doctor's only check still runs,
// still folds several findings into one, and still carries the worst severity.
func TestDoctorGitHubCheckSurvives(t *testing.T) {
	env := testEnv(t)
	env.checkAuth = func(context.Context) github.AuthStatus {
		return github.AuthStatus{GHInstalled: false}
	}
	c := byID(t, runDoctor(env))["github-auth"]
	if c.Status != "error" {
		t.Errorf("github-auth = %q, want error: %s", c.Status, c.Summary)
	}
	if !strings.Contains(c.Summary, "not installed") {
		t.Errorf("summary %q lost the original wording", c.Summary)
	}

	env.checkAuth = func(context.Context) github.AuthStatus {
		s := healthyAuth()
		s.AccountType = github.AccountTypePersonal
		s.Account = "someone"
		return s
	}
	if c := byID(t, runDoctor(env))["github-auth"]; c.Status != "warning" {
		t.Errorf("personal account = %q, want warning: %s", c.Status, c.Summary)
	}
}

// TestRenderDoctorPrintsEveryCheck: the human output must not hide a check that
// the JSON reports, or someone will trust a clean screen over a dirty report.
func TestRenderDoctorPrintsEveryCheck(t *testing.T) {
	report := runDoctor(testEnv(t))
	var out strings.Builder
	renderDoctor(&out, report)
	text := out.String()
	for _, c := range report.Checks {
		if !strings.Contains(text, c.ID) {
			t.Errorf("rendered output omits check %q", c.ID)
		}
	}
	if !strings.Contains(text, "doctor only reports") {
		t.Error("rendered output does not say that nothing was changed")
	}
}

// ---- seeding helpers --------------------------------------------------------

func seedTask(t *testing.T, dbPath string, task *db.Task) int64 {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open seed database: %v", err)
	}
	defer database.Close()
	if task.Type == "" {
		task.Type = db.TypeCode
	}
	if task.Project == "" {
		task.Project = "personal"
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.Status != db.StatusBacklog {
		if _, err := database.Exec(`UPDATE tasks SET status = ?, worktree_path = ?, placement_target = ?, updated_at = ? WHERE id = ?`,
			task.Status, task.WorktreePath, task.PlacementTarget, time.Now(), task.ID); err != nil {
			t.Fatalf("set seeded task state: %v", err)
		}
	}
	return task.ID
}

func seedProcessingTask(t *testing.T, dbPath, worktree string) int64 {
	t.Helper()
	return seedTask(t, dbPath, &db.Task{
		Title:        "a running task",
		Status:       db.StatusProcessing,
		Executor:     db.ExecutorClaude,
		WorktreePath: worktree,
	})
}
