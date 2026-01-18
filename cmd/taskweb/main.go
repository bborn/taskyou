// Package main provides the entry point for the taskweb server.
// This server provides a web-based UI for taskyou with Fly Sprites integration.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bborn/workflow/internal/hostdb"
	"github.com/bborn/workflow/internal/webserver"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

var (
	addr    string
	dbPath  string
	baseURL string
	secure  bool
	domain  string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "taskweb",
		Short: "Web server for taskyou with Fly Sprites integration",
		Long: `taskweb provides a web-based UI for taskyou.

Each user gets their own isolated Fly Sprite running the task executor.
User data (tasks, projects) stays entirely within their sprite.
This host service handles only authentication and sprite orchestration.

Environment variables:
  TASKWEB_DATABASE_PATH  - Path to host database (default: ~/.local/share/taskweb/taskweb.db)
  GOOGLE_CLIENT_ID       - Google OAuth client ID
  GOOGLE_CLIENT_SECRET   - Google OAuth client secret
  GITHUB_CLIENT_ID       - GitHub OAuth client ID
  GITHUB_CLIENT_SECRET   - GitHub OAuth client secret
  SPRITES_TOKEN          - Fly Sprites API token`,
		Run: runServer,
	}

	rootCmd.Flags().StringVarP(&addr, "addr", "a", getEnvOrDefault("TASKWEB_ADDR", ":8080"), "Server address")
	rootCmd.Flags().StringVarP(&dbPath, "db", "d", hostdb.DefaultPath(), "Database path")
	rootCmd.Flags().StringVar(&baseURL, "base-url", getEnvOrDefault("TASKWEB_BASE_URL", "http://localhost:8080"), "Base URL for OAuth callbacks")
	rootCmd.Flags().BoolVar(&secure, "secure", getEnvOrDefault("TASKWEB_SECURE", "") == "true", "Use secure cookies (HTTPS)")
	rootCmd.Flags().StringVar(&domain, "domain", getEnvOrDefault("TASKWEB_DOMAIN", ""), "Cookie domain")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runServer(cmd *cobra.Command, args []string) {
	logger := log.NewWithOptions(os.Stderr, log.Options{
		Prefix: "taskweb",
	})

	// Open host database
	db, err := hostdb.Open(dbPath)
	if err != nil {
		logger.Fatal("failed to open database", "error", err)
	}
	defer db.Close()

	logger.Info("database opened", "path", dbPath)

	// Create server
	server, err := webserver.New(webserver.Config{
		Addr:    addr,
		DB:      db,
		BaseURL: baseURL,
		Secure:  secure,
		Domain:  domain,
	})
	if err != nil {
		logger.Fatal("failed to create server", "error", err)
	}

	// Setup context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	// Log configuration
	logger.Info("starting server",
		"addr", addr,
		"base_url", baseURL,
		"secure", secure,
	)

	// Check OAuth configuration
	if os.Getenv("GOOGLE_CLIENT_ID") == "" && os.Getenv("GITHUB_CLIENT_ID") == "" {
		logger.Warn("no OAuth providers configured - authentication will not work")
	}

	if os.Getenv("SPRITES_TOKEN") == "" {
		logger.Warn("SPRITES_TOKEN not set - sprite management will not work")
	}

	// Start server
	if err := server.Start(ctx); err != nil {
		logger.Fatal("server error", "error", err)
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
