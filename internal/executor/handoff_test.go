package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// fastHandoffPolling shrinks the poll so the suite does not sleep for real.
func fastHandoffPolling(t *testing.T) {
	t.Helper()
	prev := handoffPollInterval
	handoffPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { handoffPollInterval = prev })
}

// The happy path: the agent answers, and what it wrote is what travels.
func TestRequestHandoffReturnsWhatTheAgentWrote(t *testing.T) {
	fastHandoffPolling(t)
	work, _ := carryRepo(t)
	src := localSource(work)

	send := func(prompt string) error {
		if !strings.Contains(prompt, HandoffPath) {
			t.Errorf("the agent was not told where to write: %q", prompt)
		}
		// Stand in for the agent doing as it was asked.
		if err := os.MkdirAll(filepath.Join(work, ".taskyou"), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(work, HandoffPath),
			[]byte("# Handoff\n\nI was halfway through the CSV exporter.\n"), 0o644)
	}

	body, fromAgent := RequestHandoff(context.Background(), src,
		HandoffTask{ID: 5277, Title: "Export", Host: "mona"}, "bruce", send, 20*time.Second)

	if !fromAgent {
		t.Error("reported a synthesised handoff when the agent had written one")
	}
	if !strings.Contains(body, "halfway through the CSV exporter") {
		t.Errorf("handoff is not what the agent wrote: %q", body)
	}
}

// A stuck agent is the most common reason to move a task, so it must not be able
// to block the move. This is the case that would otherwise hang forever.
func TestRequestHandoffDoesNotWaitForeverOnASilentAgent(t *testing.T) {
	fastHandoffPolling(t)
	work, _ := carryRepo(t)
	src := localSource(work)

	start := time.Now()
	body, fromAgent := RequestHandoff(context.Background(), src,
		HandoffTask{ID: 5277, Title: "Export", Host: "mona"}, "bruce",
		func(string) error { return nil }, 200*time.Millisecond)
	elapsed := time.Since(start)

	if fromAgent {
		t.Error("claimed the agent answered when nothing was ever written")
	}
	if elapsed > 20*time.Second {
		t.Errorf("waited %s on a silent agent; a stuck agent blocks the move that rescues it", elapsed)
	}
	if !strings.Contains(body, "did not answer in time") {
		t.Errorf("synthesised handoff does not say why it is synthetic: %q", body)
	}
}

// A dead agent is not an error. It is the normal case for a task whose host has
// gone away, which is exactly when you reach for a move.
func TestRequestHandoffTreatsADeadAgentAsNormal(t *testing.T) {
	work, _ := carryRepo(t)
	body, fromAgent := RequestHandoff(context.Background(), localSource(work),
		HandoffTask{ID: 5277, Title: "Export", Host: "mona"}, "bruce", nil, time.Second)

	if fromAgent {
		t.Error("claimed an agent wrote the handoff when there was no agent")
	}
	if !strings.Contains(body, "not running") {
		t.Errorf("handoff does not explain the missing agent: %q", body)
	}
}

// A stale handoff from an earlier move must not be mistaken for an answer to
// this one — that would hand the new agent a description of the wrong session.
func TestRequestHandoffIgnoresAStaleHandoff(t *testing.T) {
	fastHandoffPolling(t)
	work, _ := carryRepo(t)
	if err := os.MkdirAll(filepath.Join(work, ".taskyou"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, HandoffPath), []byte("from a move last week\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	body, fromAgent := RequestHandoff(context.Background(), localSource(work),
		HandoffTask{ID: 5277, Title: "Export", Host: "mona"}, "bruce",
		func(string) error { return nil }, 200*time.Millisecond)

	if fromAgent || strings.Contains(body, "last week") {
		t.Errorf("a handoff from an earlier move was passed off as this one's: %q", body)
	}
}

// The synthesised handoff must not read like an account of the session. Its job
// is to say what is reliably true and send the agent to the diff.
func TestSyntheticHandoffDoesNotInventASession(t *testing.T) {
	body := SyntheticHandoff(HandoffTask{ID: 5277, Title: "Export", Host: "mona"}, "bruce", "its agent was not running")
	for _, want := range []string{"No handoff was written", "mona", "bruce", "ignores did NOT travel", "Read the diff"} {
		if !strings.Contains(body, want) {
			t.Errorf("synthesised handoff is missing %q:\n%s", want, body)
		}
	}
}

// The handoff only matters if the new agent actually reads it, and it has to
// come BEFORE the task body: the body says what was originally asked for, the
// handoff says what already happened, and an agent that reads them the other way
// round starts by redoing work already on its branch.
func TestHandoffSectionLeadsThePromptOnTheNewHost(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".taskyou"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, HandoffPath),
		[]byte("I finished the parser; the writer is stubbed.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := &Executor{}
	section := e.handoffSection(&db.Task{ID: 5277, WorktreePath: dir})
	if !strings.Contains(section, "the writer is stubbed") {
		t.Errorf("the previous agent's handoff is not in the prompt:\n%s", section)
	}
	if !strings.Contains(section, "read the") || !strings.Contains(section, "diff") {
		t.Errorf("the new agent is not told to check the diff:\n%s", section)
	}
}

// A task that never moved must not gain a phantom "handoff from the previous
// session" heading.
func TestHandoffSectionIsEmptyWithoutAHandoff(t *testing.T) {
	e := &Executor{}
	if got := e.handoffSection(&db.Task{ID: 1, WorktreePath: t.TempDir()}); got != "" {
		t.Errorf("invented a handoff for a task that never moved: %q", got)
	}
	if got := e.handoffSection(&db.Task{ID: 1}); got != "" {
		t.Errorf("handoff section for a task with no worktree: %q", got)
	}
}
