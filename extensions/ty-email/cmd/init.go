package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bborn/workflow/extensions/ty-email/internal/adapter"
	"github.com/bborn/workflow/extensions/ty-email/internal/state"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212")).
			MarginBottom(1)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("42"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243"))
)

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Setup ty-email configuration",
		Long:  "Interactive wizard to configure email provider and LLM. Can be re-run anytime to update settings.",
		RunE:  runInit,
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	// Determine config path
	configPath := cfgFile
	if configPath == "" {
		home, _ := os.UserHomeDir()
		configPath = filepath.Join(home, ".config", "ty-email", "config.yaml")
	}

	// Load existing config if present
	cfg := &Config{}
	if data, err := os.ReadFile(configPath); err == nil {
		yaml.Unmarshal(data, cfg)
		fmt.Println(infoStyle.Render("Existing config found. Current values shown as defaults.\n"))
	}

	home, _ := os.UserHomeDir()

	// === Email Provider ===
	fmt.Println(titleStyle.Render("📧 Email Provider"))

	var emailProvider string
	if cfg.Adapter.Type != "" {
		emailProvider = cfg.Adapter.Type
	}

	err := huh.NewSelect[string]().
		Title("How will you receive emails?").
		Options(
			huh.NewOption("Gmail", "gmail"),
			huh.NewOption("Other IMAP (Fastmail, ProtonMail, etc.)", "imap"),
		).
		Value(&emailProvider).
		Run()
	if err != nil {
		return err
	}

	// Both use IMAP adapter, Gmail just has pre-filled servers
	cfg.Adapter.Type = "imap"

	if emailProvider == "gmail" {
		if err := configureGmailIMAP(cfg, home); err != nil {
			return err
		}
	} else {
		if err := configureIMAP(cfg, home); err != nil {
			return err
		}
		if err := configureSMTP(cfg); err != nil {
			return err
		}
	}

	// === Claude API ===
	fmt.Println(titleStyle.Render("\n🤖 Claude API"))

	if err := configureLLM(cfg); err != nil {
		return err
	}

	// === TaskYou ===
	fmt.Println(titleStyle.Render("\n📋 TaskYou"))

	if err := configureTaskYou(cfg); err != nil {
		return err
	}

	// === Save ===
	fmt.Println(titleStyle.Render("\n💾 Saving Configuration"))

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

	fmt.Println(successStyle.Render("✓ Configuration saved to " + configPath))

	// Initialize state DB
	st, err := state.Open("")
	if err != nil {
		fmt.Println(errorStyle.Render("✗ Failed to initialize state database: " + err.Error()))
	} else {
		st.Close()
		fmt.Println(successStyle.Render("✓ State database initialized at " + state.DefaultPath()))
	}

	fmt.Println()
	fmt.Println(successStyle.Render("Setup complete! Run 'ty-email serve' to start processing emails."))
	fmt.Println(infoStyle.Render("\nTip: Add [projectname] to your email subject to route to a specific project."))

	return nil
}

func configureIMAP(cfg *Config, home string) error {
	if cfg.Adapter.IMAP == nil {
		cfg.Adapter.IMAP = &adapter.IMAPConfig{}
	}

	server := cfg.Adapter.IMAP.Server
	if server == "" {
		server = "imap.fastmail.com:993"
	}

	username := cfg.Adapter.IMAP.Username
	passwordCmd := cfg.Adapter.IMAP.PasswordCmd
	folder := cfg.Adapter.IMAP.Folder
	if folder == "" {
		folder = "INBOX"
	}
	pollInterval := cfg.Adapter.IMAP.PollInterval
	if pollInterval == "" {
		pollInterval = "30s"
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("IMAP Server").
				Description("e.g., imap.fastmail.com:993").
				Value(&server),

			huh.NewInput().
				Title("Username / Email").
				Value(&username).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("username is required")
					}
					return nil
				}),

			huh.NewInput().
				Title("Password Command").
				Description("Command to retrieve password (e.g., op read 'op://...')").
				Value(&passwordCmd).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("password command is required")
					}
					return nil
				}),

			huh.NewInput().
				Title("Folder").
				Value(&folder),

			huh.NewInput().
				Title("Poll Interval").
				Description("e.g., 30s, 1m").
				Value(&pollInterval),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	// Test password command
	fmt.Print("Testing password command... ")
	out, err := exec.Command("sh", "-c", passwordCmd).Output()
	if err != nil {
		fmt.Println(errorStyle.Render("FAILED: " + err.Error()))
		return fmt.Errorf("password command failed - please check and re-run init")
	} else if len(strings.TrimSpace(string(out))) == 0 {
		fmt.Println(errorStyle.Render("FAILED: command returned empty"))
		return fmt.Errorf("password command returned empty - please check and re-run init")
	}
	fmt.Println(successStyle.Render("OK"))

	cfg.Adapter.IMAP.Server = server
	cfg.Adapter.IMAP.Username = username
	cfg.Adapter.IMAP.PasswordCmd = passwordCmd
	cfg.Adapter.IMAP.Folder = folder
	cfg.Adapter.IMAP.PollInterval = pollInterval

	return nil
}

func configureGmailIMAP(cfg *Config, home string) error {
	if cfg.Adapter.IMAP == nil {
		cfg.Adapter.IMAP = &adapter.IMAPConfig{}
	}

	// Explain how it works
	fmt.Println(infoStyle.Render(`
How ty-email works with Gmail:

  1. You email a special address (like yourname+ty@gmail.com)
  2. A Gmail filter auto-applies a label and moves it to a folder
  3. ty-email watches that folder via IMAP
  4. It creates tasks, provides input, etc. based on email content
  5. It replies back with confirmations

Let's set this up.
`))

	// Get their email
	fmt.Println(titleStyle.Render("Step 1: Gmail Address"))

	username := cfg.Adapter.IMAP.Username
	err := huh.NewInput().
		Title("Your Gmail address").
		Value(&username).
		Validate(func(s string) error {
			if s == "" || !strings.Contains(s, "@") {
				return fmt.Errorf("enter a valid email address")
			}
			return nil
		}).
		Run()
	if err != nil {
		return err
	}

	// Generate suggested alias
	parts := strings.Split(username, "@")
	suggestedAlias := parts[0] + "+ty@" + parts[1]

	fmt.Println(successStyle.Render("\n✓ Your task email address: " + suggestedAlias))
	fmt.Println(infoStyle.Render("  Send emails here from anywhere (phone, other accounts, etc.)"))

	// App password
	fmt.Println(titleStyle.Render("\nStep 2: App Password"))
	fmt.Println(infoStyle.Render(`
Create an app password for ty-email:

  1. Go to https://myaccount.google.com/apppasswords
     (requires 2FA to be enabled on your account)
  2. Select app: "Mail"
  3. Select device: "Other" → enter "ty-email"
  4. Click "Generate"
  5. Copy the 16-character password
`))

	var appPassword string
	err = huh.NewInput().
		Title("App Password").
		Description("Paste the 16-character password from Google").
		Placeholder("xxxx xxxx xxxx xxxx").
		Value(&appPassword).
		Validate(func(s string) error {
			clean := strings.ReplaceAll(s, " ", "")
			if len(clean) != 16 {
				return fmt.Errorf("app password should be 16 characters (got %d)", len(clean))
			}
			return nil
		}).
		Run()
	if err != nil {
		return err
	}

	// Store as echo command (keeps config format consistent)
	appPassword = strings.ReplaceAll(appPassword, " ", "")
	passwordCmd := fmt.Sprintf("echo '%s'", appPassword)

	// Gmail filter setup
	fmt.Println(titleStyle.Render("\nStep 3: Gmail Filter"))
	fmt.Println(infoStyle.Render(`
Create a filter to route task emails to the ty-email folder:

  1. In Gmail, click the search options (▼) in the search bar
  2. In "To" field, enter: ` + suggestedAlias + `
  3. Click "Create filter"
  4. Check "Skip the Inbox"
  5. Check "Apply the label" → create new label: "ty-email"
  6. Click "Create filter"

Direct link: https://mail.google.com/mail/u/0/#settings/filters

Note: ty-email replies will appear in your Inbox (not the ty-email label).
`))

	var filterReady bool
	err = huh.NewConfirm().
		Title("Have you created the Gmail filter?").
		Value(&filterReady).
		Run()
	if err != nil {
		return err
	}

	if !filterReady {
		fmt.Println(infoStyle.Render("Create it before running ty-email serve"))
	}

	// Set Gmail IMAP/SMTP settings
	cfg.Adapter.IMAP.Server = "imap.gmail.com:993"
	cfg.Adapter.IMAP.Username = username
	cfg.Adapter.IMAP.PasswordCmd = passwordCmd
	cfg.Adapter.IMAP.Folder = "ty-email" // Gmail labels become IMAP folders
	cfg.Adapter.IMAP.PollInterval = "30s"

	cfg.SMTP.Server = "smtp.gmail.com:587"
	cfg.SMTP.Username = username
	cfg.SMTP.PasswordCmd = passwordCmd
	cfg.SMTP.From = suggestedAlias

	// Security - only allow emails from self
	cfg.Security.AllowedSenders = []string{username}

	fmt.Println(successStyle.Render("\n✓ Gmail configured (only emails from " + username + " will be processed)"))

	return nil
}

func configureSMTP(cfg *Config) error {
	fmt.Println(titleStyle.Render("\n📤 Outbound Email (SMTP)"))

	server := cfg.SMTP.Server
	if server == "" {
		server = "smtp.fastmail.com:587"
	}
	username := cfg.SMTP.Username
	if username == "" && cfg.Adapter.IMAP != nil {
		username = cfg.Adapter.IMAP.Username
	}
	passwordCmd := cfg.SMTP.PasswordCmd
	if passwordCmd == "" && cfg.Adapter.IMAP != nil {
		passwordCmd = cfg.Adapter.IMAP.PasswordCmd
	}
	from := cfg.SMTP.From
	if from == "" {
		from = username
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("SMTP Server").
				Value(&server),

			huh.NewInput().
				Title("Username").
				Value(&username),

			huh.NewInput().
				Title("Password Command").
				Value(&passwordCmd),

			huh.NewInput().
				Title("From Address").
				Value(&from),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	cfg.SMTP.Server = server
	cfg.SMTP.Username = username
	cfg.SMTP.PasswordCmd = passwordCmd
	cfg.SMTP.From = from

	return nil
}

func configureLLM(cfg *Config) error {
	cfg.Classifier.Provider = "claude"

	model := cfg.Classifier.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	var apiKeyMethod string
	err := huh.NewSelect[string]().
		Title("API Key Retrieval").
		Options(
			huh.NewOption("Command (e.g., op read, pass, etc.)", "command"),
			huh.NewOption("Environment variable (ANTHROPIC_API_KEY)", "env"),
		).
		Value(&apiKeyMethod).
		Run()
	if err != nil {
		return err
	}

	var apiKeyCmd string
	if apiKeyMethod == "command" {
		apiKeyCmd = cfg.Classifier.APIKeyCmd

		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Model").
					Value(&model),

				huh.NewInput().
					Title("API Key Command").
					Description("Command that outputs your Anthropic API key").
					Placeholder("op read 'op://Private/Anthropic/api_key'").
					Value(&apiKeyCmd).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("API key command is required")
						}
						return nil
					}),
			),
		)

		if err := form.Run(); err != nil {
			return err
		}

		// Test the command
		fmt.Print("Testing API key command... ")
		out, err := exec.Command("sh", "-c", apiKeyCmd).Output()
		if err != nil {
			fmt.Println(errorStyle.Render("FAILED: " + err.Error()))
			return fmt.Errorf("API key command failed - please check and re-run init")
		} else if len(strings.TrimSpace(string(out))) == 0 {
			fmt.Println(errorStyle.Render("FAILED: command returned empty"))
			return fmt.Errorf("API key command returned empty - please check and re-run init")
		}
		fmt.Println(successStyle.Render("OK"))

	} else {
		// Environment variable
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			fmt.Println(errorStyle.Render("✗ ANTHROPIC_API_KEY is not set"))
			fmt.Println(infoStyle.Render("  Set it and re-run init: export ANTHROPIC_API_KEY=sk-ant-..."))
			return fmt.Errorf("ANTHROPIC_API_KEY environment variable is not set")
		}
		fmt.Println(successStyle.Render("✓ ANTHROPIC_API_KEY is set"))
		apiKeyCmd = "echo $ANTHROPIC_API_KEY"

		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Model").
					Value(&model),
			),
		)

		if err := form.Run(); err != nil {
			return err
		}
	}

	cfg.Classifier.Model = model
	cfg.Classifier.APIKeyCmd = apiKeyCmd

	return nil
}

func configureTaskYou(cfg *Config) error {
	tyPath := cfg.TaskYou.CLI
	if tyPath == "" {
		tyPath = "ty"
	}
	defaultProject := cfg.Routing.DefaultProject
	if defaultProject == "" {
		defaultProject = "personal"
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Path to ty CLI").
				Value(&tyPath),

			huh.NewInput().
				Title("Default Project").
				Description("Tasks without [project] tag go here").
				Value(&defaultProject),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	// Test ty CLI
	fmt.Print("Testing ty CLI... ")
	if _, err := exec.LookPath(tyPath); err != nil {
		fmt.Println(errorStyle.Render("NOT FOUND"))
		fmt.Println(infoStyle.Render("  Make sure ty is installed and in your PATH"))
	} else {
		fmt.Println(successStyle.Render("OK"))
	}

	cfg.TaskYou.CLI = tyPath
	cfg.Routing.DefaultProject = defaultProject

	return nil
}
