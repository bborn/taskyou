package tmuxctl

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// relayedCopy runs an agent that writes an OSC 52 copy on an inner server, views
// it through a nested client in a pane of an outer server configured as ty
// configures the UI server, and returns what reached the outer server's paste
// buffers. The outer server stands in for the user's terminal: with
// set-clipboard on, tmux keeps an OSC 52 it relays as a buffer.
func relayedCopy(t *testing.T, inner [][]string) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	tmuxtest.Isolate(t)
	in, out := "tyclip-in", "tyclip-out"
	tm := func(sock string, args ...string) *exec.Cmd {
		return exec.Command("tmux", append([]string{"-f", "/dev/null", "-L", sock}, args...)...)
	}
	t.Cleanup(func() {
		tm(out, "kill-server").Run()
		tm(in, "kill-server").Run()
	})

	// The agent waits until a client is attached, then copies.
	agent := `while [ "$(tmux display-message -p '#{session_attached}')" = 0 ]; do sleep 0.1; done; sleep 0.5; ` +
		`printf '\033]52;c;%s\a' "$(printf 'exact text' | base64)"; sleep 30`
	if b, err := tm(in, "new-session", "-d", "-s", "agent", "-x", "80", "-y", "24", agent).CombinedOutput(); err != nil {
		t.Fatalf("inner server: %v: %s", err, b)
	}
	for _, args := range inner {
		if b, err := tm(in, args...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, b)
		}
	}
	if b, err := tm(out, "new-session", "-d", "-s", "ui", "-x", "100", "-y", "30").CombinedOutput(); err != nil {
		t.Fatalf("outer server: %v: %s", err, b)
	}
	if b, err := tm(out, ClipboardRelayArgs()...).CombinedOutput(); err != nil {
		t.Fatalf("outer relay: %v: %s", err, b)
	}
	if b, err := tm(out, "split-window", "-t", "ui", "env -u TMUX tmux -L "+in+" attach -t agent").CombinedOutput(); err != nil {
		t.Fatalf("nested client: %v: %s", err, b)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := tm(out, "show-buffer").Output(); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ""
}

// A copy an application makes inside the agent's tmux — Claude Code's copy, an
// editor's yank, a printf — must come out of it. Under tmux's default it does
// not: the inner server swallows the OSC 52, and the user's clipboard never
// hears of it, which is how a copy in a placed task's pane went missing.
func TestAgentClipboardArgsLetAnAppCopyLeaveTheAgentServer(t *testing.T) {
	if got := relayedCopy(t, AgentClipboardArgs()); got != "exact text" {
		t.Fatalf("outer server got %q, want %q", got, "exact text")
	}
}

// The control for the test above: without the agent-side options the copy is
// dropped, so it is those options, not the outer relay alone, that deliver it.
func TestWithoutAgentClipboardArgsAnAppCopyIsDropped(t *testing.T) {
	if got := relayedCopy(t, nil); got != "" {
		t.Fatalf("outer server got %q with tmux defaults; the test above proves nothing", got)
	}
}

// Set every time a view opens, so applying them again must change nothing.
func TestAgentClipboardArgsAreIdempotent(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	tmuxtest.Isolate(t)
	tm := func(args ...string) string {
		b, err := exec.Command("tmux", append([]string{"-f", "/dev/null", "-L", "tyclip-idem"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, b)
		}
		return string(b)
	}
	t.Cleanup(func() { exec.Command("tmux", "-L", "tyclip-idem", "kill-server").Run() })
	tm("new-session", "-d", "-s", "s")
	apply := func() string {
		for _, args := range AgentClipboardArgs() {
			tm(args...)
		}
		return tm("show-options", "-s", "terminal-features")
	}
	once := apply()
	if twice := apply(); twice != once {
		t.Errorf("applying twice changed terminal-features:\n%s\nthen\n%s", once, twice)
	}
	for _, want := range []string{"tmux*:clipboard", "screen*:clipboard"} {
		if !strings.Contains(once, want) {
			t.Errorf("terminal-features lacks %q:\n%s", want, once)
		}
	}
	if got := strings.Join(PassthroughArgs("@3"), " "); got != "set-option -w -t @3 allow-passthrough on" {
		t.Errorf("PassthroughArgs = %q", got)
	}
}
