// Package server provides the HTTP API for the feedback widget.
package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/bborn/workflow/extensions/ty-feedback/internal/bridge"
	"github.com/bborn/workflow/extensions/ty-feedback/internal/widget"
)

// Config holds server configuration.
type Config struct {
	Port           int      `yaml:"port"`
	Project        string   `yaml:"project"`
	AllowedOrigins []string `yaml:"allowed_origins"`
	APIKey         string   `yaml:"api_key"`
	DefaultTags    string   `yaml:"default_tags"`
	AutoExecute    bool     `yaml:"auto_execute"`
}

// Server is the HTTP server for the feedback widget.
type Server struct {
	bridge *bridge.Bridge
	config *Config
	logger *slog.Logger
	mux    *http.ServeMux
}

// New creates a new feedback server.
func New(br *bridge.Bridge, cfg *Config, logger *slog.Logger) *Server {
	s := &Server{
		bridge: br,
		config: cfg,
		logger: logger,
		mux:    http.NewServeMux(),
	}

	s.mux.HandleFunc("POST /api/feedback", s.handleFeedback)
	s.mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	s.mux.HandleFunc("GET /api/tasks/", s.handleGetTask)
	s.mux.HandleFunc("POST /api/tasks/", s.handleTaskInput)
	s.mux.HandleFunc("GET /widget.js", s.handleWidget)
	s.mux.HandleFunc("GET /health", s.handleHealth)

	return s
}

// Handler returns the HTTP handler with middleware applied.
func (s *Server) Handler() http.Handler {
	return s.corsMiddleware(s.authMiddleware(s.mux))
}

// ListenAndServe starts the server.
func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf(":%d", s.config.Port)
	s.logger.Info("feedback server starting", "addr", addr, "project", s.config.Project)
	return http.ListenAndServe(addr, s.Handler())
}

// FeedbackRequest is the JSON body for POST /api/feedback.
type FeedbackRequest struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Category string `json:"category"` // bug, feature, question, other
	URL      string `json:"url"`      // Page URL where feedback was submitted
	UserInfo string `json:"user"`     // Optional user identifier
}

// FeedbackResponse is returned after creating feedback.
type FeedbackResponse struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Project string `json:"project"`
}

// ErrorResponse is returned on errors.
type ErrorResponse struct {
	Error string `json:"error"`
}

func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	var req FeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Body == "" {
		s.jsonError(w, "body is required", http.StatusBadRequest)
		return
	}

	// Build task title
	title := req.Title
	if title == "" {
		title = buildTitle(req.Category, req.Body)
	}

	// Build task body with metadata
	body := req.Body
	if req.URL != "" || req.UserInfo != "" || req.Category != "" {
		var meta []string
		if req.Category != "" {
			meta = append(meta, fmt.Sprintf("Category: %s", req.Category))
		}
		if req.URL != "" {
			meta = append(meta, fmt.Sprintf("Page: %s", req.URL))
		}
		if req.UserInfo != "" {
			meta = append(meta, fmt.Sprintf("User: %s", req.UserInfo))
		}
		body = strings.Join(meta, "\n") + "\n\n" + body
	}

	// Build tags
	tags := s.config.DefaultTags
	if req.Category != "" {
		if tags != "" {
			tags += ","
		}
		tags += req.Category
	}

	input := &bridge.FeedbackInput{
		Title:   title,
		Body:    body,
		Project: s.config.Project,
		Type:    "code",
		Tags:    tags,
		Execute: s.config.AutoExecute,
	}

	result, err := s.bridge.CreateTask(input)
	if err != nil {
		s.logger.Error("failed to create task", "error", err)
		s.jsonError(w, "failed to create task", http.StatusInternalServerError)
		return
	}

	s.logger.Info("feedback submitted", "task_id", result.ID, "title", result.Title)

	resp := FeedbackResponse{
		ID:      result.ID,
		Title:   result.Title,
		Status:  result.Status,
		Project: result.Project,
	}
	s.jsonResponse(w, resp, http.StatusCreated)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	tasks, err := s.bridge.ListTasks(s.config.Project, status)
	if err != nil {
		s.logger.Error("failed to list tasks", "error", err)
		s.jsonError(w, "failed to list tasks", http.StatusInternalServerError)
		return
	}

	s.jsonResponse(w, tasks, http.StatusOK)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	// Check if this is a sub-path like /api/tasks/123/input
	parts := strings.SplitN(idStr, "/", 2)
	idStr = parts[0]

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.jsonError(w, "invalid task ID", http.StatusBadRequest)
		return
	}

	task, err := s.bridge.GetTask(id)
	if err != nil {
		s.jsonError(w, "task not found", http.StatusNotFound)
		return
	}

	s.jsonResponse(w, task, http.StatusOK)
}

func (s *Server) handleTaskInput(w http.ResponseWriter, r *http.Request) {
	// POST /api/tasks/123/input
	path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[1] != "input" {
		s.jsonError(w, "not found", http.StatusNotFound)
		return
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		s.jsonError(w, "invalid task ID", http.StatusBadRequest)
		return
	}

	var req struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.bridge.SendInput(id, req.Input); err != nil {
		s.logger.Error("failed to send input", "task_id", id, "error", err)
		s.jsonError(w, "failed to send input", http.StatusInternalServerError)
		return
	}

	s.jsonResponse(w, map[string]string{"status": "sent"}, http.StatusOK)
}

func (s *Server) handleWidget(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "public, max-age=3600")

	apiURL := r.URL.Query().Get("api")
	if apiURL == "" {
		// Default to the same origin
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		apiURL = fmt.Sprintf("%s://%s", scheme, r.Host)
	}

	js := widget.Generate(apiURL, s.config.Project)
	w.Write([]byte(js))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	available := s.bridge.IsAvailable()
	status := http.StatusOK
	if !available {
		status = http.StatusServiceUnavailable
	}
	s.jsonResponse(w, map[string]interface{}{
		"status":       "ok",
		"ty_available": available,
		"project":      s.config.Project,
	}, status)
}

// corsMiddleware adds CORS headers.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false

		if len(s.config.AllowedOrigins) == 0 {
			// Allow all origins if none specified
			allowed = true
		} else {
			for _, o := range s.config.AllowedOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}
		}

		if allowed && origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// authMiddleware checks the API key if configured.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for widget.js and health endpoints
		if r.URL.Path == "/widget.js" || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Skip auth for OPTIONS
		if r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}

		if s.config.APIKey != "" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+s.config.APIKey {
				s.jsonError(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) jsonResponse(w http.ResponseWriter, data interface{}, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) jsonError(w http.ResponseWriter, msg string, status int) {
	s.jsonResponse(w, ErrorResponse{Error: msg}, status)
}

func buildTitle(category, body string) string {
	prefix := "Feedback"
	switch category {
	case "bug":
		prefix = "Bug report"
	case "feature":
		prefix = "Feature request"
	case "question":
		prefix = "Question"
	}

	// Use first line of body as title, truncated
	firstLine := strings.SplitN(body, "\n", 2)[0]
	if len(firstLine) > 60 {
		firstLine = firstLine[:57] + "..."
	}

	return fmt.Sprintf("[%s] %s", prefix, firstLine)
}
