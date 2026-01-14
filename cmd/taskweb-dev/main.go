// taskweb-dev runs the web API server locally for development.
// It connects to your local task database and serves the API on port 8081.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/webapi"
)

func main() {
	// Use the same database as the local task CLI
	dbPath := db.DefaultPath()
	fmt.Printf("Using database: %s\n", dbPath)

	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// Create the API server with dev mode enabled
	server := webapi.New(webapi.Config{
		Addr:      ":8081",
		DB:        database,
		DevMode:   true,
		DevOrigin: "http://localhost:5173",
	})

	// Handle graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	fmt.Println("Starting web API server on http://localhost:8081")
	fmt.Println("Frontend should run on http://localhost:5173")
	fmt.Println()
	fmt.Println("To start the frontend:")
	fmt.Println("  cd web && npm install && npm run dev")
	fmt.Println()

	if err := server.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
