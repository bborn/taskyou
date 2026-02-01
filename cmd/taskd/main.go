// taskd is the task queue daemon.
// It runs an SSH server that serves the TUI and a background executor for Claude tasks.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/server"
	"github.com/charmbracelet/log"
)

func main() {
	// Flags
	addr := flag.String("addr", ":2222", "SSH server address")
	dbPath := flag.String("db", "", "Database path (default: ~/.local/share/task/tasks.db)")
	hostKey := flag.String("host-key", "", "SSH host key path (default: ~/.ssh/task_ed25519)")
	flag.Parse()

	// Setup logger
	logger := log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: true,
		Prefix:          "taskd",
	})

	// Resolve paths
	home, _ := os.UserHomeDir()

	if *dbPath == "" {
		*dbPath = filepath.Join(home, ".local", "share", "task", "tasks.db")
	}
	if *hostKey == "" {
		*hostKey = filepath.Join(home, ".ssh", "task_ed25519")
	}

	// Open database
	database, err := db.Open(*dbPath)
	if err != nil {
		logger.Fatal("Failed to open database", "error", err)
	}
	defer database.Close()
	logger.Info("Database opened", "path", *dbPath)

	// Load config from database
	cfg := config.New(database)

	logger.Info("Starting taskd",
		"addr", *addr,
		"db", *dbPath,
		"projects_dir", cfg.ProjectsDir,
	)

	// Create executor (with logging enabled for daemon mode)
	exec := executor.NewWithLogging(database, cfg, os.Stderr)

	// Create SSH server
	srv, err := server.New(server.Config{
		Addr:        *addr,
		HostKeyPath: *hostKey,
		DB:          database,
		Executor:    exec,
	})
	if err != nil {
		logger.Fatal("Failed to create server", "error", err)
	}

	// Start background executor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec.Start(ctx)

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start server
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	logger.Info("SSH server listening", "addr", *addr)
	fmt.Printf("\n  SSH: ssh -p %s localhost\n\n", (*addr)[1:])

	// Wait for signal or error
	select {
	case err := <-errCh:
		if err != nil {
			logger.Fatal("Server error", "error", err)
		}
	case sig := <-sigCh:
		logger.Info("Received signal, shutting down", "signal", sig)
		exec.Stop()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		srv.Shutdown(shutdownCtx)
	}
}
