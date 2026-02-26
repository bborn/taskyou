package agent

import (
	"context"
	"time"

	"github.com/bborn/workflow/extensions/ty-sandbox/internal/events"
	"github.com/google/uuid"
)

// MockAgent is a test agent that emits synthetic events without running a real CLI.
type MockAgent struct{}

func NewMockAgent() *MockAgent { return &MockAgent{} }

func (a *MockAgent) ID() AgentID { return AgentMock }

func (a *MockAgent) Info() AgentInfo {
	return AgentInfo{
		ID:          AgentMock,
		Name:        "Mock Agent",
		Installed:   true,
		Available:   true,
		Description: "Test agent that emits synthetic events",
	}
}

func (a *MockAgent) IsInstalled() bool { return true }

func (a *MockAgent) Install(ctx context.Context) error { return nil }

func (a *MockAgent) Spawn(ctx context.Context, sessionID string, cfg SpawnConfig, eventCh chan<- *events.UniversalEvent) error {
	go func() {
		var seq uint64

		// session.started
		evt, _ := events.NewEvent(sessionID, seq, events.EventSessionStarted, events.SourceDaemon, &events.SessionStartedData{
			Agent: string(AgentMock),
		})
		eventCh <- evt
		seq++

		time.Sleep(200 * time.Millisecond)

		// Emit a turn
		turnID := uuid.New().String()
		evt, _ = events.NewEvent(sessionID, seq, events.EventTurnStarted, events.SourceDaemon, &events.TurnEventData{TurnID: turnID})
		eventCh <- evt
		seq++

		// Emit an assistant message
		itemID := uuid.New().String()
		evt, _ = events.NewEvent(sessionID, seq, events.EventItemStarted, events.SourceAgent, &events.ItemEventData{
			Item: events.UniversalItem{
				ItemID: itemID,
				Kind:   events.ItemKindMessage,
				Role:   events.RoleAssistant,
				Status: events.ItemStatusInProgress,
			},
		})
		eventCh <- evt
		seq++

		time.Sleep(100 * time.Millisecond)

		response := "Hello! I'm the mock agent. Your prompt was: " + cfg.Prompt
		evt, _ = events.NewEvent(sessionID, seq, events.EventItemDelta, events.SourceAgent, &events.ItemDeltaData{
			ItemID: itemID,
			Delta:  events.ContentPart{Type: "text", Text: response},
		})
		eventCh <- evt
		seq++

		evt, _ = events.NewEvent(sessionID, seq, events.EventItemCompleted, events.SourceAgent, &events.ItemEventData{
			Item: events.UniversalItem{
				ItemID: itemID,
				Kind:   events.ItemKindMessage,
				Role:   events.RoleAssistant,
				Status: events.ItemStatusCompleted,
				Content: []events.ContentPart{
					{Type: "text", Text: response},
				},
			},
		})
		eventCh <- evt
		seq++

		evt, _ = events.NewEvent(sessionID, seq, events.EventTurnEnded, events.SourceDaemon, &events.TurnEventData{TurnID: turnID})
		eventCh <- evt
		seq++

		time.Sleep(100 * time.Millisecond)

		// session.ended
		evt, _ = events.NewEvent(sessionID, seq, events.EventSessionEnded, events.SourceDaemon, &events.SessionEndedData{
			Reason: events.EndReasonCompleted,
		})
		eventCh <- evt
	}()
	return nil
}

func (a *MockAgent) SendMessage(ctx context.Context, sessionID string, message string) error {
	return nil
}

func (a *MockAgent) Terminate(ctx context.Context, sessionID string) error {
	return nil
}
