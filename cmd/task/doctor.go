package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/github"
	"github.com/bborn/workflow/internal/handshake"
	"github.com/bborn/workflow/internal/tmuxctl"
)

// doctorCheck is one diagnosis. The JSON tags are the contract for
// `ty doctor --json`: ids and field names are stable, so a fleet sweep can
// grep for a check by id and not by the wording of its summary.
type doctorCheck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`  // ok | info | warning | error
	Summary string `json:"summary"` // one line, always present
	Details string `json:"details"` // may be empty, never omitted
}

// doctorReport is the whole diagnosis. Status is the worst check's status.
type doctorReport struct {
	Status string        `json:"status"`
	Checks []doctorCheck `json:"checks"`
}

// Check ids. Everything ty ships that reads a doctor report — and everything a
// user greps for — goes through these, so adding a check is an additive change
// and renaming one is not.
const (
	checkGitHubAuth        = "github-auth"
	checkDaemon            = "daemon"
	checkDaemonHandshake   = handshake.CheckBuild
	checkDaemonEnv         = handshake.CheckEnv
	checkTmux              = "tmux"
	checkTmuxAgentServer   = "tmux-agent-server"
	checkClaudeHooks       = "claude-hooks"
	checkClaudeMCPConfig   = "claude-mcp-config"
	checkDatabase          = "database"
	checkSchema            = "db-schema"
	checkStatusConsistency = "status-consistency"
	checkExecutors         = "executors"
)

// doctorEnv is everything doctor touches outside its own process, gathered in
// one place so a test can run the whole report without a daemon, a tmux server,
// a gh login or the user's real database.
//
// It is also the read-only boundary. Doctor's contract is that it reports and
// never repairs: it opens the database read-only (so it cannot migrate it),
// never starts or stops the daemon, and never writes a config. Every hook here
// is a reader.
type doctorEnv struct {
	ctx context.Context

	client  handshake.Record // what this binary is
	dbPath  string
	pidFile string

	// daemonRecord returns the running daemon's record, or nil for "no daemon
	// record to trust" (see readDaemonRecord).
	daemonRecord func() *handshake.Record
	// daemonPID reports the live daemon's pid.
	daemonPID func() (int, bool)

	checkAuth func(context.Context) github.AuthStatus
	lookPath  func(string) (string, error)
	// runTmux runs a tmux command against the agent server.
	runTmux func(ctx context.Context, args ...string) ([]byte, error)
}

// newDoctorEnv wires the real implementations.
func newDoctorEnv(ctx context.Context) doctorEnv {
	return doctorEnv{
		ctx:          ctx,
		client:       currentRecord(),
		dbPath:       db.DefaultPath(),
		pidFile:      getPidFilePath(),
		daemonRecord: readDaemonRecord,
		daemonPID:    daemonRunning,
		checkAuth:    github.CheckAuth,
		lookPath:     osexec.LookPath,
		runTmux: func(ctx context.Context, args ...string) ([]byte, error) {
			return tmuxctl.Agent(ctx, args...).Output()
		},
	}
}

// runDoctor performs every check and folds them into a report. It has no side
// effects beyond reading, so tests call it directly.
func runDoctor(env doctorEnv) doctorReport {
	checks := []doctorCheck{
		checkGitHub(env),
		checkDaemonProcess(env),
	}
	checks = append(checks, checkHandshakeAgainstDaemon(env)...)
	checks = append(checks,
		checkTmuxPresent(env),
		checkAgentServer(env),
	)
	// The database checks hand their open handle to the checks that need one,
	// so a doctor run opens the file once, read-only.
	checks = append(checks, checkDatabaseGroup(env)...)

	return doctorReport{Status: string(overallStatus(checks)), Checks: checks}
}

// overallStatus folds the checks into one verdict: the worst of them, except
// that info never counts against the install.
//
// info means "a fact you might want, nothing to do" — no daemon record because
// the daemon is older, no active task to inspect hooks against, no agent tmux
// server because nothing has started one. Letting those set the top-level
// status would mean a perfectly healthy machine never reports "ok", and a
// status nobody can ever get clean is a status nobody reads. So the top level
// is only ever ok, warning or error.
func overallStatus(checks []doctorCheck) handshake.Severity {
	worst := handshake.SeverityOK
	for _, c := range checks {
		if s := handshake.Severity(c.Status); s == handshake.SeverityWarning || s == handshake.SeverityError {
			worst = handshake.Worst(worst, s)
		}
	}
	return worst
}

// ---- GitHub -----------------------------------------------------------------

// checkGitHub keeps the original doctor: the gh auth and rate-limit probe, whose
// several findings fold into this one check at their worst severity. The
// individual findings survive in Details, which is what a human reads anyway.
func checkGitHub(env doctorEnv) doctorCheck {
	status := env.checkAuth(env.ctx)
	findings := status.Findings()

	worst := handshake.SeverityOK
	var summaries, details []string
	for _, f := range findings {
		worst = handshake.Worst(worst, githubSeverity(f.Severity))
		summaries = append(summaries, f.Message)
		if f.Detail != "" {
			details = append(details, f.Message+": "+f.Detail)
		}
	}
	if status.Err != nil {
		worst = handshake.Worst(worst, handshake.SeverityWarning)
		details = append(details, "probe error: "+status.Err.Error())
	}
	summary := strings.Join(summaries, "; ")
	if summary == "" {
		summary = "no GitHub auth findings"
	}
	return doctorCheck{
		ID:      checkGitHubAuth,
		Status:  string(worst),
		Summary: summary,
		Details: strings.Join(details, "\n"),
	}
}

// githubSeverity maps the github package's own scale onto doctor's. They are
// deliberately separate types: github.SeverityWarn predates doctor and renaming
// it would churn an unrelated package.
func githubSeverity(s github.Severity) handshake.Severity {
	switch s {
	case github.SeverityError:
		return handshake.SeverityError
	case github.SeverityWarn:
		return handshake.SeverityWarning
	default:
		return handshake.SeverityOK
	}
}

// ---- Daemon -----------------------------------------------------------------

// checkDaemonProcess answers "is anything running my tasks".
//
// A stopped daemon is a warning, not an error: plenty of ty is useful without
// one, and every path that needs a daemon starts one. Doctor itself must not —
// that is the one thing it is forbidden to do.
func checkDaemonProcess(env doctorEnv) doctorCheck {
	pid, running := env.daemonPID()
	if !running {
		return doctorCheck{
			ID:      checkDaemon,
			Status:  string(handshake.SeverityWarning),
			Summary: "daemon is not running — queued tasks will not execute",
			Details: fmt.Sprintf("No live process for the pid file at %s. Start it with `ty daemon` or `ty restart`. "+
				"(doctor never starts anything itself.)", env.pidFile),
		}
	}
	details := fmt.Sprintf("pid %d, pid file %s.", pid, env.pidFile)
	if rec := env.daemonRecord(); rec != nil {
		if !rec.StartedAt.IsZero() {
			details += fmt.Sprintf(" Up since %s.", rec.StartedAt.Local().Format(time.RFC1123))
		}
		if rec.Executable != "" {
			details += " Running " + rec.Executable + "."
		}
	}
	return doctorCheck{
		ID:      checkDaemon,
		Status:  string(handshake.SeverityOK),
		Summary: fmt.Sprintf("daemon is running (pid %d)", pid),
		Details: details,
	}
}

// checkHandshakeAgainstDaemon turns the build/environment comparison into
// checks. It always emits both ids, so the JSON shape does not depend on
// whether a daemon happens to be up.
func checkHandshakeAgainstDaemon(env doctorEnv) []doctorCheck {
	_, running := env.daemonPID()
	if !running {
		return []doctorCheck{
			{
				ID:      checkDaemonHandshake,
				Status:  string(handshake.SeverityInfo),
				Summary: fmt.Sprintf("no daemon to compare against — this binary is build %s (protocol %d)", describeVersion(env.client.Version), env.client.Protocol),
				Details: "",
			},
			{
				ID:      checkDaemonEnv,
				Status:  string(handshake.SeverityInfo),
				Summary: "no daemon to compare environments with",
				Details: fmt.Sprintf("This process: claude config dir %s, tmux server %s, database %s.",
					env.client.ClaudeConfigDir, describeTmuxServer(env.client.TmuxSocket), env.client.DBPath),
			},
		}
	}
	var out []doctorCheck
	for _, f := range handshake.Compare(env.daemonRecord(), env.client) {
		out = append(out, doctorCheck{
			ID:      f.ID,
			Status:  string(f.Severity),
			Summary: f.Summary,
			Details: f.Detail,
		})
	}
	return out
}

func describeVersion(v string) string {
	if v == "" {
		return "(unknown)"
	}
	return v
}

func describeTmuxServer(socket string) string {
	if socket == "" {
		return "default"
	}
	return socket
}

// ---- tmux -------------------------------------------------------------------

// checkTmuxPresent verifies tmux is installed and reports its version. Without
// it nothing executes at all, so a missing tmux is an error.
func checkTmuxPresent(env doctorEnv) doctorCheck {
	path, err := env.lookPath("tmux")
	if err != nil {
		return doctorCheck{
			ID:      checkTmux,
			Status:  string(handshake.SeverityError),
			Summary: "tmux is not installed",
			Details: "Every agent runs in a tmux window; without tmux no task can execute. Install it with your package manager.",
		}
	}
	out, err := env.runTmux(env.ctx, "-V")
	if err != nil {
		return doctorCheck{
			ID:      checkTmux,
			Status:  string(handshake.SeverityWarning),
			Summary: "tmux is installed but `tmux -V` failed",
			Details: fmt.Sprintf("%s: %v", path, err),
		}
	}
	return doctorCheck{
		ID:      checkTmux,
		Status:  string(handshake.SeverityOK),
		Summary: "tmux " + strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "tmux ")),
		Details: path,
	}
}

// checkAgentServer asks the agent server — the private `tmux -L taskyou` one on
// a fresh install — to list its sessions.
//
// "Not running" is normal and reported as info: the server starts with the
// first agent, and doctor will not start one. What matters is that it is
// reachable when it exists, and that doctor names which server it asked, since
// asking the wrong one is exactly how two builds lose each other's agents.
func checkAgentServer(env doctorEnv) doctorCheck {
	server := describeTmuxServer(env.client.TmuxSocket)
	if _, err := env.lookPath("tmux"); err != nil {
		return doctorCheck{
			ID:      checkTmuxAgentServer,
			Status:  string(handshake.SeverityError),
			Summary: "cannot reach the agent tmux server: tmux is not installed",
			Details: "agent server: " + server,
		}
	}
	out, err := env.runTmux(env.ctx, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		// tmux exits non-zero with "no server running on ..." when nothing has
		// started it yet. That is a fact about the machine, not a fault.
		return doctorCheck{
			ID:      checkTmuxAgentServer,
			Status:  string(handshake.SeverityInfo),
			Summary: fmt.Sprintf("agent tmux server %q is not running", server),
			Details: "It starts with the first agent. Nothing to fix.",
		}
	}
	var sessions, daemons []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sessions = append(sessions, line)
		if strings.HasPrefix(line, "task-daemon-") {
			daemons = append(daemons, line)
		}
	}
	details := fmt.Sprintf("%d session(s): %s", len(sessions), strings.Join(sessions, ", "))
	if len(daemons) > 1 {
		return doctorCheck{
			ID:      checkTmuxAgentServer,
			Status:  string(handshake.SeverityWarning),
			Summary: fmt.Sprintf("agent tmux server %q holds %d daemon sessions", server, len(daemons)),
			Details: "More than one task-daemon-* session means two ty instances have been running agents here: " +
				strings.Join(daemons, ", ") + ". " + details,
		}
	}
	return doctorCheck{
		ID:      checkTmuxAgentServer,
		Status:  string(handshake.SeverityOK),
		Summary: fmt.Sprintf("agent tmux server %q is reachable (%d session(s))", server, len(sessions)),
		Details: details,
	}
}

// ---- Database, schema, hooks, executors ------------------------------------

// checkDatabaseGroup runs everything that needs the database, opening it once,
// read-only. If it cannot be opened, the dependent checks say so rather than
// disappearing — a missing check id is much harder to read than a stated one.
func checkDatabaseGroup(env doctorEnv) []doctorCheck {
	database, err := db.OpenReadOnly(env.dbPath)
	if err != nil {
		// A file that is not there yet is a fresh install, not a fault: ty
		// creates the database on first use, and doctor will not do it for it.
		// Anything else — unreadable, corrupt, wrong permissions — is an error.
		reason := err.Error()
		severity := handshake.SeverityError
		summary := "cannot open the task database"
		if os.IsNotExist(err) {
			reason = "It is created on first use; nothing has used ty on this machine yet."
			severity = handshake.SeverityInfo
			summary = "no task database yet at " + env.dbPath
		}
		blocked := func(id string) doctorCheck {
			return doctorCheck{
				ID:      id,
				Status:  string(handshake.SeverityInfo),
				Summary: "not checked — the database could not be opened",
				Details: reason,
			}
		}
		return []doctorCheck{
			{
				ID:      checkDatabase,
				Status:  string(severity),
				Summary: summary,
				Details: reason,
			},
			blocked(checkSchema),
			blocked(checkStatusConsistency),
			blocked(checkClaudeHooks),
			blocked(checkClaudeMCPConfig),
			blocked(checkExecutors),
		}
	}
	defer database.Close()

	sample, sampleErr := sampleActiveTask(database)
	return []doctorCheck{
		{
			ID:      checkDatabase,
			Status:  string(handshake.SeverityOK),
			Summary: "task database opens read-only",
			Details: env.dbPath,
		},
		checkSchemaVersion(database),
		checkConsistency(database),
		checkHooksForTask(sample, sampleErr),
		checkMCPConfigForTask(sample, sampleErr),
		checkExecutorBinaries(env, database),
	}
}

// checkSchemaVersion compares the stamp in the file against this build's
// db.SchemaVersion.
//
// An unstamped database is info, not a warning: every ty older than the stamp
// leaves one, and simply running any normal ty command migrates and stamps it.
func checkSchemaVersion(database *db.DB) doctorCheck {
	got, ok := database.ReadSchemaVersion()
	switch {
	case !ok:
		return doctorCheck{
			ID:      checkSchema,
			Status:  string(handshake.SeverityInfo),
			Summary: fmt.Sprintf("database records no schema version; this build expects %d", db.SchemaVersion),
			Details: "It was last opened by a ty from before schema stamping. Any normal ty command migrates and stamps it; doctor will not, because it opens the database read-only.",
		}
	case got < db.SchemaVersion:
		return doctorCheck{
			ID:      checkSchema,
			Status:  string(handshake.SeverityWarning),
			Summary: fmt.Sprintf("database is at schema %d, this build expects %d", got, db.SchemaVersion),
			Details: "An older ty last migrated this file. Run any ty command with this build (or `ty restart`) to bring it up.",
		}
	case got > db.SchemaVersion:
		return doctorCheck{
			ID:      checkSchema,
			Status:  string(handshake.SeverityError),
			Summary: fmt.Sprintf("database is at schema %d, newer than this build's %d", got, db.SchemaVersion),
			Details: "A newer ty has migrated this database. This binary may not understand rows it writes — upgrade with `ty upgrade`.",
		}
	default:
		return doctorCheck{
			ID:      checkSchema,
			Status:  string(handshake.SeverityOK),
			Summary: fmt.Sprintf("database schema is current (version %d)", got),
		}
	}
}

// checkConsistency is `ty debug status-consistency` as a check: every task's
// cached status must equal the fold over its status log.
func checkConsistency(database *db.DB) doctorCheck {
	mismatches, err := database.CheckStatusConsistency()
	if err != nil {
		return doctorCheck{
			ID:      checkStatusConsistency,
			Status:  string(handshake.SeverityWarning),
			Summary: "could not check status-log consistency",
			Details: err.Error(),
		}
	}
	total, _ := database.CountStatusEvents()
	if len(mismatches) == 0 {
		return doctorCheck{
			ID:      checkStatusConsistency,
			Status:  string(handshake.SeverityOK),
			Summary: fmt.Sprintf("every task's status matches its log (%d event(s))", total),
		}
	}
	var lines []string
	for _, m := range mismatches {
		lines = append(lines, m.String())
	}
	return doctorCheck{
		ID:      checkStatusConsistency,
		Status:  string(handshake.SeverityError),
		Summary: fmt.Sprintf("%d task(s) disagree with their status log", len(mismatches)),
		Details: strings.Join(lines, "\n") + "\nA mismatch means tasks.status was written outside SetTaskStatus. See `ty debug status-log <id>`.",
	}
}

// sampleActiveTask picks one running task with a worktree on this machine: the
// only kind whose generated Claude settings and MCP config are supposed to
// exist right now. Placed tasks are skipped — their files live on another host.
func sampleActiveTask(database *db.DB) (*db.Task, error) {
	for _, status := range []string{db.StatusProcessing, db.StatusBlocked} {
		tasks, err := database.ListTasks(db.ListTasksOptions{Status: status, Limit: 50})
		if err != nil {
			return nil, err
		}
		for _, t := range tasks {
			if t.PlacementTarget != "" || t.WorktreePath == "" {
				continue
			}
			if t.Executor != "" && t.Executor != db.ExecutorClaude {
				continue // only the Claude executor installs these files
			}
			if _, err := os.Stat(t.WorktreePath); err != nil {
				continue
			}
			return t, nil
		}
	}
	return nil, nil
}

// checkHooksForTask verifies that the settings ty generated for a live task
// really do carry every hook event in executor.ClaudeHookEvents.
//
// This is the check that catches the daemon and the TUI drifting apart: they
// each build this file, and a missing event is a status transition that never
// happens — a task stuck on "processing" forever with a finished agent in it.
func checkHooksForTask(task *db.Task, sampleErr error) doctorCheck {
	want := strings.Join(executor.ClaudeHookEvents, ", ")
	if sampleErr != nil {
		return doctorCheck{
			ID:      checkClaudeHooks,
			Status:  string(handshake.SeverityWarning),
			Summary: "could not look for an active task to check hooks against",
			Details: sampleErr.Error(),
		}
	}
	if task == nil {
		return doctorCheck{
			ID:      checkClaudeHooks,
			Status:  string(handshake.SeverityInfo),
			Summary: "no active Claude task to check generated hooks against",
			Details: "Expected events when one is running: " + want + ".",
		}
	}
	path := executor.ClaudeSettingsPath(task.WorktreePath)
	data, err := os.ReadFile(path)
	if err != nil {
		return doctorCheck{
			ID:      checkClaudeHooks,
			Status:  string(handshake.SeverityError),
			Summary: fmt.Sprintf("task #%d has no generated Claude settings", task.ID),
			Details: fmt.Sprintf("%s: %v. Without it no hook fires and the task's status stops moving.", path, err),
		}
	}
	var settings struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return doctorCheck{
			ID:      checkClaudeHooks,
			Status:  string(handshake.SeverityError),
			Summary: fmt.Sprintf("task #%d's Claude settings do not parse", task.ID),
			Details: fmt.Sprintf("%s: %v", path, err),
		}
	}
	var missing []string
	for _, event := range executor.ClaudeHookEvents {
		if _, ok := settings.Hooks[event]; !ok {
			missing = append(missing, event)
		}
	}
	if len(missing) > 0 {
		return doctorCheck{
			ID:      checkClaudeHooks,
			Status:  string(handshake.SeverityError),
			Summary: fmt.Sprintf("task #%d is missing hook event(s): %s", task.ID, strings.Join(missing, ", ")),
			Details: fmt.Sprintf("%s carries %s; this build expects %s. A task whose hooks were written by another "+
				"build loses the status transitions those events carry — run `ty restart` and restart the task.",
				path, strings.Join(sortedKeys(settings.Hooks), ", "), want),
		}
	}
	return doctorCheck{
		ID:      checkClaudeHooks,
		Status:  string(handshake.SeverityOK),
		Summary: fmt.Sprintf("task #%d's generated hooks cover every expected event", task.ID),
		Details: path + ": " + want,
	}
}

// checkMCPConfigForTask verifies the per-task MCP config exists and parses with
// the taskyou server in it — the file that gives an agent its taskyou_* tools.
func checkMCPConfigForTask(task *db.Task, sampleErr error) doctorCheck {
	if sampleErr != nil || task == nil {
		return doctorCheck{
			ID:      checkClaudeMCPConfig,
			Status:  string(handshake.SeverityInfo),
			Summary: "no active Claude task to check the MCP config of",
			Details: "",
		}
	}
	path := executor.WorktreeMCPConfigPath(task.ID)
	data, err := os.ReadFile(path)
	if err != nil {
		return doctorCheck{
			ID:      checkClaudeMCPConfig,
			Status:  string(handshake.SeverityError),
			Summary: fmt.Sprintf("task #%d has no MCP config file", task.ID),
			Details: fmt.Sprintf("%s: %v. Its agent has no taskyou_* tools, so it cannot complete or hand off its own task.", path, err),
		}
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return doctorCheck{
			ID:      checkClaudeMCPConfig,
			Status:  string(handshake.SeverityError),
			Summary: fmt.Sprintf("task #%d's MCP config does not parse", task.ID),
			Details: fmt.Sprintf("%s: %v", path, err),
		}
	}
	server, ok := cfg.MCPServers["taskyou"]
	if !ok {
		return doctorCheck{
			ID:      checkClaudeMCPConfig,
			Status:  string(handshake.SeverityError),
			Summary: fmt.Sprintf("task #%d's MCP config has no taskyou server", task.ID),
			Details: path + " parses but declares: " + strings.Join(sortedServerNames(cfg.MCPServers), ", "),
		}
	}
	return doctorCheck{
		ID:      checkClaudeMCPConfig,
		Status:  string(handshake.SeverityOK),
		Summary: fmt.Sprintf("task #%d's MCP config parses and wires the taskyou server", task.ID),
		Details: fmt.Sprintf("%s: %s %s", path, server.Command, strings.Join(server.Args, " ")),
	}
}

// checkExecutorBinaries reports which executor CLIs are actually on PATH.
//
// "Configured" means the ones this install will really try to run: the default
// executor plus every executor named by a task that is not finished. Reporting
// on all eight built-ins would warn about tools the user has deliberately never
// installed, which is noise rather than diagnosis.
func checkExecutorBinaries(env doctorEnv, database *db.DB) doctorCheck {
	wanted := configuredExecutors(database)

	var present, missing []string
	for _, name := range wanted {
		bins := executor.ExecutorBinaries(name)
		if len(bins) == 0 {
			missing = append(missing, name+" (unknown executor)")
			continue
		}
		found := ""
		for _, bin := range bins {
			if path, err := env.lookPath(bin); err == nil {
				found = path
				break
			}
		}
		if found != "" {
			present = append(present, name+" ("+found+")")
		} else {
			missing = append(missing, name+" ("+strings.Join(bins, " or ")+")")
		}
	}
	details := fmt.Sprintf("configured: %s. found: %s.", strings.Join(wanted, ", "), orNoneList(strings.Join(present, ", ")))
	if len(missing) == 0 {
		return doctorCheck{
			ID:      checkExecutors,
			Status:  string(handshake.SeverityOK),
			Summary: fmt.Sprintf("every configured executor is on PATH (%s)", strings.Join(wanted, ", ")),
			Details: details,
		}
	}
	return doctorCheck{
		ID:      checkExecutors,
		Status:  string(handshake.SeverityError),
		Summary: "executor binary not on PATH: " + strings.Join(missing, ", "),
		Details: details + " A task assigned to a missing executor fails the moment the daemon picks it up.",
	}
}

// configuredExecutors is the default executor plus every executor named by an
// unfinished task, deduplicated and ordered so the report reads the same twice.
func configuredExecutors(database *db.DB) []string {
	seen := map[string]bool{db.DefaultExecutor(): true}
	rows, err := database.Query(`
		SELECT DISTINCT COALESCE(executor, '') FROM tasks
		WHERE deleted_at IS NULL AND status NOT IN ('done', 'archived')`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil && strings.TrimSpace(name) != "" {
				seen[name] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// orNoneList renders an empty list as the word "none" rather than nothing at
// all, so a report never trails off mid-sentence.
func orNoneList(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedServerNames(m map[string]struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- Rendering and the command ---------------------------------------------

// renderDoctor writes the human report.
func renderDoctor(w io.Writer, report doctorReport) {
	fmt.Fprintln(w, boldStyle.Render("TaskYou Doctor"))
	fmt.Fprintln(w, dimStyle.Render(fmt.Sprintf("build %s · protocol %d · %s",
		describeVersion(version), handshake.Protocol, filepath.Base(db.DefaultPath()))))
	fmt.Fprintln(w)

	for _, c := range report.Checks {
		icon, style := doctorIcon(handshake.Severity(c.Status))
		fmt.Fprintf(w, "%s %s %s\n", icon, dimStyle.Render(fmt.Sprintf("%-18s", c.ID)), style(c.Summary))
		if c.Details != "" {
			for _, line := range strings.Split(c.Details, "\n") {
				fmt.Fprintln(w, dimStyle.Render("     "+line))
			}
		}
	}

	fmt.Fprintln(w)
	switch handshake.Severity(report.Status) {
	case handshake.SeverityError:
		fmt.Fprintln(w, errorStyle.Render("Problems found. Nothing was changed — doctor only reports."))
	case handshake.SeverityWarning:
		fmt.Fprintln(w, warnStyle.Render("Warnings found. Nothing was changed — doctor only reports."))
	default:
		fmt.Fprintln(w, successStyle.Render("All checks passed."))
	}
}

// doctorIcon pairs a severity with its glyph and style.
func doctorIcon(s handshake.Severity) (string, func(...string) string) {
	switch s {
	case handshake.SeverityError:
		return errorStyle.Render("✗"), errorStyle.Render
	case handshake.SeverityWarning:
		return warnStyle.Render("⚠"), warnStyle.Render
	case handshake.SeverityInfo:
		return dimStyle.Render("·"), func(s ...string) string { return strings.Join(s, " ") }
	default:
		return successStyle.Render("✓"), func(s ...string) string { return strings.Join(s, " ") }
	}
}

// doctorExitCode is the exit status for a report: non-zero on errors always,
// and on warnings too under --strict, so a fleet sweep can act on them.
func doctorExitCode(report doctorReport, strict bool) int {
	switch handshake.Severity(report.Status) {
	case handshake.SeverityError:
		return 1
	case handshake.SeverityWarning:
		if strict {
			return 1
		}
	}
	return 0
}

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose this ty install, read-only",
		Long: `Check everything a working ty install depends on, and change none of it.

Doctor never starts, stops or repairs anything: it opens the database
read-only, refuses to launch a daemon or a tmux server, and reports what it
finds so you can decide. Each check is ok, info, warning or error:

  github-auth         gh installed, logged in, and not sharing a personal
                      account's 5,000 pt/hr GraphQL bucket across servers
  daemon              is a daemon running, and which process
  daemon-handshake    its build and protocol against this binary — the check
                      that catches a fix you built but never restarted into
  daemon-env          whether the daemon resolved the same claude config dir,
                      database and tmux server this process did
  tmux                tmux present, and its version
  tmux-agent-server   the private agent server (tmux -L taskyou) is reachable
  claude-hooks        a live task's generated settings carry every hook event
  claude-mcp-config   that task's MCP config exists, parses, wires taskyou
  database            the task database opens
  db-schema           its schema version matches this build
  status-consistency  every task's status matches the fold over its status log
  executors           the CLI binaries for configured executors are on PATH

Exits non-zero when any check is an error. Pass --strict to exit non-zero on
warnings too, so a fleet sweep can act on them:

  for s in host1 host2; do ssh $s ty doctor --strict || echo "$s unhealthy"; done

Pass --json for a stable machine-readable report:

  {"status":"warning","checks":[{"id":"daemon","status":"ok","summary":"...","details":"..."}]}

The top-level status is the worst check that needs attention, so it is only
ever ok, warning or error: an info check is a note, not a problem, and never
keeps a healthy machine from reporting ok.`,
		Run: func(cmd *cobra.Command, args []string) {
			strict, _ := cmd.Flags().GetBool("strict")
			asJSON, _ := cmd.Flags().GetBool("json")

			report := runDoctor(newDoctorEnv(cmd.Context()))

			if asJSON {
				data, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
					os.Exit(1)
				}
				fmt.Println(string(data))
			} else {
				renderDoctor(os.Stdout, report)
			}

			if code := doctorExitCode(report, strict); code != 0 {
				os.Exit(code)
			}
		},
	}
	cmd.Flags().Bool("strict", false, "Exit non-zero on warnings too (e.g. personal-account auth, a stale daemon), for fleet health sweeps")
	cmd.Flags().Bool("json", false, "Print the report as JSON: {status, checks:[{id,status,summary,details}]}")
	return cmd
}
