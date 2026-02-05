// Command ty-qmd provides QMD search integration for TaskYou.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bborn/workflow/extensions/ty-qmd/internal/exporter"
	"github.com/bborn/workflow/extensions/ty-qmd/internal/qmd"
	"github.com/bborn/workflow/extensions/ty-qmd/internal/tasks"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Config holds the configuration.
type Config struct {
	QMD struct {
		Binary string `yaml:"binary"`
		Index  string `yaml:"index"`
	} `yaml:"qmd"`
	TaskYou struct {
		DB  string `yaml:"db"`
		CLI string `yaml:"cli"`
	} `yaml:"taskyou"`
	Sync struct {
		Auto         bool          `yaml:"auto"`
		Interval     time.Duration `yaml:"interval"`
		Statuses     []string      `yaml:"statuses"`
		IncludeLogs  bool          `yaml:"include_logs"`
		MaxLogLines  int           `yaml:"max_log_lines"`
	} `yaml:"sync"`
	Collections struct {
		Tasks    string            `yaml:"tasks"`
		Projects map[string]string `yaml:"projects"`
	} `yaml:"collections"`
}

var (
	cfgFile string
	verbose bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "ty-qmd",
		Short: "QMD search integration for TaskYou",
		Long:  "Index completed tasks and search across task history, project docs, and knowledge bases.",
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.config/ty-qmd/config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	rootCmd.AddCommand(syncCmd())
	rootCmd.AddCommand(searchCmd())
	rootCmd.AddCommand(serveCmd())
	rootCmd.AddCommand(indexProjectCmd())
	rootCmd.AddCommand(statusCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func syncCmd() *cobra.Command {
	var all bool
	var project string

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync completed tasks to QMD index",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			logger := setupLogger()
			q := qmd.New(cfg.QMD.Binary, logger)

			// Check qmd is available
			if !q.IsAvailable() {
				return fmt.Errorf("qmd not found at: %s\nInstall with: bun install -g github:tobi/qmd", cfg.QMD.Binary)
			}

			// Open tasks database
			taskDB, err := tasks.Open(cfg.TaskYou.DB)
			if err != nil {
				return fmt.Errorf("failed to open tasks db: %w", err)
			}
			defer taskDB.Close()

			// Get tasks to sync
			opts := tasks.ListOptions{
				Statuses: cfg.Sync.Statuses,
			}
			if project != "" {
				opts.Project = project
			}
			if !all {
				opts.NotSynced = true
			}

			taskList, err := taskDB.ListTasks(opts)
			if err != nil {
				return fmt.Errorf("failed to list tasks: %w", err)
			}

			if len(taskList) == 0 {
				logger.Info("no tasks to sync")
				return nil
			}

			logger.Info("syncing tasks", "count", len(taskList))

			// Ensure collection exists
			collection := cfg.Collections.Tasks
			if err := q.EnsureCollection(collection); err != nil {
				return fmt.Errorf("failed to ensure collection: %w", err)
			}

			// Export and index each task
			exp := exporter.New(cfg.Sync.IncludeLogs, cfg.Sync.MaxLogLines)
			synced := 0

			for _, t := range taskList {
				// Export task to markdown
				md := exp.Export(t)

				// Write to temp file
				tmpDir := filepath.Join(os.TempDir(), "ty-qmd-export")
				os.MkdirAll(tmpDir, 0755)

				filename := fmt.Sprintf("task-%d.md", t.ID)
				tmpPath := filepath.Join(tmpDir, filename)

				if err := os.WriteFile(tmpPath, []byte(md), 0644); err != nil {
					logger.Error("failed to write temp file", "task", t.ID, "error", err)
					continue
				}

				// Index with qmd
				if err := q.IndexFile(tmpPath, collection); err != nil {
					logger.Error("failed to index task", "task", t.ID, "error", err)
					continue
				}

				// Mark as synced
				if err := taskDB.MarkSynced(t.ID); err != nil {
					logger.Warn("failed to mark task synced", "task", t.ID, "error", err)
				}

				synced++
				logger.Debug("synced task", "id", t.ID, "title", t.Title)
			}

			logger.Info("sync complete", "synced", synced, "total", len(taskList))
			return nil
		},
	}

	cmd.Flags().BoolVarP(&all, "all", "a", false, "re-sync all tasks, not just new ones")
	cmd.Flags().StringVarP(&project, "project", "p", "", "sync only tasks from specific project")

	return cmd
}

func searchCmd() *cobra.Command {
	var count int
	var collection string
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search indexed content",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			logger := setupLogger()
			q := qmd.New(cfg.QMD.Binary, logger)

			query := args[0]
			for _, arg := range args[1:] {
				query += " " + arg
			}

			if collection == "" {
				collection = cfg.Collections.Tasks
			}

			results, err := q.Query(query, collection, count)
			if err != nil {
				return fmt.Errorf("search failed: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}

			if len(results) == 0 {
				fmt.Println("No results found.")
				return nil
			}

			for _, r := range results {
				fmt.Printf("\n%.2f  %s\n", r.Score, r.Title)
				fmt.Printf("     %s\n", r.Path)
				if r.Snippet != "" {
					fmt.Printf("     %s\n", r.Snippet)
				}
			}

			return nil
		},
	}

	cmd.Flags().IntVarP(&count, "count", "n", 5, "number of results")
	cmd.Flags().StringVarP(&collection, "collection", "c", "", "collection to search (default: tasks)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")

	return cmd
}

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run QMD MCP server for Claude integration",
		Long: `Start the QMD MCP server as a sidecar process.

This allows Claude to use QMD search tools during task execution:
- qmd_search: Fast keyword search
- qmd_vsearch: Semantic vector search
- qmd_query: Hybrid search with re-ranking
- qmd_get: Retrieve documents`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			logger := setupLogger()

			// Check qmd is available
			q := qmd.New(cfg.QMD.Binary, logger)
			if !q.IsAvailable() {
				return fmt.Errorf("qmd not found at: %s\nInstall with: bun install -g github:tobi/qmd", cfg.QMD.Binary)
			}

			logger.Info("starting qmd mcp server", "binary", cfg.QMD.Binary)

			// Setup signal handling
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

			// Start qmd mcp
			qmdCmd := exec.CommandContext(ctx, cfg.QMD.Binary, "mcp")
			qmdCmd.Stdin = os.Stdin
			qmdCmd.Stdout = os.Stdout
			qmdCmd.Stderr = os.Stderr

			if err := qmdCmd.Start(); err != nil {
				return fmt.Errorf("failed to start qmd mcp: %w", err)
			}

			// Wait for signal or process exit
			doneCh := make(chan error, 1)
			go func() {
				doneCh <- qmdCmd.Wait()
			}()

			select {
			case <-sigCh:
				logger.Info("shutting down...")
				qmdCmd.Process.Signal(syscall.SIGTERM)
				return nil
			case err := <-doneCh:
				if err != nil {
					return fmt.Errorf("qmd mcp exited: %w", err)
				}
				return nil
			}
		},
	}
}

func indexProjectCmd() *cobra.Command {
	var name string
	var mask string

	cmd := &cobra.Command{
		Use:   "index-project <path>",
		Short: "Index project documentation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			logger := setupLogger()
			q := qmd.New(cfg.QMD.Binary, logger)

			if !q.IsAvailable() {
				return fmt.Errorf("qmd not found at: %s", cfg.QMD.Binary)
			}

			path := args[0]
			if name == "" {
				name = filepath.Base(path)
			}

			logger.Info("indexing project", "path", path, "collection", name, "mask", mask)

			if err := q.AddCollection(path, name, mask); err != nil {
				return fmt.Errorf("failed to add collection: %w", err)
			}

			if err := q.Embed(); err != nil {
				return fmt.Errorf("failed to embed: %w", err)
			}

			logger.Info("project indexed successfully", "collection", name)
			return nil
		},
	}

	cmd.Flags().StringVarP(&name, "name", "n", "", "collection name (default: directory name)")
	cmd.Flags().StringVarP(&mask, "mask", "m", "**/*.md", "file glob pattern")

	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sync and index status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			logger := setupLogger()
			q := qmd.New(cfg.QMD.Binary, logger)

			if !q.IsAvailable() {
				fmt.Println("QMD: not installed")
				fmt.Printf("     Install with: bun install -g github:tobi/qmd\n")
				return nil
			}

			// Get qmd status
			status, err := q.Status()
			if err != nil {
				fmt.Printf("QMD: error getting status: %v\n", err)
			} else {
				fmt.Println("QMD Status:")
				fmt.Printf("  Collections: %d\n", status.Collections)
				fmt.Printf("  Documents: %d\n", status.Documents)
				fmt.Printf("  Embedded: %d\n", status.Embedded)
			}

			// Get tasks sync status
			taskDB, err := tasks.Open(cfg.TaskYou.DB)
			if err != nil {
				fmt.Printf("\nTaskYou DB: error: %v\n", err)
				return nil
			}
			defer taskDB.Close()

			syncStats, err := taskDB.SyncStats()
			if err != nil {
				fmt.Printf("\nSync Status: error: %v\n", err)
				return nil
			}

			fmt.Printf("\nSync Status:\n")
			fmt.Printf("  Total completed: %d\n", syncStats.Total)
			fmt.Printf("  Synced: %d\n", syncStats.Synced)
			fmt.Printf("  Pending: %d\n", syncStats.Pending)

			return nil
		},
	}
}

func loadConfig() (*Config, error) {
	path := cfgFile
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "ty-qmd", "config.yaml")
	}

	var cfg Config

	// Set defaults
	cfg.QMD.Binary = "qmd"
	cfg.Sync.Interval = 5 * time.Minute
	cfg.Sync.Statuses = []string{"done", "archived"}
	cfg.Sync.MaxLogLines = 100
	cfg.Collections.Tasks = "ty-tasks"

	home, _ := os.UserHomeDir()
	cfg.TaskYou.DB = filepath.Join(home, ".local", "share", "taskyou", "tasks.db")
	cfg.TaskYou.CLI = "ty"

	// Load config file if exists
	data, err := os.ReadFile(path)
	if err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config: %w", err)
		}
	}

	// Expand home directory in paths
	if cfg.TaskYou.DB != "" && cfg.TaskYou.DB[0] == '~' {
		cfg.TaskYou.DB = filepath.Join(home, cfg.TaskYou.DB[1:])
	}

	return &cfg, nil
}

func setupLogger() *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))
}
