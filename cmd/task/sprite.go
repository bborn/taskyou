package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	sprites "github.com/superfly/sprites-go"
)

// Sprite settings keys
const (
	SettingSpriteToken = "sprite_token" // Sprites API token
)

// Sprite status values
const (
	SpriteStatusReady        = "ready"
	SpriteStatusCheckpointed = "checkpointed"
	SpriteStatusError        = "error"
)

// Styles for sprite command output
var (
	spriteTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#61AFEF"))
	spriteCheckStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
	spritePendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))
	spriteErrorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
)

// getSpriteClient creates a Sprites API client.
func getSpriteClient() (*sprites.Client, error) {
	// First try environment variable
	token := os.Getenv("SPRITES_TOKEN")

	// Fall back to database setting
	if token == "" {
		dbPath := db.DefaultPath()
		database, err := db.Open(dbPath)
		if err != nil {
			return nil, fmt.Errorf("open database: %w", err)
		}
		defer database.Close()

		token, _ = database.GetSetting(SettingSpriteToken)
	}

	if token == "" {
		return nil, fmt.Errorf("no Sprites token configured. Set SPRITES_TOKEN env var or run: task config set sprite_token <token>")
	}

	return sprites.New(token), nil
}

// createSpriteCommand creates the sprite subcommand with all its children.
func createSpriteCommand() *cobra.Command {
	spriteCmd := &cobra.Command{
		Use:   "sprite",
		Short: "Manage project sprites (cloud execution environments)",
		Long: `Sprite management for running tasks in isolated cloud environments.

Sprites are persistent, isolated Linux VMs that run Claude in dangerous mode safely.
Each project gets its own sprite with its development environment.

Commands:
  init     - Initialize a sprite for a project
  status   - Show sprite status for projects
  destroy  - Delete a project's sprite
  attach   - Attach to a sprite's tmux session
  sync     - Sync code and dependencies to sprite`,
		Run: func(cmd *cobra.Command, args []string) {
			// Show sprite status by default
			showSpriteStatus("")
		},
	}

	// sprite init
	initCmd := &cobra.Command{
		Use:   "init <project>",
		Short: "Initialize a sprite for a project",
		Long: `Create a new sprite for a project and set up the development environment.

This will:
1. Create a new sprite VM
2. Clone the project repository
3. Install dependencies (detected automatically)
4. Create an initial checkpoint
5. Mark the project for sprite execution`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := runSpriteInit(args[0]); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	spriteCmd.AddCommand(initCmd)

	// sprite status
	statusCmd := &cobra.Command{
		Use:   "status [project]",
		Short: "Show sprite status for projects",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			project := ""
			if len(args) > 0 {
				project = args[0]
			}
			showSpriteStatus(project)
		},
	}
	spriteCmd.AddCommand(statusCmd)

	// sprite destroy
	destroyCmd := &cobra.Command{
		Use:   "destroy <project>",
		Short: "Delete a project's sprite",
		Long:  `Destroy the sprite VM for a project. This will delete all data on the sprite.`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := runSpriteDestroy(args[0]); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	spriteCmd.AddCommand(destroyCmd)

	// sprite attach
	attachCmd := &cobra.Command{
		Use:   "attach <project>",
		Short: "Attach to a sprite's tmux session",
		Long:  `Open an interactive shell to the sprite and attach to the tmux session.`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			taskID, _ := cmd.Flags().GetInt64("task")
			if err := runSpriteAttach(args[0], taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	attachCmd.Flags().Int64P("task", "t", 0, "Attach to a specific task window")
	spriteCmd.AddCommand(attachCmd)

	// sprite sync
	syncCmd := &cobra.Command{
		Use:   "sync <project>",
		Short: "Sync code and dependencies to sprite",
		Long:  `Pull latest code and reinstall dependencies if needed.`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := runSpriteSync(args[0]); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	spriteCmd.AddCommand(syncCmd)

	// sprite checkpoint
	checkpointCmd := &cobra.Command{
		Use:   "checkpoint <project>",
		Short: "Create a checkpoint of the sprite",
		Long:  `Save the current state of the sprite for later restoration.`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := runSpriteCheckpoint(args[0]); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	spriteCmd.AddCommand(checkpointCmd)

	return spriteCmd
}

// showSpriteStatus displays sprite status for one or all projects.
func showSpriteStatus(projectName string) {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	var projects []*db.Project
	if projectName != "" {
		p, err := database.GetProjectByName(projectName)
		if err != nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
			os.Exit(1)
		}
		if p == nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render("Project not found: "+projectName))
			os.Exit(1)
		}
		projects = []*db.Project{p}
	} else {
		projects, err = database.ListProjects()
		if err != nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
			os.Exit(1)
		}
	}

	fmt.Println(spriteTitleStyle.Render("Sprite Status"))
	fmt.Println()

	hasSprites := false
	for _, p := range projects {
		if p.SpriteName == "" {
			continue
		}
		hasSprites = true

		statusIcon := "●"
		statusStyle := spriteCheckStyle
		switch p.SpriteStatus {
		case SpriteStatusReady:
			statusStyle = spriteCheckStyle
		case SpriteStatusCheckpointed:
			statusStyle = spritePendingStyle
		case SpriteStatusError:
			statusStyle = spriteErrorStyle
		default:
			statusStyle = dimStyle
		}

		fmt.Printf("  %s %s\n", boldStyle.Render(p.Name), statusStyle.Render(statusIcon+" "+p.SpriteStatus))
		fmt.Printf("    Sprite: %s\n", dimStyle.Render(p.SpriteName))
	}

	if !hasSprites {
		fmt.Println(dimStyle.Render("  No sprites configured."))
		fmt.Println(dimStyle.Render("  Run 'task sprite init <project>' to create one."))
	}
}

// runSpriteInit initializes a sprite for a project.
func runSpriteInit(projectName string) error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Get project
	project, err := database.GetProjectByName(projectName)
	if err != nil {
		return fmt.Errorf("get project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("project not found: %s", projectName)
	}

	// Check if sprite already exists
	if project.SpriteName != "" {
		return fmt.Errorf("sprite already exists for project %s (name: %s)", projectName, project.SpriteName)
	}

	// Get sprite client
	client, err := getSpriteClient()
	if err != nil {
		return err
	}

	// Generate sprite name
	spriteName := fmt.Sprintf("task-%s-%d", projectName, time.Now().Unix())
	fmt.Printf("Creating sprite: %s\n", spriteName)

	// Create sprite
	ctx := context.Background()
	sprite, err := client.CreateSprite(ctx, spriteName, nil)
	if err != nil {
		return fmt.Errorf("create sprite: %w", err)
	}

	fmt.Println(spriteCheckStyle.Render("✓ Sprite created"))

	// Get git remote URL
	gitRemote, err := getGitRemote(project.Path)
	if err != nil {
		// Clean up sprite on failure
		sprite.Destroy()
		return fmt.Errorf("get git remote: %w", err)
	}

	// Clone repository
	fmt.Printf("Cloning repository: %s\n", gitRemote)
	cmd := sprite.Command("git", "clone", gitRemote, "/workspace")
	if output, err := cmd.CombinedOutput(); err != nil {
		sprite.Destroy()
		return fmt.Errorf("clone repository: %w\n%s", err, string(output))
	}
	fmt.Println(spriteCheckStyle.Render("✓ Repository cloned"))

	// Detect and run setup commands
	fmt.Println("Setting up development environment...")
	setupCommands := detectSetupCommands(sprite)
	for _, setupCmd := range setupCommands {
		fmt.Printf("  Running: %s\n", setupCmd)
		cmd := sprite.Command("sh", "-c", "cd /workspace && "+setupCmd)
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("  %s: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
			fmt.Printf("  %s\n", dimStyle.Render(string(output)))
		} else {
			fmt.Println(spriteCheckStyle.Render("  ✓ Done"))
		}
	}

	// Install Claude CLI
	fmt.Println("Installing Claude CLI...")
	cmd = sprite.Command("npm", "install", "-g", "@anthropic-ai/claude-code")
	if _, err := cmd.CombinedOutput(); err != nil {
		fmt.Printf("  %s: Could not install Claude CLI: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
	} else {
		fmt.Println(spriteCheckStyle.Render("✓ Claude CLI installed"))
	}

	// Initialize tmux session
	fmt.Println("Initializing tmux session...")
	cmd = sprite.Command("tmux", "new-session", "-d", "-s", "task-daemon")
	cmd.Run() // Ignore error if session exists

	// Create checkpoint
	fmt.Println("Creating initial checkpoint...")
	checkpointStream, err := sprite.CreateCheckpointWithComment(ctx, "initial-setup")
	if err != nil {
		fmt.Printf("  %s: Could not create checkpoint: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
	} else {
		// Wait for checkpoint to complete
		if err := checkpointStream.ProcessAll(func(msg *sprites.StreamMessage) error {
			return nil // Just wait for completion
		}); err != nil {
			fmt.Printf("  %s: Checkpoint error: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
		} else {
			fmt.Println(spriteCheckStyle.Render("✓ Checkpoint created"))
		}
	}

	// Update project in database
	project.SpriteName = spriteName
	project.SpriteStatus = SpriteStatusReady
	if err := database.UpdateProject(project); err != nil {
		return fmt.Errorf("update project: %w", err)
	}

	fmt.Println()
	fmt.Println(spriteCheckStyle.Render("✓ Sprite ready!"))
	fmt.Printf("  Tasks for %s will now execute on the sprite.\n", projectName)
	fmt.Printf("  Use 'task execute <id> --sprite' or 'task sprite attach %s'\n", projectName)

	return nil
}

// detectSetupCommands checks for common project files and returns setup commands.
func detectSetupCommands(sprite *sprites.Sprite) []string {
	var commands []string

	// Check for various project files
	checks := []struct {
		file    string
		command string
	}{
		{"Gemfile", "bundle install"},
		{"package-lock.json", "npm ci"},
		{"yarn.lock", "yarn install --frozen-lockfile"},
		{"pnpm-lock.yaml", "pnpm install --frozen-lockfile"},
		{"requirements.txt", "pip install -r requirements.txt"},
		{"pyproject.toml", "pip install -e '.[dev]' 2>/dev/null || pip install -e ."},
		{"go.mod", "go mod download"},
		{"Cargo.toml", "cargo fetch"},
		{"bin/setup", "./bin/setup"},
	}

	for _, check := range checks {
		cmd := sprite.Command("test", "-f", "/workspace/"+check.file)
		if cmd.Run() == nil {
			commands = append(commands, check.command)
		}
	}

	return commands
}

// runSpriteDestroy destroys a project's sprite.
func runSpriteDestroy(projectName string) error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Get project
	project, err := database.GetProjectByName(projectName)
	if err != nil {
		return fmt.Errorf("get project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("project not found: %s", projectName)
	}

	if project.SpriteName == "" {
		return fmt.Errorf("no sprite configured for project: %s", projectName)
	}

	// Get sprite client
	client, err := getSpriteClient()
	if err != nil {
		return err
	}

	fmt.Printf("Destroying sprite: %s\n", project.SpriteName)

	// Destroy sprite
	ctx := context.Background()
	if err := client.DestroySprite(ctx, project.SpriteName); err != nil {
		// Log but continue - sprite might already be gone
		fmt.Printf("  %s: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
	} else {
		fmt.Println(spriteCheckStyle.Render("✓ Sprite destroyed"))
	}

	// Update project in database
	project.SpriteName = ""
	project.SpriteStatus = ""
	if err := database.UpdateProject(project); err != nil {
		return fmt.Errorf("update project: %w", err)
	}

	fmt.Println(spriteCheckStyle.Render("✓ Project updated"))
	return nil
}

// runSpriteAttach attaches to a sprite's tmux session.
func runSpriteAttach(projectName string, taskID int64) error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Get project
	project, err := database.GetProjectByName(projectName)
	if err != nil {
		return fmt.Errorf("get project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("project not found: %s", projectName)
	}

	if project.SpriteName == "" {
		return fmt.Errorf("no sprite configured for project: %s", projectName)
	}

	// Get sprite client
	client, err := getSpriteClient()
	if err != nil {
		return err
	}

	sprite := client.Sprite(project.SpriteName)

	// Build tmux command
	tmuxCmd := "tmux attach -t task-daemon"
	if taskID > 0 {
		tmuxCmd = fmt.Sprintf("tmux select-window -t task-daemon:task-%d && tmux attach -t task-daemon", taskID)
	}

	fmt.Printf("Attaching to sprite: %s\n", project.SpriteName)
	fmt.Println(dimStyle.Render("Press Ctrl+B, D to detach"))
	fmt.Println()

	// Run interactive command
	cmd := sprite.Command("sh", "-c", tmuxCmd)
	cmd.SetTTY(true)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// runSpriteSync syncs code and dependencies to a sprite.
func runSpriteSync(projectName string) error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Get project
	project, err := database.GetProjectByName(projectName)
	if err != nil {
		return fmt.Errorf("get project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("project not found: %s", projectName)
	}

	if project.SpriteName == "" {
		return fmt.Errorf("no sprite configured for project: %s", projectName)
	}

	// Get sprite client
	client, err := getSpriteClient()
	if err != nil {
		return err
	}

	sprite := client.Sprite(project.SpriteName)

	fmt.Println("Syncing sprite...")

	// Get current HEAD before pull
	cmd := sprite.Command("git", "-C", "/workspace", "rev-parse", "HEAD")
	oldHead, _ := cmd.Output()

	// Pull latest
	fmt.Println("  Pulling latest code...")
	cmd = sprite.Command("git", "-C", "/workspace", "fetch", "origin")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch: %w\n%s", err, string(output))
	}

	cmd = sprite.Command("git", "-C", "/workspace", "reset", "--hard", "origin/main")
	if output, err := cmd.CombinedOutput(); err != nil {
		// Try master if main doesn't exist
		cmd = sprite.Command("git", "-C", "/workspace", "reset", "--hard", "origin/master")
		if output2, err2 := cmd.CombinedOutput(); err2 != nil {
			return fmt.Errorf("reset: %w\n%s", err, string(output)+"\n"+string(output2))
		}
	}
	fmt.Println(spriteCheckStyle.Render("  ✓ Code updated"))

	// Get new HEAD
	cmd = sprite.Command("git", "-C", "/workspace", "rev-parse", "HEAD")
	newHead, _ := cmd.Output()

	// Check if deps changed
	if strings.TrimSpace(string(oldHead)) != strings.TrimSpace(string(newHead)) {
		// Check for dependency file changes
		depFiles := []string{"Gemfile.lock", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "requirements.txt", "go.sum", "Cargo.lock"}
		depsChanged := false

		for _, f := range depFiles {
			cmd = sprite.Command("git", "-C", "/workspace", "diff", "--name-only", strings.TrimSpace(string(oldHead)), strings.TrimSpace(string(newHead)), "--", f)
			if output, _ := cmd.Output(); len(strings.TrimSpace(string(output))) > 0 {
				depsChanged = true
				break
			}
		}

		if depsChanged {
			fmt.Println("  Dependencies changed, reinstalling...")
			setupCommands := detectSetupCommands(sprite)
			for _, setupCmd := range setupCommands {
				fmt.Printf("    Running: %s\n", setupCmd)
				cmd := sprite.Command("sh", "-c", "cd /workspace && "+setupCmd)
				if _, err := cmd.CombinedOutput(); err != nil {
					fmt.Printf("    %s: %s\n", spriteErrorStyle.Render("Warning"), err.Error())
				}
			}
			fmt.Println(spriteCheckStyle.Render("  ✓ Dependencies updated"))
		}
	}

	fmt.Println(spriteCheckStyle.Render("✓ Sync complete"))
	return nil
}

// runSpriteCheckpoint creates a checkpoint of the sprite.
func runSpriteCheckpoint(projectName string) error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Get project
	project, err := database.GetProjectByName(projectName)
	if err != nil {
		return fmt.Errorf("get project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("project not found: %s", projectName)
	}

	if project.SpriteName == "" {
		return fmt.Errorf("no sprite configured for project: %s", projectName)
	}

	// Get sprite client
	client, err := getSpriteClient()
	if err != nil {
		return err
	}

	sprite := client.Sprite(project.SpriteName)
	ctx := context.Background()

	fmt.Printf("Creating checkpoint for %s...\n", project.SpriteName)

	checkpointStream, err := sprite.CreateCheckpointWithComment(ctx, fmt.Sprintf("manual-%s", time.Now().Format("2006-01-02-150405")))
	if err != nil {
		return fmt.Errorf("create checkpoint: %w", err)
	}

	// Wait for checkpoint to complete
	if err := checkpointStream.ProcessAll(func(msg *sprites.StreamMessage) error {
		return nil // Just wait for completion
	}); err != nil {
		return fmt.Errorf("checkpoint failed: %w", err)
	}
	fmt.Println(spriteCheckStyle.Render("✓ Checkpoint created"))

	// Update status
	project.SpriteStatus = SpriteStatusCheckpointed
	if err := database.UpdateProject(project); err != nil {
		return fmt.Errorf("update project: %w", err)
	}

	return nil
}
