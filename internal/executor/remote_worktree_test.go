package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// The bug this guards: ssh hands back stdout and stderr as two channels, and a
// merged capture orders them by whichever copy won, not by when the remote host
// wrote them. Task 5277's worktree was created on mona and ty called it a
// failure because git's "HEAD is now at ..." landed after the script's answer.
func TestSetupRemoteWorktreeReadsTheAnswerWhenGitChatterLandsLast(t *testing.T) {
	stubSSH(t, "#!/bin/sh\n"+
		"echo \"Preparing worktree (new branch 'task/1-a-task')\" >&2\n"+
		"echo 'created /home/bruno/Projects/taskyou/.task-worktrees/1-a-task'\n"+
		"echo 'HEAD is now at fe55c08 a commit' >&2\n")

	wt, err := setupRemoteWorktreeForTest(t)
	if err != nil {
		t.Fatalf("setupRemoteWorktree: %v", err)
	}
	if wt.Path != "/home/bruno/Projects/taskyou/.task-worktrees/1-a-task" {
		t.Errorf("Path = %q", wt.Path)
	}
	if !wt.Created {
		t.Error("Created = false, want true")
	}
}

// A login shell that greets on stdout must not read as a failure to provision.
func TestSetupRemoteWorktreeIgnoresChatterOnStdout(t *testing.T) {
	stubSSH(t, "#!/bin/sh\necho 'mise: activating'\necho 'reused /repo/.task-worktrees/1-a-task'\n")

	wt, err := setupRemoteWorktreeForTest(t)
	if err != nil {
		t.Fatalf("setupRemoteWorktree: %v", err)
	}
	if wt.Path != "/repo/.task-worktrees/1-a-task" || wt.Created {
		t.Errorf("got %+v, want the reused path", wt)
	}
}

// Nothing that looks like an answer means the script did not get there, even
// when the shell exited 0.
func TestSetupRemoteWorktreeFailsWhenTheScriptNeverAnswers(t *testing.T) {
	stubSSH(t, "#!/bin/sh\necho 'fatal: not a git repository' >&2\n")

	_, err := setupRemoteWorktreeForTest(t)
	if err == nil {
		t.Fatal("want an error when the script printed no answer")
	}
	// The stderr chatter is the only clue to what went wrong, so it has to
	// survive into the message the board shows.
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q drops git's own explanation", err)
	}
}

func setupRemoteWorktreeForTest(t *testing.T) (remoteWorktree, error) {
	t.Helper()
	e := &Executor{}
	task := &db.Task{ID: 1, Title: "a task"}
	return e.setupRemoteWorktree(context.Background(), task, RemoteRunner{Host: "mona", WorkDir: "/repo"})
}
