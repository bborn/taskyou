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

	"github.com/charmbracelet/log"

	"github.com/bborn/workflow/internal/db"
)

// VibeExecutor implements TaskExecutor for Mistral Vibe.
type VibeExecutor struct {
	executor       *Executor
	logger         *log.Logger
	suspendedTasks map[int64]time.Time
}

// NewVibeExecutor creates a new Vibe executor.
func NewVibeExecutor(e *Executor) *VibeExecutor {
	return &VibeExecutor{
		executor:       e,
		logger:         e.logger,
		suspendedTasks: make(map[int64]time.Time),
	}
}

// Name returns the executor name.
func (v *VibeExecutor) Name() string {
	return db.ExecutorVibe
}

// IsAvailable checks if the vibe CLI is installed.
func (v *VibeExecutor) IsAvailable() bool {
	_, err := exec.LookPath("vibe")
	return err == nil
}

// Execute runs a task using the Vibe CLI.
func (v *VibeExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	return v.runVibe(ctx, task, workDir, prompt, "", false)
}

// Resume runs Vibe again with the full prompt plus feedback since sessions are stateless.
func (v *VibeExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	return v.runVibe(ctx, task, workDir, prompt, feedback, true)
}

func (v *VibeExecutor) runVibe(ctx context.Context, task *db.Task, workDir, prompt, feedback string, isResume bool) ExecResult {
	paths := v.executor.claudePathsForProject(task.Project)

	if !v.IsAvailable() {
		v.executor.logLine(task.ID, "error", "vibe CLI is not installed")
		return ExecResult{Message: "vibe CLI is not installed"}
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		v.executor.logLine(task.ID, "error", "tmux is not installed - required for task execution")
		return ExecResult{Message: "tmux is not installed"}
	}

	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		v.logger.Error("could not create task-daemon session", "error", err)
		v.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux daemon: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux daemon: %s", err.Error())}
	}

	windowName := TmuxWindowName(task.ID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	// Kill ALL existing windows with this name (handles duplicates)
	killAllWindowsByNameAllSessions(windowName)

	promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
	if err != nil {
		v.logger.Error("could not create temp file", "error", err)
		v.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
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
	dangerousFlag := buildVibeDangerousFlag(task.DangerousMode)
	// Vibe uses -p (--prompt) to pass initial prompt
	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %svibe %s-p "$(cat %q)"`,
		task.ID, sessionID, task.Port, task.WorktreePath, envPrefix, dangerousFlag, promptFile.Name())

	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script, v.executor.getProjectDir(task.Project))
	if tmuxErr != nil {
		v.logger.Error("tmux new-window failed", "error", tmuxErr, "session", daemonSession)
		v.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux window: %s", tmuxErr.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux window: %s", tmuxErr.Error())}
	}

	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	time.Sleep(200 * time.Millisecond)

	if err := v.executor.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		v.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := v.executor.db.UpdateTaskWindowID(task.ID, windowID); err != nil {
			v.logger.Warn("failed to save window ID", "task", task.ID, "error", err)
		}
	}

	v.executor.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, paths.configDir)
	v.executor.configureTmuxWindow(windowTarget)

	result := v.executor.pollTmuxSession(ctx, task.ID, windowTarget)

	return ExecResult(result)
}

// GetProcessID returns the PID of the Vibe process for a task.
func (v *VibeExecutor) GetProcessID(taskID int64) int {
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
		if strings.Contains(string(cmdOut), "vibe") {
			return pid
		}
		childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "vibe").Output()
		if err == nil && len(childOut) > 0 {
			childPid, err := strconv.Atoi(strings.TrimSpace(string(childOut)))
			if err == nil {
				return childPid
			}
		}
	}
	return 0
}

// Kill terminates the Vibe process for a task.
func (v *VibeExecutor) Kill(taskID int64) bool {
	pid := v.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		v.logger.Debug("Failed to find Vibe process", "pid", pid, "error", err)
		return false
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		v.logger.Debug("Failed to terminate Vibe process", "pid", pid, "error", err)
		return false
	}
	v.logger.Info("Terminated Vibe process", "task", taskID, "pid", pid)
	delete(v.suspendedTasks, taskID)
	return true
}

// Suspend pauses the Vibe process for a task.
func (v *VibeExecutor) Suspend(taskID int64) bool {
	pid := v.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		v.logger.Debug("Failed to find process", "pid", pid, "error", err)
		return false
	}
	if err := sendSIGTSTP(proc); err != nil {
		v.logger.Debug("Failed to suspend process", "pid", pid, "error", err)
		return false
	}
	v.suspendedTasks[taskID] = time.Now()
	v.logger.Info("Suspended Vibe process", "task", taskID, "pid", pid)
	v.executor.logLine(taskID, "system", "Vibe suspended (idle timeout)")
	return true
}

// IsSuspended reports whether the Vibe process is suspended for a task.
func (v *VibeExecutor) IsSuspended(taskID int64) bool {
	_, suspended := v.suspendedTasks[taskID]
	return suspended
}

// ResumeProcess resumes a previously suspended Vibe process.
func (v *VibeExecutor) ResumeProcess(taskID int64) bool {
	if !v.IsSuspended(taskID) {
		return false
	}
	pid := v.GetProcessID(taskID)
	if pid == 0 {
		delete(v.suspendedTasks, taskID)
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		delete(v.suspendedTasks, taskID)
		return false
	}
	if err := sendSIGCONT(proc); err != nil {
		v.logger.Debug("Failed to resume process", "pid", pid, "error", err)
		return false
	}
	delete(v.suspendedTasks, taskID)
	v.logger.Info("Resumed Vibe process", "task", taskID, "pid", pid)
	v.executor.logLine(taskID, "system", "Vibe resumed")
	return true
}

// BuildCommand returns the shell command to start an interactive Vibe session.
func (v *VibeExecutor) BuildCommand(task *db.Task, sessionID, prompt string) string {
	dangerousFlag := buildVibeDangerousFlag(task.DangerousMode)

	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	if prompt != "" {
		promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
		if err != nil {
			v.logger.Error("BuildCommand: failed to create temp file", "error", err)
			return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q vibe %s`,
				task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag)
		}
		promptFile.WriteString(prompt)
		promptFile.Close()
		// Use -p (--prompt) to pass initial prompt
		return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q vibe %s-p "$(cat %q)"; rm -f %q`,
				task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag, promptFile.Name(), promptFile.Name())
	}

	return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q vibe %s`,
		task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag)
}

func buildVibeDangerousFlag(enabled bool) string {
	useDanger := enabled || os.Getenv("WORKTREE_DANGEROUS_MODE") == "1"
	if !useDanger {
		return ""
	}
	flag := strings.TrimSpace(os.Getenv("VIBE_DANGEROUS_ARGS"))
	if flag == "" {
		flag = "--dangerous"
	}
	if !strings.HasSuffix(flag, " ") {
		flag += " "
	}
	return flag
}

// ---- Session and Dangerous Mode Support ---- 

// SupportsSessionResume returns false - Vibe doesn't support session resume.
func (v *VibeExecutor) SupportsSessionResume() bool {
	return false
}

// SupportsDangerousMode returns true - Vibe supports --dangerous flag.
func (v *VibeExecutor) SupportsDangerousMode() bool {
	return true
}

// FindSessionID is not supported for Vibe.
func (v *VibeExecutor) FindSessionID(workDir string) string {
	return ""
}

// ResumeDangerous kills the current Vibe process and restarts with --dangerous.
func (v *VibeExecutor) ResumeDangerous(task *db.Task, workDir string) bool {
	return v.executor.resumeVibeWithMode(task, workDir, true)
}

// ResumeSafe kills the current Vibe process and restarts without the dangerous flag.
func (v *VibeExecutor) ResumeSafe(task *db.Task, workDir string) bool {
	return v.executor.resumeVibeWithMode(task, workDir, false)
}