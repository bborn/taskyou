package ui

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

// Retry-with-feedback on a live agent is one of the surfaces that types into a
// task's pane, so it has to obey the same two rules as the others: find the pane
// by tag, and leave a working agent alone.
func TestRetryFeedbackGoesToTheTaggedPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	tmuxtest.Isolate(t)

	database, task := feedbackFixture(t, db.StatusBlocked)

	dir := t.TempDir()
	agentOut := filepath.Join(dir, "agent.txt")
	strangerOut := filepath.Join(dir, "stranger.txt")
	stranger, agent := twoPanes(t, strangerOut, agentOut)

	// The row still names the pane this task had before tmux reused the id.
	database.UpdateTaskPaneIDs(task.ID, stranger, "")
	tagPane(t, stranger, task.ID+1000, tmuxctl.RoleAgent)
	tagPane(t, agent, task.ID, tmuxctl.RoleAgent)

	m := &AppModel{db: database}
	if msg := m.retryTaskWithAttachments(task.ID, "please use the cached response", nil, false)(); msg.(taskRetriedMsg).err != nil {
		t.Fatalf("retry: %v", msg.(taskRetriedMsg).err)
	}

	if got := waitForContent(t, agentOut, "cached response"); !strings.Contains(got, "please use the cached response") {
		t.Errorf("agent pane got %q", got)
	}
	if b, _ := os.ReadFile(strangerOut); strings.Contains(string(b), "cached response") {
		t.Errorf("feedback reached the stale pane's task: %q", b)
	}

	updated, _ := database.GetTask(task.ID)
	if updated.Status != db.StatusProcessing {
		t.Errorf("status = %q, want processing", updated.Status)
	}
}

// A working agent is not typed over: the user is told to wait rather than having
// their feedback dropped into the middle of the agent's own output.
func TestRetryFeedbackRefusesAWorkingAgent(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	tmuxtest.Isolate(t)

	database, task := feedbackFixture(t, db.StatusProcessing)

	dir := t.TempDir()
	agentOut := filepath.Join(dir, "agent.txt")
	_, agent := twoPanes(t, filepath.Join(dir, "stranger.txt"), agentOut)
	tagPane(t, agent, task.ID, tmuxctl.RoleAgent)

	m := &AppModel{db: database}
	msg := m.retryTaskWithAttachments(task.ID, "actually, do the other thing", nil, false)()
	err := msg.(taskRetriedMsg).err
	if err == nil || !strings.Contains(err.Error(), "still working") {
		t.Fatalf("err = %v, want a 'still working' refusal", err)
	}

	time.Sleep(200 * time.Millisecond)
	if b, _ := os.ReadFile(agentOut); strings.Contains(string(b), "other thing") {
		t.Errorf("refused feedback was typed anyway: %q", b)
	}
}

func feedbackFixture(t *testing.T, status string) (*db.DB, *db.Task) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "feedback.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "storefront", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &db.Task{Title: "Cache the pricing lookup", Status: status, Project: "storefront"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return database, task
}

// twoPanes starts a window with two real panes, each spooling what is typed into
// it to its own file.
func twoPanes(t *testing.T, firstOut, secondOut string) (first, second string) {
	t.Helper()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("new-session", "-d", "-s", "uisend", "-n", "w", "sh", "-c", "cat > "+firstOut)
	first = run("display-message", "-t", "uisend:w.0", "-p", "#{pane_id}")
	second = run("split-window", "-d", "-P", "-F", "#{pane_id}", "-t", "uisend:w", "sh", "-c", "cat > "+secondOut)
	return first, second
}

func tagPane(t *testing.T, pane string, taskID int64, role string) {
	t.Helper()
	for _, args := range tmuxctl.TagPaneArgs(pane, taskID, role) {
		if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
			t.Fatalf("tag pane %s: %v: %s", pane, err, out)
		}
	}
}

func waitForContent(t *testing.T, path, want string) string {
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
