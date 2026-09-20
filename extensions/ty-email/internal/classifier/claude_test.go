package classifier

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/bborn/workflow/extensions/ty-email/internal/adapter"
)

// closureTransport lets a test intercept the HTTP request issued by the
// Anthropic SDK and return a canned response without touching the network or
// requiring a live API key.
type closureTransport struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (t *closureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.fn(req)
}

// newTestClassifier builds a ClaudeClassifier whose Anthropic client uses the
// supplied transport (so no network access) and with maxTokens set explicitly.
// It bypasses NewClaudeClassifier, which would shell out for the API key.
func newTestClassifier(t *testing.T, transport http.RoundTripper, maxTokens int) *ClaudeClassifier {
	t.Helper()
	client := anthropic.NewClient(
		option.WithAPIKey("test-api-key"),
		option.WithHTTPClient(&http.Client{Transport: transport}),
	)
	return &ClaudeClassifier{
		client:    client,
		model:     "test-model",
		maxTokens: maxTokens,
	}
}

// anthropicResponse builds a canned Anthropic Messages non-streaming response
// body. All required fields (id, type, role, model, content, stop_reason,
// stop_sequence, usage) are populated so the SDK's strict unmarshaler accepts
// it. contentText is placed as a single text content block.
func anthropicResponse(stopReason, contentText string, outputTokens int64) string {
	resp := map[string]any{
		"id":            "msg_test",
		"type":          "message",
		"role":          "assistant",
		"model":         "test-model",
		"content":       []map[string]any{{"type": "text", "text": contentText}},
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":                100,
			"output_tokens":               outputTokens,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// jsonResponse wraps a body string in an http.Response with the
// application/json Content-Type the Anthropic SDK requires for unmarshalling.
func jsonResponse(body string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestParseResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    *Action
	}{
		{
			name:  "valid full input action",
			input: `{"type":"input","task_id":42,"input_text":"use --no-submit","reply":"ok","reasoning":"u said go","confidence":0.9}`,
			want: &Action{
				Type:       ActionInput,
				TaskID:     42,
				InputText:  "use --no-submit",
				Reply:      "ok",
				Reasoning:  "u said go",
				Confidence: 0.9,
			},
		},
		{
			name:  "valid minimal ignore action",
			input: `{"type":"ignore"}`,
			want:  &Action{Type: ActionIgnore},
		},
		{
			name:  "markdown json code block",
			input: "```json\n" + `{"type":"ignore"}` + "\n```",
			want:  &Action{Type: ActionIgnore},
		},
		{
			name:  "markdown plain code block",
			input: "```\n" + `{"type":"ignore"}` + "\n```",
			want:  &Action{Type: ActionIgnore},
		},
		{
			name:  "whitespace-padded JSON",
			input: "  " + `{"type":"ignore"}` + "  ",
			want:  &Action{Type: ActionIgnore},
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "truncated mid-string JSON (max_tokens shape)",
			input:   `{"type":"input","task_id":42,"input_text":"the db url is postgres://user:hunter2@`,
			wantErr: true,
		},
		{
			name:    "non-json text",
			input:   "the user is asking about their tasks",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClaudeClassifier{}
			got, err := c.parseResponse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (action=%+v)", got)
				}
				if !strings.Contains(err.Error(), "invalid JSON") {
					t.Errorf("error should wrap 'invalid JSON', got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Type != tt.want.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.want.Type)
			}
			if got.TaskID != tt.want.TaskID {
				t.Errorf("TaskID = %d, want %d", got.TaskID, tt.want.TaskID)
			}
			if got.InputText != tt.want.InputText {
				t.Errorf("InputText = %q, want %q", got.InputText, tt.want.InputText)
			}
			if got.Reply != tt.want.Reply {
				t.Errorf("Reply = %q, want %q", got.Reply, tt.want.Reply)
			}
			if got.Reasoning != tt.want.Reasoning {
				t.Errorf("Reasoning = %q, want %q", got.Reasoning, tt.want.Reasoning)
			}
			if got.Confidence != tt.want.Confidence {
				t.Errorf("Confidence = %v, want %v", got.Confidence, tt.want.Confidence)
			}
		})
	}
}

func TestNewClaudeClassifierMaxTokensDefault(t *testing.T) {
	tests := []struct {
		name string
		cfg  int
		want int
	}{
		{"unset uses default", 0, defaultMaxTokens},
		{"negative uses default", -1, defaultMaxTokens},
		{"explicit value is respected", 1024, 1024},
		{"small explicit value is respected", 128, 128},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewClaudeClassifier(&Config{APIKey: "test-key", MaxTokens: tt.cfg})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.maxTokens != tt.want {
				t.Errorf("maxTokens = %d, want %d", c.maxTokens, tt.want)
			}
		})
	}
}

// TestClassifyMaxTokensTruncation is the core regression test: when the
// Anthropic SDK reports stop_reason "max_tokens", Classify must return a
// distinct, diagnosable error (mentioning "max_tokens" and "truncated") rather
// than the opaque "invalid JSON" produced by parseResponse on truncated JSON.
// Before the fix this surfaced as an opaque parse error and the processor
// dropped the email via classify:giveup with no signal that the cap was the
// cause.
func TestClassifyMaxTokensTruncation(t *testing.T) {
	truncated := `{"type":"input","task_id":42,"input_text":"the db url is postgres://user:hunter2@`
	var capturedReqBody []byte
	// Use the old (256) cap to prove the StopReason detection fires
	// regardless of where the cap sits, so the fix also helps an operator who
	// configures too small a value.
	c := newTestClassifier(t, &closureTransport{
		fn: func(req *http.Request) (*http.Response, error) {
			capturedReqBody, _ = io.ReadAll(req.Body)
			req.Body.Close()
			return jsonResponse(anthropicResponse("max_tokens", truncated, 256)), nil
		},
	}, 256)

	email := &adapter.Email{ID: "<test@example.com>", From: "u@gmail.com", Subject: "Re: blocked", Body: "use this long clarification"}

	_, err := c.Classify(context.Background(), email, nil, int64Ptr(42))
	if err == nil {
		t.Fatal("expected error for max_tokens truncation, got nil")
	}
	if !strings.Contains(err.Error(), "max_tokens") {
		t.Errorf("error should mention max_tokens, got: %v", err)
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error should mention truncation, got: %v", err)
	}
	if !strings.Contains(err.Error(), "256") {
		t.Errorf("error should mention the configured cap (256), got: %v", err)
	}
	// The distinct max_tokens error must short-circuit parsing so the failure is
	// diagnosable rather than looking like an arbitrary malformed response.
	if strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("max_tokens truncation must not surface as 'invalid JSON': %v", err)
	}

	// Sanity: the request actually used the configured cap (so the cap is the
	// cause and the operator can act on the error's "raise classifier
	// max_tokens" instruction).
	var req struct {
		MaxTokens int64 `json:"max_tokens"`
	}
	if err := json.Unmarshal(capturedReqBody, &req); err != nil {
		t.Fatalf("could not parse request body: %v", err)
	}
	if req.MaxTokens != 256 {
		t.Errorf("request max_tokens = %d, want 256", req.MaxTokens)
	}
}

// TestClassifyHappyPath confirms that an end_turn response with valid JSON
// still classifies normally (no regression from the StopReason check).
func TestClassifyHappyPath(t *testing.T) {
	validJSON := `{"type":"input","task_id":42,"input_text":"use --no-submit","reply":"Got it, sent.","reasoning":"user provided input","confidence":0.9}`
	c := newTestClassifier(t, &closureTransport{
		fn: func(req *http.Request) (*http.Response, error) {
			io.ReadAll(req.Body)
			req.Body.Close()
			return jsonResponse(anthropicResponse("end_turn", validJSON, 50)), nil
		},
	}, defaultMaxTokens)

	email := &adapter.Email{ID: "<test@example.com>", From: "u@gmail.com", Subject: "Re: blocked", Body: "use --no-submit"}

	action, err := c.Classify(context.Background(), email, nil, int64Ptr(42))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action.Type != ActionInput {
		t.Errorf("Type = %q, want %q", action.Type, ActionInput)
	}
	if action.TaskID != 42 {
		t.Errorf("TaskID = %d, want 42", action.TaskID)
	}
	if action.InputText != "use --no-submit" {
		t.Errorf("InputText = %q, want %q", action.InputText, "use --no-submit")
	}
	if action.Reply != "Got it, sent." {
		t.Errorf("Reply = %q, want %q", action.Reply, "Got it, sent.")
	}
}

// TestClassifyUsesConfiguredMaxTokens confirms the configured cap is what the
// SDK actually sends to the API (so operator tuning flows through end-to-end).
func TestClassifyUsesConfiguredMaxTokens(t *testing.T) {
	var capturedMaxTokens int64
	c := newTestClassifier(t, &closureTransport{
		fn: func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			req.Body.Close()
			var p struct {
				MaxTokens int64 `json:"max_tokens"`
			}
			_ = json.Unmarshal(body, &p)
			capturedMaxTokens = p.MaxTokens
			return jsonResponse(anthropicResponse("end_turn", `{"type":"ignore"}`, 1)), nil
		},
	}, 1024)

	email := &adapter.Email{ID: "<test@example.com>", From: "u@gmail.com", Subject: "x", Body: "x"}
	if _, err := c.Classify(context.Background(), email, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedMaxTokens != 1024 {
		t.Errorf("request max_tokens = %d, want 1024", capturedMaxTokens)
	}
}
