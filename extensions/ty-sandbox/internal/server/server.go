// Package server implements the HTTP/SSE API for the sandbox agent.
// The API is modeled after rivet-dev/sandbox-agent's v1 endpoints.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bborn/workflow/extensions/ty-sandbox/internal/agent"
	"github.com/bborn/workflow/extensions/ty-sandbox/internal/session"
)

// Config holds server configuration.
type Config struct {
	Addr      string
	AuthToken string
}

// Server is the HTTP server for the sandbox agent API.
type Server struct {
	cfg     Config
	manager *session.Manager
	agents  *agent.Registry
	mux     *http.ServeMux
}

// New creates a new HTTP server.
func New(cfg Config, registry *agent.Registry, manager *session.Manager) *Server {
	s := &Server{
		cfg:     cfg,
		manager: manager,
		agents:  registry,
		mux:     http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

// Start begins listening for HTTP requests.
func (s *Server) Start() error {
	handler := s.withMiddleware(s.mux)
	log.Printf("ty-sandbox listening on %s", s.cfg.Addr)
	return http.ListenAndServe(s.cfg.Addr, handler)
}

func (s *Server) withMiddleware(h http.Handler) http.Handler {
	// CORS
	h = corsMiddleware(h)
	// Auth
	if s.cfg.AuthToken != "" {
		h = authMiddleware(s.cfg.AuthToken, h)
	}
	// Logging
	h = loggingMiddleware(h)
	return h
}

func (s *Server) registerRoutes() {
	// Health
	s.mux.HandleFunc("GET /v1/health", s.handleHealth)

	// Agents
	s.mux.HandleFunc("GET /v1/agents", s.handleListAgents)
	s.mux.HandleFunc("POST /v1/agents/{agent}/install", s.handleInstallAgent)

	// Sessions
	s.mux.HandleFunc("GET /v1/sessions", s.handleListSessions)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}", s.handleCreateSession)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/messages", s.handlePostMessage)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/terminate", s.handleTerminateSession)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/events", s.handleGetEvents)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/events/sse", s.handleGetEventsSSE)

	// Questions (HITL)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/questions/{question_id}/reply", s.handleReplyQuestion)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/questions/{question_id}/reject", s.handleRejectQuestion)

	// Permissions
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/permissions/{permission_id}/reply", s.handleReplyPermission)
}

// --- Health ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": "0.1.0",
	})
}

// --- Agents ---

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"agents": s.agents.List(),
	})
}

func (s *Server) handleInstallAgent(w http.ResponseWriter, r *http.Request) {
	agentID := agent.AgentID(r.PathValue("agent"))
	a, err := s.agents.Get(agentID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if err := a.Install(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("install failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"installed": true})
}

// --- Sessions ---

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": s.manager.ListSessions(),
	})
}

type createSessionRequest struct {
	Agent        string   `json:"agent"`
	Model        string   `json:"model,omitempty"`
	Prompt       string   `json:"prompt,omitempty"`
	SystemPrompt string   `json:"system_prompt,omitempty"`
	WorkDir      string   `json:"work_dir,omitempty"`
	MaxTurns     int      `json:"max_turns,omitempty"`
	Args         []string `json:"args,omitempty"`
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")

	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	if req.Agent == "" {
		req.Agent = string(agent.AgentClaude)
	}

	cfg := agent.SpawnConfig{
		Agent:        agent.AgentID(req.Agent),
		Model:        req.Model,
		Prompt:       req.Prompt,
		SystemPrompt: req.SystemPrompt,
		WorkDir:      req.WorkDir,
		MaxTurns:     req.MaxTurns,
		Args:         req.Args,
	}

	info, err := s.manager.CreateSession(r.Context(), sessionID, cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, info)
}

type postMessageRequest struct {
	Message string `json:"message"`
}

func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")

	var req postMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	if err := s.manager.SendMessage(r.Context(), sessionID, req.Message); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}

func (s *Server) handleTerminateSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")

	if err := s.manager.TerminateSession(r.Context(), sessionID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"terminated": true})
}

// --- Events ---

func (s *Server) handleGetEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")

	var afterSeq *uint64
	if v := r.URL.Query().Get("after_sequence"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid after_sequence")
			return
		}
		afterSeq = &n
	}

	evts, err := s.manager.GetEvents(sessionID, afterSeq)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"events": evts,
	})
}

func (s *Server) handleGetEventsSSE(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")

	ch, err := s.manager.SubscribeEvents(sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				// Channel closed - session ended
				fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}

			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}

			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, data)
			flusher.Flush()
		}
	}
}

// --- Questions ---

type replyQuestionRequest struct {
	Answer string `json:"answer"`
}

func (s *Server) handleReplyQuestion(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	questionID := r.PathValue("question_id")

	var req replyQuestionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	if err := s.manager.ReplyQuestion(sessionID, questionID, req.Answer); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"replied": true})
}

func (s *Server) handleRejectQuestion(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	questionID := r.PathValue("question_id")

	if err := s.manager.RejectQuestion(sessionID, questionID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"rejected": true})
}

// --- Permissions ---

type replyPermissionRequest struct {
	Allow bool `json:"allow"`
}

func (s *Server) handleReplyPermission(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	permissionID := r.PathValue("permission_id")

	var req replyPermissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	if err := s.manager.ReplyPermission(sessionID, permissionID, req.Allow); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"replied": true})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": message,
			"status":  status,
		},
	})
}

// --- Middleware ---

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health check
		if r.URL.Path == "/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		if auth == "" {
			writeError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}

		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" || parts[1] != token {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start))
	})
}

// WithContext returns a new Server that uses the given context for background operations.
func (s *Server) WithContext(ctx context.Context) *Server {
	_ = ctx
	return s
}
