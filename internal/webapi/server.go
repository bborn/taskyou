// Package webapi provides the HTTP API that runs inside each user's sprite.
// This exposes the task database and executor state via REST and WebSocket.
package webapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/log"
	"github.com/gorilla/websocket"
)

// Server is the web API server that runs inside each sprite.
type Server struct {
	db        *db.DB
	addr      string
	logger    *log.Logger
	wsHub     *WebSocketHub
	devMode   bool
	devOrigin string
}

// Config holds server configuration.
type Config struct {
	Addr      string
	DB        *db.DB
	DevMode   bool   // Enable CORS for local development
	DevOrigin string // Allowed origin in dev mode (e.g., "http://localhost:5173")
}

// New creates a new API server.
func New(cfg Config) *Server {
	return &Server{
		db:        cfg.DB,
		addr:      cfg.Addr,
		logger:    log.NewWithOptions(os.Stderr, log.Options{Prefix: "webapi"}),
		wsHub:     NewWebSocketHub(),
		devMode:   cfg.DevMode,
		devOrigin: cfg.DevOrigin,
	}
}

// Start starts the API server.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /health", s.handleHealth)

	// Task endpoints
	mux.HandleFunc("GET /tasks", s.handleListTasks)
	mux.HandleFunc("POST /tasks", s.handleCreateTask)
	mux.HandleFunc("GET /tasks/{id}", s.handleGetTask)
	mux.HandleFunc("PUT /tasks/{id}", s.handleUpdateTask)
	mux.HandleFunc("DELETE /tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("POST /tasks/{id}/queue", s.handleQueueTask)
	mux.HandleFunc("POST /tasks/{id}/retry", s.handleRetryTask)
	mux.HandleFunc("POST /tasks/{id}/close", s.handleCloseTask)

	// Project endpoints
	mux.HandleFunc("GET /projects", s.handleListProjects)
	mux.HandleFunc("POST /projects", s.handleCreateProject)
	mux.HandleFunc("GET /projects/{id}", s.handleGetProject)
	mux.HandleFunc("PUT /projects/{id}", s.handleUpdateProject)
	mux.HandleFunc("DELETE /projects/{id}", s.handleDeleteProject)

	// Settings endpoints
	mux.HandleFunc("GET /settings", s.handleGetSettings)
	mux.HandleFunc("PUT /settings", s.handleUpdateSettings)

	// Task logs
	mux.HandleFunc("GET /tasks/{id}/logs", s.handleGetTaskLogs)

	// WebSocket
	mux.HandleFunc("GET /ws", s.handleWebSocket)

	// Apply middleware
	var handler http.Handler = mux
	if s.devMode {
		handler = s.corsMiddleware(handler)
	}
	handler = s.loggingMiddleware(handler)

	server := &http.Server{
		Addr:         s.addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start WebSocket hub
	go s.wsHub.Run()

	s.logger.Info("starting API server", "addr", s.addr)

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// corsMiddleware adds CORS headers for local development.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := s.devOrigin
		if origin == "" {
			origin = "http://localhost:5173"
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs HTTP requests.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)

		s.logger.Debug("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration", time.Since(start),
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// JSON response helpers
func jsonResponse(w http.ResponseWriter, data interface{}, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, message string, status int) {
	jsonResponse(w, map[string]string{"error": message}, status)
}

func parseJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// getIDParam extracts an ID from the URL path.
func getIDParam(r *http.Request) (int64, error) {
	idStr := r.PathValue("id")
	return strconv.ParseInt(idStr, 10, 64)
}

// handleHealth handles health check requests.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// BroadcastTaskUpdate broadcasts a task update to all connected WebSocket clients.
func (s *Server) BroadcastTaskUpdate(task *db.Task) {
	s.wsHub.Broadcast(Message{
		Type: "task_update",
		Data: task,
	})
}

// BroadcastTaskLog broadcasts a task log entry to all connected WebSocket clients.
func (s *Server) BroadcastTaskLog(taskID int64, log *db.TaskLog) {
	s.wsHub.Broadcast(Message{
		Type: "task_log",
		Data: map[string]interface{}{
			"task_id": taskID,
			"log":     log,
		},
	})
}

// WebSocketHub manages WebSocket connections.
type WebSocketHub struct {
	clients    map[*WebSocketClient]bool
	broadcast  chan Message
	register   chan *WebSocketClient
	unregister chan *WebSocketClient
	mu         sync.RWMutex
}

// WebSocketClient represents a connected WebSocket client.
type WebSocketClient struct {
	hub  *WebSocketHub
	conn *websocket.Conn
	send chan []byte
}

// Message represents a WebSocket message.
type Message struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// NewWebSocketHub creates a new WebSocket hub.
func NewWebSocketHub() *WebSocketHub {
	return &WebSocketHub{
		clients:    make(map[*WebSocketClient]bool),
		broadcast:  make(chan Message),
		register:   make(chan *WebSocketClient),
		unregister: make(chan *WebSocketClient),
	}
}

// Run starts the WebSocket hub.
func (h *WebSocketHub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			data, err := json.Marshal(message)
			if err != nil {
				continue
			}

			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- data:
				default:
					h.mu.RUnlock()
					h.mu.Lock()
					close(client.send)
					delete(h.clients, client)
					h.mu.Unlock()
					h.mu.RLock()
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast sends a message to all connected clients.
func (h *WebSocketHub) Broadcast(msg Message) {
	h.broadcast <- msg
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins (we're behind the proxy)
	},
}

// handleWebSocket handles WebSocket connections.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("websocket upgrade failed", "error", err)
		return
	}

	client := &WebSocketClient{
		hub:  s.wsHub,
		conn: conn,
		send: make(chan []byte, 256),
	}

	s.wsHub.register <- client

	// Start goroutines for reading and writing
	go client.writePump()
	go client.readPump()
}

func (c *WebSocketClient) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(512 * 1024) // 512KB
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				// Log unexpected close errors
			}
			break
		}
	}
}

func (c *WebSocketClient) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
