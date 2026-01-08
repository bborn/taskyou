// task is the local CLI for managing tasks.
// It connects to a remote taskd server over SSH by default, or runs locally with -l flag.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/mcp"
	"github.com/bborn/workflow/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/term"
)

var (
	// Default server configuration
	defaultHost = "cloud-claude"
	defaultPort = "2222"

	// Styles for CLI output
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	boldStyle    = lipgloss.NewStyle().Bold(true)
)

func main() {
	var (
		host  string
		port  string
		local bool
	)

	rootCmd := &cobra.Command{
		Use:   "task",
		Short: "Task queue manager",
		Long:  "A beautiful terminal UI for managing your task queue.",
		Run: func(cmd *cobra.Command, args []string) {
			if local {
				if err := runLocal(); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
					os.Exit(1)
				}
				return
			}
			// Connect to remote server
			if err := connectRemote(host, port); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Connection failed: "+err.Error()))
				os.Exit(1)
			}
		},
	}

	rootCmd.Flags().StringVarP(&host, "host", "H", defaultHost, "Remote server host")
	rootCmd.Flags().StringVarP(&port, "port", "p", defaultPort, "Remote server port")
	rootCmd.Flags().BoolVarP(&local, "local", "l", false, "Run locally (use local database)")

	// Daemon subcommand - runs executor in background
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the task executor daemon",
		Long:  "Runs the background executor that processes queued tasks.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := runDaemon(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}

	// Daemon stop subcommand
	daemonStopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		Run: func(cmd *cobra.Command, args []string) {
			if err := stopDaemon(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			fmt.Println(successStyle.Render("Daemon stopped"))
		},
	}
	daemonCmd.AddCommand(daemonStopCmd)

	// Daemon status subcommand
	daemonStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Check daemon status",
		Run: func(cmd *cobra.Command, args []string) {
			pidFile := getPidFilePath()
			if pid, err := readPidFile(pidFile); err == nil && processExists(pid) {
				fmt.Println(successStyle.Render(fmt.Sprintf("Daemon running (pid %d)", pid)))
			} else {
				fmt.Println(dimStyle.Render("Daemon not running"))
			}
		},
	}
	daemonCmd.AddCommand(daemonStatusCmd)

	rootCmd.AddCommand(daemonCmd)

	// Logs subcommand - tail claude session logs
	logsCmd := &cobra.Command{
		Use:   "logs",
		Short: "Tail claude session logs for debugging",
		Long:  "Streams all claude session logs across all projects in real-time.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := tailClaudeLogs(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	rootCmd.AddCommand(logsCmd)

	// MCP subcommand - run MCP server for Claude integration (internal use)
	mcpCmd := &cobra.Command{
		Use:    "mcp",
		Short:  "Run MCP server for Claude integration",
		Hidden: true, // Internal use only
		Run: func(cmd *cobra.Command, args []string) {
			taskID, _ := cmd.Flags().GetInt64("task")
			if taskID == 0 {
				fmt.Fprintln(os.Stderr, "Error: --task flag required")
				os.Exit(1)
			}
			if err := runMCPServer(taskID); err != nil {
				fmt.Fprintln(os.Stderr, "Error: "+err.Error())
				os.Exit(1)
			}
		},
	}
	mcpCmd.Flags().Int64("task", 0, "Task ID for this MCP server")
	rootCmd.AddCommand(mcpCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
}

// runLocal runs the TUI locally with a local SQLite database.
func runLocal() error {
	// Ensure daemon is running
	if err := ensureDaemonRunning(); err != nil {
		fmt.Fprintln(os.Stderr, dimStyle.Render("Warning: could not start daemon: "+err.Error()))
	}

	// Open database
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	fmt.Fprintln(os.Stderr, dimStyle.Render("Using local database: "+dbPath))

	// Load config from database
	cfg := config.New(database)

	// Create a non-running executor (just for status display)
	// The actual execution happens in the daemon process
	exec := executor.New(database, cfg)

	// Get current working directory for project detection
	cwd, _ := os.Getwd()

	// Create and run TUI
	model := ui.NewAppModel(database, exec, cwd)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}

	return nil
}

// ensureDaemonRunning starts the daemon if it's not already running.
func ensureDaemonRunning() error {
	pidFile := getPidFilePath()

	// Check if daemon is already running
	if pid, err := readPidFile(pidFile); err == nil {
		if processExists(pid) {
			return nil // Already running
		}
		// Stale pid file, remove it
		os.Remove(pidFile)
	}

	// Start daemon as background process
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable: %w", err)
	}

	cmd := exec.Command(executable, "daemon")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Detach from parent process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	// Write pid file
	if err := writePidFile(pidFile, cmd.Process.Pid); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}

	fmt.Fprintln(os.Stderr, dimStyle.Render(fmt.Sprintf("Started daemon (pid %d)", cmd.Process.Pid)))
	return nil
}

func getPidFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "task", "daemon.pid")
}

func readPidFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0, err
	}
	return pid, nil
}

func writePidFile(path string, pid int) error {
	return os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0644)
}

func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds, so we need to send signal 0
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

func stopDaemon() error {
	pidFile := getPidFilePath()
	pid, err := readPidFile(pidFile)
	if err != nil {
		return fmt.Errorf("daemon not running (no pid file)")
	}

	if !processExists(pid) {
		os.Remove(pidFile)
		return fmt.Errorf("daemon not running (stale pid file)")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send signal: %w", err)
	}

	os.Remove(pidFile)
	return nil
}

// runDaemon runs the background executor that processes queued tasks.
func runDaemon() error {
	pidFile := getPidFilePath()

	// Acquire exclusive lock on PID file to prevent duplicates
	lockFile, err := os.OpenFile(pidFile+".lock", os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("daemon already running (could not acquire lock)")
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	// Write our PID file
	if err := writePidFile(pidFile, os.Getpid()); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	defer os.Remove(pidFile)

	// Setup logger
	logger := log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: true,
		Prefix:          "task-daemon",
	})

	// Open database
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	logger.Info("Database opened", "path", dbPath)

	// Load config from database
	cfg := config.New(database)

	// Create executor with logging
	exec := executor.NewWithLogging(database, cfg, os.Stderr)

	// Start background executor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec.Start(ctx)

	logger.Info("Executor started, waiting for tasks...")

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Wait for signal
	sig := <-sigCh
	logger.Info("Received signal, shutting down", "signal", sig)
	exec.Stop()

	return nil
}

func connectRemote(host, port string) error {
	// Get SSH auth methods
	authMethods, err := getAuthMethods()
	if err != nil {
		return fmt.Errorf("auth setup: %w", err)
	}

	// Get current user
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	username := currentUser.Username

	config := &ssh.ClientConfig{
		User:            username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: proper host key verification
	}

	// Connect
	addr := net.JoinHostPort(host, port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer client.Close()

	// Create session
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("session: %w", err)
	}
	defer session.Close()

	// Set up terminal
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		// Get terminal size
		width, height, err := term.GetSize(fd)
		if err != nil {
			width, height = 80, 24
		}

		// Set raw mode
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("raw mode: %w", err)
		}
		defer term.Restore(fd, oldState)

		// Request PTY
		modes := ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}
		if err := session.RequestPty("xterm-256color", height, width, modes); err != nil {
			return fmt.Errorf("pty: %w", err)
		}
	}

	// Connect I/O
	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// Send current working directory for project detection
	if cwd, err := os.Getwd(); err == nil {
		session.Setenv("TASK_CWD", cwd)
	}

	// Start shell (taskd serves TUI directly)
	if err := session.Shell(); err != nil {
		return fmt.Errorf("shell: %w", err)
	}

	return session.Wait()
}

func getAuthMethods() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Try SSH agent first
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			agentClient := agent.NewClient(conn)
			signers, err := agentClient.Signers()
			if err == nil && len(signers) > 0 {
				methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))
			}
		}
	}

	// Try default key files
	home, _ := os.UserHomeDir()
	keyFiles := []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_rsa"),
	}

	for _, keyFile := range keyFiles {
		if key, err := loadPrivateKey(keyFile); err == nil {
			methods = append(methods, ssh.PublicKeys(key))
		}
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no SSH keys found (checked agent and ~/.ssh/id_*)")
	}

	return methods, nil
}

func loadPrivateKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}

// runMCPServer runs the MCP server for Claude integration.
// This is invoked by Claude as its MCP server for workflow tools.
func runMCPServer(taskID int64) error {
	// Open database
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Create and run MCP server
	server := mcp.NewServer(database, taskID)
	return server.Run()
}

// tailClaudeLogs tails all claude session logs for debugging.
func tailClaudeLogs() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	projectsDir := filepath.Join(home, ".claude", "projects")

	// Find all .jsonl files
	pattern := filepath.Join(projectsDir, "*", "*.jsonl")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob: %w", err)
	}

	if len(files) == 0 {
		return fmt.Errorf("no claude session files found in %s", projectsDir)
	}

	fmt.Fprintf(os.Stderr, "%s\n", dimStyle.Render(fmt.Sprintf("Watching %d session files...", len(files))))
	fmt.Fprintf(os.Stderr, "%s\n\n", dimStyle.Render("Press Ctrl+C to stop"))

	// Track file positions
	positions := make(map[string]int64)

	// Initialize positions to end of file (only show new content)
	for _, f := range files {
		info, err := os.Stat(f)
		if err == nil {
			positions[f] = info.Size()
		}
	}

	// Handle interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			fmt.Println()
			return nil
		case <-ticker.C:
			// Re-glob to catch new files
			files, _ = filepath.Glob(pattern)

			for _, f := range files {
				// Skip internal agent files (Claude Code sub-agents)
				if strings.HasPrefix(filepath.Base(f), "agent-") {
					continue
				}
				pos := positions[f]
				newPos, err := tailFile(f, pos)
				if err == nil {
					positions[f] = newPos
				}
			}
		}
	}
}

// tailFile reads new content from a file starting at the given position.
func tailFile(path string, pos int64) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return pos, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return pos, err
	}

	// File was truncated or rotated
	if info.Size() < pos {
		pos = 0
	}

	// No new content
	if info.Size() == pos {
		return pos, nil
	}

	// Seek to last position
	if _, err := file.Seek(pos, io.SeekStart); err != nil {
		return pos, err
	}

	// Extract project name from path for display
	project := filepath.Base(filepath.Dir(path))
	projectStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Parse JSON and extract useful content
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		// Format output based on entry type
		output := formatLogEntry(entry)
		if output != "" {
			fmt.Printf("%s %s\n", projectStyle.Render("["+project+"]"), output)
		}
	}

	return info.Size(), nil
}

// formatLogEntry formats a claude log entry for display.
// Only shows meaningful content: user input and claude's text responses.
// Tool calls and results are hidden - attach to tmux or view claude logs for details.
func formatLogEntry(entry map[string]interface{}) string {
	msgType, _ := entry["type"].(string)

	switch msgType {
	case "user":
		if msg, ok := entry["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].(string); ok {
				return boldStyle.Render("USER: ") + truncate(content, 200)
			}
		}
	case "assistant":
		if msg, ok := entry["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].([]interface{}); ok {
				// Only extract text blocks, skip tool calls
				var textParts []string
				for _, c := range content {
					if block, ok := c.(map[string]interface{}); ok {
						if text, ok := block["text"].(string); ok {
							text = strings.TrimSpace(text)
							if text != "" {
								textParts = append(textParts, text)
							}
						}
						// Skip tool_use blocks entirely - they're noise
					}
				}
				if len(textParts) > 0 {
					return successStyle.Render("CLAUDE: ") + truncate(strings.Join(textParts, " "), 200)
				}
			}
		}
	// Skip "result" type - tool results are noise for high-level view
	}

	return ""
}

// truncate shortens a string to maxLen, adding ellipsis if needed.
func truncate(s string, maxLen int) string {
	// Replace newlines with spaces for single-line display
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")

	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
