// Package server provides the SSH server using Wish.
package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	"github.com/charmbracelet/wish/bubbletea"
	"github.com/charmbracelet/wish/logging"
)

// Server is the SSH server.
type Server struct {
	db       *db.DB
	executor *executor.Executor
	srv      *ssh.Server
	logger   *log.Logger
	addr     string
	hostKey  string
}

// Config holds server configuration.
type Config struct {
	Addr        string // e.g. ":2222"
	HostKeyPath string // e.g. ".ssh/task_ed25519"
	DB          *db.DB
	Executor    *executor.Executor
}

// New creates a new SSH server.
func New(cfg Config) (*Server, error) {
	s := &Server{
		db:       cfg.DB,
		executor: cfg.Executor,
		addr:     cfg.Addr,
		hostKey:  cfg.HostKeyPath,
		logger:   log.NewWithOptions(os.Stderr, log.Options{Prefix: "ssh"}),
	}

	// Ensure host key directory exists
	if err := os.MkdirAll(filepath.Dir(s.hostKey), 0700); err != nil {
		return nil, fmt.Errorf("create host key dir: %w", err)
	}

	srv, err := wish.NewServer(
		wish.WithAddress(s.addr),
		wish.WithHostKeyPath(s.hostKey),
		wish.WithMiddleware(
			bubbletea.Middleware(s.teaHandler),
			activeterm.Middleware(),
			logging.Middleware(),
		),
		// Accept all public keys for now (you'd add proper auth here)
		wish.WithPublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
			return true // Accept all keys - customize for security
		}),
		wish.WithPasswordAuth(func(ctx ssh.Context, password string) bool {
			return false // Disable password auth
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create server: %w", err)
	}

	s.srv = srv
	return s, nil
}

// Start starts the SSH server.
func (s *Server) Start() error {
	s.logger.Info("SSH server starting", "addr", s.addr)
	return s.srv.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("SSH server shutting down")
	return s.srv.Shutdown(ctx)
}

// teaHandler returns the Bubble Tea program for each SSH session.
func (s *Server) teaHandler(sess ssh.Session) (tea.Model, []tea.ProgramOption) {
	model := ui.NewAppModel(s.db, s.executor)

	return model, []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	}
}
