// task is the local CLI for managing tasks.
// It connects to a remote taskd server over SSH by default, or runs locally with -l flag.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"syscall"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
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

	// Create and run TUI
	model := ui.NewAppModel(database, exec)
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
