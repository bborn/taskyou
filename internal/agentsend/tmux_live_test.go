package agentsend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/tmuxctl"
	"github.com/bborn/workflow/internal/tmuxtest"
)

// liveRunner is what the real surfaces use: plain tmux, on the agent server.
type liveRunner struct{}

func (liveRunner) Run(name string, args ...string) error {
	return exec.Command(name, tmuxctl.AgentArgs(args...)...).Run()
}

func (liveRunner) Output(name string, args ...string) ([]byte, error) {
	return exec.Command(name, tmuxctl.AgentArgs(args...)...).Output()
}

// A real tmux server, two real panes, and a stored pane id pointing at the
// wrong one — the shape of the bug this exists to stop. The prompt must land in
// the pane tmux says is task 7's agent, and multi-line text must arrive whole.
func TestLiveSendLandsInTheTaggedPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	tmuxtest.Isolate(t)

	dir := t.TempDir()
	agentOut := filepath.Join(dir, "agent.txt")
	strangerOut := filepath.Join(dir, "stranger.txt")

	tmux := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	// Pane one belongs to another task; pane two is task 7's agent. Each one
	// spools what is typed into it to its own file.
	tmux("new-session", "-d", "-s", "sendtest", "-n", "w", "sh", "-c", "cat > "+strangerOut)
	stranger := tmux("display-message", "-t", "sendtest:w.0", "-p", "#{pane_id}")
	agent := tmux("split-window", "-d", "-P", "-F", "#{pane_id}", "-t", "sendtest:w", "sh", "-c", "cat > "+agentOut)

	for _, args := range tmuxctl.TagPaneArgs(stranger, 9, tmuxctl.RoleAgent) {
		tmux(args...)
	}
	for _, args := range tmuxctl.TagPaneArgs(agent, 7, tmuxctl.RoleAgent) {
		tmux(args...)
	}

	// The task row still names the pane task 7 had before tmux reused the id.
	store := &fakeStore{task: &db.Task{ID: 7, Status: db.StatusBlocked, ClaudePaneID: stranger}}

	text := "first line\nsecond line with $shell `chars`"
	if err := New(liveRunner{}, store).Send(Prompt{TaskID: 7, Text: text, Submit: true}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := waitForFile(t, agentOut, "second line")
	for _, want := range []string{"first line", "second line with $shell `chars`"} {
		if !strings.Contains(got, want) {
			t.Errorf("agent pane received %q, missing %q", got, want)
		}
	}
	if b, err := os.ReadFile(strangerOut); err == nil && strings.Contains(string(b), "first line") {
		t.Errorf("prompt reached the stale pane's task: %q", b)
	}
}

// waitForFile reads path until it contains want, or the test gives up. The
// panes are real processes; their writes land when they land.
func waitForFile(t *testing.T, path, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), want) {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in %s; have %q", want, path, b)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
