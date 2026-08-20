package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

func TestGrokExecutor_Name(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	exec := New(database, &config.Config{})
	grokExec := exec.GetExecutor(db.ExecutorGrok)
	if grokExec == nil {
		t.Fatal("grok executor not registered")
	}
	if grokExec.Name() != db.ExecutorGrok {
		t.Errorf("Name() = %q, want %q", grokExec.Name(), db.ExecutorGrok)
	}
}

func TestGrokExecutor_Supports(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	exec := New(database, &config.Config{})
	grokExec := exec.GetExecutor(db.ExecutorGrok)
	if grokExec == nil {
		t.Fatal("grok executor not registered")
	}
	if !grokExec.SupportsSessionResume() {
		t.Error("Grok should support session resume")
	}
	if !grokExec.SupportsDangerousMode() {
		t.Error("Grok should support dangerous mode")
	}
}

func TestGrokEncodeCwd(t *testing.T) {
	got := grokEncodeCwd("/Users/bruno/Projects/workflow")
	want := "%2FUsers%2Fbruno%2FProjects%2Fworkflow"
	if got != want {
		t.Errorf("grokEncodeCwd() = %q, want %q", got, want)
	}
	if strings.Contains(grokEncodeCwd("/tmp/a b"), "+") {
		t.Errorf("spaces should be %%20, not +: %q", grokEncodeCwd("/tmp/a b"))
	}
	if grokEncodeCwd("/tmp/a b") != "%2Ftmp%2Fa%20b" {
		t.Errorf("grokEncodeCwd space = %q", grokEncodeCwd("/tmp/a b"))
	}
}

func TestFindGrokSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)

	workDir := "/tmp/proj/.task-worktrees/42-fix"
	group := filepath.Join(home, "sessions", grokEncodeCwd(workDir))
	if err := os.MkdirAll(filepath.Join(group, "session-old"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(group, "session-new"), 0755); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(filepath.Join(group, "session-old"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(group, "session-new"), newTime, newTime); err != nil {
		t.Fatal(err)
	}

	got := findGrokSessionID(workDir)
	if got != "session-new" {
		t.Errorf("findGrokSessionID() = %q, want session-new", got)
	}
	if !grokSessionExists("session-new") {
		t.Error("grokSessionExists(session-new) = false, want true")
	}
	if grokSessionExists("missing-session") {
		t.Error("grokSessionExists(missing-session) = true, want false")
	}
}

func TestFindGrokSessionID_CwdFileFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)

	workDir := "/very/long/path/that/would/exceed/the/encoded-name-limit"
	group := filepath.Join(home, "sessions", "slug-hash")
	if err := os.MkdirAll(filepath.Join(group, "abc-session"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(group, ".cwd"), []byte(workDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got := findGrokSessionID(workDir)
	if got != "abc-session" {
		t.Errorf("findGrokSessionID via .cwd = %q, want abc-session", got)
	}
}

func TestBuildGrokDangerousFlag(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	t.Setenv("GROK_DANGEROUS_ARGS", "")

	if got := buildGrokDangerousFlag(false); got != "" {
		t.Errorf("disabled = %q, want empty", got)
	}
	if got := buildGrokDangerousFlag(true); got != "--always-approve " {
		t.Errorf("enabled = %q, want %q", got, "--always-approve ")
	}

	t.Setenv("WORKTREE_DANGEROUS_MODE", "1")
	if got := buildGrokDangerousFlag(false); got != "--always-approve " {
		t.Errorf("env override = %q, want --always-approve ", got)
	}

	t.Setenv("GROK_DANGEROUS_ARGS", "--yolo")
	if got := buildGrokDangerousFlag(true); got != "--yolo " {
		t.Errorf("custom args = %q, want %q", got, "--yolo ")
	}
}

func TestGrokLaunchFlags(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	t.Setenv("GROK_DANGEROUS_ARGS", "")

	dangerous := &db.Task{PermissionMode: db.PermissionModeDangerous}
	if got := grokLaunchFlags(dangerous); got != "--always-approve " {
		t.Errorf("dangerous = %q, want --always-approve ", got)
	}

	auto := &db.Task{PermissionMode: db.PermissionModeAuto}
	if got := grokLaunchFlags(auto); got != "--permission-mode auto " {
		t.Errorf("auto = %q, want --permission-mode auto ", got)
	}

	accept := &db.Task{PermissionMode: db.PermissionModeAcceptEdits}
	if got := grokLaunchFlags(accept); got != "--permission-mode acceptEdits " {
		t.Errorf("acceptEdits = %q, want --permission-mode acceptEdits ", got)
	}

	def := &db.Task{PermissionMode: db.PermissionModeDefault}
	if got := grokLaunchFlags(def); got != "" {
		t.Errorf("default = %q, want empty", got)
	}
}

func TestGrokBuildCommand(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	t.Setenv("GROK_DANGEROUS_ARGS", "")
	t.Setenv("WORKTREE_SESSION_ID", "sess")

	tmpFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	exec := New(database, &config.Config{})
	grokExec := exec.GetExecutor(db.ExecutorGrok)
	if grokExec == nil {
		t.Fatal("grok executor not registered")
	}

	task := &db.Task{
		ID:             42,
		Port:           3100,
		WorktreePath:   t.TempDir(),
		PermissionMode: db.PermissionModeDangerous,
	}
	cmd := grokExec.BuildCommand(task, "sess-id", "")
	for _, want := range []string{
		"WORKTREE_TASK_ID=42",
		"WORKTREE_PORT=3100",
		"WORKTREE_PATH=",
		"GROK_FOLDER_TRUST=0",
		"grok",
		"--always-approve",
		"--resume sess-id",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("BuildCommand missing %q in %q", want, cmd)
		}
	}
	if strings.Contains(cmd, "--worktree") {
		t.Errorf("BuildCommand must not pass grok --worktree (TaskYou owns worktrees): %q", cmd)
	}

	task.Model = "grok-4"
	cmd = grokExec.BuildCommand(task, "", "")
	if !strings.Contains(cmd, "--model 'grok-4'") && !strings.Contains(cmd, `--model "grok-4"`) {
		t.Errorf("BuildCommand with model should contain --model grok-4, got %q", cmd)
	}
}

func TestDetectExecutorIdentityGrok(t *testing.T) {
	t.Setenv("TASK_EXECUTOR", "grok")
	slug, display := detectExecutorIdentity()
	if slug != "grok" {
		t.Fatalf("expected slug grok, got %q", slug)
	}
	if display != "Grok" {
		t.Fatalf("expected display Grok, got %q", display)
	}
}

func newTestGrokExecutor(t *testing.T) TaskExecutor {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	ex := New(database, &config.Config{})
	grokExec := ex.GetExecutor(db.ExecutorGrok)
	if grokExec == nil {
		t.Fatal("grok executor not registered")
	}
	return grokExec
}

// TestGrokBuildCommand_ResumeOnlyWithSessionID drives the shipped BuildCommand:
// --resume must appear only when a session id is supplied.
func TestGrokBuildCommand_ResumeOnlyWithSessionID(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	grokExec := newTestGrokExecutor(t)
	task := &db.Task{ID: 9, Port: 3100, WorktreePath: t.TempDir()}

	fresh := grokExec.BuildCommand(task, "", "")
	if strings.Contains(fresh, "--resume") {
		t.Errorf("BuildCommand with empty sessionID must not pass --resume; got:\n  %s", fresh)
	}

	resumed := grokExec.BuildCommand(task, "01abc-session", "")
	if !strings.Contains(resumed, "--resume 01abc-session") {
		t.Errorf("BuildCommand with sessionID must pass --resume 01abc-session; got:\n  %s", resumed)
	}
}

// TestGrokBuildCommand_EffortAndDBPath asserts Claude-parity env/flags on the
// shipped grok BuildCommand (not a reimplementation).
func TestGrokBuildCommand_EffortAndDBPath(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	grokExec := newTestGrokExecutor(t)
	task := &db.Task{
		ID:           11,
		Port:         3100,
		WorktreePath: t.TempDir(),
		EffortLevel:  db.EffortHigh,
		Model:        "grok-4",
	}

	t.Setenv("WORKTREE_DB_PATH", "/tmp/iso/tasks.db")
	cmd := grokExec.BuildCommand(task, "", "")
	if !strings.Contains(cmd, `WORKTREE_DB_PATH="/tmp/iso/tasks.db"`) {
		t.Errorf("BuildCommand must carry WORKTREE_DB_PATH; got:\n  %s", cmd)
	}
	if di, gi := strings.Index(cmd, "WORKTREE_DB_PATH="), strings.Index(cmd, "grok "); di < 0 || gi < 0 || di > gi {
		t.Errorf("WORKTREE_DB_PATH must precede `grok`; got:\n  %s", cmd)
	}
	if !strings.Contains(cmd, "--effort high") {
		t.Errorf("BuildCommand with EffortLevel=high must contain --effort high; got:\n  %s", cmd)
	}
	if !strings.Contains(cmd, "--model 'grok-4'") && !strings.Contains(cmd, `--model "grok-4"`) {
		t.Errorf("BuildCommand with Model=grok-4 must contain --model grok-4; got:\n  %s", cmd)
	}

	t.Setenv("WORKTREE_DB_PATH", "")
	task.EffortLevel = ""
	task.Model = ""
	def := grokExec.BuildCommand(task, "", "")
	if strings.Contains(def, "WORKTREE_DB_PATH=") {
		t.Errorf("default instance: BuildCommand must NOT set WORKTREE_DB_PATH; got:\n  %s", def)
	}
	if strings.Contains(def, "--effort") {
		t.Errorf("BuildCommand with no effort override must not contain --effort; got:\n  %s", def)
	}
}

// TestGrokBuildCommand_WritesTaskyouMCPConfig verifies BuildCommand writes a
// grok-native project MCP config that points at `ty mcp-server --task-id <id>`
// — the same stdio server Claude gets via --mcp-config.
func TestGrokBuildCommand_WritesTaskyouMCPConfig(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	grokExec := newTestGrokExecutor(t)
	workDir := t.TempDir()
	task := &db.Task{ID: 77, Port: 3100, WorktreePath: workDir}

	cmd := grokExec.BuildCommand(task, "", "")
	if !strings.Contains(cmd, "grok") {
		t.Fatalf("BuildCommand must invoke grok; got:\n  %s", cmd)
	}

	cfgPath := filepath.Join(workDir, ".grok", "config.toml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("expected grok MCP config at %s: %v", cfgPath, err)
	}
	body := string(data)
	if !strings.Contains(body, "[mcp_servers.taskyou]") {
		t.Errorf("config missing [mcp_servers.taskyou]:\n%s", body)
	}
	if !strings.Contains(body, "mcp-server") {
		t.Errorf("config must invoke mcp-server:\n%s", body)
	}
	if !strings.Contains(body, "--task-id") || !strings.Contains(body, "77") {
		t.Errorf("config must pass --task-id 77:\n%s", body)
	}
}

// TestGrokResumeModeFlags covers ResumeDangerous / ResumeSafe: bypass on,
// bypass off. Uses the shared flag helper those methods call.
func TestGrokResumeModeFlags(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	t.Setenv("GROK_DANGEROUS_ARGS", "")
	task := &db.Task{ID: 3, Port: 3100, WorktreePath: "/tmp/wt", EffortLevel: db.EffortLow}

	dangerous := true
	got := grokCLIFlags(task, "sess-1", &dangerous)
	if !strings.Contains(got, "--always-approve") {
		t.Errorf("ResumeDangerous flags must include --always-approve; got %q", got)
	}
	if !strings.Contains(got, "--resume sess-1") {
		t.Errorf("ResumeDangerous flags must include --resume sess-1; got %q", got)
	}

	safe := false
	got = grokCLIFlags(task, "sess-1", &safe)
	if strings.Contains(got, "--always-approve") {
		t.Errorf("ResumeSafe flags must not include --always-approve; got %q", got)
	}
	if !strings.Contains(got, "--resume sess-1") {
		t.Errorf("ResumeSafe flags must still include --resume; got %q", got)
	}
}
