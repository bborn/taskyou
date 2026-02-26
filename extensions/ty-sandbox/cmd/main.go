// ty-sandbox is an HTTP/SSE server that enables remote control of coding agents
// (Claude Code, Codex, etc.) running in isolated sandbox environments.
//
// It exposes a unified REST + SSE API modeled after rivet-dev/sandbox-agent,
// and integrates with TaskYou for task tracking.
//
// Usage:
//
//	ty-sandbox [flags]
//	ty-sandbox --addr :8080 --auth-token secret123
//	ty-sandbox --work-dir /workspace
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/bborn/workflow/extensions/ty-sandbox/internal/agent"
	"github.com/bborn/workflow/extensions/ty-sandbox/internal/bridge"
	"github.com/bborn/workflow/extensions/ty-sandbox/internal/server"
	"github.com/bborn/workflow/extensions/ty-sandbox/internal/session"
)

var (
	version = "0.1.0"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	authToken := flag.String("auth-token", "", "bearer token for API authentication (or SANDBOX_AUTH_TOKEN env)")
	workDir := flag.String("work-dir", "", "default working directory for agent sessions")
	tyPath := flag.String("ty-path", "", "path to ty CLI for TaskYou integration")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("ty-sandbox %s\n", version)
		os.Exit(0)
	}

	// Auth token from env if not set via flag
	if *authToken == "" {
		*authToken = os.Getenv("SANDBOX_AUTH_TOKEN")
	}

	// Work dir from env if not set
	if *workDir == "" {
		*workDir = os.Getenv("SANDBOX_WORK_DIR")
	}

	log.Printf("ty-sandbox %s starting", version)

	// Initialize components
	registry := agent.NewRegistry()
	mgr := session.NewManager(registry)
	br := bridge.New(*tyPath)

	if br.IsAvailable() {
		log.Printf("TaskYou bridge: available")
	} else {
		log.Printf("TaskYou bridge: not available (ty CLI not found)")
	}

	cfg := server.Config{
		Addr:      *addr,
		AuthToken: *authToken,
	}

	srv := server.New(cfg, registry, mgr)

	// Graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = ctx

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("shutting down...")
		cancel()
		os.Exit(0)
	}()

	if err := srv.Start(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
