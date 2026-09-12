package tmuxctl

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/tmuxtest"
)

// freshInstall points the database (and so the choice file) at an empty temp
// dir, on an isolated tmux server, with no override in the environment, and
// lets Socket use the disk as a real run would.
func freshInstall(t *testing.T) string {
	t.Helper()
	tmuxtest.Isolate(t)
	dir := t.TempDir()
	t.Setenv("WORKTREE_DB_PATH", filepath.Join(dir, "tasks.db"))
	t.Setenv(EnvSocket, "")
	os.Unsetenv(EnvSocket)
	underTest = false
	t.Cleanup(func() { underTest = true })
	return filepath.Join(dir, choiceFileName)
}

// Under `go test`, with no override, Socket must not look at or write the data
// directory: that is the live install's choice.
func TestTestsNeverRecordAChoice(t *testing.T) {
	tmuxtest.Isolate(t)
	dir := t.TempDir()
	t.Setenv("WORKTREE_DB_PATH", filepath.Join(dir, "tasks.db"))
	t.Setenv(EnvSocket, "")
	os.Unsetenv(EnvSocket)
	if got := Socket(); got != "" {
		t.Errorf("Socket() under test = %q, want the default server", got)
	}
	if _, err := os.Stat(filepath.Join(dir, choiceFileName)); !os.IsNotExist(err) {
		t.Errorf("a test run recorded a choice file: %v", err)
	}
}

func TestSocketEnvOverride(t *testing.T) {
	t.Setenv(EnvSocket, "qa")
	if got := Socket(); got != "qa" {
		t.Errorf("Socket() = %q, want qa", got)
	}
	t.Setenv(EnvSocket, "default")
	if got := Socket(); got != "" {
		t.Errorf("Socket() with default = %q, want the default server", got)
	}
	if got := AgentArgs("ls"); strings.Join(got, " ") != "ls" {
		t.Errorf("AgentArgs on the default server = %v", got)
	}
}

func TestFreshInstallChoosesPrivateServerAndRecordsIt(t *testing.T) {
	file := freshInstall(t)
	if got := Socket(); got != PrivateSocket {
		t.Fatalf("Socket() = %q, want %q", got, PrivateSocket)
	}
	b, err := os.ReadFile(file)
	if err != nil || strings.TrimSpace(string(b)) != PrivateSocket {
		t.Errorf("choice file = %q, %v; want %q", b, err, PrivateSocket)
	}
	if got := AgentArgs("ls"); strings.Join(got, " ") != "-L taskyou ls" {
		t.Errorf("AgentArgs = %v", got)
	}
}

// An install whose agents already run on the default server must keep using
// it: switching would hide every running agent and start a second one.
func TestExistingAgentsKeepTheDefaultServer(t *testing.T) {
	file := freshInstall(t)
	if err := exec.Command("tmux", "new-session", "-d", "-s", "task-daemon-1234", "sleep", "60").Run(); err != nil {
		t.Fatalf("create legacy daemon session: %v", err)
	}
	if got := Socket(); got != "" {
		t.Fatalf("Socket() = %q, want the default server", got)
	}
	b, _ := os.ReadFile(file)
	if strings.TrimSpace(string(b)) != "default" {
		t.Errorf("choice file = %q, want default", b)
	}
}

func TestRecordedChoiceSticks(t *testing.T) {
	file := freshInstall(t)
	if err := os.WriteFile(file, []byte("default\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Socket(); got != "" {
		t.Errorf("Socket() = %q, want the recorded default server", got)
	}
}

func TestPaneTagsAndDefaultSize(t *testing.T) {
	got := TagPaneArgs("%5", 42, RoleShell)
	want := [][]string{
		{"set-option", "-p", "-t", "%5", "@ty_task", "42"},
		{"set-option", "-p", "-t", "%5", "@ty_role", "shell"},
	}
	if strings.Join(got[0], " ") != strings.Join(want[0], " ") || strings.Join(got[1], " ") != strings.Join(want[1], " ") {
		t.Errorf("TagPaneArgs = %v, want %v", got, want)
	}
	if DefaultSize() != "200x50" || strings.Join(DefaultSizeArgs(), " ") != "-x 200 -y 50" {
		t.Errorf("default size = %s / %v", DefaultSize(), DefaultSizeArgs())
	}
}

func TestViewAttachScriptTargetsTheAgentServer(t *testing.T) {
	t.Setenv(EnvSocket, "taskyou")
	got := ViewAttachScript("ty-view-9")
	for _, want := range []string{"env -u TMUX -u TMUX_PANE tmux -L 'taskyou' attach-session -t 'ty-view-9'", "destroy-unattached on"} {
		if !strings.Contains(got, want) {
			t.Errorf("ViewAttachScript = %q, missing %q", got, want)
		}
	}
}
