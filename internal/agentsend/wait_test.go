package agentsend

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// A surface that sends a prompt and waits for the answer must not accept the
// Stop of the turn that was already running. Driven through a real database,
// because that is the channel the hooks actually write to.
func TestSendAndWaitIgnoresThePreviousTurnsCompletion(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "wait.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	if err := database.CreateProject(&db.Project{Name: "storefront", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &db.Task{Title: "Rotate the staging API key", Status: db.StatusBlocked, Project: "storefront"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// The agent has a turn in flight from before this prompt.
	if _, err := database.BeginAgentTurn(task.ID); err != nil {
		t.Fatalf("BeginAgentTurn: %v", err)
	}

	tmux := &fakeTmux{panes: "%8 " + strconv.FormatInt(task.ID, 10) + " agent"}
	sender := New(tmux, database)

	// That earlier turn ends right after the send. It is not the answer.
	stale := make(chan struct{})
	go func() {
		<-stale
		database.CompleteAgentTurn(task.ID)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	close(stale)
	time.Sleep(50 * time.Millisecond)
	if _, err := sender.SendAndWait(ctx, Prompt{TaskID: task.ID, Text: "status?", Submit: true}, 200*time.Millisecond); !errors.Is(err, db.ErrReplyTimeout) {
		t.Fatalf("err = %v, want db.ErrReplyTimeout", err)
	}

	// Now the agent takes the prompt and answers it.
	go func() {
		time.Sleep(20 * time.Millisecond)
		database.BeginAgentTurn(task.ID)
		time.Sleep(20 * time.Millisecond)
		database.CompleteAgentTurn(task.ID)
	}()
	turn, err := sender.SendAndWait(ctx, Prompt{TaskID: task.ID, Text: "status?", Submit: true}, 5*time.Second)
	if err != nil {
		t.Fatalf("SendAndWait: %v", err)
	}
	if turn.Started != 2 || turn.Completed != 2 {
		t.Errorf("turn = %+v, want {2 2}", turn)
	}
}
