package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func turnsDB(t *testing.T) (*DB, int64) {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "turns.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&Project{Name: "storefront", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &Task{Title: "Wire the checkout webhook", Status: StatusProcessing, Project: "storefront"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return database, task.ID
}

func TestAgentTurnCountersStartAtZero(t *testing.T) {
	database, id := turnsDB(t)
	got, err := database.AgentTurnState(id)
	if err != nil {
		t.Fatalf("AgentTurnState: %v", err)
	}
	if (got != AgentTurn{}) {
		t.Errorf("fresh task = %+v, want zero", got)
	}
}

func TestHooksAdvanceTheTurnCounters(t *testing.T) {
	database, id := turnsDB(t)

	if got, _ := database.BeginAgentTurn(id); got.Started != 1 || got.Completed != 0 {
		t.Fatalf("after first prompt = %+v, want {1 0}", got)
	}
	if got, _ := database.CompleteAgentTurn(id); got.Started != 1 || got.Completed != 1 {
		t.Fatalf("after first stop = %+v, want {1 1}", got)
	}
	if got, _ := database.BeginAgentTurn(id); got.Started != 2 || got.Completed != 1 {
		t.Fatalf("after second prompt = %+v, want {2 1}", got)
	}
}

// A Stop with no turn behind it must not push Completed past Started: the next
// wait would then return on a turn that never began.
func TestCompleteWithoutABegunTurnStaysAtZero(t *testing.T) {
	database, id := turnsDB(t)
	got, err := database.CompleteAgentTurn(id)
	if err != nil {
		t.Fatalf("CompleteAgentTurn: %v", err)
	}
	if (got != AgentTurn{}) {
		t.Errorf("stray stop = %+v, want zero", got)
	}
}

// The point of the whole mechanism: the Stop belonging to the turn that was
// already running when we sent is not our answer.
func TestWaitIgnoresThePreviousTurnsCompletion(t *testing.T) {
	database, id := turnsDB(t)
	shortPolls(t)

	// The agent is mid-turn when the prompt goes out.
	database.BeginAgentTurn(id)
	before, _ := database.AgentTurnState(id)

	// That earlier turn now ends. It is not a reply to us.
	database.CompleteAgentTurn(id)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := database.WaitForAgentReply(ctx, id, before, 150*time.Millisecond); !errors.Is(err, ErrReplyTimeout) {
		t.Fatalf("err = %v, want ErrReplyTimeout — the previous turn's Stop was taken as the answer", err)
	}

	// Our prompt starts its own turn, which then finishes: that is the answer.
	database.BeginAgentTurn(id)
	go func() {
		time.Sleep(30 * time.Millisecond)
		database.CompleteAgentTurn(id)
	}()
	got, err := database.WaitForAgentReply(ctx, id, before, 3*time.Second)
	if err != nil {
		t.Fatalf("WaitForAgentReply: %v", err)
	}
	if got.Started != 2 || got.Completed != 2 {
		t.Errorf("reply turn = %+v, want {2 2}", got)
	}
}

func TestWaitReturnsWhenTheCallerGivesUp(t *testing.T) {
	database, id := turnsDB(t)
	shortPolls(t)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := database.WaitForAgentReply(ctx, id, AgentTurn{}, 10*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// shortPolls keeps the waiting tests in milliseconds rather than seconds.
func shortPolls(t *testing.T) {
	t.Helper()
	prev := agentTurnPollInterval
	agentTurnPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { agentTurnPollInterval = prev })
}
