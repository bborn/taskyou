package processor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bborn/workflow/extensions/ty-email/internal/adapter"
	"github.com/bborn/workflow/extensions/ty-email/internal/bridge"
	"github.com/bborn/workflow/extensions/ty-email/internal/classifier"
	"github.com/bborn/workflow/extensions/ty-email/internal/state"
)

// mockAdapter implements adapter.Adapter for testing.
type mockAdapter struct {
	sentEmails          []*adapter.OutboundEmail
	lastMarkProcessedID string
}

func (m *mockAdapter) Name() string                    { return "mock" }
func (m *mockAdapter) Start(ctx context.Context) error { return nil }
func (m *mockAdapter) Stop() error                     { return nil }
func (m *mockAdapter) Emails() <-chan *adapter.Email   { return nil }
func (m *mockAdapter) MarkProcessed(ctx context.Context, id string) error {
	m.lastMarkProcessedID = id
	return nil
}
func (m *mockAdapter) Send(ctx context.Context, email *adapter.OutboundEmail) error {
	m.sentEmails = append(m.sentEmails, email)
	return nil
}

// mockClassifier implements classifier.Classifier for testing.
type mockClassifier struct {
	action *classifier.Action
	err    error
	calls  int
}

func (m *mockClassifier) Classify(ctx context.Context, email *adapter.Email, tasks []classifier.Task, threadTaskID *int64) (*classifier.Action, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.action, nil
}

func (m *mockClassifier) Name() string      { return "mock" }
func (m *mockClassifier) IsAvailable() bool { return true }

// mockBridge implements TaskBridge for testing.
type mockBridge struct {
	tasks   []bridge.Task
	blocked []bridge.Task
	created []*classifier.Action
	inputs  map[int64]string
}

func (m *mockBridge) ListTasks(status string) ([]bridge.Task, error) { return m.tasks, nil }
func (m *mockBridge) CreateTask(action *classifier.Action) (*bridge.CreateResult, error) {
	m.created = append(m.created, action)
	return &bridge.CreateResult{ID: int64(len(m.created)), Title: action.Title, Status: "backlog", Project: action.Project}, nil
}
func (m *mockBridge) SendInput(taskID int64, input string) error {
	if m.inputs == nil {
		m.inputs = map[int64]string{}
	}
	m.inputs[taskID] = input
	return nil
}
func (m *mockBridge) ExecuteTask(taskID int64) error                { return nil }
func (m *mockBridge) GetBlockedTasks() ([]bridge.Task, error)       { return m.blocked, nil }
func (m *mockBridge) GetTaskOutput(id int64, n int) (string, error) { return "output", nil }

func TestReplyFromAddress(t *testing.T) {
	// Setup state DB
	st, err := state.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Test: QueueOutbound stores the from address
	id, err := st.QueueOutbound(
		"sender@gmail.com",    // to
		"sender+ty@gmail.com", // from (the +ty alias)
		"Re: Test",            // subject
		"Reply body",          // body
		nil,                   // taskID
		"<msg123@gmail.com>",  // inReplyTo
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
		"", // no from override
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

func TestStripQuotedText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain text",
			input:    "Please create a task for this",
			expected: "Please create a task for this",
		},
		{
			name:     "with signature",
			input:    "Please create a task\n\n--\nJohn Doe\nCEO, Acme Corp",
			expected: "Please create a task",
		},
		{
			name:     "with quoted reply",
			input:    "Sounds good, do it.\n\nOn Mon, Feb 20, 2026 at 12:00 PM Someone wrote:\n> Original message here\n> More original",
			expected: "Sounds good, do it.",
		},
		{
			name:     "with inline quotes",
			input:    "My response\n> quoted line\nMore response",
			expected: "My response\nMore response",
		},
		{
			name:     "truncates long body",
			input:    strings.Repeat("a", 3000),
			expected: strings.Repeat("a", 2000) + "\n[truncated]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripQuotedText(tt.input)
			if got != tt.expected {
				t.Errorf("stripQuotedText() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestSenderAllowlistExactMatch(t *testing.T) {
	p := &Processor{allowedSenders: []string{"bruno@gmail.com"}}

	tests := []struct {
		from    string
		allowed bool
	}{
		{"bruno@gmail.com", true},
		{"BRUNO@gmail.com", true},
		{"Bruno Bornsztein <bruno@gmail.com>", true},
		// Display-name spoof: address is the attacker's
		{`"bruno@gmail.com" <evil@attacker.com>`, false},
		// Domain-suffix spoof (would pass the old substring match)
		{"bruno@gmail.com.attacker.com", false},
		{"evil-bruno@gmail.com", false},
		{"other@gmail.com", false},
	}

	for _, tt := range tests {
		if got := p.senderAllowed(tt.from); got != tt.allowed {
			t.Errorf("senderAllowed(%q) = %v, want %v", tt.from, got, tt.allowed)
		}
	}

	// Empty allowlist allows everything
	open := &Processor{}
	if !open.senderAllowed("anyone@example.com") {
		t.Error("empty allowlist should allow all senders")
	}
}

func TestAutoReplySkipped(t *testing.T) {
	st, err := state.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mock := &mockAdapter{}
	br := &mockBridge{}
	proc := New(mock, &mockClassifier{}, br, st, nil, nil)

	email := &adapter.Email{
		ID:        "<auto1@example.com>",
		From:      "me@gmail.com",
		Subject:   "Out of office",
		Body:      "I am away",
		AutoReply: true,
	}

	if err := proc.ProcessEmail(context.Background(), email); err != nil {
		t.Fatal(err)
	}

	if len(br.created) != 0 {
		t.Errorf("auto-reply should not create a task, created %d", len(br.created))
	}
	processed, _ := st.IsProcessed(email.ID)
	if !processed {
		t.Error("auto-reply should be marked processed to avoid re-fetching")
	}
	if mock.lastMarkProcessedID != email.ID {
		t.Error("auto-reply should be marked processed in the adapter (seen flag)")
	}
}

func TestClassifyFailureGivesUp(t *testing.T) {
	st, err := state.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Link a thread so the email goes through the classifier path
	if err := st.LinkThread("<thread@example.com>", 42); err != nil {
		t.Fatal(err)
	}

	mock := &mockAdapter{}
	cls := &mockClassifier{err: errors.New("api down")}
	proc := New(mock, cls, &mockBridge{}, st, nil, nil)

	email := &adapter.Email{
		ID:        "<fail1@example.com>",
		From:      "me@gmail.com",
		Subject:   "Re: something",
		Body:      "reply text",
		InReplyTo: "<thread@example.com>",
	}

	ctx := context.Background()

	// First two attempts: error returned, email NOT marked processed (retry ok)
	for i := 1; i <= maxClassifyAttempts-1; i++ {
		if err := proc.ProcessEmail(ctx, email); err == nil {
			t.Fatalf("attempt %d: expected error", i)
		}
		processed, _ := st.IsProcessed(email.ID)
		if processed {
			t.Fatalf("attempt %d: should not be marked processed yet", i)
		}
	}

	// Final attempt: gives up, marks processed, returns nil
	if err := proc.ProcessEmail(ctx, email); err != nil {
		t.Fatalf("final attempt should give up cleanly, got %v", err)
	}
	processed, _ := st.IsProcessed(email.ID)
	if !processed {
		t.Fatal("email should be marked processed after giving up")
	}

	// Subsequent polls must not call the classifier again
	callsBefore := cls.calls
	if err := proc.ProcessEmail(ctx, email); err != nil {
		t.Fatal(err)
	}
	if cls.calls != callsBefore {
		t.Errorf("classifier called again after giving up (%d -> %d)", callsBefore, cls.calls)
	}
}

func TestRateLimitDefersProcessing(t *testing.T) {
	st, err := state.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	br := &mockBridge{}
	proc := New(&mockAdapter{}, &mockClassifier{}, br, st, &Config{MaxTasksPerHour: 1}, nil)

	ctx := context.Background()
	first := &adapter.Email{ID: "<rl1@example.com>", From: "me@gmail.com", Subject: "Task one", Body: "body"}
	if err := proc.ProcessEmail(ctx, first); err != nil {
		t.Fatal(err)
	}
	if len(br.created) != 1 {
		t.Fatalf("expected 1 task created, got %d", len(br.created))
	}

	// Second email within the window: deferred (not processed, no task)
	second := &adapter.Email{ID: "<rl2@example.com>", From: "me@gmail.com", Subject: "Task two", Body: "body"}
	if err := proc.ProcessEmail(ctx, second); err != nil {
		t.Fatal(err)
	}
	if len(br.created) != 1 {
		t.Errorf("rate-limited email should not create a task, created %d", len(br.created))
	}
	processed, _ := st.IsProcessed(second.ID)
	if processed {
		t.Error("rate-limited email should stay unprocessed so it is retried later")
	}
}

func TestDirectCreateAppliesRoutingDefaults(t *testing.T) {
	st, err := state.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	br := &mockBridge{}
	cfg := &Config{DefaultProject: "personal", AutoExecute: true}
	proc := New(&mockAdapter{}, &mockClassifier{}, br, st, cfg, nil)

	email := &adapter.Email{ID: "<d1@example.com>", From: "me@gmail.com", Subject: "Fix the bug", Body: "details"}
	if err := proc.ProcessEmail(context.Background(), email); err != nil {
		t.Fatal(err)
	}

	if len(br.created) != 1 {
		t.Fatalf("expected 1 task, got %d", len(br.created))
	}
	action := br.created[0]
	if action.Project != "personal" {
		t.Errorf("expected default project 'personal', got %q", action.Project)
	}
	if !action.Execute {
		t.Error("auto_execute should queue the task for execution")
	}
}

func TestDirectCreateTitleFallback(t *testing.T) {
	st, err := state.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	br := &mockBridge{}
	proc := New(&mockAdapter{}, &mockClassifier{}, br, st, nil, nil)

	email := &adapter.Email{ID: "<d2@example.com>", From: "me@gmail.com", Subject: "", Body: "Fix the login page\nIt returns 500"}
	if err := proc.ProcessEmail(context.Background(), email); err != nil {
		t.Fatal(err)
	}

	if len(br.created) != 1 {
		t.Fatalf("expected 1 task, got %d", len(br.created))
	}
	if br.created[0].Title != "Fix the login page" {
		t.Errorf("expected title from first body line, got %q", br.created[0].Title)
	}
}

func TestBlockedNotificationDedup(t *testing.T) {
	st, err := state.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Task 7 was created via email (thread linked) and is now blocked
	if err := st.LinkThread("<task7@example.com>", 7); err != nil {
		t.Fatal(err)
	}
	br := &mockBridge{blocked: []bridge.Task{{ID: 7, Title: "Blocked task", Status: "blocked"}}}
	proc := New(&mockAdapter{}, &mockClassifier{}, br, st, nil, nil)

	ctx := context.Background()

	// Two check cycles: only one notification queued
	for i := 0; i < 2; i++ {
		if err := proc.CheckBlockedTasks(ctx, "me@gmail.com"); err != nil {
			t.Fatal(err)
		}
	}
	pending, _ := st.GetPendingOutbound(3)
	if len(pending) != 1 {
		t.Fatalf("expected 1 queued notification, got %d", len(pending))
	}

	// Input to the task clears the dedup; re-blocking notifies again
	if _, _, err := proc.handleInput(ctx, &classifier.Action{TaskID: 7, InputText: "go"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := proc.CheckBlockedTasks(ctx, "me@gmail.com"); err != nil {
		t.Fatal(err)
	}
	pending, _ = st.GetPendingOutbound(3)
	if len(pending) != 2 {
		t.Fatalf("expected re-notification after input, got %d queued", len(pending))
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
		"bruno@gmail.com",    // to (reply recipient)
		"bruno+ty@gmail.com", // from (the +ty alias from original email's To)
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
