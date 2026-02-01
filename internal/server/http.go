// Package server provides HTTP endpoints for event streaming.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/events"
	"github.com/charmbracelet/log"
)

// HTTPServer serves HTTP endpoints for event streaming and API access.
type HTTPServer struct {
	addr          string
	eventsManager *events.Manager
	logger        *log.Logger
	srv           *http.Server

	// SSE connection tracking
	mu          sync.RWMutex
	connections map[*sseConnection]bool
}

// sseConnection represents an active SSE client.
type sseConnection struct {
	id            string
	w             http.ResponseWriter
	flusher       http.Flusher
	eventChan     chan events.Event
	closeCh       chan struct{}
	typeFilter    string
	taskIDFilter  int64
	projectFilter string
}

// NewHTTPServer creates a new HTTP server for events.
func NewHTTPServer(addr string, eventsManager *events.Manager) *HTTPServer {
	h := &HTTPServer{
		addr:          addr,
		eventsManager: eventsManager,
		logger:        log.NewWithOptions(os.Stderr, log.Options{Prefix: "http"}),
		connections:   make(map[*sseConnection]bool),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/events/stream", h.handleEventStream)
	mux.HandleFunc("/health", h.handleHealth)

	h.srv = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0, // No timeout for SSE
		IdleTimeout:  120 * time.Second,
	}

	return h
}

// Start starts the HTTP server.
func (h *HTTPServer) Start() error {
	h.logger.Info("HTTP server starting", "addr", h.addr)
	if err := h.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server error: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the HTTP server.
func (h *HTTPServer) Shutdown(ctx context.Context) error {
	h.logger.Info("HTTP server shutting down")
	
	// Close all SSE connections
	h.mu.Lock()
	for conn := range h.connections {
		close(conn.closeCh)
		delete(h.connections, conn)
	}
	h.mu.Unlock()

	return h.srv.Shutdown(ctx)
}

// handleHealth is a simple health check endpoint.
func (h *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, `{"status":"ok"}`)
}

// handleEventStream streams events via Server-Sent Events (SSE).
// Query parameters:
//   - type: Filter by event type (e.g., task.completed)
//   - task: Filter by task ID
//   - project: Filter by project name
func (h *HTTPServer) handleEventStream(w http.ResponseWriter, r *http.Request) {
	// Check if client supports SSE
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Parse filters from query parameters
	typeFilter := r.URL.Query().Get("type")
	taskIDFilter := int64(0)
	if taskIDStr := r.URL.Query().Get("task"); taskIDStr != "" {
		if id, err := strconv.ParseInt(taskIDStr, 10, 64); err == nil {
			taskIDFilter = id
		}
	}
	projectFilter := r.URL.Query().Get("project")

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*") // Allow CORS for web clients

	// Create connection
	conn := &sseConnection{
		id:            fmt.Sprintf("%d", time.Now().UnixNano()),
		w:             w,
		flusher:       flusher,
		eventChan:     make(chan events.Event, 100),
		closeCh:       make(chan struct{}),
		typeFilter:    typeFilter,
		taskIDFilter:  taskIDFilter,
		projectFilter: projectFilter,
	}

	// Register connection
	h.mu.Lock()
	h.connections[conn] = true
	h.mu.Unlock()

	// Subscribe to events
	globalEventChan := h.eventsManager.Subscribe()
	defer h.eventsManager.Unsubscribe(globalEventChan)

	h.logger.Debug("SSE client connected", "id", conn.id, "filters", map[string]interface{}{
		"type":    typeFilter,
		"task":    taskIDFilter,
		"project": projectFilter,
	})

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"message\":\"Connected to TaskYou event stream\"}\n\n")
	flusher.Flush()

	// Create context that cancels when client disconnects
	ctx := r.Context()

	// Forward events to this connection in background
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-conn.closeCh:
				return
			case event := <-globalEventChan:
				// Apply filters
				if !h.matchesFilters(event, conn) {
					continue
				}
				
				// Send to connection's channel (non-blocking)
				select {
				case conn.eventChan <- event:
				default:
					// Channel full, skip event
				}
			}
		}
	}()

	// Main event loop - send events to client
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			h.removeConnection(conn)
			h.logger.Debug("SSE client disconnected", "id", conn.id)
			return

		case <-conn.closeCh:
			// Server shutting down
			h.removeConnection(conn)
			return

		case event := <-conn.eventChan:
			// Send event to client
			if err := h.sendEvent(conn, event); err != nil {
				h.logger.Debug("Error sending event to client", "id", conn.id, "error", err)
				h.removeConnection(conn)
				return
			}

		case <-ticker.C:
			// Send keepalive comment to prevent connection timeout
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// matchesFilters checks if an event matches the connection's filters.
func (h *HTTPServer) matchesFilters(event events.Event, conn *sseConnection) bool {
	// Type filter
	if conn.typeFilter != "" && event.Type != conn.typeFilter {
		return false
	}

	// Task ID filter
	if conn.taskIDFilter > 0 && event.TaskID != conn.taskIDFilter {
		return false
	}

	// Project filter
	if conn.projectFilter != "" && event.Task != nil {
		if !strings.EqualFold(event.Task.Project, conn.projectFilter) {
			return false
		}
	}

	return true
}

// sendEvent sends an event to an SSE client.
func (h *HTTPServer) sendEvent(conn *sseConnection, event events.Event) error {
	// Serialize event to JSON
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	// Send SSE formatted event
	// Format: event: <type>\ndata: <json>\n\n
	_, err = fmt.Fprintf(conn.w, "event: %s\ndata: %s\n\n", event.Type, string(data))
	if err != nil {
		return err
	}

	conn.flusher.Flush()
	return nil
}

// removeConnection removes a connection from tracking.
func (h *HTTPServer) removeConnection(conn *sseConnection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if _, exists := h.connections[conn]; exists {
		delete(h.connections, conn)
		close(conn.eventChan)
	}
}

// ActiveConnections returns the number of active SSE connections.
func (h *HTTPServer) ActiveConnections() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.connections)
}
