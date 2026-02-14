// Package events defines the universal event schema for sandbox agent communication.
// This schema normalizes output from different coding agents (Claude Code, Codex, etc.)
// into a consistent event stream, modeled after rivet-dev/sandbox-agent.
package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EventType identifies the kind of universal event.
type EventType string

const (
	EventSessionStarted      EventType = "session.started"
	EventSessionEnded        EventType = "session.ended"
	EventTurnStarted         EventType = "turn.started"
	EventTurnEnded           EventType = "turn.ended"
	EventItemStarted         EventType = "item.started"
	EventItemDelta           EventType = "item.delta"
	EventItemCompleted       EventType = "item.completed"
	EventQuestionRequested   EventType = "question.requested"
	EventQuestionResolved    EventType = "question.resolved"
	EventPermissionRequested EventType = "permission.requested"
	EventPermissionResolved  EventType = "permission.resolved"
	EventAgentUnparsed       EventType = "agent.unparsed"
	EventError               EventType = "error"
)

// EventSource indicates where the event originated.
type EventSource string

const (
	SourceAgent  EventSource = "agent"
	SourceDaemon EventSource = "daemon"
)

// UniversalEvent is the top-level envelope for all events in the stream.
type UniversalEvent struct {
	EventID         string          `json:"event_id"`
	SessionID       string          `json:"session_id"`
	NativeSessionID *string         `json:"native_session_id,omitempty"`
	Type            EventType       `json:"type"`
	Source          EventSource     `json:"source"`
	Synthetic       bool            `json:"synthetic"`
	Sequence        uint64          `json:"sequence"`
	Time            string          `json:"time"`
	Data            json.RawMessage `json:"data"`
	Raw             json.RawMessage `json:"raw,omitempty"`
}

// NewEvent creates a new universal event with a unique ID and timestamp.
func NewEvent(sessionID string, seq uint64, eventType EventType, source EventSource, data any) (*UniversalEvent, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal event data: %w", err)
	}
	return &UniversalEvent{
		EventID:   uuid.New().String(),
		SessionID: sessionID,
		Type:      eventType,
		Source:    source,
		Synthetic: source == SourceDaemon,
		Sequence:  seq,
		Time:      time.Now().UTC().Format(time.RFC3339Nano),
		Data:      dataBytes,
	}, nil
}

// SessionStartedData is emitted when a session begins.
type SessionStartedData struct {
	Agent string `json:"agent"`
	Model string `json:"model,omitempty"`
}

// SessionEndReason explains why a session ended.
type SessionEndReason string

const (
	EndReasonCompleted  SessionEndReason = "completed"
	EndReasonTerminated SessionEndReason = "terminated"
	EndReasonError      SessionEndReason = "error"
)

// SessionEndedData is emitted when a session ends.
type SessionEndedData struct {
	Reason SessionEndReason `json:"reason"`
}

// TurnPhase identifies the phase of a conversation turn.
type TurnPhase string

const (
	TurnPhaseStarted TurnPhase = "started"
	TurnPhaseEnded   TurnPhase = "ended"
)

// TurnEventData is emitted at the start and end of conversation turns.
type TurnEventData struct {
	TurnID string    `json:"turn_id"`
	Phase  TurnPhase `json:"phase,omitempty"`
}

// ItemKind categorizes the type of an item in the conversation.
type ItemKind string

const (
	ItemKindMessage  ItemKind = "message"
	ItemKindToolCall ItemKind = "tool_call"
)

// ItemRole identifies the role of the message author.
type ItemRole string

const (
	RoleUser      ItemRole = "user"
	RoleAssistant ItemRole = "assistant"
	RoleSystem    ItemRole = "system"
)

// ItemStatus tracks the state of a conversation item.
type ItemStatus string

const (
	ItemStatusInProgress ItemStatus = "in_progress"
	ItemStatusCompleted  ItemStatus = "completed"
)

// ContentPart represents a piece of content in a message or tool call.
type ContentPart struct {
	Type string `json:"type"` // "text", "tool_call", "tool_result", "file_ref", "reasoning"

	// text
	Text string `json:"text,omitempty"`

	// tool_call
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// tool_result
	Output string `json:"output,omitempty"`

	// file_ref
	Path   string `json:"path,omitempty"`
	Action string `json:"action,omitempty"`
	Diff   string `json:"diff,omitempty"`

	// reasoning
	Visibility string `json:"visibility,omitempty"`
}

// UniversalItem represents a normalized message or tool call.
type UniversalItem struct {
	ItemID  string        `json:"item_id"`
	Kind    ItemKind      `json:"kind"`
	Role    ItemRole      `json:"role,omitempty"`
	Status  ItemStatus    `json:"status"`
	Content []ContentPart `json:"content,omitempty"`
}

// ItemEventData is emitted for item.started and item.completed events.
type ItemEventData struct {
	Item UniversalItem `json:"item"`
}

// ItemDeltaData is emitted for streaming content updates.
type ItemDeltaData struct {
	ItemID string      `json:"item_id"`
	Delta  ContentPart `json:"delta"`
}

// QuestionStatus tracks the state of a human-in-the-loop question.
type QuestionStatus string

const (
	QuestionPending  QuestionStatus = "pending"
	QuestionAnswered QuestionStatus = "answered"
	QuestionRejected QuestionStatus = "rejected"
)

// QuestionEventData is emitted for HITL questions.
type QuestionEventData struct {
	QuestionID string         `json:"question_id"`
	Status     QuestionStatus `json:"status"`
	Question   string         `json:"question,omitempty"`
	Answer     string         `json:"answer,omitempty"`
}

// PermissionStatus tracks the state of a tool permission request.
type PermissionStatus string

const (
	PermissionPending  PermissionStatus = "pending"
	PermissionApproved PermissionStatus = "approved"
	PermissionDenied   PermissionStatus = "denied"
)

// PermissionEventData is emitted for tool execution approval requests.
type PermissionEventData struct {
	PermissionID string           `json:"permission_id"`
	Status       PermissionStatus `json:"status"`
	ToolName     string           `json:"tool_name,omitempty"`
	Arguments    string           `json:"arguments,omitempty"`
}

// ErrorData is emitted when an error occurs.
type ErrorData struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// AgentUnparsedData is emitted when agent output can't be parsed.
type AgentUnparsedData struct {
	Error    string `json:"error"`
	Location string `json:"location"`
	RawHash  string `json:"raw_hash,omitempty"`
}
