// Package web provides a lightweight HTTP API server for TaskYou.
// It exposes the same operations as the CLI over HTTP/JSON,
// allowing external frontends (like ty-web) to build on top.
package web

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// CommandRunner abstracts command execution for testability.
type CommandRunner interface {
	Run(name string, args ...string) error
	Output(name string, args ...string) ([]byte, error)
}

// Config holds web server configuration.
type Config struct {
	Addr      string // e.g. ":8080"
	DB        *db.DB
	CmdRunner CommandRunner
}

// Server is the HTTP API server.
type Server struct {
	db      *db.DB
	srv     *http.Server
	runner  CommandRunner
	relay   *browserRelay
	baseURL string
}

// cors wraps a handler with permissive CORS headers for local development.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// New creates a new API server.
func New(cfg Config) *Server {
	s := &Server{
		db:      cfg.DB,
		runner:  cfg.CmdRunner,
		relay:   newBrowserRelay(),
		baseURL: baseURLFromAddr(cfg.Addr),
	}

	mux := http.NewServeMux()

	// Board
	mux.HandleFunc("GET /api/board", s.handleBoard)
	mux.HandleFunc("GET /api/board/stream", s.handleBoardStream)

	// Tasks CRUD
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleTaskDetail)
	mux.HandleFunc("PATCH /api/tasks/{id}", s.handleUpdateTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.handleDeleteTask)

	// Task actions
	mux.HandleFunc("POST /api/tasks/{id}/move", s.handleMoveTask)
	mux.HandleFunc("POST /api/tasks/{id}/status", s.handleSetStatus)
	mux.HandleFunc("POST /api/tasks/{id}/execute", s.handleExecuteTask)
	mux.HandleFunc("POST /api/tasks/{id}/close", s.handleCloseTask)
	mux.HandleFunc("POST /api/tasks/{id}/retry", s.handleRetryTask)
	mux.HandleFunc("POST /api/tasks/{id}/pin", s.handlePinTask)
	mux.HandleFunc("POST /api/tasks/{id}/input", s.handleTaskInput)
	mux.HandleFunc("POST /api/tasks/{id}/annotations", s.handleTaskAnnotations)

	// Browser bridge (executor ↔ ty-chrome extension)
	mux.HandleFunc("POST /api/tasks/{id}/browser", s.handleBrowserExec)
	mux.HandleFunc("GET /api/tasks/{id}/browser/poll", s.handleBrowserPoll)
	mux.HandleFunc("POST /api/tasks/{id}/browser/result", s.handleBrowserResult)

	// Task logs, streaming, executor output & terminal
	mux.HandleFunc("GET /api/tasks/{id}/logs", s.handleTaskLogs)
	mux.HandleFunc("GET /api/tasks/{id}/stream", s.handleTaskStream)
	mux.HandleFunc("GET /api/tasks/{id}/output", s.handleTaskOutput)
	mux.HandleFunc("GET /api/tasks/{id}/terminal", s.handleTerminal)

	// Dependencies
	mux.HandleFunc("GET /api/tasks/{id}/deps", s.handleGetDeps)
	mux.HandleFunc("POST /api/tasks/{id}/block", s.handleBlock)
	mux.HandleFunc("POST /api/tasks/{id}/unblock", s.handleUnblock)

	// Projects
	mux.HandleFunc("GET /api/projects", s.handleListProjects)
	mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	mux.HandleFunc("GET /api/projects/{name}", s.handleGetProject)
	mux.HandleFunc("PATCH /api/projects/{name}", s.handleUpdateProject)
	mux.HandleFunc("DELETE /api/projects/{name}", s.handleDeleteProject)

	// Task types
	mux.HandleFunc("GET /api/types", s.handleListTypes)
	mux.HandleFunc("POST /api/types", s.handleCreateType)
	mux.HandleFunc("GET /api/types/{name}", s.handleGetType)
	mux.HandleFunc("PATCH /api/types/{name}", s.handleUpdateType)
	mux.HandleFunc("DELETE /api/types/{name}", s.handleDeleteType)

	// Events
	mux.HandleFunc("GET /api/events", s.handleListEvents)

	// Status
	mux.HandleFunc("GET /api/status", s.handleStatus)

	s.srv = &http.Server{
		Addr:         cfg.Addr,
		Handler:      cors(mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 60 * time.Second, // long for SSE
		IdleTimeout:  120 * time.Second,
	}

	return s
}

// baseURLFromAddr turns a listen address like ":8080" into a URL the executor
// can curl from inside the worktree.
func baseURLFromAddr(addr string) string {
	host, port, ok := strings.Cut(addr, ":")
	if !ok || port == "" {
		return "http://127.0.0.1:8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

// Start begins listening. It blocks until the server shuts down.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.srv.Addr, err)
	}
	log.Printf("TaskYou API server listening on http://%s", ln.Addr())
	if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
