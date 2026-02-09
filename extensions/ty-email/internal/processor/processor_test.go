package processor

import (
	"context"
	"testing"

	"github.com/bborn/workflow/extensions/ty-email/internal/adapter"
	"github.com/bborn/workflow/extensions/ty-email/internal/classifier"
	"github.com/bborn/workflow/extensions/ty-email/internal/state"
)

// mockAdapter implements adapter.Adapter for testing.
type mockAdapter struct {
	sentEmails []*adapter.OutboundEmail
}

func (m *mockAdapter) Name() string                                        { return "mock" }
func (m *mockAdapter) Start(ctx context.Context) error                     { return nil }
func (m *mockAdapter) Stop() error                                         { return nil }
func (m *mockAdapter) Emails() <-chan *adapter.Email                       { return nil }
func (m *mockAdapter) MarkProcessed(ctx context.Context, id string) error  { return nil }
func (m *mockAdapter) Send(ctx context.Context, email *adapter.OutboundEmail) error {
	m.sentEmails = append(m.sentEmails, email)
	return nil
}

// mockClassifier implements classifier.Classifier for testing.
type mockClassifier struct {
	action *classifier.Action
}

func (m *mockClassifier) Classify(ctx context.Context, email *adapter.Email, tasks []classifier.Task, threadTaskID *int64) (*classifier.Action, error) {
	return m.action, nil
}

func TestReplyFromAddress(t *testing.T) {
	// Setup state DB
	st, err := state.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Test: QueueOutbound stores the from address
	id, err := st.QueueOutbound(
		"sender@gmail.com",         // to
		"sender+ty@gmail.com",      // from (the +ty alias)
		"Re: Test",                 // subject
		"Reply body",               // body
		nil,                        // taskID
		"<msg123@gmail.com>",       // inReplyTo
	)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero ID")
	}

	// Verify the from address is persisted and retrieved
	pending, err := st.GetPendingOutbound(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
	if pending[0].From != "sender+ty@gmail.com" {
		t.Errorf("expected from 'sender+ty@gmail.com', got '%s'", pending[0].From)
	}
	if pending[0].To != "sender@gmail.com" {
		t.Errorf("expected to 'sender@gmail.com', got '%s'", pending[0].To)
	}
}

func TestReplyFromAddressPassedToAdapter(t *testing.T) {
	// Setup
	st, err := state.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mock := &mockAdapter{}
	proc := New(mock, nil, nil, st, nil, nil)

	// Queue an outbound email with a from address
	_, err = st.QueueOutbound(
		"user@gmail.com",
		"user+ty@gmail.com",
		"Re: Task",
		"Reply",
		nil,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}

	// Send pending replies
	err = proc.SendPendingReplies(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Verify the adapter received the correct from address
	if len(mock.sentEmails) != 1 {
		t.Fatalf("expected 1 sent email, got %d", len(mock.sentEmails))
	}
	if mock.sentEmails[0].From != "user+ty@gmail.com" {
		t.Errorf("expected from 'user+ty@gmail.com', got '%s'", mock.sentEmails[0].From)
	}
}

func TestReplyFromEmptyFallsBackToDefault(t *testing.T) {
	// When no from address is stored, the adapter should use its default SMTP from.
	st, err := state.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mock := &mockAdapter{}
	proc := New(mock, nil, nil, st, nil, nil)

	// Queue without a from address (e.g., from CheckBlockedTasks)
	_, err = st.QueueOutbound(
		"user@gmail.com",
		"",          // no from override
		"Re: Task",
		"Reply",
		nil,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}

	err = proc.SendPendingReplies(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(mock.sentEmails) != 1 {
		t.Fatalf("expected 1 sent email, got %d", len(mock.sentEmails))
	}
	// From should be empty, letting the adapter use its SMTP config default
	if mock.sentEmails[0].From != "" {
		t.Errorf("expected empty from (adapter default), got '%s'", mock.sentEmails[0].From)
	}
}

func TestRoundTripFromAddress(t *testing.T) {
	// End-to-end: queue with +ty from, send, verify adapter gets +ty from
	st, err := state.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mock := &mockAdapter{}
	proc := New(mock, nil, nil, st, nil, nil)

	// Simulate what ProcessEmail does: queue reply with the To address as From
	_, err = st.QueueOutbound(
		"bruno@gmail.com",        // to (reply recipient)
		"bruno+ty@gmail.com",     // from (the +ty alias from original email's To)
		"Re: New task",
		"Got it! I'll create a task: New task",
		nil,
		"<original-msg@gmail.com>",
	)
	if err != nil {
		t.Fatal(err)
	}

	// Send
	err = proc.SendPendingReplies(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Verify
	if len(mock.sentEmails) != 1 {
		t.Fatalf("expected 1 sent email, got %d", len(mock.sentEmails))
	}
	sent := mock.sentEmails[0]
	if sent.From != "bruno+ty@gmail.com" {
		t.Errorf("reply From should be +ty alias, got '%s'", sent.From)
	}
	if len(sent.To) != 1 || sent.To[0] != "bruno@gmail.com" {
		t.Errorf("reply To should be original sender, got %v", sent.To)
	}
	if sent.InReplyTo != "<original-msg@gmail.com>" {
		t.Errorf("reply InReplyTo should reference original, got '%s'", sent.InReplyTo)
	}
}
