package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/log"
)

// OpenClawExecutor implements TaskExecutor for OpenClaw AI assistant.
// OpenClaw is an open-source personal AI assistant that runs locally,
// capable of automating tasks across your digital life.
// See: https://openclaw.ai
//
// CLI Reference:
//   - openclaw agent --message "prompt" - Run a prompt
//   - openclaw agent --session-id <id> - Resume a session
//   - openclaw agent --thinking <level> - Set reasoning depth (low/medium/high)
//   - openclaw agent --local - Use embedded mode instead of Gateway
type OpenClawExecutor struct {
	executor       *Executor
	logger         *log.Logger
	suspendedTasks map[int64]time.Time
}

// NewOpenClawExecutor creates a new OpenClaw executor.
func NewOpenClawExecutor(e *Executor) *OpenClawExecutor {
	return &OpenClawExecutor{
		executor:       e,
		logger:         e.logger,
		suspendedTasks: make(map[int64]time.Time),
	}
}

// Name returns the executor name.
func (o *OpenClawExecutor) Name() string {
	return db.ExecutorOpenClaw
}

// IsAvailable checks if the openclaw CLI is installed.
func (o *OpenClawExecutor) IsAvailable() bool {
	_, err := exec.LookPath("openclaw")
	return err == nil
}

// Execute runs a task using the OpenClaw CLI.
func (o *OpenClawExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	return o.runOpenClaw(ctx, task, workDir, prompt, "", false)
}

// Resume resumes a previous session using OpenClaw's --session-id flag.
// If no session ID exists, starts fresh with the full prompt + feedback.
func (o *OpenClawExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	return o.runOpenClaw(ctx, task, workDir, prompt, feedback, true)
}

func (o *OpenClawExecutor) runOpenClaw(ctx context.Context, task *db.Task, workDir, prompt, feedback string, isResume bool) ExecResult {
	paths := o.executor.claudePathsForProject(task.Project)

	if !o.IsAvailable() {
		o.executor.logLine(task.ID, "error", "openclaw CLI is not installed - run: npm i -g openclaw@latest && openclaw onboard")
		return ExecResult{Message: "openclaw CLI is not installed"}
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		o.executor.logLine(task.ID, "error", "tmux is not installed - required for task execution")
		return ExecResult{Message: "tmux is not installed"}
	}

	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		o.logger.Error("could not create task-daemon session", "error", err)
		o.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux daemon: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux daemon: %s", err.Error())}
	}

	windowName := TmuxWindowName(task.ID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	killAllWindowsByNameAllSessions(windowName)

	// Build the prompt content
	promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
	if err != nil {
		o.logger.Error("could not create temp file", "error", err)
		o.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create temp file: %s", err.Error())}
	}

	fullPrompt := prompt
	if isResume && feedback != "" {
		fullPrompt = prompt + "\n\n## User Feedback\n\n" + feedback
	}
	promptFile.WriteString(fullPrompt)
	promptFile.Close()
	defer os.Remove(promptFile.Name())

	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	envPrefix := claudeEnvPrefix(paths.configDir)

	// Build OpenClaw command with appropriate flags
	// Use --session-id for session continuity, --message for the prompt
	// Use --local for embedded mode (doesn't require Gateway)
	sessionFlag := ""
	if task.ClaudeSessionID != "" {
		// Resume existing session
		sessionFlag = fmt.Sprintf("--session-id %s ", task.ClaudeSessionID)
	} else {
		// Create new session ID based on task ID for future resumption
		newSessionID := fmt.Sprintf("task-%d", task.ID)
		sessionFlag = fmt.Sprintf("--session-id %s ", newSessionID)
		// Save session ID for future resumption
		if err := o.executor.db.UpdateTaskClaudeSessionID(task.ID, newSessionID); err != nil {
			o.logger.Warn("failed to save session ID", "task", task.ID, "error", err)
		}
	}

	thinkingFlag := buildOpenClawThinkingFlag()
	dangerousFlag := buildOpenClawDangerousFlag(task.DangerousMode)

	// openclaw agent --message "prompt" --session-id <id> --local --thinking <level>
	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sopenclaw agent %s%s%s--message "$(cat %q)"`,
		task.ID, worktreeSessionID, task.Port, task.WorktreePath, envPrefix, sessionFlag, thinkingFlag, dangerousFlag, promptFile.Name())

	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script)
	if tmuxErr != nil {
		o.logger.Error("tmux new-window failed", "error", tmuxErr, "session", daemonSession)
		o.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux window: %s", tmuxErr.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux window: %s", tmuxErr.Error())}
	}

	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	time.Sleep(200 * time.Millisecond)

	if err := o.executor.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		o.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := o.executor.db.UpdateTaskWindowID(task.ID, windowID); err != nil {
			o.logger.Warn("failed to save window ID", "task", task.ID, "error", err)
		}
	}

	o.executor.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, paths.configDir)
	o.executor.configureTmuxWindow(windowTarget)

	result := o.executor.pollTmuxSession(ctx, task.ID, windowTarget)

	return ExecResult(result)
}

// GetProcessID returns the PID of the OpenClaw process for a task.
func (o *OpenClawExecutor) GetProcessID(taskID int64) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	windowName := TmuxWindowName(taskID)

	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{session_name}:#{window_name}:#{pane_index} #{pane_pid}").Output()
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		target := parts[0]
		pidStr := parts[1]
		if !strings.Contains(target, windowName) {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		cmdOut, _ := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
		if strings.Contains(string(cmdOut), "openclaw") || strings.Contains(string(cmdOut), "node") {
			return pid
		}
		// Check child processes (openclaw runs via node)
		childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "-f", "openclaw").Output()
		if err == nil && len(childOut) > 0 {
			childPid, err := strconv.Atoi(strings.TrimSpace(string(childOut)))
			if err == nil {
				return childPid
			}
		}
		// Also check for node processes
		nodeOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "node").Output()
		if err == nil && len(nodeOut) > 0 {
			nodePid, err := strconv.Atoi(strings.TrimSpace(string(nodeOut)))
			if err == nil {
				return nodePid
			}
		}
	}
	return 0
}

// Kill terminates the OpenClaw process for a task.
func (o *OpenClawExecutor) Kill(taskID int64) bool {
	pid := o.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		o.logger.Debug("Failed to find OpenClaw process", "pid", pid, "error", err)
		return false
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		o.logger.Debug("Failed to terminate OpenClaw process", "pid", pid, "error", err)
		return false
	}
	o.logger.Info("Terminated OpenClaw process", "task", taskID, "pid", pid)
	delete(o.suspendedTasks, taskID)
	return true
}

// Suspend pauses the OpenClaw process for a task.
func (o *OpenClawExecutor) Suspend(taskID int64) bool {
	pid := o.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		o.logger.Debug("Failed to find process", "pid", pid, "error", err)
		return false
	}
	if err := proc.Signal(syscall.SIGTSTP); err != nil {
		o.logger.Debug("Failed to suspend process", "pid", pid, "error", err)
		return false
	}
	o.suspendedTasks[taskID] = time.Now()
	o.logger.Info("Suspended OpenClaw process", "task", taskID, "pid", pid)
	o.executor.logLine(taskID, "system", "OpenClaw suspended (idle timeout)")
	return true
}

// IsSuspended reports whether the OpenClaw process is suspended for a task.
func (o *OpenClawExecutor) IsSuspended(taskID int64) bool {
	_, suspended := o.suspendedTasks[taskID]
	return suspended
}

// ResumeProcess resumes a previously suspended OpenClaw process.
func (o *OpenClawExecutor) ResumeProcess(taskID int64) bool {
	if !o.IsSuspended(taskID) {
		return false
	}
	pid := o.GetProcessID(taskID)
	if pid == 0 {
		delete(o.suspendedTasks, taskID)
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		delete(o.suspendedTasks, taskID)
		return false
	}
	if err := proc.Signal(syscall.SIGCONT); err != nil {
		o.logger.Debug("Failed to resume process", "pid", pid, "error", err)
		return false
	}
	delete(o.suspendedTasks, taskID)
	o.logger.Info("Resumed OpenClaw process", "task", taskID, "pid", pid)
	o.executor.logLine(taskID, "system", "OpenClaw resumed")
	return true
}

// BuildCommand returns the shell command to start an interactive OpenClaw session.
func (o *OpenClawExecutor) BuildCommand(task *db.Task, sessionID, prompt string) string {
	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	// Build session flag - use existing session ID or create one from task ID
	sessionFlag := ""
	if sessionID != "" {
		sessionFlag = fmt.Sprintf("--session-id %s ", sessionID)
	} else if task.ClaudeSessionID != "" {
		sessionFlag = fmt.Sprintf("--session-id %s ", task.ClaudeSessionID)
	} else {
		sessionFlag = fmt.Sprintf("--session-id task-%d ", task.ID)
	}

	thinkingFlag := buildOpenClawThinkingFlag()
	dangerousFlag := buildOpenClawDangerousFlag(task.DangerousMode)

	envVars := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q`,
		task.ID, worktreeSessionID, task.Port, task.WorktreePath)

	if prompt != "" {
		promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
		if err != nil {
			o.logger.Error("BuildCommand: failed to create temp file", "error", err)
			return fmt.Sprintf(`%s openclaw agent %s%s%s`,
				envVars, sessionFlag, thinkingFlag, dangerousFlag)
		}
		promptFile.WriteString(prompt)
		promptFile.Close()
		return fmt.Sprintf(`%s openclaw agent %s%s%s--message "$(cat %q)"; rm -f %q`,
			envVars, sessionFlag, thinkingFlag, dangerousFlag, promptFile.Name(), promptFile.Name())
	}

	return fmt.Sprintf(`%s openclaw agent %s%s%s`,
		envVars, sessionFlag, thinkingFlag, dangerousFlag)
}

// buildOpenClawThinkingFlag returns the --thinking flag based on environment config.
func buildOpenClawThinkingFlag() string {
	level := strings.TrimSpace(os.Getenv("OPENCLAW_THINKING"))
	if level == "" {
		// Default to high thinking for complex tasks
		level = "high"
	}
	return fmt.Sprintf("--thinking %s ", level)
}

// buildOpenClawDangerousFlag returns flags for dangerous/auto-approve mode.
func buildOpenClawDangerousFlag(enabled bool) string {
	useDanger := enabled || os.Getenv("WORKTREE_DANGEROUS_MODE") == "1"
	if !useDanger {
		return ""
	}
	flag := strings.TrimSpace(os.Getenv("OPENCLAW_DANGEROUS_ARGS"))
	if flag == "" {
		// OpenClaw uses --local for embedded mode which auto-approves actions
		flag = "--local"
	}
	if !strings.HasSuffix(flag, " ") {
		flag += " "
	}
	return flag
}
