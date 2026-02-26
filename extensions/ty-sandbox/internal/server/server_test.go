package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bborn/workflow/extensions/ty-sandbox/internal/agent"
	"github.com/bborn/workflow/extensions/ty-sandbox/internal/session"
)

func newTestServer() *Server {
	registry := agent.NewRegistry()
	mgr := session.NewManager(registry)
	cfg := Config{Addr: ":0"}
	return New(cfg, registry, mgr)
}

func TestHealthEndpoint(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/v1/health", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", resp["status"])
	}
}

func TestListAgents(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/v1/agents", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	agents, ok := resp["agents"].([]any)
	if !ok || len(agents) == 0 {
		t.Fatalf("expected agents list, got %v", resp)
	}
}

func TestListSessionsEmpty(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/v1/sessions", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestCreateMockSession(t *testing.T) {
	srv := newTestServer()

	body, _ := json.Marshal(map[string]any{
		"agent":  "mock",
		"prompt": "hello world",
	})
	req := httptest.NewRequest("POST", "/v1/sessions/test-session-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp session.SessionInfo
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.ID != "test-session-1" {
		t.Fatalf("expected session ID test-session-1, got %s", resp.ID)
	}
	if resp.Status != "active" {
		t.Fatalf("expected active status, got %s", resp.Status)
	}
}

func TestGetEventsForMockSession(t *testing.T) {
	srv := newTestServer()

	// Create session
	body, _ := json.Marshal(map[string]any{
		"agent":  "mock",
		"prompt": "test prompt",
	})
	req := httptest.NewRequest("POST", "/v1/sessions/test-events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: expected 201, got %d", w.Code)
	}

	// Wait for mock agent to produce events (~400ms total)
	time.Sleep(1 * time.Second)

	// Get events
	req = httptest.NewRequest("GET", "/v1/sessions/test-events/events", nil)
	w = httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get events: expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	evts, ok := resp["events"].([]any)
	if !ok {
		t.Fatalf("expected events array, got %v", resp)
	}
	if len(evts) == 0 {
		t.Fatalf("expected events, got none")
	}
}

func TestAuthMiddleware(t *testing.T) {
	registry := agent.NewRegistry()
	mgr := session.NewManager(registry)
	cfg := Config{Addr: ":0", AuthToken: "secret123"}
	srv := New(cfg, registry, mgr)

	handler := srv.withMiddleware(srv.mux)

	// Without token
	req := httptest.NewRequest("GET", "/v1/agents", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// With correct token
	req = httptest.NewRequest("GET", "/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Health should skip auth
	req = httptest.NewRequest("GET", "/v1/health", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for health without auth, got %d", w.Code)
	}
}

func TestSessionNotFound(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest("GET", "/v1/sessions/nonexistent/events", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
