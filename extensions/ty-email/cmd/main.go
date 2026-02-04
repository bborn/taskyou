// Command ty-email provides an email interface for TaskYou.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bborn/workflow/extensions/ty-email/internal/adapter"
	"github.com/bborn/workflow/extensions/ty-email/internal/bridge"
	"github.com/bborn/workflow/extensions/ty-email/internal/classifier"
	"github.com/bborn/workflow/extensions/ty-email/internal/processor"
	"github.com/bborn/workflow/extensions/ty-email/internal/state"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Config holds the full configuration.
type Config struct {
	Adapter    adapter.Config    `yaml:"adapter"`
	SMTP       adapter.SMTPConfig `yaml:"smtp"`
	Classifier classifier.Config `yaml:"classifier"`
	TaskYou    struct {
		CLI string `yaml:"cli"`
	} `yaml:"taskyou"`
	Routing struct {
		DefaultProject string `yaml:"default_project"`
	} `yaml:"routing"`
}

var (
	cfgFile string
	verbose bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "ty-email",
		Short: "Email interface for TaskYou",
		Long:  "Send emails to create tasks, reply to provide input, receive notifications when tasks need attention.",
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.config/ty-email/config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	rootCmd.AddCommand(serveCmd())
	rootCmd.AddCommand(processCmd())
	rootCmd.AddCommand(testCmd())
	rootCmd.AddCommand(statusCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run as daemon, continuously processing emails",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			logger := setupLogger()

			// Initialize components
			adp, err := setupAdapter(cfg, logger)
			if err != nil {
				return fmt.Errorf("failed to setup adapter: %w", err)
			}

			cls, err := setupClassifier(cfg)
			if err != nil {
				return fmt.Errorf("failed to setup classifier: %w", err)
			}

			br := bridge.New(cfg.TaskYou.CLI)
			if !br.IsAvailable() {
				return fmt.Errorf("ty CLI not found at: %s", cfg.TaskYou.CLI)
			}

			st, err := state.Open("")
			if err != nil {
				return fmt.Errorf("failed to open state: %w", err)
			}
			defer st.Close()

			proc := processor.New(adp, cls, br, st, logger)

			// Setup signal handling
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

			// Start adapter
			if err := adp.Start(ctx); err != nil {
				return fmt.Errorf("failed to start adapter: %w", err)
			}
			defer adp.Stop()

			logger.Info("ty-email started", "adapter", adp.Name())

			// Process loop
			replyTicker := time.NewTicker(10 * time.Second)
			defer replyTicker.Stop()

			for {
				select {
				case <-sigCh:
					logger.Info("shutting down...")
					return nil

				case email := <-adp.Emails():
					if err := proc.ProcessEmail(ctx, email); err != nil {
						logger.Error("failed to process email", "error", err)
					}

				case <-replyTicker.C:
					if err := proc.SendPendingReplies(ctx); err != nil {
						logger.Error("failed to send replies", "error", err)
					}
				}
			}
		},
	}
}

func processCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "process",
		Short: "Process pending emails once and exit",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			logger := setupLogger()

			adp, err := setupAdapter(cfg, logger)
			if err != nil {
				return fmt.Errorf("failed to setup adapter: %w", err)
			}

			cls, err := setupClassifier(cfg)
			if err != nil {
				return fmt.Errorf("failed to setup classifier: %w", err)
			}

			br := bridge.New(cfg.TaskYou.CLI)
			st, err := state.Open("")
			if err != nil {
				return fmt.Errorf("failed to open state: %w", err)
			}
			defer st.Close()

			proc := processor.New(adp, cls, br, st, logger)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			// Start adapter briefly to poll
			if err := adp.Start(ctx); err != nil {
				return fmt.Errorf("failed to start adapter: %w", err)
			}

			// Wait a bit for emails to come in
			time.Sleep(5 * time.Second)

			// Process any emails
			for {
				select {
				case email := <-adp.Emails():
					if err := proc.ProcessEmail(ctx, email); err != nil {
						logger.Error("failed to process email", "error", err)
					}
				default:
					// No more emails
					adp.Stop()
					return proc.SendPendingReplies(ctx)
				}
			}
		},
	}
}

func testCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Test classification with a sample email from stdin",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			logger := setupLogger()

			cls, err := setupClassifier(cfg)
			if err != nil {
				return fmt.Errorf("failed to setup classifier: %w", err)
			}

			br := bridge.New(cfg.TaskYou.CLI)

			// Read email from stdin (simple format: headers then body)
			// For now, just accept subject and body
			fmt.Println("Enter email (Subject: line, blank line, then body, Ctrl+D to end):")

			var subject, body string
			var inBody bool
			var lines []string

			// Simple stdin reading
			buf := make([]byte, 4096)
			n, _ := os.Stdin.Read(buf)
			input := string(buf[:n])

			for _, line := range splitLines(input) {
				if !inBody {
					if line == "" {
						inBody = true
						continue
					}
					if len(line) > 9 && line[:9] == "Subject: " {
						subject = line[9:]
					}
				} else {
					lines = append(lines, line)
				}
			}
			body = joinLines(lines)

			email := &adapter.Email{
				ID:      "test-" + time.Now().Format("20060102150405"),
				From:    "test@example.com",
				Subject: subject,
				Body:    body,
			}

			// Get tasks for context
			tasks, _ := br.ListTasks("")

			ctx := context.Background()
			action, err := cls.Classify(ctx, email, bridge.ToClassifierTasks(tasks), nil)
			if err != nil {
				return fmt.Errorf("classification failed: %w", err)
			}

			logger.Info("classification result",
				"type", action.Type,
				"title", action.Title,
				"body", action.Body,
				"task_id", action.TaskID,
				"confidence", action.Confidence,
				"reasoning", action.Reasoning,
			)

			fmt.Printf("\nAction: %s\n", action.Type)
			if action.Title != "" {
				fmt.Printf("Title: %s\n", action.Title)
			}
			if action.Body != "" {
				fmt.Printf("Body: %s\n", action.Body)
			}
			if action.TaskID != 0 {
				fmt.Printf("Task ID: %d\n", action.TaskID)
			}
			if action.Reply != "" {
				fmt.Printf("\nReply:\n%s\n", action.Reply)
			}

			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show ty-email status",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := state.Open("")
			if err != nil {
				return fmt.Errorf("failed to open state: %w", err)
			}
			defer st.Close()

			pending, err := st.GetPendingOutbound(100)
			if err != nil {
				return fmt.Errorf("failed to get pending emails: %w", err)
			}

			fmt.Printf("State database: %s\n", state.DefaultPath())
			fmt.Printf("Pending outbound emails: %d\n", len(pending))

			if len(pending) > 0 {
				fmt.Println("\nPending emails:")
				for _, e := range pending {
					fmt.Printf("  - To: %s, Subject: %s (attempts: %d)\n", e.To, e.Subject, e.Attempts)
				}
			}

			return nil
		},
	}
}

func loadConfig() (*Config, error) {
	path := cfgFile
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "ty-email", "config.yaml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Defaults
	if cfg.TaskYou.CLI == "" {
		cfg.TaskYou.CLI = "ty"
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

func setupAdapter(cfg *Config, logger *slog.Logger) (adapter.Adapter, error) {
	switch cfg.Adapter.Type {
	case "imap":
		if cfg.Adapter.IMAP == nil {
			return nil, fmt.Errorf("IMAP config required")
		}
		return adapter.NewIMAPAdapter(cfg.Adapter.IMAP, &cfg.SMTP, logger), nil
	// TODO: Add gmail, webhook adapters
	default:
		return nil, fmt.Errorf("unsupported adapter type: %s", cfg.Adapter.Type)
	}
}

func setupClassifier(cfg *Config) (classifier.Classifier, error) {
	switch cfg.Classifier.Provider {
	case "claude", "":
		return classifier.NewClaudeClassifier(&cfg.Classifier)
	// TODO: Add openai, ollama classifiers
	default:
		return nil, fmt.Errorf("unsupported classifier: %s", cfg.Classifier.Provider)
	}
}

func splitLines(s string) []string {
	var lines []string
	var line []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, string(line))
			line = nil
		} else if s[i] != '\r' {
			line = append(line, s[i])
		}
	}
	if len(line) > 0 {
		lines = append(lines, string(line))
	}
	return lines
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	result := lines[0]
	for i := 1; i < len(lines); i++ {
		result += "\n" + lines[i]
	}
	return result
}
