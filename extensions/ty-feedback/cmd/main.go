// Command ty-feedback provides a feedback widget and HTTP API for TaskYou.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/bborn/workflow/extensions/ty-feedback/internal/bridge"
	"github.com/bborn/workflow/extensions/ty-feedback/internal/server"
	"github.com/bborn/workflow/extensions/ty-feedback/internal/widget"
	"gopkg.in/yaml.v3"
)

// Config holds the full configuration.
type Config struct {
	Server  server.Config `yaml:"server"`
	TaskYou struct {
		CLI string `yaml:"cli"`
	} `yaml:"taskyou"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		if err := serve(); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
	case "snippet":
		snippet()
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func serve() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	br := bridge.New(cfg.TaskYou.CLI)
	if !br.IsAvailable() {
		return fmt.Errorf("ty CLI not found at: %s (is it installed and in PATH?)", cfg.TaskYou.CLI)
	}

	srv := server.New(br, &cfg.Server, logger)
	return srv.ListenAndServe()
}

func snippet() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Warning: could not load config:", err)
		fmt.Println(widget.ScriptTag("http://localhost:8090", ""))
		return
	}

	url := fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
	fmt.Println("Add this to your HTML:")
	fmt.Println()
	fmt.Println("  " + widget.ScriptTag(url, cfg.Server.APIKey))
	fmt.Println()
}

func loadConfig() (*Config, error) {
	// Check for --config flag
	path := ""
	for i, arg := range os.Args {
		if arg == "--config" && i+1 < len(os.Args) {
			path = os.Args[i+1]
			break
		}
	}

	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "ty-feedback", "config.yaml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config not found at %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Defaults
	if cfg.TaskYou.CLI == "" {
		cfg.TaskYou.CLI = "ty"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8090
	}
	if cfg.Server.Project == "" {
		cfg.Server.Project = "feedback"
	}

	return &cfg, nil
}

func printUsage() {
	fmt.Println(`ty-feedback - Feedback widget and API for TaskYou

Usage:
  ty-feedback serve [--config path]    Start the feedback API server
  ty-feedback snippet                  Print the HTML snippet to embed the widget
  ty-feedback help                     Show this help

The server provides:
  POST /api/feedback     Submit feedback (creates a task)
  GET  /api/tasks        List tasks for the project
  GET  /api/tasks/:id    Get task details
  POST /api/tasks/:id/input  Send input to a blocked task
  GET  /widget.js        Embeddable JavaScript widget
  GET  /health           Health check

Config: ~/.config/ty-feedback/config.yaml (or --config path)`)
}
