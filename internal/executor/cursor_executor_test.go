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

func TestCursorExecutor_Name(t *testing.T) {
	cursorExec := newTestCursorExecutor(t)
	if cursorExec.Name() != db.ExecutorCursor {
		t.Errorf("Name() = %q, want %q", cursorExec.Name(), db.ExecutorCursor)
	}
}

func TestCursorExecutor_Supports(t *testing.T) {
	cursorExec := newTestCursorExecutor(t)
	if !cursorExec.SupportsSessionResume() {
		t.Error("Cursor should support session resume")
	}
	if !cursorExec.SupportsDangerousMode() {
		t.Error("Cursor should support dangerous mode")
	}
}

func TestCursorWorkspaceHash(t *testing.T) {
	path := "/Users/bruno/Projects/workflow"
	got := cursorWorkspaceHash(path)
	if len(got) != 32 {
		t.Errorf("cursorWorkspaceHash() length = %d, want 32 hex chars, got %q", len(got), got)
	}
	for _, r := range got {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Errorf("cursorWorkspaceHash() = %q, want lowercase hex", got)
			break
		}
	}
	if cursorWorkspaceHash(path) != got {
		t.Error("cursorWorkspaceHash must be deterministic")
	}
	if cursorWorkspaceHash(path+"/other") == got {
		t.Error("different paths must not hash to the same folder name")
	}
}

func TestFindCursorSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CURSOR_CONFIG_DIR", home)

	workDir := "/tmp/proj/.task-worktrees/42-fix"
	group := filepath.Join(home, "chats", cursorWorkspaceHash(workDir))
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

	got := findCursorSessionID(workDir)
	if got != "session-new" {
		t.Errorf("findCursorSessionID() = %q, want session-new", got)
	}
	if !cursorSessionExists("session-new") {
		t.Error("cursorSessionExists(session-new) = false, want true")
	}
	if cursorSessionExists("missing-session") {
		t.Error("cursorSessionExists(missing-session) = true, want false")
	}
}

func TestFindCursorSessionID_MetaJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CURSOR_CONFIG_DIR", home)

	workDir := "/tmp/proj/.task-worktrees/99-meta"
	group := filepath.Join(home, "chats", "not-the-hash")
	if err := os.MkdirAll(filepath.Join(group, "abc-session"), 0755); err != nil {
		t.Fatal(err)
	}
	meta := `{"cwd":"` + workDir + `","updatedAtMs":1700000000000,"title":"fix"}`
	if err := os.WriteFile(filepath.Join(group, "abc-session", "meta.json"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}

	got := findCursorSessionID(workDir)
	if got != "abc-session" {
		t.Errorf("findCursorSessionID via meta.json = %q, want abc-session", got)
	}
}

func TestBuildCursorDangerousFlag(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	t.Setenv("CURSOR_DANGEROUS_ARGS", "")

	if got := buildCursorDangerousFlag(false); got != "" {
		t.Errorf("disabled = %q, want empty", got)
	}
	if got := buildCursorDangerousFlag(true); got != "--force " {
		t.Errorf("enabled = %q, want %q", got, "--force ")
	}

	t.Setenv("WORKTREE_DANGEROUS_MODE", "1")
	if got := buildCursorDangerousFlag(false); got != "--force " {
		t.Errorf("env override = %q, want --force ", got)
	}

	t.Setenv("CURSOR_DANGEROUS_ARGS", "--yolo")
	if got := buildCursorDangerousFlag(true); got != "--yolo " {
		t.Errorf("custom args = %q, want %q", got, "--yolo ")
	}
}

func TestCursorLaunchFlags(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	t.Setenv("CURSOR_DANGEROUS_ARGS", "")

	dangerous := &db.Task{PermissionMode: db.PermissionModeDangerous}
	if got := cursorLaunchFlags(dangerous); got != "--force " {
		t.Errorf("dangerous = %q, want --force ", got)
	}

	auto := &db.Task{PermissionMode: db.PermissionModeAuto}
	if got := cursorLaunchFlags(auto); got != "" {
		t.Errorf("auto = %q, want empty (Cursor has no --permission-mode)", got)
	}

	accept := &db.Task{PermissionMode: db.PermissionModeAcceptEdits}
	if got := cursorLaunchFlags(accept); got != "" {
		t.Errorf("acceptEdits = %q, want empty", got)
	}

	def := &db.Task{PermissionMode: db.PermissionModeDefault}
	if got := cursorLaunchFlags(def); got != "" {
		t.Errorf("default = %q, want empty", got)
	}
}

func TestCursorBuildCommand(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	t.Setenv("CURSOR_DANGEROUS_ARGS", "")
	t.Setenv("WORKTREE_SESSION_ID", "sess")

	cursorExec := newTestCursorExecutor(t)
	task := &db.Task{
		ID:             42,
		Port:           3100,
		WorktreePath:   t.TempDir(),
		PermissionMode: db.PermissionModeDangerous,
	}
	cmd := cursorExec.BuildCommand(task, "sess-id", "")
	for _, want := range []string{
		"WORKTREE_TASK_ID=42",
		"WORKTREE_PORT=3100",
		"WORKTREE_PATH=",
		"--force",
		"--approve-mcps",
		"--resume sess-id",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("BuildCommand missing %q in %q", want, cmd)
		}
	}
	if !cursorCommandInvokesCLI(cmd) {
		t.Errorf("BuildCommand must invoke the Cursor CLI; got %q", cmd)
	}
	if strings.Contains(cmd, "--worktree") {
		t.Errorf("BuildCommand must not pass Cursor --worktree (TaskYou owns worktrees): %q", cmd)
	}

	task.Model = "gpt-5"
	cmd = cursorExec.BuildCommand(task, "", "")
	if !strings.Contains(cmd, "--model 'gpt-5'") && !strings.Contains(cmd, `--model "gpt-5"`) {
		t.Errorf("BuildCommand with model should contain --model gpt-5, got %q", cmd)
	}
}

func TestDetectExecutorIdentityCursor(t *testing.T) {
	t.Setenv("TASK_EXECUTOR", "cursor")
	slug, display := detectExecutorIdentity()
	if slug != "cursor" {
		t.Fatalf("expected slug cursor, got %q", slug)
	}
	if display != "Cursor" {
		t.Fatalf("expected display Cursor, got %q", display)
	}
}

func newTestCursorExecutor(t *testing.T) TaskExecutor {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	ex := New(database, &config.Config{})
	cursorExec := ex.GetExecutor(db.ExecutorCursor)
	if cursorExec == nil {
		t.Fatal("cursor executor not registered")
	}
	return cursorExec
}

func TestCursorBuildCommand_ResumeOnlyWithSessionID(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	cursorExec := newTestCursorExecutor(t)
	task := &db.Task{ID: 9, Port: 3100, WorktreePath: t.TempDir()}

	fresh := cursorExec.BuildCommand(task, "", "")
	if strings.Contains(fresh, "--resume") {
		t.Errorf("BuildCommand with empty sessionID must not pass --resume; got:\n  %s", fresh)
	}

	resumed := cursorExec.BuildCommand(task, "01abc-session", "")
	if !strings.Contains(resumed, "--resume 01abc-session") {
		t.Errorf("BuildCommand with sessionID must pass --resume 01abc-session; got:\n  %s", resumed)
	}
}

func TestCursorBuildCommand_ModelAndDBPath(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	cursorExec := newTestCursorExecutor(t)
	task := &db.Task{
		ID:           11,
		Port:         3100,
		WorktreePath: t.TempDir(),
		EffortLevel:  db.EffortHigh,
		Model:        "composer-1",
	}

	t.Setenv("WORKTREE_DB_PATH", "/tmp/iso/tasks.db")
	cmd := cursorExec.BuildCommand(task, "", "")
	if !strings.Contains(cmd, `WORKTREE_DB_PATH="/tmp/iso/tasks.db"`) {
		t.Errorf("BuildCommand must carry WORKTREE_DB_PATH; got:\n  %s", cmd)
	}
	bin := cursorLaunchBin()
	if di, gi := strings.Index(cmd, "WORKTREE_DB_PATH="), strings.Index(cmd, bin+" "); di < 0 || gi < 0 || di > gi {
		t.Errorf("WORKTREE_DB_PATH must precede `%s`; got:\n  %s", bin, cmd)
	}
	if strings.Contains(cmd, "--effort") {
		t.Errorf("Cursor CLI has no --effort flag; got:\n  %s", cmd)
	}
	if !strings.Contains(cmd, "--model 'composer-1'") && !strings.Contains(cmd, `--model "composer-1"`) {
		t.Errorf("BuildCommand with Model=composer-1 must contain --model composer-1; got:\n  %s", cmd)
	}

	t.Setenv("WORKTREE_DB_PATH", "")
	task.EffortLevel = ""
	task.Model = ""
	def := cursorExec.BuildCommand(task, "", "")
	if strings.Contains(def, "WORKTREE_DB_PATH=") {
		t.Errorf("default instance: BuildCommand must NOT set WORKTREE_DB_PATH; got:\n  %s", def)
	}
	if strings.Contains(def, "--force") {
		t.Errorf("BuildCommand with default permission must not contain --force; got:\n  %s", def)
	}
}

func TestCursorBuildCommand_WritesTaskyouMCPConfig(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	cursorExec := newTestCursorExecutor(t)
	workDir := t.TempDir()
	task := &db.Task{ID: 77, Port: 3100, WorktreePath: workDir}

	cmd := cursorExec.BuildCommand(task, "", "")
	if !cursorCommandInvokesCLI(cmd) {
		t.Fatalf("BuildCommand must invoke the Cursor CLI; got:\n  %s", cmd)
	}

	cfgPath := filepath.Join(workDir, ".cursor", "mcp.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("expected cursor MCP config at %s: %v", cfgPath, err)
	}
	body := string(data)
	if !strings.Contains(body, `"taskyou"`) {
		t.Errorf("config missing taskyou server:\n%s", body)
	}
	if !strings.Contains(body, "mcp-server") {
		t.Errorf("config must invoke mcp-server:\n%s", body)
	}
	if !strings.Contains(body, "--task-id") || !strings.Contains(body, "77") {
		t.Errorf("config must pass --task-id 77:\n%s", body)
	}
}

func TestCursorBuildCommand_MergesExistingMCPConfig(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	cursorExec := newTestCursorExecutor(t)
	workDir := t.TempDir()
	mcpDir := filepath.Join(workDir, ".cursor")
	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatal(err)
	}
	existing := `{
  "mcpServers": {
    "linear": {
      "command": "npx",
      "args": ["-y", "linear-mcp"]
    }
  }
}`
	if err := os.WriteFile(filepath.Join(mcpDir, "mcp.json"), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	task := &db.Task{ID: 12, Port: 3100, WorktreePath: workDir}
	_ = cursorExec.BuildCommand(task, "", "")

	data, err := os.ReadFile(filepath.Join(mcpDir, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, `"linear"`) {
		t.Errorf("merge dropped existing MCP server:\n%s", body)
	}
	if !strings.Contains(body, `"taskyou"`) {
		t.Errorf("merge must add taskyou server:\n%s", body)
	}
}

func TestCursorResumeModeFlags(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")
	t.Setenv("CURSOR_DANGEROUS_ARGS", "")
	task := &db.Task{ID: 3, Port: 3100, WorktreePath: "/tmp/wt", Model: "gpt-5"}

	dangerous := true
	got := cursorCLIFlags(task, "sess-1", &dangerous)
	if !strings.Contains(got, "--force") {
		t.Errorf("ResumeDangerous flags must include --force; got %q", got)
	}
	if !strings.Contains(got, "--resume sess-1") {
		t.Errorf("ResumeDangerous flags must include --resume sess-1; got %q", got)
	}

	safe := false
	got = cursorCLIFlags(task, "sess-1", &safe)
	if strings.Contains(got, "--force") {
		t.Errorf("ResumeSafe flags must not include --force; got %q", got)
	}
	if !strings.Contains(got, "--resume sess-1") {
		t.Errorf("ResumeSafe flags must still include --resume; got %q", got)
	}
}

func cursorCommandInvokesCLI(cmd string) bool {
	return strings.Contains(cmd, " cursor-agent ") || strings.Contains(cmd, " agent ") ||
		strings.HasSuffix(cmd, " cursor-agent") || strings.HasSuffix(cmd, " agent")
}
