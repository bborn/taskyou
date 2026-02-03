package relay

import (
	"testing"
)

func TestRelay_RegisterAgent(t *testing.T) {
	r := New(nil)

	// Register an agent
	agent := r.Register("TestAgent", 123)

	if agent.Name != "TestAgent" {
		t.Errorf("Expected name 'TestAgent', got '%s'", agent.Name)
	}
	if agent.TaskID != 123 {
		t.Errorf("Expected taskID 123, got %d", agent.TaskID)
	}
	if agent.Status != "active" {
		t.Errorf("Expected status 'active', got '%s'", agent.Status)
	}

	// Retrieve by name (case-insensitive)
	found := r.GetAgent("testagent")
	if found == nil {
		t.Error("Expected to find agent with lowercase name")
	}
	if found != nil && found.Name != "TestAgent" {
		t.Errorf("Expected original name 'TestAgent', got '%s'", found.Name)
	}
}

func TestRelay_ListAgents(t *testing.T) {
	r := New(nil)

	r.Register("Agent1", 1)
	r.Register("Agent2", 2)

	agents := r.ListAgents()
	if len(agents) != 2 {
		t.Errorf("Expected 2 agents, got %d", len(agents))
	}
}

func TestRelay_Unregister(t *testing.T) {
	r := New(nil)

	r.Register("TestAgent", 123)
	r.Unregister("TestAgent")

	found := r.GetAgent("TestAgent")
	if found != nil {
		t.Error("Expected agent to be unregistered")
	}
}

func TestRelay_SendAndReceive(t *testing.T) {
	r := New(nil)

	// Register an agent
	r.Register("Bob", 1)

	// Send a message to Bob
	msgID, err := r.Send("Alice", "Bob", "Hello Bob!", 0)
	if err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}
	if msgID == "" {
		t.Error("Expected non-empty message ID")
	}

	// Check pending messages for Bob
	pending := r.GetPendingMessages("Bob")
	if len(pending) != 1 {
		t.Errorf("Expected 1 pending message, got %d", len(pending))
	}

	if len(pending) > 0 {
		if pending[0].From != "Alice" {
			t.Errorf("Expected from 'Alice', got '%s'", pending[0].From)
		}
		if pending[0].Content != "Hello Bob!" {
			t.Errorf("Expected content 'Hello Bob!', got '%s'", pending[0].Content)
		}
	}

	// Mark as delivered
	r.MarkDelivered(msgID)

	// Should no longer be pending
	pending = r.GetPendingMessages("Bob")
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending messages after delivery, got %d", len(pending))
	}
}

func TestRelay_BroadcastMessage(t *testing.T) {
	r := New(nil)

	// Register multiple agents
	r.Register("Agent1", 1)
	r.Register("Agent2", 2)

	// Send broadcast
	_, err := r.Send("Coordinator", "*", "Attention all agents!", 0)
	if err != nil {
		t.Fatalf("Failed to send broadcast: %v", err)
	}

	// Both agents should receive the message
	pending1 := r.GetPendingMessages("Agent1")
	pending2 := r.GetPendingMessages("Agent2")

	if len(pending1) != 1 {
		t.Errorf("Agent1 expected 1 pending message, got %d", len(pending1))
	}
	if len(pending2) != 1 {
		t.Errorf("Agent2 expected 1 pending message, got %d", len(pending2))
	}
}

func TestRelay_CaseInsensitiveNames(t *testing.T) {
	r := New(nil)

	r.Register("TestAgent", 1)

	// Should find with different cases
	if r.GetAgent("testagent") == nil {
		t.Error("Should find agent with lowercase")
	}
	if r.GetAgent("TESTAGENT") == nil {
		t.Error("Should find agent with uppercase")
	}
	if r.GetAgent("TestAgent") == nil {
		t.Error("Should find agent with exact case")
	}
}

func TestParseRelayCommand(t *testing.T) {
	tests := []struct {
		input       string
		wantTarget  string
		wantMessage string
		wantOk      bool
	}{
		{"->relay:Bob Hello!", "Bob", "Hello!", true},
		{"->relay:Alice Can you help?", "Alice", "Can you help?", true},
		{"->relay:* Broadcast message", "*", "Broadcast message", true},
		{"->relay:Agent-1 Test", "Agent-1", "Test", true},
		{"not a relay command", "", "", false},
		{"->relay:", "", "", false},
		{"->relay:Bob", "", "", false}, // No message
	}

	for _, tt := range tests {
		target, message, ok := ParseRelayCommand(tt.input)
		if ok != tt.wantOk {
			t.Errorf("ParseRelayCommand(%q): ok = %v, want %v", tt.input, ok, tt.wantOk)
		}
		if target != tt.wantTarget {
			t.Errorf("ParseRelayCommand(%q): target = %q, want %q", tt.input, target, tt.wantTarget)
		}
		if message != tt.wantMessage {
			t.Errorf("ParseRelayCommand(%q): message = %q, want %q", tt.input, message, tt.wantMessage)
		}
	}
}

func TestMessage_FormatForInjection(t *testing.T) {
	msg := &Message{
		ID:      "abc123",
		From:    "Alice",
		Content: "Hello!",
	}

	formatted := msg.FormatForInjection()
	if formatted == "" {
		t.Error("Expected non-empty formatted message")
	}
	if !contains(formatted, "[RELAY from Alice]") {
		t.Error("Expected formatted message to contain sender")
	}
	if !contains(formatted, "Hello!") {
		t.Error("Expected formatted message to contain content")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
