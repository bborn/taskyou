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

// OpenCodeExecutor implements TaskExecutor for OpenCode AI assistant.
// OpenCode is an open-source AI coding assistant that runs in terminal, IDE, or desktop.
// It supports multiple LLM providers including Claude, GPT, and Gemini.
// See: https://opencode.ai
//
// CLI Reference:
//   - opencode - Start interactive TUI session
//   - Supports @-symbol fuzzy search for file references
//   - Plan mode (Tab) for review before implementation
//   - /init - Analyze project and generate AGENTS.md
//   - /undo, /redo - Revert or restore changes
//   - /share - Generate shareable conversation links
type OpenCodeExecutor struct {
	executor       *Executor
	logger         *log.Logger
	suspendedTasks map[int64]time.Time
}

// NewOpenCodeExecutor creates a new OpenCode executor.
func NewOpenCodeExecutor(e *Executor) *OpenCodeExecutor {
	return &OpenCodeExecutor{
		executor:       e,
		logger:         e.logger,
		suspendedTasks: make(map[int64]time.Time),
	}
}

// Name returns the executor name.
func (o *OpenCodeExecutor) Name() string {
	return db.ExecutorOpenCode
}

// IsAvailable checks if the opencode CLI is installed.
func (o *OpenCodeExecutor) IsAvailable() bool {
	_, err := exec.LookPath("opencode")
	return err == nil
}

// Execute runs a task using the OpenCode CLI.
func (o *OpenCodeExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	return o.runOpenCode(ctx, task, workDir, prompt, "", false)
}

// Resume resumes a previous session with feedback.
// OpenCode doesn't have explicit session resumption, so we replay full prompt + feedback.
func (o *OpenCodeExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	return o.runOpenCode(ctx, task, workDir, prompt, feedback, true)
}

func (o *OpenCodeExecutor) runOpenCode(ctx context.Context, task *db.Task, workDir, prompt, feedback string, isResume bool) ExecResult {
	paths := o.executor.claudePathsForProject(task.Project)

	if !o.IsAvailable() {
		o.executor.logLine(task.ID, "error", "opencode CLI is not installed - run: npm install -g opencode-ai")
		return ExecResult{Message: "opencode CLI is not installed"}
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

	// Kill ALL existing windows with this name (handles duplicates)
	killAllWindowsByNameAllSessions(windowName)

	// Build the prompt content
	promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
	if err != nil {
		o.logger.Error("could not create temp file", "error", err)
		o.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create temp file: %s", err.Error())}
	}

	// Prepend working directory context - OpenCode needs to know where to work
	var fullPrompt strings.Builder
	fullPrompt.WriteString("## Working Directory\n\n")
	fullPrompt.WriteString(fmt.Sprintf("You are working in a git worktree at: `%s`\n\n", workDir))
	fullPrompt.WriteString("IMPORTANT: All file operations (reading, writing, creating files) MUST be done within this directory. ")
	fullPrompt.WriteString("Do NOT use your default workspace. Always use absolute paths or paths relative to this working directory.\n\n")
	fullPrompt.WriteString("---\n\n")
	fullPrompt.WriteString(prompt)
	if isResume && feedback != "" {
		fullPrompt.WriteString("\n\n## User Feedback\n\n")
		fullPrompt.WriteString(feedback)
	}
	promptFile.WriteString(fullPrompt.String())
	promptFile.Close()
	defer os.Remove(promptFile.Name())

	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	envPrefix := claudeEnvPrefix(paths.configDir)

	// Build OpenCode command
	// OpenCode CLI uses --prompt flag to pass initial prompt to the TUI
	// The positional [project] argument is for the working directory path, not the prompt
	// We start opencode in the working directory and pass the prompt via --prompt flag
	script := fmt.Sprintf(`cd %q && WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sopencode`,
		workDir, task.ID, worktreeSessionID, task.Port, task.WorktreePath, envPrefix)

	// If we have a prompt, pass it via --prompt flag
	if prompt != "" {
		script = fmt.Sprintf(`cd %q && WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sopencode --prompt "$(cat %q)"; rm -f %q`,
			workDir, task.ID, worktreeSessionID, task.Port, task.WorktreePath, envPrefix, promptFile.Name(), promptFile.Name())
	}

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

// GetProcessID returns the PID of the OpenCode process for a task.
func (o *OpenCodeExecutor) GetProcessID(taskID int64) int {
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
		if strings.Contains(string(cmdOut), "opencode") || strings.Contains(string(cmdOut), "node") {
			return pid
		}
		// Check child processes (opencode runs via node)
		childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "-f", "opencode").Output()
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

// Kill terminates the OpenCode process for a task.
func (o *OpenCodeExecutor) Kill(taskID int64) bool {
	pid := o.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		o.logger.Debug("Failed to find OpenCode process", "pid", pid, "error", err)
		return false
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		o.logger.Debug("Failed to terminate OpenCode process", "pid", pid, "error", err)
		return false
	}
	o.logger.Info("Terminated OpenCode process", "task", taskID, "pid", pid)
	delete(o.suspendedTasks, taskID)
	return true
}

// Suspend pauses the OpenCode process for a task.
func (o *OpenCodeExecutor) Suspend(taskID int64) bool {
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
	o.logger.Info("Suspended OpenCode process", "task", taskID, "pid", pid)
	o.executor.logLine(taskID, "system", "OpenCode suspended (idle timeout)")
	return true
}

// IsSuspended reports whether the OpenCode process is suspended for a task.
func (o *OpenCodeExecutor) IsSuspended(taskID int64) bool {
	_, suspended := o.suspendedTasks[taskID]
	return suspended
}

// ResumeProcess resumes a previously suspended OpenCode process.
func (o *OpenCodeExecutor) ResumeProcess(taskID int64) bool {
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
	o.logger.Info("Resumed OpenCode process", "task", taskID, "pid", pid)
	o.executor.logLine(taskID, "system", "OpenCode resumed")
	return true
}

// BuildCommand returns the shell command to start an interactive OpenCode session.
func (o *OpenCodeExecutor) BuildCommand(task *db.Task, sessionID, prompt string) string {
	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	envVars := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q`,
		task.ID, worktreeSessionID, task.Port, task.WorktreePath)

	if prompt != "" {
		promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
		if err != nil {
			o.logger.Error("BuildCommand: failed to create temp file", "error", err)
			return fmt.Sprintf(`%s opencode`, envVars)
		}
		promptFile.WriteString(prompt)
		promptFile.Close()
		return fmt.Sprintf(`%s opencode --prompt "$(cat %q)"; rm -f %q`,
			envVars, promptFile.Name(), promptFile.Name())
	}

	return fmt.Sprintf(`%s opencode`, envVars)
}

// ---- Session and Dangerous Mode Support ----

// SupportsSessionResume returns false - OpenCode doesn't have explicit session resume.
// Conversations can be shared via /share but not resumed from command line.
func (o *OpenCodeExecutor) SupportsSessionResume() bool {
	return false
}

// SupportsDangerousMode returns false - OpenCode does not currently have a dangerous mode flag.
// It runs in an interactive TUI and requires user interaction for actions.
func (o *OpenCodeExecutor) SupportsDangerousMode() bool {
	return false
}

// FindSessionID returns empty - OpenCode doesn't support session discovery.
func (o *OpenCodeExecutor) FindSessionID(workDir string) string {
	return ""
}

// ResumeDangerous is not supported for OpenCode as it doesn't have a dangerous mode flag.
func (o *OpenCodeExecutor) ResumeDangerous(task *db.Task, workDir string) bool {
	o.executor.logLine(task.ID, "system", "OpenCode does not support dangerous mode")
	return false
}

// ResumeSafe is not supported for OpenCode as it doesn't have a dangerous mode flag.
func (o *OpenCodeExecutor) ResumeSafe(task *db.Task, workDir string) bool {
	o.executor.logLine(task.ID, "system", "OpenCode does not support dangerous mode toggle")
	return false
}
