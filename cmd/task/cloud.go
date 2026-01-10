// cloud.go provides the `task cloud` subcommand for managing cloud server deployments.
// It enables easy local-to-cloud migration with an interactive setup wizard.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// Cloud configuration setting keys
const (
	SettingCloudServer     = "cloud_server"      // e.g., "root@my-hetzner.com"
	SettingCloudSSHPort    = "cloud_ssh_port"    // e.g., "22"
	SettingCloudTaskPort   = "cloud_task_port"   // e.g., "2222"
	SettingCloudRemoteUser = "cloud_remote_user" // e.g., "runner"
	SettingCloudRemoteDir  = "cloud_remote_dir"  // e.g., "/home/runner"
)

// Styling for cloud command output
var (
	cloudHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#61AFEF"))
	cloudBoxStyle    = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#6B7280")).
				Padding(0, 1)
	cloudCheckStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
	cloudXStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
	cloudDimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
)

// createCloudCmd creates the cloud command and its subcommands.
func createCloudCmd() *cobra.Command {
	cloudCmd := &cobra.Command{
		Use:   "cloud",
		Short: "Manage cloud server deployment",
		Long: `Manage cloud server deployment for the task system.

Commands:
  init    Interactive wizard to set up a new cloud server
  status  Check cloud server status
  logs    Stream logs from the cloud server
  sync    Sync projects and optionally redeploy`,
		Run: func(cmd *cobra.Command, args []string) {
			// Show help if no subcommand
			cmd.Help()
		},
	}

	// Add subcommands
	cloudCmd.AddCommand(createCloudInitCmd())
	cloudCmd.AddCommand(createCloudStatusCmd())
	cloudCmd.AddCommand(createCloudLogsCmd())
	cloudCmd.AddCommand(createCloudSyncCmd())

	return cloudCmd
}

// createCloudInitCmd creates the `task cloud init` command.
func createCloudInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Interactive wizard to set up a cloud server",
		Long: `Set up a new cloud server for running tasks remotely.

This wizard will:
1. Connect to your server and install dependencies
2. Set up GitHub SSH access
3. Configure Claude authentication
4. Clone your projects
5. Deploy and start the task service`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runCloudInit(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
}

// createCloudStatusCmd creates the `task cloud status` command.
func createCloudStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check cloud server status",
		Long:  `Check if the cloud server is reachable and show service status.`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runCloudStatus(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
}

// createCloudLogsCmd creates the `task cloud logs` command.
func createCloudLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs",
		Short: "Stream logs from the cloud server",
		Long:  `Stream the taskd service logs from the cloud server using journalctl.`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runCloudLogs(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
}

// createCloudSyncCmd creates the `task cloud sync` command.
func createCloudSyncCmd() *cobra.Command {
	var redeploy bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync projects and optionally redeploy",
		Long:  `Pull latest changes for all projects on the cloud server and optionally redeploy the binary.`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runCloudSync(redeploy); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}

	cmd.Flags().BoolVarP(&redeploy, "redeploy", "r", false, "Also rebuild and redeploy the taskd binary")

	return cmd
}

// SSH helper functions

// sshRun executes a command on the remote server and returns the output.
func sshRun(server, command string) (string, error) {
	cmd := osexec.Command("ssh", server, command)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// sshRunInteractive executes a command on the remote server with interactive I/O.
func sshRunInteractive(server, command string) error {
	cmd := osexec.Command("ssh", "-t", server, command)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// scpFile copies a local file to the remote server.
func scpFile(local, remote string) error {
	return osexec.Command("scp", local, remote).Run()
}

// getCloudConfig retrieves cloud configuration from the database.
func getCloudConfig() (server, sshPort, taskPort, remoteUser, remoteDir string, err error) {
	database, err := db.Open(db.DefaultPath())
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	server, _ = database.GetSetting(SettingCloudServer)
	sshPort, _ = database.GetSetting(SettingCloudSSHPort)
	taskPort, _ = database.GetSetting(SettingCloudTaskPort)
	remoteUser, _ = database.GetSetting(SettingCloudRemoteUser)
	remoteDir, _ = database.GetSetting(SettingCloudRemoteDir)

	// Defaults
	if sshPort == "" {
		sshPort = "22"
	}
	if taskPort == "" {
		taskPort = "2222"
	}
	if remoteUser == "" {
		remoteUser = "runner"
	}
	if remoteDir == "" {
		remoteDir = "/home/runner"
	}

	return server, sshPort, taskPort, remoteUser, remoteDir, nil
}

// saveCloudConfig saves cloud configuration to the database.
func saveCloudConfig(server, sshPort, taskPort, remoteUser, remoteDir string) error {
	database, err := db.Open(db.DefaultPath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	if err := database.SetSetting(SettingCloudServer, server); err != nil {
		return err
	}
	if err := database.SetSetting(SettingCloudSSHPort, sshPort); err != nil {
		return err
	}
	if err := database.SetSetting(SettingCloudTaskPort, taskPort); err != nil {
		return err
	}
	if err := database.SetSetting(SettingCloudRemoteUser, remoteUser); err != nil {
		return err
	}
	if err := database.SetSetting(SettingCloudRemoteDir, remoteDir); err != nil {
		return err
	}

	return nil
}

// promptLine prompts the user for input and returns the trimmed response.
func promptLine(prompt string, defaultValue string) string {
	if defaultValue != "" {
		fmt.Printf("%s [%s]: ", prompt, defaultValue)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(response)
	if response == "" {
		return defaultValue
	}
	return response
}

// promptYesNo prompts the user for yes/no confirmation.
func promptYesNo(prompt string, defaultYes bool) bool {
	suffix := " [y/N]: "
	if defaultYes {
		suffix = " [Y/n]: "
	}
	fmt.Print(prompt + suffix)
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))
	if response == "" {
		return defaultYes
	}
	return response == "y" || response == "yes"
}

// waitForEnter waits for the user to press Enter.
func waitForEnter(prompt string) {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	reader.ReadString('\n')
}

// runCloudInit runs the interactive cloud setup wizard.
func runCloudInit() error {
	fmt.Println()
	fmt.Println(cloudHeaderStyle.Render("☁️  Cloud Setup"))
	fmt.Println(cloudDimStyle.Render(strings.Repeat("─", 50)))
	fmt.Println()

	// Step 1: Server Connection
	fmt.Println(boldStyle.Render("1. Server Connection"))

	server := promptLine("   Enter server address (e.g., root@my-hetzner.com)", "")
	if server == "" {
		return fmt.Errorf("server address is required")
	}

	// Test connection
	fmt.Print("   Testing connection... ")
	if _, err := sshRun(server, "echo ok"); err != nil {
		fmt.Println(cloudXStyle.Render("✗ Failed"))
		return fmt.Errorf("could not connect to server: %w", err)
	}
	fmt.Println(cloudCheckStyle.Render("✓ Connected"))

	// Install dependencies
	fmt.Print("   Installing dependencies... ")
	if _, err := sshRun(server, "apt-get update -qq && apt-get install -y -qq tmux git"); err != nil {
		fmt.Println(cloudXStyle.Render("✗ Failed"))
		return fmt.Errorf("could not install dependencies: %w", err)
	}
	fmt.Println(cloudCheckStyle.Render("✓ Installed (tmux, git)"))

	// Get config values
	remoteUser := promptLine("   Remote user for running taskd", "runner")
	remoteDir := promptLine("   Remote directory", "/home/"+remoteUser)

	// Create runner user if needed
	fmt.Print("   Setting up user... ")
	userCheck, _ := sshRun(server, fmt.Sprintf("id %s 2>/dev/null && echo exists", remoteUser))
	if !strings.Contains(userCheck, "exists") {
		createCmd := fmt.Sprintf("useradd -m -s /bin/bash %s", remoteUser)
		if _, err := sshRun(server, createCmd); err != nil {
			fmt.Println(cloudXStyle.Render("✗ Failed"))
			return fmt.Errorf("could not create user %s: %w", remoteUser, err)
		}
		fmt.Println(cloudCheckStyle.Render(fmt.Sprintf("✓ Created user '%s'", remoteUser)))
	} else {
		fmt.Println(cloudCheckStyle.Render(fmt.Sprintf("✓ User '%s' exists", remoteUser)))
	}

	fmt.Println()

	// Step 2: GitHub Access
	fmt.Println(boldStyle.Render("2. GitHub Access"))

	// Check if SSH key exists, generate if not
	keyCheck, _ := sshRun(server, fmt.Sprintf("sudo -u %s test -f %s/.ssh/id_ed25519 && echo exists", remoteUser, remoteDir))
	var pubKey string
	if !strings.Contains(keyCheck, "exists") {
		fmt.Print("   Generating SSH key... ")
		sshDir := fmt.Sprintf("%s/.ssh", remoteDir)
		keyPath := fmt.Sprintf("%s/id_ed25519", sshDir)

		// Create .ssh dir and generate key
		cmds := []string{
			fmt.Sprintf("mkdir -p %s", sshDir),
			fmt.Sprintf("ssh-keygen -t ed25519 -f %s -N ''", keyPath),
			fmt.Sprintf("chown -R %s:%s %s", remoteUser, remoteUser, sshDir),
			fmt.Sprintf("chmod 700 %s && chmod 600 %s", sshDir, keyPath),
		}
		for _, c := range cmds {
			if _, err := sshRun(server, c); err != nil {
				fmt.Println(cloudXStyle.Render("✗ Failed"))
				return fmt.Errorf("ssh key setup failed: %w", err)
			}
		}
		fmt.Println(cloudCheckStyle.Render("✓ Generated"))
	} else {
		fmt.Println(cloudCheckStyle.Render("   ✓ SSH key already exists"))
	}

	// Get public key
	pubKeyCmd := fmt.Sprintf("cat %s/.ssh/id_ed25519.pub", remoteDir)
	pubKey, err := sshRun(server, pubKeyCmd)
	if err != nil {
		return fmt.Errorf("could not read public key: %w", err)
	}
	pubKey = strings.TrimSpace(pubKey)

	fmt.Println()
	fmt.Println("   Add this key to GitHub → Settings → SSH Keys:")
	fmt.Println()
	fmt.Println(cloudBoxStyle.Render(pubKey))
	fmt.Println()

	waitForEnter("   [Press Enter when done]")

	// Test GitHub access
	fmt.Print("   Testing GitHub access... ")
	githubTest, _ := sshRun(server, fmt.Sprintf("sudo -u %s ssh -o StrictHostKeyChecking=accept-new -T git@github.com 2>&1 || true", remoteUser))
	if strings.Contains(githubTest, "successfully authenticated") {
		fmt.Println(cloudCheckStyle.Render("✓ GitHub access verified"))
	} else {
		fmt.Println(cloudXStyle.Render("✗ Could not verify"))
		fmt.Println(cloudDimStyle.Render("   (You can retry later with 'task cloud sync')"))
	}

	fmt.Println()

	// Step 3: Claude Authentication
	fmt.Println(boldStyle.Render("3. Claude Authentication"))

	// Detect local auth method
	fmt.Println("   Select authentication method:")
	fmt.Println()
	fmt.Println("   [1] Claude Max/Pro (subscription) - recommended")
	fmt.Println("   [2] API key")
	fmt.Println()
	authChoice := promptLine("   Choice", "1")

	if authChoice == "2" {
		// API key authentication
		apiKey := promptLine("   Enter your Claude API key", "")
		if apiKey != "" {
			// Set API key on server
			configCmd := fmt.Sprintf("sudo -u %s claude config set api_key %s", remoteUser, apiKey)
			if _, err := sshRun(server, configCmd); err != nil {
				fmt.Println(cloudXStyle.Render("   ✗ Failed to set API key"))
			} else {
				fmt.Println(cloudCheckStyle.Render("   ✓ API key configured"))
			}
		}
	} else {
		// Claude Max OAuth
		fmt.Println()
		fmt.Println("   Run this command in another terminal to authenticate:")
		fmt.Println()
		fmt.Println(cloudBoxStyle.Render(fmt.Sprintf("ssh %s \"sudo -u %s claude login\"", server, remoteUser)))
		fmt.Println()
		fmt.Println("   This will open a browser for OAuth authentication.")

		waitForEnter("   [Press Enter when done]")

		// Verify auth
		fmt.Print("   Verifying Claude authentication... ")
		authCheck, _ := sshRun(server, fmt.Sprintf("sudo -u %s claude --version 2>/dev/null", remoteUser))
		if strings.Contains(authCheck, "claude") {
			fmt.Println(cloudCheckStyle.Render("✓ Claude available"))
		} else {
			fmt.Println(cloudDimStyle.Render("○ Could not verify (may still work)"))
		}
	}

	fmt.Println()

	// Step 4: Projects
	fmt.Println(boldStyle.Render("4. Projects"))

	// Get local projects
	database, err := db.Open(db.DefaultPath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	projects, err := database.ListProjects()
	if err != nil {
		database.Close()
		return fmt.Errorf("list projects: %w", err)
	}
	database.Close()

	// For each project, detect git remote
	type projectInfo struct {
		name   string
		path   string
		remote string
	}
	var projectsWithRemotes []projectInfo

	for _, p := range projects {
		// Skip personal project
		if p.Name == "personal" {
			continue
		}

		// Get git remote
		remoteCmd := osexec.Command("git", "-C", p.Path, "remote", "get-url", "origin")
		remoteOut, err := remoteCmd.Output()
		if err == nil {
			remote := strings.TrimSpace(string(remoteOut))
			projectsWithRemotes = append(projectsWithRemotes, projectInfo{
				name:   p.Name,
				path:   p.Path,
				remote: remote,
			})
		}
	}

	if len(projectsWithRemotes) == 0 {
		fmt.Println(cloudDimStyle.Render("   No projects with git remotes found"))
	} else {
		fmt.Println("   Select projects to clone on server:")
		fmt.Println()

		// Show projects and let user select
		selected := make(map[int]bool)
		for i, p := range projectsWithRemotes {
			selected[i] = true // Default all selected
			fmt.Printf("   [%d] %s → %s\n", i+1, p.name, cloudDimStyle.Render(p.remote))
		}

		fmt.Println()
		fmt.Println("   Enter numbers to toggle (comma-separated), or Enter to accept all:")
		toggleInput := promptLine("   Toggle", "")

		if toggleInput != "" {
			// Parse toggle input
			for i := range selected {
				selected[i] = false
			}
			for _, s := range strings.Split(toggleInput, ",") {
				s = strings.TrimSpace(s)
				var idx int
				if _, err := fmt.Sscanf(s, "%d", &idx); err == nil && idx >= 1 && idx <= len(projectsWithRemotes) {
					selected[idx-1] = true
				}
			}
		}

		// Clone selected projects
		clonedCount := 0
		projectsDir := fmt.Sprintf("%s/Projects", remoteDir)

		// Create Projects directory
		sshRun(server, fmt.Sprintf("sudo -u %s mkdir -p %s", remoteUser, projectsDir))

		for i, p := range projectsWithRemotes {
			if !selected[i] {
				continue
			}

			fmt.Printf("   Cloning %s... ", p.name)
			clonePath := fmt.Sprintf("%s/%s", projectsDir, p.name)

			// Check if already exists
			existsCheck, _ := sshRun(server, fmt.Sprintf("test -d %s && echo exists", clonePath))
			if strings.Contains(existsCheck, "exists") {
				fmt.Println(cloudCheckStyle.Render("✓ Already exists"))
				clonedCount++
				continue
			}

			// Clone the repo
			cloneCmd := fmt.Sprintf("sudo -u %s git clone %s %s", remoteUser, p.remote, clonePath)
			if _, err := sshRun(server, cloneCmd); err != nil {
				fmt.Println(cloudXStyle.Render("✗ Failed"))
			} else {
				fmt.Println(cloudCheckStyle.Render("✓ Cloned"))
				clonedCount++
			}
		}

		if clonedCount > 0 {
			fmt.Println()
			fmt.Println(cloudCheckStyle.Render(fmt.Sprintf("   ✓ %d project(s) ready", clonedCount)))
		}
	}

	fmt.Println()

	// Step 5: Deploy
	fmt.Println(boldStyle.Render("5. Deploy"))

	// Build linux binary
	fmt.Print("   Building taskd for linux/amd64... ")
	buildCmd := osexec.Command("go", "build", "-o", "bin/taskd-linux", "./cmd/taskd")
	buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")

	// Find the project root (where go.mod is)
	cwd, _ := os.Getwd()
	projectRoot := findProjectRoot(cwd)
	if projectRoot == "" {
		// Try to find it relative to the executable
		exe, _ := os.Executable()
		projectRoot = findProjectRoot(filepath.Dir(exe))
	}
	if projectRoot != "" {
		buildCmd.Dir = projectRoot
	}

	if err := buildCmd.Run(); err != nil {
		fmt.Println(cloudXStyle.Render("✗ Failed"))
		return fmt.Errorf("build failed: %w", err)
	}
	fmt.Println(cloudCheckStyle.Render("✓ Built"))

	// Deploy binary
	fmt.Print("   Deploying binary... ")
	binaryPath := filepath.Join(projectRoot, "bin", "taskd-linux")
	remoteBinary := fmt.Sprintf("%s:%s/taskd", server, remoteDir)

	if err := scpFile(binaryPath, remoteBinary); err != nil {
		fmt.Println(cloudXStyle.Render("✗ Failed"))
		return fmt.Errorf("deploy failed: %w", err)
	}

	// Set permissions
	permCmd := fmt.Sprintf("chmod +x %s/taskd && chown %s:%s %s/taskd", remoteDir, remoteUser, remoteUser, remoteDir)
	if _, err := sshRun(server, permCmd); err != nil {
		fmt.Println(cloudXStyle.Render("✗ Failed to set permissions"))
	}
	fmt.Println(cloudCheckStyle.Render("✓ Deployed"))

	// Install systemd service
	fmt.Print("   Installing systemd service... ")
	serviceContent := fmt.Sprintf(`[Unit]
Description=Task Queue Daemon
After=network.target

[Service]
ExecStart=%s/taskd
WorkingDirectory=%s
User=%s
Restart=always
Environment=HOME=%s

[Install]
WantedBy=multi-user.target`, remoteDir, remoteDir, remoteUser, remoteDir)

	// Write service file
	writeCmd := fmt.Sprintf("cat > /etc/systemd/system/taskd.service << 'EOFSERVICE'\n%s\nEOFSERVICE", serviceContent)
	if _, err := sshRun(server, writeCmd); err != nil {
		fmt.Println(cloudXStyle.Render("✗ Failed"))
		return fmt.Errorf("could not write service file: %w", err)
	}

	// Enable and start service
	if _, err := sshRun(server, "systemctl daemon-reload && systemctl enable taskd && systemctl restart taskd"); err != nil {
		fmt.Println(cloudXStyle.Render("✗ Failed to start service"))
	} else {
		fmt.Println(cloudCheckStyle.Render("✓ Service started"))
	}

	// Save configuration
	taskPort := "2222"
	if err := saveCloudConfig(server, "22", taskPort, remoteUser, remoteDir); err != nil {
		fmt.Println(cloudDimStyle.Render("   Warning: could not save config: " + err.Error()))
	}

	fmt.Println()
	fmt.Println(cloudDimStyle.Render(strings.Repeat("─", 50)))

	// Extract host from server for display
	serverHost := server
	if idx := strings.Index(server, "@"); idx >= 0 {
		serverHost = server[idx+1:]
	}

	fmt.Println(cloudHeaderStyle.Render(fmt.Sprintf("☁️  Ready! Connect: ssh -p %s %s", taskPort, serverHost)))
	fmt.Println()

	// Offer to migrate tasks
	if promptYesNo("   Migrate existing tasks from local DB?", false) {
		if err := migrateLocalTasks(server, remoteUser, remoteDir); err != nil {
			fmt.Println(cloudXStyle.Render("   ✗ Migration failed: " + err.Error()))
		} else {
			fmt.Println(cloudCheckStyle.Render("   ✓ Tasks migrated"))
		}
	}

	return nil
}

// findProjectRoot finds the project root directory (containing go.mod).
func findProjectRoot(start string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// migrateLocalTasks copies the local database to the remote server.
func migrateLocalTasks(server, remoteUser, remoteDir string) error {
	localDB := db.DefaultPath()
	remoteDB := fmt.Sprintf("%s:%s/.local/share/task/tasks.db", server, remoteDir)

	// Create remote directory
	sshRun(server, fmt.Sprintf("sudo -u %s mkdir -p %s/.local/share/task", remoteUser, remoteDir))

	// Copy database
	if err := scpFile(localDB, remoteDB); err != nil {
		return fmt.Errorf("copy database: %w", err)
	}

	// Set permissions
	sshRun(server, fmt.Sprintf("chown %s:%s %s/.local/share/task/tasks.db", remoteUser, remoteUser, remoteDir))

	return nil
}

// runCloudStatus checks the cloud server status.
func runCloudStatus() error {
	server, _, taskPort, remoteUser, remoteDir, err := getCloudConfig()
	if err != nil {
		return err
	}

	if server == "" {
		return fmt.Errorf("no cloud server configured. Run 'task cloud init' first")
	}

	fmt.Println()
	fmt.Println(cloudHeaderStyle.Render("☁️  Cloud Status"))
	fmt.Println(cloudDimStyle.Render(strings.Repeat("─", 50)))
	fmt.Println()

	fmt.Printf("   Server:      %s\n", server)
	fmt.Printf("   User:        %s\n", remoteUser)
	fmt.Printf("   Directory:   %s\n", remoteDir)
	fmt.Printf("   Task Port:   %s\n", taskPort)
	fmt.Println()

	// Check connectivity
	fmt.Print("   Connectivity: ")
	if _, err := sshRun(server, "echo ok"); err != nil {
		fmt.Println(cloudXStyle.Render("✗ Unreachable"))
		return nil
	}
	fmt.Println(cloudCheckStyle.Render("✓ Connected"))

	// Check service status
	fmt.Print("   Service:      ")
	statusOutput, _ := sshRun(server, "systemctl is-active taskd 2>/dev/null || echo inactive")
	status := strings.TrimSpace(statusOutput)
	if status == "active" {
		fmt.Println(cloudCheckStyle.Render("✓ Running"))
	} else {
		fmt.Println(cloudXStyle.Render("✗ " + status))
	}

	// Get task counts
	fmt.Print("   Tasks:        ")
	countCmd := fmt.Sprintf("sudo -u %s sqlite3 %s/.local/share/task/tasks.db \"SELECT status, COUNT(*) FROM tasks GROUP BY status\" 2>/dev/null", remoteUser, remoteDir)
	countOutput, err := sshRun(server, countCmd)
	if err != nil {
		fmt.Println(cloudDimStyle.Render("(could not query)"))
	} else {
		counts := parseTaskCounts(countOutput)
		if len(counts) == 0 {
			fmt.Println(cloudDimStyle.Render("0 tasks"))
		} else {
			var parts []string
			for status, count := range counts {
				parts = append(parts, fmt.Sprintf("%d %s", count, status))
			}
			fmt.Println(strings.Join(parts, ", "))
		}
	}

	fmt.Println()

	return nil
}

// parseTaskCounts parses SQLite output into status counts.
func parseTaskCounts(output string) map[string]int {
	counts := make(map[string]int)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) == 2 {
			var count int
			fmt.Sscanf(parts[1], "%d", &count)
			counts[parts[0]] = count
		}
	}
	return counts
}

// runCloudLogs streams logs from the cloud server.
func runCloudLogs() error {
	server, _, _, _, _, err := getCloudConfig()
	if err != nil {
		return err
	}

	if server == "" {
		return fmt.Errorf("no cloud server configured. Run 'task cloud init' first")
	}

	fmt.Println(cloudDimStyle.Render("Streaming logs from " + server + " (Ctrl+C to stop)..."))
	fmt.Println()

	return sshRunInteractive(server, "journalctl -u taskd -f")
}

// runCloudSync syncs projects and optionally redeploys.
func runCloudSync(redeploy bool) error {
	server, _, _, remoteUser, remoteDir, err := getCloudConfig()
	if err != nil {
		return err
	}

	if server == "" {
		return fmt.Errorf("no cloud server configured. Run 'task cloud init' first")
	}

	fmt.Println()
	fmt.Println(cloudHeaderStyle.Render("☁️  Cloud Sync"))
	fmt.Println(cloudDimStyle.Render(strings.Repeat("─", 50)))
	fmt.Println()

	// Get list of projects on server
	projectsDir := fmt.Sprintf("%s/Projects", remoteDir)
	listCmd := fmt.Sprintf("ls -1 %s 2>/dev/null || echo ''", projectsDir)
	listOutput, _ := sshRun(server, listCmd)

	projects := []string{}
	for _, p := range strings.Split(listOutput, "\n") {
		p = strings.TrimSpace(p)
		if p != "" {
			projects = append(projects, p)
		}
	}

	if len(projects) == 0 {
		fmt.Println(cloudDimStyle.Render("   No projects found on server"))
	} else {
		fmt.Println(boldStyle.Render("   Updating projects:"))
		for _, p := range projects {
			fmt.Printf("   %s: ", p)
			pullCmd := fmt.Sprintf("cd %s/%s && sudo -u %s git pull --ff-only 2>&1", projectsDir, p, remoteUser)
			output, err := sshRun(server, pullCmd)
			if err != nil {
				fmt.Println(cloudXStyle.Render("✗ Failed"))
			} else if strings.Contains(output, "Already up to date") {
				fmt.Println(cloudDimStyle.Render("up to date"))
			} else {
				fmt.Println(cloudCheckStyle.Render("✓ Updated"))
			}
		}
	}

	if redeploy {
		fmt.Println()
		fmt.Println(boldStyle.Render("   Redeploying taskd:"))

		// Build
		fmt.Print("   Building... ")
		buildCmd := osexec.Command("go", "build", "-o", "bin/taskd-linux", "./cmd/taskd")
		buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
		cwd, _ := os.Getwd()
		if projectRoot := findProjectRoot(cwd); projectRoot != "" {
			buildCmd.Dir = projectRoot
		}
		if err := buildCmd.Run(); err != nil {
			fmt.Println(cloudXStyle.Render("✗ Failed"))
			return fmt.Errorf("build failed: %w", err)
		}
		fmt.Println(cloudCheckStyle.Render("✓ Built"))

		// Deploy
		fmt.Print("   Deploying... ")
		projectRoot := findProjectRoot(cwd)
		binaryPath := filepath.Join(projectRoot, "bin", "taskd-linux")
		remoteBinary := fmt.Sprintf("%s:%s/taskd", server, remoteDir)
		if err := scpFile(binaryPath, remoteBinary); err != nil {
			fmt.Println(cloudXStyle.Render("✗ Failed"))
			return fmt.Errorf("deploy failed: %w", err)
		}
		fmt.Println(cloudCheckStyle.Render("✓ Deployed"))

		// Restart
		fmt.Print("   Restarting service... ")
		if _, err := sshRun(server, "systemctl restart taskd"); err != nil {
			fmt.Println(cloudXStyle.Render("✗ Failed"))
		} else {
			fmt.Println(cloudCheckStyle.Render("✓ Restarted"))
		}
	}

	fmt.Println()

	return nil
}

// Cloud config JSON output for machine parsing
type cloudConfigOutput struct {
	Server     string `json:"server"`
	SSHPort    string `json:"ssh_port"`
	TaskPort   string `json:"task_port"`
	RemoteUser string `json:"remote_user"`
	RemoteDir  string `json:"remote_dir"`
}

// getCloudConfigJSON returns the cloud configuration as JSON.
func getCloudConfigJSON() (string, error) {
	server, sshPort, taskPort, remoteUser, remoteDir, err := getCloudConfig()
	if err != nil {
		return "", err
	}

	config := cloudConfigOutput{
		Server:     server,
		SSHPort:    sshPort,
		TaskPort:   taskPort,
		RemoteUser: remoteUser,
		RemoteDir:  remoteDir,
	}

	jsonBytes, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}

	return string(jsonBytes), nil
}
