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

// GeminiExecutor implements TaskExecutor for Google's Gemini CLI.
type GeminiExecutor struct {
	executor       *Executor
	logger         *log.Logger
	suspendedTasks map[int64]time.Time
}

// NewGeminiExecutor creates a new Gemini executor.
func NewGeminiExecutor(e *Executor) *GeminiExecutor {
	return &GeminiExecutor{
		executor:       e,
		logger:         e.logger,
		suspendedTasks: make(map[int64]time.Time),
	}
}

// Name returns the executor name.
func (g *GeminiExecutor) Name() string {
	return db.ExecutorGemini
}

// IsAvailable checks if the gemini CLI is installed.
func (g *GeminiExecutor) IsAvailable() bool {
	_, err := exec.LookPath("gemini")
	return err == nil
}

// Execute runs a task using the Gemini CLI.
func (g *GeminiExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	return g.runGemini(ctx, task, workDir, prompt, "", false)
}

// Resume runs Gemini again with the full prompt plus feedback since sessions are stateless.
func (g *GeminiExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	return g.runGemini(ctx, task, workDir, prompt, feedback, true)
}

func (g *GeminiExecutor) runGemini(ctx context.Context, task *db.Task, workDir, prompt, feedback string, isResume bool) ExecResult {
	paths := g.executor.claudePathsForProject(task.Project)

	if !g.IsAvailable() {
		g.executor.logLine(task.ID, "error", "gemini CLI is not installed - see https://ai.google.dev/gemini-api/docs/cli")
		return ExecResult{Message: "gemini CLI is not installed"}
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		g.executor.logLine(task.ID, "error", "tmux is not installed - required for task execution")
		return ExecResult{Message: "tmux is not installed"}
	}

	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		g.logger.Error("could not create task-daemon session", "error", err)
		g.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux daemon: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux daemon: %s", err.Error())}
	}

	windowName := TmuxWindowName(task.ID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	killAllWindowsByNameAllSessions(windowName)

	promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
	if err != nil {
		g.logger.Error("could not create temp file", "error", err)
		g.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create temp file: %s", err.Error())}
	}
	fullPrompt := prompt
	if isResume && feedback != "" {
		fullPrompt = prompt + "\n\n## User Feedback\n\n" + feedback
	}
	promptFile.WriteString(fullPrompt)
	promptFile.Close()
	defer os.Remove(promptFile.Name())

	sessionID := os.Getenv("WORKTREE_SESSION_ID")
	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", os.Getpid())
	}

	envPrefix := claudeEnvPrefix(paths.configDir)
	dangerousFlag := buildGeminiDangerousFlag(task.DangerousMode)
	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sgemini %s"$(cat %q)"`,
		task.ID, sessionID, task.Port, task.WorktreePath, envPrefix, dangerousFlag, promptFile.Name())

	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script)
	if tmuxErr != nil {
		g.logger.Error("tmux new-window failed", "error", tmuxErr, "session", daemonSession)
		g.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux window: %s", tmuxErr.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux window: %s", tmuxErr.Error())}
	}

	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	time.Sleep(200 * time.Millisecond)

	if err := g.executor.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		g.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := g.executor.db.UpdateTaskWindowID(task.ID, windowID); err != nil {
			g.logger.Warn("failed to save window ID", "task", task.ID, "error", err)
		}
	}

	g.executor.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, paths.configDir)
	g.executor.configureTmuxWindow(windowTarget)

	result := g.executor.pollTmuxSession(ctx, task.ID, windowTarget)

	return ExecResult(result)
}

// GetProcessID returns the PID of the Gemini process for a task.
func (g *GeminiExecutor) GetProcessID(taskID int64) int {
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
		if strings.Contains(string(cmdOut), "gemini") {
			return pid
		}
		childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "gemini").Output()
		if err == nil && len(childOut) > 0 {
			childPid, err := strconv.Atoi(strings.TrimSpace(string(childOut)))
			if err == nil {
				return childPid
			}
		}
	}
	return 0
}

// Kill terminates the Gemini process for a task.
func (g *GeminiExecutor) Kill(taskID int64) bool {
	pid := g.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		g.logger.Debug("Failed to find Gemini process", "pid", pid, "error", err)
		return false
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		g.logger.Debug("Failed to terminate Gemini process", "pid", pid, "error", err)
		return false
	}
	g.logger.Info("Terminated Gemini process", "task", taskID, "pid", pid)
	delete(g.suspendedTasks, taskID)
	return true
}

// Suspend pauses the Gemini process for a task.
func (g *GeminiExecutor) Suspend(taskID int64) bool {
	pid := g.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		g.logger.Debug("Failed to find process", "pid", pid, "error", err)
		return false
	}
	if err := proc.Signal(syscall.SIGTSTP); err != nil {
		g.logger.Debug("Failed to suspend process", "pid", pid, "error", err)
		return false
	}
	g.suspendedTasks[taskID] = time.Now()
	g.logger.Info("Suspended Gemini process", "task", taskID, "pid", pid)
	g.executor.logLine(taskID, "system", "Gemini suspended (idle timeout)")
	return true
}

// IsSuspended reports whether the Gemini process is suspended for a task.
func (g *GeminiExecutor) IsSuspended(taskID int64) bool {
	_, suspended := g.suspendedTasks[taskID]
	return suspended
}

// ResumeProcess resumes a previously suspended Gemini process.
func (g *GeminiExecutor) ResumeProcess(taskID int64) bool {
	if !g.IsSuspended(taskID) {
		return false
	}
	pid := g.GetProcessID(taskID)
	if pid == 0 {
		delete(g.suspendedTasks, taskID)
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		delete(g.suspendedTasks, taskID)
		return false
	}
	if err := proc.Signal(syscall.SIGCONT); err != nil {
		g.logger.Debug("Failed to resume process", "pid", pid, "error", err)
		return false
	}
	delete(g.suspendedTasks, taskID)
	g.logger.Info("Resumed Gemini process", "task", taskID, "pid", pid)
	g.executor.logLine(taskID, "system", "Gemini resumed")
	return true
}

// BuildCommand returns the shell command to start an interactive Gemini session.
func (g *GeminiExecutor) BuildCommand(task *db.Task, sessionID, prompt string) string {
	dangerousFlag := buildGeminiDangerousFlag(task.DangerousMode)

	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	if prompt != "" {
		promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
		if err != nil {
			g.logger.Error("BuildCommand: failed to create temp file", "error", err)
			return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q gemini %s`,
				task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag)
		}
		promptFile.WriteString(prompt)
		promptFile.Close()
		return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q gemini %s"$(cat %q)"; rm -f %q`,
			task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag, promptFile.Name(), promptFile.Name())
	}

	return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q gemini %s`,
		task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag)
}

func buildGeminiDangerousFlag(enabled bool) string {
	useDanger := enabled || os.Getenv("WORKTREE_DANGEROUS_MODE") == "1"
	if !useDanger {
		return ""
	}
	flag := strings.TrimSpace(os.Getenv("GEMINI_DANGEROUS_ARGS"))
	if flag == "" {
		flag = "--dangerously-allow-run"
	}
	if !strings.HasSuffix(flag, " ") {
		flag += " "
	}
	return flag
}
