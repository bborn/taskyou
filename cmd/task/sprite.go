package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	sprites "github.com/superfly/sprites-go"
)

// Sprite settings keys
const (
	SettingSpriteToken = "sprite_token" // Sprites API token
	SettingSpriteName  = "sprite_name"  // Name of the daemon sprite
)

// Default sprite name
const defaultSpriteName = "task-daemon"

// Styles for sprite command output
var (
	spriteTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#61AFEF"))
	spriteCheckStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
	spritePendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))
	spriteErrorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
)

// getSpriteToken returns the Sprites API token from env or database.
func getSpriteToken(database *db.DB) string {
	// First try environment variable
	token := os.Getenv("SPRITES_TOKEN")
	if token != "" {
		return token
	}

	// Fall back to database setting
	if database != nil {
		token, _ = database.GetSetting(SettingSpriteToken)
	}
	return token
}

// getSpriteClient creates a Sprites API client.
func getSpriteClient(database *db.DB) (*sprites.Client, error) {
	token := getSpriteToken(database)
	if token == "" {
		return nil, fmt.Errorf("no Sprites token configured. Set SPRITES_TOKEN env var or run: task config set sprite_token <token>")
	}
	return sprites.New(token), nil
}

// getSpriteName returns the name of the daemon sprite.
func getSpriteName(database *db.DB) string {
	if database != nil {
		name, _ := database.GetSetting(SettingSpriteName)
		if name != "" {
			return name
		}
	}
	return defaultSpriteName
}

// createSpriteCommand creates the sprite subcommand with all its children.
func createSpriteCommand() *cobra.Command {
	spriteCmd := &cobra.Command{
		Use:   "sprite",
		Short: "Manage the cloud sprite for task execution",
		Long: `Sprite management for running tasks in the cloud.

When SPRITES_TOKEN is set, 'task' automatically runs on a cloud sprite.
Use these commands to manage the sprite manually.

Commands:
  status     - Show sprite status
  up         - Start/restore the sprite
  down       - Checkpoint and stop the sprite
  attach     - Attach to the sprite's tmux session
  destroy    - Delete the sprite entirely
  token      - Set the Sprites API token`,
		Run: func(cmd *cobra.Command, args []string) {
			showSpriteStatus()
		},
	}

	// sprite status
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show sprite status",
		Run: func(cmd *cobra.Command, args []string) {
			showSpriteStatus()
		},
	}
	spriteCmd.AddCommand(statusCmd)

	// sprite up
	upCmd := &cobra.Command{
		Use:   "up",
		Short: "Start or restore the sprite",
		Long:  `Ensure the sprite is running. Creates it if it doesn't exist, restores from checkpoint if suspended.`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runSpriteUp(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	spriteCmd.AddCommand(upCmd)

	// sprite down
	downCmd := &cobra.Command{
		Use:   "down",
		Short: "Checkpoint and stop the sprite",
		Long:  `Save the sprite state and suspend it. Saves money when not in use.`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runSpriteDown(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	spriteCmd.AddCommand(downCmd)

	// sprite attach
	attachCmd := &cobra.Command{
		Use:   "attach",
		Short: "Attach to the sprite's shell",
		Long:  `Open an interactive shell session on the sprite.`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runSpriteAttach(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	spriteCmd.AddCommand(attachCmd)

	// sprite destroy
	destroyCmd := &cobra.Command{
		Use:   "destroy",
		Short: "Delete the sprite entirely",
		Long:  `Permanently delete the sprite and all its data. Use with caution.`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runSpriteDestroy(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	spriteCmd.AddCommand(destroyCmd)

	// sprite token
	tokenCmd := &cobra.Command{
		Use:   "token [token]",
		Short: "Set or show the Sprites API token",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				showSpriteToken()
			} else {
				if err := setSpriteToken(args[0]); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
					os.Exit(1)
				}
			}
		},
	}
	spriteCmd.AddCommand(tokenCmd)

	return spriteCmd
}

// showSpriteStatus displays the current sprite status.
func showSpriteStatus() {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	token := getSpriteToken(database)
	spriteName := getSpriteName(database)

	fmt.Println(spriteTitleStyle.Render("Sprite Status"))
	fmt.Println()

	if token == "" {
		fmt.Println(dimStyle.Render("  No Sprites token configured."))
		fmt.Println(dimStyle.Render("  Set SPRITES_TOKEN env var or run: task sprite token <token>"))
		return
	}

	fmt.Printf("  Token: %s\n", dimStyle.Render("configured"))
	fmt.Printf("  Name:  %s\n", spriteName)

	// Try to get sprite status from API
	client, err := getSpriteClient(database)
	if err != nil {
		fmt.Printf("  Status: %s\n", spriteErrorStyle.Render("error - "+err.Error()))
		return
	}

	ctx := context.Background()
	sprite, err := client.GetSprite(ctx, spriteName)
	if err != nil {
		fmt.Printf("  Status: %s\n", dimStyle.Render("not created"))
		fmt.Println()
		fmt.Println(dimStyle.Render("  Run 'task sprite up' to create it, or just run 'task'."))
		return
	}

	statusStyle := spriteCheckStyle
	statusIcon := "●"
	if sprite.Status == "suspended" || sprite.Status == "stopped" {
		statusStyle = spritePendingStyle
	}
	fmt.Printf("  Status: %s\n", statusStyle.Render(statusIcon+" "+sprite.Status))
	fmt.Printf("  URL:    %s\n", dimStyle.Render(sprite.URL))
}

// runSpriteUp ensures the sprite is running.
func runSpriteUp() error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	client, err := getSpriteClient(database)
	if err != nil {
		return err
	}

	spriteName := getSpriteName(database)
	ctx := context.Background()

	// Check if sprite exists
	sprite, err := client.GetSprite(ctx, spriteName)
	if err != nil {
		// Sprite doesn't exist, create it
		fmt.Printf("Creating sprite: %s\n", spriteName)
		sprite, err = client.CreateSprite(ctx, spriteName, nil)
		if err != nil {
			return fmt.Errorf("create sprite: %w", err)
		}
		fmt.Println(spriteCheckStyle.Render("✓ Sprite created"))

		// Save sprite name to database
		database.SetSetting(SettingSpriteName, spriteName)

		// Set up the sprite with task daemon
		if err := setupSprite(client, sprite); err != nil {
			return fmt.Errorf("setup sprite: %w", err)
		}
	} else if sprite.Status == "suspended" || sprite.Status == "stopped" {
		// Restore from checkpoint
		fmt.Println("Restoring sprite from checkpoint...")
		checkpoints, err := sprite.ListCheckpoints(ctx, "")
		if err != nil || len(checkpoints) == 0 {
			return fmt.Errorf("no checkpoints available to restore")
		}

		restoreStream, err := sprite.RestoreCheckpoint(ctx, checkpoints[0].ID)
		if err != nil {
			return fmt.Errorf("restore checkpoint: %w", err)
		}

		if err := restoreStream.ProcessAll(func(msg *sprites.StreamMessage) error {
			return nil
		}); err != nil {
			return fmt.Errorf("restore failed: %w", err)
		}
		fmt.Println(spriteCheckStyle.Render("✓ Sprite restored"))
	} else {
		fmt.Println(spriteCheckStyle.Render("✓ Sprite is already running"))
	}

	return nil
}

// setupSprite installs dependencies and task daemon on a new sprite.
func setupSprite(client *sprites.Client, sprite *sprites.Sprite) error {
	ctx := context.Background()

	fmt.Println("Setting up sprite...")

	// Install essential packages
	steps := []struct {
		desc string
		cmd  string
	}{
		{"Installing tmux", "apt-get update && apt-get install -y tmux git"},
		{"Installing Go", "curl -L https://go.dev/dl/go1.22.0.linux-amd64.tar.gz | tar -C /usr/local -xzf - && echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc"},
		{"Creating workspace", "mkdir -p /workspace"},
		{"Installing Node.js", "curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && apt-get install -y nodejs"},
		{"Installing Claude CLI", "npm install -g @anthropic-ai/claude-code"},
	}

	for _, step := range steps {
		fmt.Printf("  %s...\n", step.desc)
		cmd := sprite.CommandContext(ctx, "sh", "-c", step.cmd)
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("    %s: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
			fmt.Printf("    %s\n", dimStyle.Render(string(output)))
		}
	}

	// Clone and build task
	fmt.Println("  Building task daemon...")
	buildCmd := `
		cd /workspace &&
		git clone https://github.com/bborn/taskyou.git task 2>/dev/null || (cd task && git pull) &&
		cd task &&
		/usr/local/go/bin/go build -o /usr/local/bin/task ./cmd/task &&
		/usr/local/go/bin/go build -o /usr/local/bin/taskd ./cmd/taskd
	`
	cmd := sprite.CommandContext(ctx, "sh", "-c", buildCmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build task: %w\n%s", err, string(output))
	}

	// Create initial checkpoint
	fmt.Println("  Creating checkpoint...")
	checkpointStream, err := sprite.CreateCheckpointWithComment(ctx, "initial-setup")
	if err != nil {
		fmt.Printf("    %s: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
	} else {
		checkpointStream.ProcessAll(func(msg *sprites.StreamMessage) error { return nil })
	}

	fmt.Println(spriteCheckStyle.Render("✓ Sprite setup complete"))
	return nil
}

// runSpriteDown checkpoints and suspends the sprite.
func runSpriteDown() error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	client, err := getSpriteClient(database)
	if err != nil {
		return err
	}

	spriteName := getSpriteName(database)
	ctx := context.Background()

	sprite, err := client.GetSprite(ctx, spriteName)
	if err != nil {
		return fmt.Errorf("sprite not found: %s", spriteName)
	}

	fmt.Println("Creating checkpoint...")
	checkpointStream, err := sprite.CreateCheckpointWithComment(ctx, fmt.Sprintf("manual-%s", time.Now().Format("2006-01-02-150405")))
	if err != nil {
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	if err := checkpointStream.ProcessAll(func(msg *sprites.StreamMessage) error {
		return nil
	}); err != nil {
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	fmt.Println(spriteCheckStyle.Render("✓ Sprite checkpointed and suspended"))
	fmt.Println(dimStyle.Render("  Storage costs only while suspended (~$0.01/day per GB)"))
	return nil
}

// runSpriteAttach opens an interactive shell on the sprite.
func runSpriteAttach() error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	client, err := getSpriteClient(database)
	if err != nil {
		return err
	}

	spriteName := getSpriteName(database)
	sprite := client.Sprite(spriteName)

	fmt.Println("Attaching to sprite...")
	fmt.Println(dimStyle.Render("Press Ctrl+D to detach"))
	fmt.Println()

	cmd := sprite.Command("bash")
	cmd.SetTTY(true)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// runSpriteDestroy permanently deletes the sprite.
func runSpriteDestroy() error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	client, err := getSpriteClient(database)
	if err != nil {
		return err
	}

	spriteName := getSpriteName(database)
	ctx := context.Background()

	fmt.Printf("Destroying sprite: %s\n", spriteName)
	if err := client.DestroySprite(ctx, spriteName); err != nil {
		fmt.Printf("  %s: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
	} else {
		fmt.Println(spriteCheckStyle.Render("✓ Sprite destroyed"))
	}

	// Clear sprite name from database
	database.SetSetting(SettingSpriteName, "")

	return nil
}

// showSpriteToken shows whether a token is configured.
func showSpriteToken() {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	token := getSpriteToken(database)
	if token == "" {
		fmt.Println(dimStyle.Render("No Sprites token configured."))
		fmt.Println(dimStyle.Render("Set with: task sprite token <token>"))
		fmt.Println(dimStyle.Render("Or: export SPRITES_TOKEN=<token>"))
	} else {
		fmt.Println(spriteCheckStyle.Render("✓ Sprites token is configured"))
		fmt.Printf("  Token: %s...%s\n", token[:8], token[len(token)-4:])
	}
}

// setSpriteToken saves the Sprites API token to the database.
func setSpriteToken(token string) error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	if err := database.SetSetting(SettingSpriteToken, token); err != nil {
		return fmt.Errorf("save token: %w", err)
	}

	fmt.Println(spriteCheckStyle.Render("✓ Sprites token saved"))
	return nil
}

// ensureSpriteRunning ensures the sprite is running and returns the sprite reference.
// This is called automatically when task starts with SPRITES_TOKEN set.
func ensureSpriteRunning(database *db.DB) (*sprites.Client, *sprites.Sprite, error) {
	client, err := getSpriteClient(database)
	if err != nil {
		return nil, nil, err
	}

	spriteName := getSpriteName(database)
	ctx := context.Background()

	// Check if sprite exists
	sprite, err := client.GetSprite(ctx, spriteName)
	if err != nil {
		// Create sprite
		fmt.Println("Creating sprite...")
		sprite, err = client.CreateSprite(ctx, spriteName, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("create sprite: %w", err)
		}

		// Save name and set up
		database.SetSetting(SettingSpriteName, spriteName)
		if err := setupSprite(client, sprite); err != nil {
			return nil, nil, err
		}
	} else if sprite.Status == "suspended" || sprite.Status == "stopped" {
		// Restore
		fmt.Println("Restoring sprite...")
		checkpoints, err := sprite.ListCheckpoints(ctx, "")
		if err != nil || len(checkpoints) == 0 {
			return nil, nil, fmt.Errorf("no checkpoints to restore")
		}

		restoreStream, err := sprite.RestoreCheckpoint(ctx, checkpoints[0].ID)
		if err != nil {
			return nil, nil, err
		}
		restoreStream.ProcessAll(func(msg *sprites.StreamMessage) error { return nil })
	}

	return client, sprite, nil
}
