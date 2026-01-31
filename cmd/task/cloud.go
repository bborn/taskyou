package main

import (
	"bufio"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// Cloud settings keys
const (
	SettingCloudServer     = "cloud_server"      // e.g., "root@my-hetzner.com"
	SettingCloudSSHPort    = "cloud_ssh_port"    // e.g., "22"
	SettingCloudTaskPort   = "cloud_task_port"   // e.g., "2222"
	SettingCloudRemoteUser = "cloud_remote_user" // e.g., "runner"
	SettingCloudRemoteDir  = "cloud_remote_dir"  // e.g., "/home/runner"
)

// Default cloud settings
const (
	defaultCloudSSHPort    = "22"
	defaultCloudTaskPort   = "2222"
	defaultCloudRemoteUser = "runner"
	defaultCloudRemoteDir  = "/home/runner"
)

// Styles for cloud command output
var (
	cloudTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#61AFEF"))
	cloudHeaderStyle  = lipgloss.NewStyle().Bold(true)
	cloudCheckStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
	cloudPendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))
	cloudBoxStyle     = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#374151")).
				Padding(0, 1)
)

// sshRun executes a command on the remote server via SSH.
func sshRun(server, command string) (string, error) {
	cmd := osexec.Command("ssh", server, command)
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

// sshRunInteractive executes an interactive command on the remote server.
func sshRunInteractive(server, command string) error {
	cmd := osexec.Command("ssh", "-t", server, command)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// getCloudSettings retrieves all cloud settings from the database.
func getCloudSettings(database *db.DB) (map[string]string, error) {
	settings := make(map[string]string)

	server, _ := database.GetSetting(SettingCloudServer)
	settings[SettingCloudServer] = server

	sshPort, _ := database.GetSetting(SettingCloudSSHPort)
	if sshPort == "" {
		sshPort = defaultCloudSSHPort
	}
	settings[SettingCloudSSHPort] = sshPort

	taskPort, _ := database.GetSetting(SettingCloudTaskPort)
	if taskPort == "" {
		taskPort = defaultCloudTaskPort
	}
	settings[SettingCloudTaskPort] = taskPort

	remoteUser, _ := database.GetSetting(SettingCloudRemoteUser)
	if remoteUser == "" {
		remoteUser = defaultCloudRemoteUser
	}
	settings[SettingCloudRemoteUser] = remoteUser

	remoteDir, _ := database.GetSetting(SettingCloudRemoteDir)
	if remoteDir == "" {
		remoteDir = defaultCloudRemoteDir
	}
	settings[SettingCloudRemoteDir] = remoteDir

	return settings, nil
}

// prompt reads a line of input from the user.
func prompt(label string, defaultValue string) string {
	if defaultValue != "" {
		fmt.Printf("%s [%s]: ", label, defaultValue)
	} else {
		fmt.Printf("%s: ", label)
	}

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		return defaultValue
	}
	return input
}

// waitForEnter waits for the user to press Enter.
func waitForEnter(message string) {
	if message == "" {
		message = "Press Enter when done"
	}
	fmt.Printf("\n[%s]\n", message)
	reader := bufio.NewReader(os.Stdin)
	reader.ReadString('\n')
}

// expandPath expands ~ to the user's home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// getGitRemote returns the git remote URL for a directory.
func getGitRemote(path string) (string, error) {
	path = expandPath(path)
	cmd := osexec.Command("git", "-C", path, "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// createCloudCommand creates the cloud subcommand with all its children.
func createCloudCommand() *cobra.Command {
	cloudCmd := &cobra.Command{
		Use:   "cloud",
		Short: "Manage cloud server deployment",
		Long: `Cloud server management for running tasks remotely.

Commands:
  init    - Set up a new cloud server
  status  - Check cloud server status
  logs    - Stream cloud server logs
  sync    - Sync projects and optionally redeploy
  push    - Push local database to cloud (for shifting to cloud work)
  pull    - Pull cloud database to local (for returning to local work)`,
		Run: func(cmd *cobra.Command, args []string) {
			// Show current cloud status by default
			showCloudStatus()
		},
	}

	// cloud init
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Interactive wizard to set up a remote server",
		Long: `Set up a new cloud server for running tasks remotely.

This wizard will:
1. Connect to your server and install dependencies
2. Set up GitHub SSH access
3. Configure Claude authentication
4. Clone your projects
5. Deploy the taskd service`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runCloudInit(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	cloudCmd.AddCommand(initCmd)

	// cloud status
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Check cloud server status",
		Run: func(cmd *cobra.Command, args []string) {
			showCloudStatus()
		},
	}
	cloudCmd.AddCommand(statusCmd)

	// cloud logs
	logsCmd := &cobra.Command{
		Use:   "logs",
		Short: "Stream cloud server logs",
		Run: func(cmd *cobra.Command, args []string) {
			if err := streamCloudLogs(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	cloudCmd.AddCommand(logsCmd)

	// cloud sync
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync projects and optionally redeploy",
		Run: func(cmd *cobra.Command, args []string) {
			deploy, _ := cmd.Flags().GetBool("deploy")
			if err := syncCloud(deploy); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	syncCmd.Flags().BoolP("deploy", "d", false, "Also redeploy the binary")
	cloudCmd.AddCommand(syncCmd)

	// cloud push
	pushCmd := &cobra.Command{
		Use:   "push",
		Short: "Push local database to cloud server",
		Long: `Push the local task database to the cloud server.

This syncs your local task state to the cloud, allowing you to continue
working on your tasks remotely. The cloud database will be replaced with
your local database (a backup is created first).

Use this when you want to shift from local work to cloud work.`,
		Run: func(cmd *cobra.Command, args []string) {
			force, _ := cmd.Flags().GetBool("force")
			if err := pushToCloud(force); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	pushCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	cloudCmd.AddCommand(pushCmd)

	// cloud pull
	pullCmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull database from cloud server to local",
		Long: `Pull the task database from the cloud server to local.

This syncs the cloud task state to your local machine, allowing you to
continue working on tasks locally after remote work. Your local database
will be replaced with the cloud database (a backup is created first).

Use this when you want to shift from cloud work back to local work.`,
		Run: func(cmd *cobra.Command, args []string) {
			force, _ := cmd.Flags().GetBool("force")
			if err := pullFromCloud(force); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	pullCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	cloudCmd.AddCommand(pullCmd)

	return cloudCmd
}

// showCloudStatus displays the current cloud configuration and status.
func showCloudStatus() {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	settings, _ := getCloudSettings(database)
	server := settings[SettingCloudServer]

	if server == "" {
		fmt.Println(dimStyle.Render("No cloud server configured."))
		fmt.Println(dimStyle.Render("Run 'task cloud init' to set up a server."))
		return
	}

	fmt.Println(cloudTitleStyle.Render("Cloud Status"))
	fmt.Println(strings.Repeat("─", 40))
	fmt.Printf("Server:    %s\n", server)
	fmt.Printf("SSH Port:  %s\n", settings[SettingCloudSSHPort])
	fmt.Printf("Task Port: %s\n", settings[SettingCloudTaskPort])
	fmt.Printf("User:      %s\n", settings[SettingCloudRemoteUser])
	fmt.Printf("Directory: %s\n", settings[SettingCloudRemoteDir])
	fmt.Println()

	// Check if server is reachable
	fmt.Print("Checking server... ")
	if _, err := sshRun(server, "echo ok"); err != nil {
		fmt.Println(errorStyle.Render("unreachable"))
		return
	}
	fmt.Println(cloudCheckStyle.Render("reachable"))

	// Check systemd service status
	fmt.Print("Service status... ")
	output, err := sshRun(server, "systemctl is-active taskd 2>/dev/null || echo inactive")
	if err != nil || output != "active" {
		fmt.Println(cloudPendingStyle.Render(output))
	} else {
		fmt.Println(cloudCheckStyle.Render("active"))
	}

	// Show task counts
	fmt.Print("Tasks... ")
	output, err = sshRun(server, fmt.Sprintf(
		"cd %s && ./taskd list --json 2>/dev/null | jq -r 'length' 2>/dev/null || echo 'N/A'",
		settings[SettingCloudRemoteDir],
	))
	if err != nil || output == "N/A" {
		fmt.Println(dimStyle.Render("N/A"))
	} else {
		fmt.Printf("%s tasks\n", output)
	}

	fmt.Println()
	fmt.Printf("Connect: %s\n", boldStyle.Render(fmt.Sprintf("ssh -p %s %s", settings[SettingCloudTaskPort], extractHost(server))))
}

// extractHost extracts the hostname from a server string like "user@host".
func extractHost(server string) string {
	if idx := strings.Index(server, "@"); idx != -1 {
		return server[idx+1:]
	}
	return server
}

// runCloudInit runs the interactive cloud setup wizard.
func runCloudInit() error {
	fmt.Println()
	fmt.Println(cloudTitleStyle.Render("Cloud Setup"))
	fmt.Println(strings.Repeat("─", 40))
	fmt.Println()

	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Step 1: Server Connection
	fmt.Println(cloudHeaderStyle.Render("1. Server Connection"))

	existingServer, _ := database.GetSetting(SettingCloudServer)
	server := prompt("Enter server address (e.g., root@my-hetzner.com)", existingServer)
	if server == "" {
		return fmt.Errorf("server address is required")
	}

	// Test SSH connection
	fmt.Print("   Connecting... ")
	if _, err := sshRun(server, "echo ok"); err != nil {
		fmt.Println(errorStyle.Render("failed"))
		return fmt.Errorf("could not connect to server: %w", err)
	}
	fmt.Println(cloudCheckStyle.Render("Connected"))

	// Install dependencies
	fmt.Print("   Installing dependencies... ")
	if _, err := sshRun(server, "apt update -qq && apt install -y -qq tmux git curl jq golang-go make"); err != nil {
		fmt.Println(errorStyle.Render("failed"))
		return fmt.Errorf("could not install dependencies: %w", err)
	}
	fmt.Println(cloudCheckStyle.Render("Done"))

	// Create runner user if needed
	fmt.Print("   Setting up runner user... ")
	sshRun(server, "id -u runner >/dev/null 2>&1 || useradd -m -s /bin/bash runner")
	fmt.Println(cloudCheckStyle.Render("Done"))

	// Save server setting
	database.SetSetting(SettingCloudServer, server)
	fmt.Println()

	// Step 2: GitHub Access
	fmt.Println(cloudHeaderStyle.Render("2. GitHub Access"))

	// Check if SSH key already exists
	output, _ := sshRun(server, "sudo -u runner cat /home/runner/.ssh/id_ed25519.pub 2>/dev/null")
	if output == "" {
		fmt.Print("   Generating SSH key... ")
		_, err := sshRun(server, "sudo -u runner mkdir -p /home/runner/.ssh && sudo -u runner ssh-keygen -t ed25519 -f /home/runner/.ssh/id_ed25519 -N '' -q")
		if err != nil {
			fmt.Println(errorStyle.Render("failed"))
			return fmt.Errorf("could not generate SSH key: %w", err)
		}
		fmt.Println(cloudCheckStyle.Render("Done"))
		output, _ = sshRun(server, "sudo -u runner cat /home/runner/.ssh/id_ed25519.pub")
	} else {
		fmt.Println("   SSH key already exists")
	}

	fmt.Println()
	fmt.Println("   Add this key to GitHub -> Settings -> SSH Keys:")
	fmt.Println()
	fmt.Println(cloudBoxStyle.Render(output))
	fmt.Println()

	waitForEnter("Press Enter when you've added the key to GitHub")

	// Test GitHub access
	fmt.Print("   Testing GitHub access... ")
	_, err = sshRun(server, "sudo -u runner ssh -o StrictHostKeyChecking=accept-new -T git@github.com 2>&1 || true")
	if err != nil {
		// The command "fails" with exit 1 even on success, so we check the output
	}
	// Verify by trying a simple git operation
	testOutput, _ := sshRun(server, "sudo -u runner ssh -o BatchMode=yes -T git@github.com 2>&1")
	if strings.Contains(testOutput, "successfully authenticated") || strings.Contains(testOutput, "You've successfully") {
		fmt.Println(cloudCheckStyle.Render("Verified"))
	} else {
		fmt.Println(cloudPendingStyle.Render("Could not verify (may still work)"))
	}
	fmt.Println()

	// Step 3: Projects
	fmt.Println(cloudHeaderStyle.Render("3. Projects"))

	projects, err := database.ListProjects()
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}

	type projectInfo struct {
		name    string
		path    string
		remote  string
		clone   bool
		skipped string
	}

	var projectInfos []projectInfo
	for _, p := range projects {
		if p.Name == "personal" {
			continue // Skip personal project (no git repo)
		}

		remote, err := getGitRemote(p.Path)
		if err != nil || remote == "" {
			projectInfos = append(projectInfos, projectInfo{
				name:    p.Name,
				path:    p.Path,
				skipped: "no git remote",
			})
			continue
		}

		projectInfos = append(projectInfos, projectInfo{
			name:   p.Name,
			path:   p.Path,
			remote: remote,
			clone:  true, // Default to clone
		})
	}

	if len(projectInfos) == 0 {
		fmt.Println("   No projects with git remotes found.")
	} else {
		fmt.Println("   Projects to clone on server:")
		fmt.Println()
		for i, p := range projectInfos {
			if p.skipped != "" {
				fmt.Printf("   [ ] %s - %s\n", p.name, dimStyle.Render(p.skipped))
			} else {
				fmt.Printf("   [x] %s -> %s\n", p.name, dimStyle.Render(p.remote))
				projectInfos[i].clone = true
			}
		}
		fmt.Println()

		// Clone projects
		cloneCount := 0
		for _, p := range projectInfos {
			if !p.clone || p.skipped != "" {
				continue
			}

			fmt.Printf("   Cloning %s... ", p.name)
			clonePath := fmt.Sprintf("/home/runner/Projects/%s", p.name)

			// Check if already exists
			exists, _ := sshRun(server, fmt.Sprintf("test -d %s && echo yes || echo no", clonePath))
			if exists == "yes" {
				fmt.Println(dimStyle.Render("already exists"))
				cloneCount++
				continue
			}

			// Clone the repository
			_, err := sshRun(server, fmt.Sprintf(
				"sudo -u runner mkdir -p /home/runner/Projects && sudo -u runner git clone '%s' '%s'",
				p.remote, clonePath,
			))
			if err != nil {
				fmt.Println(errorStyle.Render("failed"))
			} else {
				fmt.Println(cloudCheckStyle.Render("Done"))
				cloneCount++
			}
		}

		if cloneCount > 0 {
			fmt.Printf("   %s Cloned %d project(s)\n", cloudCheckStyle.Render(""), cloneCount)
		}
	}
	fmt.Println()

	// Step 4: Deploy
	fmt.Println(cloudHeaderStyle.Render("4. Deploy"))

	remoteDir := defaultCloudRemoteDir
	remoteUser := defaultCloudRemoteUser
	workflowDir := "/home/runner/Projects/tasks"

	// Pull latest and build on server
	fmt.Print("   Pulling latest code... ")
	if _, err := sshRun(server, fmt.Sprintf("cd %s && sudo -u %s git pull --ff-only", workflowDir, remoteUser)); err != nil {
		fmt.Println(errorStyle.Render("failed"))
		return fmt.Errorf("git pull failed: %w", err)
	}
	fmt.Println(cloudCheckStyle.Render("Done"))

	fmt.Print("   Building on server... ")
	if _, err := sshRun(server, fmt.Sprintf("cd %s && sudo -u %s make build", workflowDir, remoteUser)); err != nil {
		fmt.Println(errorStyle.Render("failed"))
		return fmt.Errorf("build failed: %w", err)
	}
	fmt.Println(cloudCheckStyle.Render("Done"))

	fmt.Print("   Installing binary... ")
	sshRun(server, fmt.Sprintf("cp %s/bin/taskd %s/taskd && chmod +x %s/taskd && chown %s:%s %s/taskd", workflowDir, remoteDir, remoteDir, remoteUser, remoteUser, remoteDir))
	fmt.Println(cloudCheckStyle.Render("Done"))

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
WantedBy=multi-user.target
`, remoteDir, remoteDir, remoteUser, remoteDir)

	// Write service file
	sshRun(server, fmt.Sprintf("cat > /etc/systemd/system/taskd.service << 'EOF'\n%sEOF", serviceContent))
	sshRun(server, "systemctl daemon-reload && systemctl enable taskd")
	fmt.Println(cloudCheckStyle.Render("Done"))

	// Start service
	fmt.Print("   Starting service... ")
	sshRun(server, "systemctl restart taskd")
	fmt.Println(cloudCheckStyle.Render("Done"))

	// Save settings
	database.SetSetting(SettingCloudSSHPort, defaultCloudSSHPort)
	database.SetSetting(SettingCloudTaskPort, defaultCloudTaskPort)
	database.SetSetting(SettingCloudRemoteUser, remoteUser)
	database.SetSetting(SettingCloudRemoteDir, remoteDir)
	fmt.Println()

	// Step 5: Claude Authentication
	fmt.Println(cloudHeaderStyle.Render("5. Claude Authentication"))
	fmt.Println()
	fmt.Println("   SSH to the server and authenticate Claude as the runner user:")
	fmt.Println()
	fmt.Println(cloudBoxStyle.Render(fmt.Sprintf("ssh %s\nsudo -iu runner\nclaude", server)))

	waitForEnter("Press Enter when authentication is complete")

	fmt.Println()
	fmt.Println(strings.Repeat("─", 40))
	fmt.Printf("%s Ready! Connect: %s\n",
		cloudTitleStyle.Render(""),
		boldStyle.Render(fmt.Sprintf("ssh -p %s %s", defaultCloudTaskPort, extractHost(server))))

	return nil
}

// streamCloudLogs streams logs from the cloud server.
func streamCloudLogs() error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	settings, _ := getCloudSettings(database)
	server := settings[SettingCloudServer]

	if server == "" {
		return fmt.Errorf("no cloud server configured. Run 'task cloud init' first")
	}

	fmt.Println(dimStyle.Render("Streaming logs from cloud server... (Ctrl+C to stop)"))
	fmt.Println()

	return sshRunInteractive(server, "journalctl -u taskd -f")
}

// syncCloud syncs projects on the cloud server.
func syncCloud(deploy bool) error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	settings, _ := getCloudSettings(database)
	server := settings[SettingCloudServer]

	if server == "" {
		return fmt.Errorf("no cloud server configured. Run 'task cloud init' first")
	}

	fmt.Println(cloudTitleStyle.Render("Cloud Sync"))
	fmt.Println(strings.Repeat("─", 40))
	fmt.Println()

	// Git pull on all projects
	fmt.Println(cloudHeaderStyle.Render("Syncing projects..."))

	projects, err := database.ListProjects()
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}

	for _, p := range projects {
		if p.Name == "personal" {
			continue
		}

		projectPath := fmt.Sprintf("/home/runner/Projects/%s", p.Name)

		// Check if project exists on server
		exists, _ := sshRun(server, fmt.Sprintf("test -d %s && echo yes || echo no", projectPath))
		if exists != "yes" {
			fmt.Printf("  %s - %s\n", p.Name, dimStyle.Render("not found on server"))
			continue
		}

		fmt.Printf("  %s... ", p.Name)
		output, err := sshRun(server, fmt.Sprintf("cd %s && sudo -u runner git pull --ff-only 2>&1", projectPath))
		if err != nil {
			fmt.Println(errorStyle.Render("failed"))
			fmt.Printf("    %s\n", dimStyle.Render(output))
		} else if strings.Contains(output, "Already up to date") {
			fmt.Println(dimStyle.Render("up to date"))
		} else {
			fmt.Println(cloudCheckStyle.Render("updated"))
		}
	}

	// Optionally redeploy binary
	if deploy {
		fmt.Println()
		fmt.Println(cloudHeaderStyle.Render("Redeploying binary..."))

		remoteUser := settings[SettingCloudRemoteUser]
		remoteDir := settings[SettingCloudRemoteDir]
		workflowDir := "/home/runner/Projects/tasks"

		// Build on server
		fmt.Print("  Building on server... ")
		if _, err := sshRun(server, fmt.Sprintf("cd %s && sudo -u %s make build", workflowDir, remoteUser)); err != nil {
			fmt.Println(errorStyle.Render("failed"))
			return fmt.Errorf("build failed: %w", err)
		}
		fmt.Println(cloudCheckStyle.Render("Done"))

		// Install binary
		fmt.Print("  Installing... ")
		sshRun(server, fmt.Sprintf("cp %s/bin/taskd %s/taskd && chmod +x %s/taskd", workflowDir, remoteDir, remoteDir))
		fmt.Println(cloudCheckStyle.Render("Done"))

		// Restart service
		fmt.Print("  Restarting service... ")
		sshRun(server, "systemctl restart taskd")
		fmt.Println(cloudCheckStyle.Render("Done"))
	}

	fmt.Println()
	fmt.Println(successStyle.Render("Sync complete!"))

	return nil
}

// getProjectRoot returns the root directory of this project.
func getProjectRoot() string {
	// Try to find go.mod to locate project root
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Fall back to current directory
	cwd, _ := os.Getwd()
	return cwd
}

// pushToCloud pushes the local database to the cloud server.
func pushToCloud(force bool) error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	settings, _ := getCloudSettings(database)
	server := settings[SettingCloudServer]
	remoteUser := settings[SettingCloudRemoteUser]
	remoteDir := settings[SettingCloudRemoteDir]

	if server == "" {
		return fmt.Errorf("no cloud server configured. Run 'task cloud init' first")
	}

	fmt.Println(cloudTitleStyle.Render("Push to Cloud"))
	fmt.Println(strings.Repeat("─", 40))
	fmt.Println()

	// Get local task count for confirmation
	localTasks, err := database.ListTasks(db.ListTasksOptions{IncludeClosed: true})
	if err != nil {
		return fmt.Errorf("list local tasks: %w", err)
	}

	// Get remote task count
	fmt.Print("Checking remote state... ")
	remoteDBPath := fmt.Sprintf("%s/.local/share/task/tasks.db", remoteDir)
	output, _ := sshRun(server, fmt.Sprintf("sudo -u %s sqlite3 '%s' 'SELECT COUNT(*) FROM tasks' 2>/dev/null || echo 0", remoteUser, remoteDBPath))
	remoteCount := strings.TrimSpace(output)
	fmt.Println(cloudCheckStyle.Render("done"))

	fmt.Println()
	fmt.Printf("Local tasks:  %d\n", len(localTasks))
	fmt.Printf("Remote tasks: %s\n", remoteCount)
	fmt.Println()

	if !force {
		fmt.Println(cloudPendingStyle.Render("⚠ This will replace the remote database with your local database."))
		confirm := prompt("Continue? (y/N)", "")
		if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	fmt.Println()

	// Create backup on remote server
	fmt.Print("Creating remote backup... ")
	backupPath := fmt.Sprintf("%s/.local/share/task/tasks.db.backup.%d", remoteDir, time.Now().Unix())
	_, err = sshRun(server, fmt.Sprintf("sudo -u %s cp -f '%s' '%s' 2>/dev/null || true", remoteUser, remoteDBPath, backupPath))
	if err == nil {
		fmt.Println(cloudCheckStyle.Render("done"))
	} else {
		fmt.Println(dimStyle.Render("skipped (no existing database)"))
	}

	// Ensure remote directory exists
	fmt.Print("Preparing remote directory... ")
	_, err = sshRun(server, fmt.Sprintf("sudo -u %s mkdir -p '%s/.local/share/task'", remoteUser, remoteDir))
	if err != nil {
		fmt.Println(errorStyle.Render("failed"))
		return fmt.Errorf("create remote directory: %w", err)
	}
	fmt.Println(cloudCheckStyle.Render("done"))

	// Stop the remote taskd service to release database locks
	fmt.Print("Stopping remote service... ")
	sshRun(server, "systemctl stop taskd 2>/dev/null || true")
	fmt.Println(cloudCheckStyle.Render("done"))

	// Copy the database file using scp
	fmt.Print("Uploading database... ")
	cmd := osexec.Command("scp", dbPath, fmt.Sprintf("%s:/tmp/tasks.db.upload", server))
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Println(errorStyle.Render("failed"))
		return fmt.Errorf("upload database: %w\n%s", err, output)
	}

	// Move to final location with correct ownership
	_, err = sshRun(server, fmt.Sprintf("mv /tmp/tasks.db.upload '%s' && chown %s:%s '%s'", remoteDBPath, remoteUser, remoteUser, remoteDBPath))
	if err != nil {
		fmt.Println(errorStyle.Render("failed"))
		return fmt.Errorf("move database: %w", err)
	}
	fmt.Println(cloudCheckStyle.Render("done"))

	// Remove WAL and SHM files (they will be recreated)
	fmt.Print("Cleaning up... ")
	sshRun(server, fmt.Sprintf("rm -f '%s-wal' '%s-shm'", remoteDBPath, remoteDBPath))
	fmt.Println(cloudCheckStyle.Render("done"))

	// Clear execution-specific fields that don't apply to the remote environment
	fmt.Print("Resetting execution state... ")
	resetSQL := `UPDATE tasks SET worktree_path = '', port = 0, daemon_session = '', tmux_window_id = '', claude_pane_id = '', shell_pane_id = '', claude_session_id = '' WHERE 1=1`
	_, err = sshRun(server, fmt.Sprintf("sqlite3 '%s' \"%s\"", remoteDBPath, resetSQL))
	if err != nil {
		fmt.Println(cloudPendingStyle.Render("warning"))
	} else {
		fmt.Println(cloudCheckStyle.Render("done"))
	}

	// Update project paths to remote paths
	fmt.Print("Updating project paths... ")
	projects, err := database.ListProjects()
	if err == nil {
		// Build a single SQL statement to update all project paths
		var updates []string
		for _, p := range projects {
			var remotePath string
			if p.Name == "personal" {
				remotePath = fmt.Sprintf("%s/.local/share/task/personal", remoteDir)
			} else {
				remotePath = fmt.Sprintf("/home/runner/Projects/%s", p.Name)
			}
			updates = append(updates, fmt.Sprintf("UPDATE projects SET path = '%s' WHERE name = '%s';", remotePath, p.Name))
		}
		// Execute all updates in one SSH call
		if len(updates) > 0 {
			allSQL := strings.Join(updates, " ")
			_, err = sshRun(server, fmt.Sprintf("sqlite3 '%s' \"%s\"", remoteDBPath, allSQL))
			if err != nil {
				fmt.Println(cloudPendingStyle.Render("warning"))
			} else {
				fmt.Printf("%s (%d projects)\n", cloudCheckStyle.Render("done"), len(projects))
			}
		} else {
			fmt.Println(cloudCheckStyle.Render("done (no projects)"))
		}
	} else {
		fmt.Println(cloudPendingStyle.Render("warning"))
	}

	// Restart the taskd service
	fmt.Print("Starting remote service... ")
	sshRun(server, "systemctl start taskd")
	fmt.Println(cloudCheckStyle.Render("done"))

	fmt.Println()
	fmt.Println(successStyle.Render("✓ Database pushed to cloud successfully!"))
	fmt.Println()
	fmt.Printf("Connect: %s\n", boldStyle.Render(fmt.Sprintf("ssh -p %s %s", settings[SettingCloudTaskPort], extractHost(server))))

	return nil
}

// pullFromCloud pulls the database from the cloud server to local.
func pullFromCloud(force bool) error {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	settings, _ := getCloudSettings(database)
	server := settings[SettingCloudServer]
	remoteUser := settings[SettingCloudRemoteUser]
	remoteDir := settings[SettingCloudRemoteDir]

	if server == "" {
		return fmt.Errorf("no cloud server configured. Run 'task cloud init' first")
	}

	fmt.Println(cloudTitleStyle.Render("Pull from Cloud"))
	fmt.Println(strings.Repeat("─", 40))
	fmt.Println()

	// Get local task count
	localTasks, err := database.ListTasks(db.ListTasksOptions{IncludeClosed: true})
	if err != nil {
		return fmt.Errorf("list local tasks: %w", err)
	}

	// Get remote task count
	fmt.Print("Checking remote state... ")
	remoteDBPath := fmt.Sprintf("%s/.local/share/task/tasks.db", remoteDir)
	output, err := sshRun(server, fmt.Sprintf("sudo -u %s sqlite3 '%s' 'SELECT COUNT(*) FROM tasks' 2>/dev/null", remoteUser, remoteDBPath))
	if err != nil {
		fmt.Println(errorStyle.Render("failed"))
		return fmt.Errorf("no database found on remote server")
	}
	remoteCount := strings.TrimSpace(output)
	fmt.Println(cloudCheckStyle.Render("done"))

	fmt.Println()
	fmt.Printf("Local tasks:  %d\n", len(localTasks))
	fmt.Printf("Remote tasks: %s\n", remoteCount)
	fmt.Println()

	if !force {
		fmt.Println(cloudPendingStyle.Render("⚠ This will replace your local database with the remote database."))
		confirm := prompt("Continue? (y/N)", "")
		if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	// Save local project paths before replacing database
	localProjects, err := database.ListProjects()
	if err != nil {
		return fmt.Errorf("list local projects: %w", err)
	}
	localProjectPaths := make(map[string]string)
	for _, p := range localProjects {
		localProjectPaths[p.Name] = p.Path
	}

	fmt.Println()

	// Create local backup
	fmt.Print("Creating local backup... ")
	backupPath := fmt.Sprintf("%s.backup.%d", dbPath, time.Now().Unix())
	if err := copyFile(dbPath, backupPath); err != nil {
		fmt.Println(cloudPendingStyle.Render("warning"))
	} else {
		fmt.Println(cloudCheckStyle.Render("done"))
		fmt.Printf("  Backup: %s\n", dimStyle.Render(backupPath))
	}

	// Close the database before replacing it
	database.Close()

	// Stop the remote taskd service to release database locks
	fmt.Print("Stopping remote service... ")
	sshRun(server, "systemctl stop taskd 2>/dev/null || true")
	fmt.Println(cloudCheckStyle.Render("done"))

	// Download the database file using scp
	fmt.Print("Downloading database... ")
	tempPath := dbPath + ".download"
	cmd := osexec.Command("scp", fmt.Sprintf("%s:%s", server, remoteDBPath), tempPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Println(errorStyle.Render("failed"))
		// Restart remote service before returning
		sshRun(server, "systemctl start taskd")
		return fmt.Errorf("download database: %w\n%s", err, output)
	}
	fmt.Println(cloudCheckStyle.Render("done"))

	// Restart the remote service
	fmt.Print("Starting remote service... ")
	sshRun(server, "systemctl start taskd")
	fmt.Println(cloudCheckStyle.Render("done"))

	// Replace the local database
	fmt.Print("Replacing local database... ")
	// Remove WAL and SHM files first
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")
	if err := os.Rename(tempPath, dbPath); err != nil {
		fmt.Println(errorStyle.Render("failed"))
		return fmt.Errorf("replace database: %w", err)
	}
	fmt.Println(cloudCheckStyle.Render("done"))

	// Re-open and reset execution-specific fields
	fmt.Print("Resetting execution state... ")
	database, err = db.Open(dbPath)
	if err != nil {
		fmt.Println(errorStyle.Render("failed"))
		return fmt.Errorf("reopen database: %w", err)
	}
	defer database.Close()

	// Reset execution-specific fields
	_, err = database.Exec(`UPDATE tasks SET
		worktree_path = '',
		port = 0,
		daemon_session = '',
		tmux_window_id = '',
		claude_pane_id = '',
		shell_pane_id = '',
		claude_session_id = ''
		WHERE 1=1`)
	if err != nil {
		fmt.Println(cloudPendingStyle.Render("warning"))
	} else {
		fmt.Println(cloudCheckStyle.Render("done"))
	}

	// Restore cloud settings (they shouldn't be overwritten from remote)
	fmt.Print("Restoring cloud settings... ")
	database.SetSetting(SettingCloudServer, server)
	database.SetSetting(SettingCloudSSHPort, settings[SettingCloudSSHPort])
	database.SetSetting(SettingCloudTaskPort, settings[SettingCloudTaskPort])
	database.SetSetting(SettingCloudRemoteUser, remoteUser)
	database.SetSetting(SettingCloudRemoteDir, remoteDir)
	fmt.Println(cloudCheckStyle.Render("done"))

	// Restore local project paths
	fmt.Print("Restoring project paths... ")
	updateCount := 0
	for name, path := range localProjectPaths {
		_, err := database.Exec(`UPDATE projects SET path = ? WHERE name = ?`, path, name)
		if err == nil {
			updateCount++
		}
	}
	fmt.Printf("%s (%d projects)\n", cloudCheckStyle.Render("done"), updateCount)

	fmt.Println()
	fmt.Println(successStyle.Render("✓ Database pulled from cloud successfully!"))

	return nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
