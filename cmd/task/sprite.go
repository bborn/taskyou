package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/sprites"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
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

Uses the 'sprite' CLI which authenticates via fly.io login.
Run 'sprite login' first if not already authenticated.

Commands:
  status     - Show sprite status and list available sprites
  use        - Select which sprite to use for task execution
  up         - Start/restore the sprite
  down       - Checkpoint and stop the sprite
  attach     - Attach to the sprite's shell
  destroy    - Delete the sprite entirely`,
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

	// sprite use
	useCmd := &cobra.Command{
		Use:   "use <sprite-name>",
		Short: "Select which sprite to use for task execution",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := runSpriteUse(args[0]); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	spriteCmd.AddCommand(useCmd)

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

	// sprite create
	createCmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new sprite",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := runSpriteCreate(args[0]); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	spriteCmd.AddCommand(createCmd)

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

	fmt.Println(spriteTitleStyle.Render("Sprite Status"))
	fmt.Println()

	// Check if sprite CLI is available
	if !sprites.IsAvailable() {
		fmt.Println(spriteErrorStyle.Render("  sprite CLI not found or not authenticated"))
		fmt.Println(dimStyle.Render("  Install: brew install superfly/tap/sprite"))
		fmt.Println(dimStyle.Render("  Then run: sprite login"))
		return
	}

	fmt.Println(spriteCheckStyle.Render("  ✓ sprite CLI authenticated"))

	// Show configured sprite name
	spriteName := sprites.GetName(database)
	fmt.Printf("  Selected sprite: %s\n", boldStyle.Render(spriteName))

	// List available sprites
	spriteList, err := sprites.ListSprites()
	if err != nil {
		fmt.Printf("  %s: %s\n", spriteErrorStyle.Render("Error listing sprites"), err.Error())
		return
	}

	fmt.Println()
	fmt.Println("  Available sprites:")
	if len(spriteList) == 0 {
		fmt.Println(dimStyle.Render("    (none - run 'task sprite create <name>' to create one)"))
	} else {
		for _, s := range spriteList {
			marker := "  "
			if s == spriteName {
				marker = spriteCheckStyle.Render("→ ")
			}
			fmt.Printf("    %s%s\n", marker, s)
		}
	}

	// Check if selected sprite exists
	found := false
	for _, s := range spriteList {
		if s == spriteName {
			found = true
			break
		}
	}

	if !found && len(spriteList) > 0 {
		fmt.Println()
		fmt.Printf("  %s\n", spritePendingStyle.Render("⚠ Selected sprite '"+spriteName+"' not found"))
		fmt.Println(dimStyle.Render("  Run 'task sprite use <name>' to select an existing sprite"))
	}
}

// runSpriteUse selects which sprite to use.
func runSpriteUse(name string) error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Verify the sprite exists
	spriteList, err := sprites.ListSprites()
	if err != nil {
		return fmt.Errorf("list sprites: %w", err)
	}

	found := false
	for _, s := range spriteList {
		if s == name {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("sprite '%s' not found. Available: %s", name, strings.Join(spriteList, ", "))
	}

	// Save to database
	if err := sprites.SetName(database, name); err != nil {
		return fmt.Errorf("save setting: %w", err)
	}

	fmt.Println(spriteCheckStyle.Render("✓ Now using sprite: " + name))
	return nil
}

// runSpriteUp ensures the sprite is running.
func runSpriteUp() error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	if !sprites.IsAvailable() {
		return fmt.Errorf("sprite CLI not available. Run 'sprite login' first")
	}

	spriteName := sprites.GetName(database)

	// Check if sprite exists
	spriteList, err := sprites.ListSprites()
	if err != nil {
		return fmt.Errorf("list sprites: %w", err)
	}

	found := false
	for _, s := range spriteList {
		if s == spriteName {
			found = true
			break
		}
	}

	if !found {
		// Create the sprite
		fmt.Printf("Creating sprite: %s\n", spriteName)
		if err := sprites.CreateSprite(spriteName); err != nil {
			return fmt.Errorf("create sprite: %w", err)
		}
		fmt.Println(spriteCheckStyle.Render("✓ Sprite created"))

		// Set up the sprite
		if err := setupSpriteCLI(spriteName); err != nil {
			return fmt.Errorf("setup sprite: %w", err)
		}
	} else {
		// Try to access the sprite to check if it's running
		info, err := sprites.GetSprite(spriteName)
		if err != nil {
			// May need to restore from checkpoint
			fmt.Println("Sprite may be suspended, checking checkpoints...")
			checkpoints, err := sprites.ListCheckpoints(spriteName)
			if err == nil && len(checkpoints) > 0 {
				fmt.Println("Restoring from checkpoint...")
				if err := sprites.RestoreCheckpoint(spriteName, checkpoints[0].ID); err != nil {
					return fmt.Errorf("restore checkpoint: %w", err)
				}
				fmt.Println(spriteCheckStyle.Render("✓ Sprite restored"))
			} else {
				return fmt.Errorf("sprite not accessible: %w", err)
			}
		} else {
			fmt.Println(spriteCheckStyle.Render("✓ Sprite is running"))
			fmt.Printf("  URL: %s\n", dimStyle.Render(info.URL))
		}
	}

	return nil
}

// setupSpriteCLI sets up a new sprite with required packages.
func setupSpriteCLI(spriteName string) error {
	fmt.Println("Setting up sprite...")

	steps := []struct {
		desc string
		cmd  string
	}{
		{"Installing packages", "apt-get update && apt-get install -y git curl"},
		{"Installing Node.js", "curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && apt-get install -y nodejs"},
		{"Installing Claude CLI", "npm install -g @anthropic-ai/claude-code"},
		{"Creating workspace", "mkdir -p /workspace"},
		{"Configuring Claude", "mkdir -p /root/.claude && echo '{\"permissions\":{\"allow\":[\"Bash(*)\",\"Read(*)\",\"Write(*)\",\"Edit(*)\",\"Grep(*)\",\"Glob(*)\"],\"deny\":[]}}' > /root/.claude/settings.json"},
	}

	for _, step := range steps {
		fmt.Printf("  %s...\n", step.desc)
		output, err := sprites.ExecCommand(spriteName, "sh", "-c", step.cmd)
		if err != nil {
			fmt.Printf("    %s: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
			if output != "" {
				fmt.Printf("    %s\n", dimStyle.Render(output))
			}
		}
	}

	// Create initial checkpoint
	fmt.Println("  Creating checkpoint...")
	if err := sprites.CreateCheckpoint(spriteName, "initial-setup"); err != nil {
		fmt.Printf("    %s: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
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

	if !sprites.IsAvailable() {
		return fmt.Errorf("sprite CLI not available")
	}

	spriteName := sprites.GetName(database)

	fmt.Println("Creating checkpoint...")
	if err := sprites.CreateCheckpoint(spriteName, "manual-checkpoint"); err != nil {
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	fmt.Println(spriteCheckStyle.Render("✓ Sprite checkpointed"))
	fmt.Println(dimStyle.Render("  The sprite will suspend after idle timeout"))
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

	if !sprites.IsAvailable() {
		return fmt.Errorf("sprite CLI not available")
	}

	spriteName := sprites.GetName(database)

	// Use the sprite CLI's console command for interactive shell
	fmt.Println("Attaching to sprite...")
	fmt.Println(dimStyle.Render("Press Ctrl+D to detach"))
	fmt.Println()

	// First select the sprite
	useCmd := exec.Command("sprite", "use", spriteName)
	if err := useCmd.Run(); err != nil {
		return fmt.Errorf("select sprite: %w", err)
	}

	// Then open console
	cmd := exec.Command("sprite", "console")
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

	if !sprites.IsAvailable() {
		return fmt.Errorf("sprite CLI not available")
	}

	spriteName := sprites.GetName(database)

	fmt.Printf("Destroying sprite: %s\n", spriteName)
	if err := sprites.Destroy(spriteName); err != nil {
		return fmt.Errorf("destroy failed: %w", err)
	}

	fmt.Println(spriteCheckStyle.Render("✓ Sprite destroyed"))

	// Clear sprite name from database
	sprites.SetName(database, "")

	return nil
}

// runSpriteCreate creates a new sprite.
func runSpriteCreate(name string) error {
	if !sprites.IsAvailable() {
		return fmt.Errorf("sprite CLI not available. Run 'sprite login' first")
	}

	fmt.Printf("Creating sprite: %s\n", name)
	if err := sprites.CreateSprite(name); err != nil {
		return fmt.Errorf("create sprite: %w", err)
	}

	fmt.Println(spriteCheckStyle.Render("✓ Sprite created: " + name))
	fmt.Println(dimStyle.Render("  Run 'task sprite use " + name + "' to select it"))
	return nil
}
