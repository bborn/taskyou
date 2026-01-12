package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/sprites"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	sdk "github.com/superfly/sprites-go"
)

// Styles for sprite command output
var (
	spriteTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#61AFEF"))
	spriteCheckStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
	spritePendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))
	spriteErrorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
)

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

	token := sprites.GetToken(database)
	spriteName := sprites.GetName(database)

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
	client, err := sprites.NewClient(database)
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

	client, err := sprites.NewClient(database)
	if err != nil {
		return err
	}

	spriteName := sprites.GetName(database)
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
		database.SetSetting(sprites.SettingName, spriteName)

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

		if err := restoreStream.ProcessAll(func(msg *sdk.StreamMessage) error {
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

// setupSprite installs dependencies and configures Claude hooks on a new sprite.
func setupSprite(client *sdk.Client, sprite *sdk.Sprite) error {
	ctx := context.Background()

	fmt.Println("Setting up sprite...")

	// Install essential packages
	steps := []struct {
		desc string
		cmd  string
	}{
		{"Installing packages", "apt-get update && apt-get install -y tmux git curl"},
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

	// Install the hook script that writes to a file for streaming back
	fmt.Println("  Installing hook script...")
	hookScript := `#!/bin/bash
# task-sprite-hook: Writes Claude hook events to a file for streaming
# The task executor on the local machine tails this file
input=$(cat)
printf '{"task_id":%s,"event":"%s","data":%s}\n' "$TASK_ID" "$1" "$input" >> /tmp/task-hooks.jsonl
`
	installHookCmd := fmt.Sprintf(`cat > /usr/local/bin/task-sprite-hook << 'HOOKEOF'
%s
HOOKEOF
chmod +x /usr/local/bin/task-sprite-hook`, hookScript)

	cmd := sprite.CommandContext(ctx, "sh", "-c", installHookCmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("install hook script: %w\n%s", err, string(output))
	}

	// Configure Claude to use our hook script
	fmt.Println("  Configuring Claude hooks...")
	claudeSettings := `{
  "permissions": {
    "allow": ["Bash(*)", "Read(*)", "Write(*)", "Edit(*)", "Grep(*)", "Glob(*)", "WebFetch(*)", "Task(*)", "TodoWrite(*)"],
    "deny": []
  },
  "hooks": {
    "PreToolUse": ["/usr/local/bin/task-sprite-hook PreToolUse"],
    "PostToolUse": ["/usr/local/bin/task-sprite-hook PostToolUse"],
    "Notification": ["/usr/local/bin/task-sprite-hook Notification"],
    "Stop": ["/usr/local/bin/task-sprite-hook Stop"]
  }
}`
	configureClaudeCmd := fmt.Sprintf(`mkdir -p ~/.claude && cat > ~/.claude/settings.json << 'SETTINGSEOF'
%s
SETTINGSEOF`, claudeSettings)

	cmd = sprite.CommandContext(ctx, "sh", "-c", configureClaudeCmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("configure claude: %w\n%s", err, string(output))
	}

	// Initialize the hooks file
	cmd = sprite.CommandContext(ctx, "sh", "-c", "touch /tmp/task-hooks.jsonl")
	cmd.Run()

	// Create initial checkpoint
	fmt.Println("  Creating checkpoint...")
	checkpointStream, err := sprite.CreateCheckpointWithComment(ctx, "initial-setup")
	if err != nil {
		fmt.Printf("    %s: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
	} else {
		checkpointStream.ProcessAll(func(msg *sdk.StreamMessage) error { return nil })
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

	client, err := sprites.NewClient(database)
	if err != nil {
		return err
	}

	spriteName := sprites.GetName(database)
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

	if err := checkpointStream.ProcessAll(func(msg *sdk.StreamMessage) error {
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

	client, err := sprites.NewClient(database)
	if err != nil {
		return err
	}

	spriteName := sprites.GetName(database)
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

	client, err := sprites.NewClient(database)
	if err != nil {
		return err
	}

	spriteName := sprites.GetName(database)
	ctx := context.Background()

	fmt.Printf("Destroying sprite: %s\n", spriteName)
	if err := client.DestroySprite(ctx, spriteName); err != nil {
		fmt.Printf("  %s: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
	} else {
		fmt.Println(spriteCheckStyle.Render("✓ Sprite destroyed"))
	}

	// Clear sprite name from database
	database.SetSetting(sprites.SettingName, "")

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

	token := sprites.GetToken(database)
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

	if err := database.SetSetting(sprites.SettingToken, token); err != nil {
		return fmt.Errorf("save token: %w", err)
	}

	fmt.Println(spriteCheckStyle.Render("✓ Sprites token saved"))
	return nil
}
