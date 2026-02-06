package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/bborn/workflow/extensions/ty-feedback/internal/bridge"
)

// mockBridge records calls to CreateTask for testing.
type mockBridge struct {
	tasks      []bridge.Task
	createErr  error
	lastInput  *bridge.FeedbackInput
	available  bool
}

func (m *mockBridge) createTask(input *bridge.FeedbackInput) (*bridge.CreateResult, error) {
	m.lastInput = input
	if m.createErr != nil {
		return nil, m.createErr
	}
	return &bridge.CreateResult{
		ID:      42,
		Title:   input.Title,
		Status:  "backlog",
		Project: input.Project,
	}, nil
}

func TestBuildTitle(t *testing.T) {
	tests := []struct {
		category string
		body     string
		want     string
	}{
		{"bug", "Login button broken", "[Bug report] Login button broken"},
		{"feature", "Dark mode support", "[Feature request] Dark mode support"},
		{"question", "How does auth work?", "[Question] How does auth work?"},
		{"other", "Just a thought", "[Feedback] Just a thought"},
		{"", "No category", "[Feedback] No category"},
		{"bug", "This is a very long description that should be truncated because it exceeds sixty characters in length", "[Bug report] This is a very long description that should be truncated ..."},
	}

	for _, tt := range tests {
		got := buildTitle(tt.category, tt.body)
		if got != tt.want {
			t.Errorf("buildTitle(%q, %q) = %q, want %q", tt.category, tt.body, got, tt.want)
		}
	}
}

func TestHandleHealth(t *testing.T) {
	br := bridge.New("nonexistent-binary-ty")
	cfg := &Config{Port: 8090, Project: "test"}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := New(br, cfg, logger)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable && w.Code != http.StatusOK {
		t.Errorf("health check returned %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["project"] != "test" {
		t.Errorf("health check project = %v, want test", resp["project"])
	}
}

func TestHandleWidget(t *testing.T) {
	br := bridge.New("ty")
	cfg := &Config{Port: 8090, Project: "myapp"}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := New(br, cfg, logger)

	req := httptest.NewRequest("GET", "/widget.js", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("widget returned %d", w.Code)
	}

	body := w.Body.String()
	if ct := w.Header().Get("Content-Type"); ct != "application/javascript" {
		t.Errorf("Content-Type = %q, want application/javascript", ct)
	}

	if !bytes.Contains([]byte(body), []byte("myapp")) {
		t.Error("widget does not contain project name")
	}
}

func TestHandleFeedbackValidation(t *testing.T) {
	br := bridge.New("ty")
	cfg := &Config{Port: 8090, Project: "test"}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := New(br, cfg, logger)

	// Missing body
	body := `{"title":"test"}`
	req := httptest.NewRequest("POST", "/api/feedback", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("empty body returned %d, want %d", w.Code, http.StatusBadRequest)
	}

	// Invalid JSON
	req = httptest.NewRequest("POST", "/api/feedback", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid json returned %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCORSMiddleware(t *testing.T) {
	br := bridge.New("ty")
	cfg := &Config{
		Port:           8090,
		Project:        "test",
		AllowedOrigins: []string{"http://localhost:3000"},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := New(br, cfg, logger)

	// Allowed origin
	req := httptest.NewRequest("OPTIONS", "/api/feedback", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("OPTIONS returned %d, want %d", w.Code, http.StatusNoContent)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("CORS origin = %q", w.Header().Get("Access-Control-Allow-Origin"))
	}

	// Disallowed origin
	req = httptest.NewRequest("OPTIONS", "/api/feedback", nil)
	req.Header.Set("Origin", "http://evil.com")
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS should not allow evil.com")
	}
}

func TestAuthMiddleware(t *testing.T) {
	br := bridge.New("ty")
	cfg := &Config{
		Port:    8090,
		Project: "test",
		APIKey:  "secret-key",
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := New(br, cfg, logger)

	// No auth header
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("no auth returned %d, want %d", w.Code, http.StatusUnauthorized)
	}

	// Wrong auth
	req = httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong auth returned %d, want %d", w.Code, http.StatusUnauthorized)
	}

	// Widget should skip auth
	req = httptest.NewRequest("GET", "/widget.js", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("widget with no auth returned %d, want %d", w.Code, http.StatusOK)
	}

	// Health should skip auth
	req = httptest.NewRequest("GET", "/health", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Error("health check should not require auth")
	}
}
