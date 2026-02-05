// Command ty-email provides an email interface for TaskYou.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
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

	rootCmd.AddCommand(initCmd())
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

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Setup ty-email configuration",
		Long:  "Interactive wizard to configure email provider and LLM. Can be re-run anytime to update settings.",
		RunE: func(cmd *cobra.Command, args []string) error {
			reader := bufio.NewReader(os.Stdin)

			// Determine config path
			configPath := cfgFile
			if configPath == "" {
				home, _ := os.UserHomeDir()
				configPath = filepath.Join(home, ".config", "ty-email", "config.yaml")
			}

			// Check for existing config
			var existingCfg *Config
			if data, err := os.ReadFile(configPath); err == nil {
				existingCfg = &Config{}
				yaml.Unmarshal(data, existingCfg)
				fmt.Printf("Existing config found at %s\n", configPath)
				fmt.Println("Running setup will update your configuration.\n")
			}

			cfg := &Config{}
			if existingCfg != nil {
				*cfg = *existingCfg
			}

			// Email provider
			fmt.Println("=== Email Provider ===")
			fmt.Println("1. IMAP (Fastmail, ProtonMail, etc.)")
			fmt.Println("2. Gmail (OAuth2)")
			fmt.Print("\nChoice [1]: ")
			choice, _ := reader.ReadString('\n')
			choice = strings.TrimSpace(choice)
			if choice == "" {
				choice = "1"
			}

			switch choice {
			case "1":
				cfg.Adapter.Type = "imap"
				if cfg.Adapter.IMAP == nil {
					cfg.Adapter.IMAP = &adapter.IMAPConfig{}
				}

				fmt.Print("\nIMAP server (e.g., imap.fastmail.com:993): ")
				server, _ := reader.ReadString('\n')
				server = strings.TrimSpace(server)
				if server != "" {
					cfg.Adapter.IMAP.Server = server
				} else if cfg.Adapter.IMAP.Server == "" {
					cfg.Adapter.IMAP.Server = "imap.fastmail.com:993"
				}

				fmt.Printf("Username/email [%s]: ", cfg.Adapter.IMAP.Username)
				username, _ := reader.ReadString('\n')
				username = strings.TrimSpace(username)
				if username != "" {
					cfg.Adapter.IMAP.Username = username
				}

				fmt.Println("\nPassword retrieval (choose one):")
				fmt.Println("1. Command (e.g., op read 'op://...' for 1Password)")
				fmt.Println("2. Environment variable")
				fmt.Print("Choice [1]: ")
				pwChoice, _ := reader.ReadString('\n')
				pwChoice = strings.TrimSpace(pwChoice)
				if pwChoice == "" {
					pwChoice = "1"
				}

				if pwChoice == "1" {
					fmt.Printf("Password command [%s]: ", cfg.Adapter.IMAP.PasswordCmd)
					pwCmd, _ := reader.ReadString('\n')
					pwCmd = strings.TrimSpace(pwCmd)
					if pwCmd != "" {
						cfg.Adapter.IMAP.PasswordCmd = pwCmd
					}

					// Test the command
					if cfg.Adapter.IMAP.PasswordCmd != "" {
						fmt.Print("Testing password command... ")
						out, err := exec.Command("sh", "-c", cfg.Adapter.IMAP.PasswordCmd).Output()
						if err != nil {
							fmt.Printf("FAILED: %v\n", err)
						} else if len(strings.TrimSpace(string(out))) == 0 {
							fmt.Println("WARNING: command returned empty string")
						} else {
							fmt.Println("OK")
						}
					}
				} else {
					fmt.Print("Environment variable name: ")
					envVar, _ := reader.ReadString('\n')
					envVar = strings.TrimSpace(envVar)
					if envVar != "" {
						cfg.Adapter.IMAP.PasswordCmd = fmt.Sprintf("echo $%s", envVar)
					}
				}

				fmt.Printf("IMAP folder [%s]: ", defaultStr(cfg.Adapter.IMAP.Folder, "INBOX"))
				folder, _ := reader.ReadString('\n')
				folder = strings.TrimSpace(folder)
				if folder != "" {
					cfg.Adapter.IMAP.Folder = folder
				} else if cfg.Adapter.IMAP.Folder == "" {
					cfg.Adapter.IMAP.Folder = "INBOX"
				}

				fmt.Printf("Poll interval [%s]: ", defaultStr(cfg.Adapter.IMAP.PollInterval, "30s"))
				interval, _ := reader.ReadString('\n')
				interval = strings.TrimSpace(interval)
				if interval != "" {
					cfg.Adapter.IMAP.PollInterval = interval
				} else if cfg.Adapter.IMAP.PollInterval == "" {
					cfg.Adapter.IMAP.PollInterval = "30s"
				}

			case "2":
				cfg.Adapter.Type = "gmail"
				if cfg.Adapter.Gmail == nil {
					cfg.Adapter.Gmail = &adapter.GmailConfig{}
				}

				home, _ := os.UserHomeDir()
				defaultCredsFile := filepath.Join(home, ".config", "ty-email", "gmail-credentials.json")
				defaultTokenFile := filepath.Join(home, ".config", "ty-email", "gmail-token.json")

				fmt.Println("\nGmail uses OAuth2 for authentication.")
				fmt.Println("You'll need to create credentials in Google Cloud Console.\n")
				fmt.Println("Step-by-step:")
				fmt.Println("  1. Go to https://console.cloud.google.com/")
				fmt.Println("  2. Create a new project (or select existing)")
				fmt.Println("  3. Enable the Gmail API:")
				fmt.Println("     - Go to APIs & Services > Library")
				fmt.Println("     - Search for 'Gmail API' and enable it")
				fmt.Println("  4. Create OAuth credentials:")
				fmt.Println("     - Go to APIs & Services > Credentials")
				fmt.Println("     - Click 'Create Credentials' > 'OAuth client ID'")
				fmt.Println("     - If prompted, configure the OAuth consent screen first:")
				fmt.Println("       - User type: External (or Internal if using Workspace)")
				fmt.Println("       - App name: 'ty-email' (or whatever you want)")
				fmt.Println("       - Add your email as a test user")
				fmt.Println("     - Application type: 'Desktop app'")
				fmt.Println("     - Name: 'ty-email'")
				fmt.Println("  5. Download the JSON file (click the download icon)")
				fmt.Println("  6. Save it to the path below")

				fmt.Printf("\nCredentials file path [%s]: ", defaultStr(cfg.Adapter.Gmail.CredentialsFile, defaultCredsFile))
				credsFile, _ := reader.ReadString('\n')
				credsFile = strings.TrimSpace(credsFile)
				if credsFile != "" {
					cfg.Adapter.Gmail.CredentialsFile = credsFile
				} else if cfg.Adapter.Gmail.CredentialsFile == "" {
					cfg.Adapter.Gmail.CredentialsFile = defaultCredsFile
				}

				// Check if credentials file exists
				if _, err := os.Stat(cfg.Adapter.Gmail.CredentialsFile); os.IsNotExist(err) {
					fmt.Printf("WARNING: Credentials file not found at %s\n", cfg.Adapter.Gmail.CredentialsFile)
					fmt.Println("Download it from Google Cloud Console before running 'ty-email serve'")
				} else {
					fmt.Println("Credentials file found.")
				}

				fmt.Printf("Token file path [%s]: ", defaultStr(cfg.Adapter.Gmail.TokenFile, defaultTokenFile))
				tokenFile, _ := reader.ReadString('\n')
				tokenFile = strings.TrimSpace(tokenFile)
				if tokenFile != "" {
					cfg.Adapter.Gmail.TokenFile = tokenFile
				} else if cfg.Adapter.Gmail.TokenFile == "" {
					cfg.Adapter.Gmail.TokenFile = defaultTokenFile
				}

				fmt.Printf("Poll interval [%s]: ", defaultStr(cfg.Adapter.Gmail.PollInterval, "30s"))
				interval, _ := reader.ReadString('\n')
				interval = strings.TrimSpace(interval)
				if interval != "" {
					cfg.Adapter.Gmail.PollInterval = interval
				} else if cfg.Adapter.Gmail.PollInterval == "" {
					cfg.Adapter.Gmail.PollInterval = "30s"
				}

				fmt.Printf("Gmail label to filter (optional, e.g., 'ty-email') [%s]: ", cfg.Adapter.Gmail.Label)
				label, _ := reader.ReadString('\n')
				label = strings.TrimSpace(label)
				if label != "" {
					cfg.Adapter.Gmail.Label = label
				}

				fmt.Println("\nNote: On first run, ty-email will open a browser for OAuth authorization.")
			}

			// SMTP for replies (skip for Gmail - it uses the API)
			if cfg.Adapter.Type != "gmail" {
				fmt.Println("\n=== Outbound Email (SMTP) ===")
				fmt.Printf("SMTP server [%s]: ", defaultStr(cfg.SMTP.Server, "smtp.fastmail.com:587"))
				smtpServer, _ := reader.ReadString('\n')
				smtpServer = strings.TrimSpace(smtpServer)
				if smtpServer != "" {
					cfg.SMTP.Server = smtpServer
				} else if cfg.SMTP.Server == "" {
					cfg.SMTP.Server = "smtp.fastmail.com:587"
				}

				// Default SMTP username to IMAP username
				defaultSmtpUser := cfg.SMTP.Username
				if defaultSmtpUser == "" && cfg.Adapter.IMAP != nil {
					defaultSmtpUser = cfg.Adapter.IMAP.Username
				}
				fmt.Printf("SMTP username [%s]: ", defaultSmtpUser)
				smtpUser, _ := reader.ReadString('\n')
				smtpUser = strings.TrimSpace(smtpUser)
				if smtpUser != "" {
					cfg.SMTP.Username = smtpUser
				} else if cfg.SMTP.Username == "" {
					cfg.SMTP.Username = defaultSmtpUser
				}

				// Default SMTP password command to IMAP password command
				defaultSmtpPwCmd := cfg.SMTP.PasswordCmd
				if defaultSmtpPwCmd == "" && cfg.Adapter.IMAP != nil {
					defaultSmtpPwCmd = cfg.Adapter.IMAP.PasswordCmd
				}
				fmt.Printf("SMTP password command [%s]: ", defaultSmtpPwCmd)
				smtpPwCmd, _ := reader.ReadString('\n')
				smtpPwCmd = strings.TrimSpace(smtpPwCmd)
				if smtpPwCmd != "" {
					cfg.SMTP.PasswordCmd = smtpPwCmd
				} else if cfg.SMTP.PasswordCmd == "" {
					cfg.SMTP.PasswordCmd = defaultSmtpPwCmd
				}

				fmt.Printf("From address [%s]: ", defaultStr(cfg.SMTP.From, cfg.SMTP.Username))
				fromAddr, _ := reader.ReadString('\n')
				fromAddr = strings.TrimSpace(fromAddr)
				if fromAddr != "" {
					cfg.SMTP.From = fromAddr
				} else if cfg.SMTP.From == "" {
					cfg.SMTP.From = cfg.SMTP.Username
				}
			}

			// LLM provider
			fmt.Println("\n=== LLM Provider ===")
			fmt.Println("1. Claude (Anthropic)")
			fmt.Println("2. OpenAI (coming soon)")
			fmt.Println("3. Ollama (local, coming soon)")
			fmt.Print("\nChoice [1]: ")
			llmChoice, _ := reader.ReadString('\n')
			llmChoice = strings.TrimSpace(llmChoice)
			if llmChoice == "" {
				llmChoice = "1"
			}

			switch llmChoice {
			case "1":
				cfg.Classifier.Provider = "claude"

				fmt.Printf("Model [%s]: ", defaultStr(cfg.Classifier.Model, "claude-sonnet-4-20250514"))
				model, _ := reader.ReadString('\n')
				model = strings.TrimSpace(model)
				if model != "" {
					cfg.Classifier.Model = model
				} else if cfg.Classifier.Model == "" {
					cfg.Classifier.Model = "claude-sonnet-4-20250514"
				}

				fmt.Println("\nAPI key retrieval:")
				fmt.Println("1. Command (e.g., op read '...')")
				fmt.Println("2. Environment variable ANTHROPIC_API_KEY")
				fmt.Print("Choice [1]: ")
				apiChoice, _ := reader.ReadString('\n')
				apiChoice = strings.TrimSpace(apiChoice)
				if apiChoice == "" {
					apiChoice = "1"
				}

				var apiKeyValid bool
				for !apiKeyValid {
					if apiChoice == "1" {
						fmt.Printf("API key command [%s]: ", cfg.Classifier.APIKeyCmd)
						apiCmd, _ := reader.ReadString('\n')
						apiCmd = strings.TrimSpace(apiCmd)
						if apiCmd != "" {
							cfg.Classifier.APIKeyCmd = apiCmd
						}

						if cfg.Classifier.APIKeyCmd == "" {
							fmt.Println("ERROR: API key command is required")
							continue
						}

						// Test the command
						fmt.Print("Testing API key command... ")
						out, err := exec.Command("sh", "-c", cfg.Classifier.APIKeyCmd).Output()
						if err != nil {
							fmt.Printf("FAILED: %v\n", err)
							fmt.Println("Please enter a valid command.")
							continue
						} else if len(strings.TrimSpace(string(out))) == 0 {
							fmt.Println("FAILED: command returned empty string")
							fmt.Println("Please enter a command that returns your API key.")
							continue
						} else {
							fmt.Println("OK")
							apiKeyValid = true
						}
					} else {
						if os.Getenv("ANTHROPIC_API_KEY") == "" {
							fmt.Println("ERROR: ANTHROPIC_API_KEY environment variable is not set")
							fmt.Println("\nPlease either:")
							fmt.Println("  1. Set it now: export ANTHROPIC_API_KEY=sk-ant-...")
							fmt.Println("  2. Or switch to using a command (option 1)")
							fmt.Print("\nTry again? [Y/n]: ")
							retry, _ := reader.ReadString('\n')
							retry = strings.TrimSpace(strings.ToLower(retry))
							if retry == "n" || retry == "no" {
								fmt.Println("\nYou can set the API key later by editing ~/.config/ty-email/config.yaml")
								fmt.Println("or by re-running 'ty-email init'")
								apiKeyValid = true // let them proceed but warn
							} else {
								fmt.Println("\nAPI key retrieval:")
								fmt.Println("1. Command (e.g., op read '...')")
								fmt.Println("2. Environment variable ANTHROPIC_API_KEY")
								fmt.Print("Choice [1]: ")
								apiChoice, _ = reader.ReadString('\n')
								apiChoice = strings.TrimSpace(apiChoice)
								if apiChoice == "" {
									apiChoice = "1"
								}
							}
						} else {
							cfg.Classifier.APIKeyCmd = "echo $ANTHROPIC_API_KEY"
							fmt.Println("OK - ANTHROPIC_API_KEY is set")
							apiKeyValid = true
						}
					}
				}

			case "2", "3":
				fmt.Println("Coming soon. Please use Claude for now.")
				return nil
			}

			// TaskYou CLI
			fmt.Println("\n=== TaskYou ===")
			fmt.Printf("Path to ty CLI [%s]: ", defaultStr(cfg.TaskYou.CLI, "ty"))
			tyCli, _ := reader.ReadString('\n')
			tyCli = strings.TrimSpace(tyCli)
			if tyCli != "" {
				cfg.TaskYou.CLI = tyCli
			} else if cfg.TaskYou.CLI == "" {
				cfg.TaskYou.CLI = "ty"
			}

			// Test ty CLI
			fmt.Print("Testing ty CLI... ")
			if _, err := exec.LookPath(cfg.TaskYou.CLI); err != nil {
				fmt.Printf("NOT FOUND: %v\n", err)
			} else {
				fmt.Println("OK")
			}

			// Default project
			fmt.Printf("Default project [%s]: ", defaultStr(cfg.Routing.DefaultProject, "personal"))
			defaultProj, _ := reader.ReadString('\n')
			defaultProj = strings.TrimSpace(defaultProj)
			if defaultProj != "" {
				cfg.Routing.DefaultProject = defaultProj
			} else if cfg.Routing.DefaultProject == "" {
				cfg.Routing.DefaultProject = "personal"
			}

			// Save config
			fmt.Println("\n=== Saving Configuration ===")

			// Ensure directory exists
			configDir := filepath.Dir(configPath)
			if err := os.MkdirAll(configDir, 0755); err != nil {
				return fmt.Errorf("failed to create config directory: %w", err)
			}

			data, err := yaml.Marshal(cfg)
			if err != nil {
				return fmt.Errorf("failed to marshal config: %w", err)
			}

			if err := os.WriteFile(configPath, data, 0600); err != nil {
				return fmt.Errorf("failed to write config: %w", err)
			}

			fmt.Printf("Configuration saved to %s\n", configPath)

			// Initialize state DB
			fmt.Print("Initializing state database... ")
			st, err := state.Open("")
			if err != nil {
				fmt.Printf("FAILED: %v\n", err)
			} else {
				st.Close()
				fmt.Printf("OK (%s)\n", state.DefaultPath())
			}

			fmt.Println("\nSetup complete! Run 'ty-email serve' to start processing emails.")
			fmt.Println("\nTip: Add [projectname] to your email subject to route to a specific project.")

			return nil
		},
	}
}

func defaultStr(val, def string) string {
	if val != "" {
		return val
	}
	return def
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
	case "gmail":
		if cfg.Adapter.Gmail == nil {
			return nil, fmt.Errorf("Gmail config required")
		}
		return adapter.NewGmailAdapter(cfg.Adapter.Gmail, logger), nil
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
