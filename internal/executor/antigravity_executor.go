package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"

	"github.com/bborn/workflow/internal/db"
)

// AntigravityExecutor implements TaskExecutor for Google's Antigravity CLI.
//
// Antigravity CLI ("agy") is Google's terminal-first coding agent and the
// official successor to the Gemini CLI, which Google deprecates on 2026-06-18.
// It shares the Antigravity 2.0 agent engine and is installed via:
//
//	curl -fsSL https://antigravity.google/cli/install.sh | bash
//
// The installer drops the binary at ~/.local/bin/agy.
//
// The Antigravity CLI is a TUI. It does not currently expose documented
// command-line flags for headless prompting, session resume, or auto-approve
// ("YOLO") mode — auto-approve is configured in-app via the /permissions
// command. To stay resilient to CLI changes while Google stabilizes the
// surface, the invocation flags are overridable via environment variables:
//
//	ANTIGRAVITY_BIN          - binary name/path (default: agy)
//	ANTIGRAVITY_PROMPT_FLAG  - flag used to pass the initial prompt (default: "-i ")
type AntigravityExecutor struct {
	executor       *Executor
	logger         *log.Logger
	suspendedTasks map[int64]time.Time
}

// NewAntigravityExecutor creates a new Antigravity executor.
func NewAntigravityExecutor(e *Executor) *AntigravityExecutor {
	return &AntigravityExecutor{
		executor:       e,
		logger:         e.logger,
		suspendedTasks: make(map[int64]time.Time),
	}
}

// Name returns the executor name.
func (a *AntigravityExecutor) Name() string {
	return db.ExecutorAntigravity
}

// antigravityBinary returns the configured Antigravity CLI binary name.
// Defaults to "agy" but can be overridden via ANTIGRAVITY_BIN.
func antigravityBinary() string {
	if bin := strings.TrimSpace(os.Getenv("ANTIGRAVITY_BIN")); bin != "" {
		return bin
	}
	return "agy"
}

// antigravityBinaryPath resolves the Antigravity CLI to an invokable path.
// It honors ANTIGRAVITY_BIN, then PATH, then falls back to the installer's
// default location (~/.local/bin/agy). If nothing is found it returns the
// bare binary name so callers still get a meaningful "not installed" error.
func antigravityBinaryPath() string {
	bin := antigravityBinary()
	if path, err := exec.LookPath(bin); err == nil {
		return path
	}
	// The install script (antigravity.google/cli/install.sh) installs to
	// ~/.local/bin/agy, which is not always on PATH for non-login shells.
	if home, err := os.UserHomeDir(); err == nil {
		fallback := filepath.Join(home, ".local", "bin", bin)
		if info, err := os.Stat(fallback); err == nil && !info.IsDir() {
			return fallback
		}
	}
	return bin
}

// buildAntigravityPromptFlag returns the flag prefix used to pass an initial
// prompt to the Antigravity CLI. Defaults to "-i " (interactive-with-prompt,
// matching the Gemini CLI it replaces) and is overridable via
// ANTIGRAVITY_PROMPT_FLAG so operators can adapt without a code change.
func buildAntigravityPromptFlag() string {
	flag := os.Getenv("ANTIGRAVITY_PROMPT_FLAG")
	if flag == "" {
		return "-i "
	}
	if !strings.HasSuffix(flag, " ") {
		flag += " "
	}
	return flag
}

// IsAvailable checks if the Antigravity CLI is installed.
func (a *AntigravityExecutor) IsAvailable() bool {
	bin := antigravityBinary()
	if _, err := exec.LookPath(bin); err == nil {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil {
		fallback := filepath.Join(home, ".local", "bin", bin)
		if info, err := os.Stat(fallback); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// Execute runs a task using the Antigravity CLI.
func (a *AntigravityExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	return a.runAntigravity(ctx, task, workDir, prompt, "", false)
}

// Resume reruns Antigravity with the full prompt plus feedback.
//
// The Antigravity CLI does not expose a documented session-resume flag, so
// retries replay the full prompt with appended feedback — the same stateless
// strategy the Codex and Gemini executors use when no session is available.
func (a *AntigravityExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	return a.runAntigravity(ctx, task, workDir, prompt, feedback, true)
}

func (a *AntigravityExecutor) runAntigravity(ctx context.Context, task *db.Task, workDir, prompt, feedback string, isResume bool) ExecResult {
	paths := a.executor.claudePathsForProject(task.Project)

	if !a.IsAvailable() {
		a.executor.logLine(task.ID, "error", "Antigravity CLI (agy) is not installed - run: curl -fsSL https://antigravity.google/cli/install.sh | bash")
		return ExecResult{Message: "Antigravity CLI is not installed"}
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		a.executor.logLine(task.ID, "error", "tmux is not installed - required for task execution")
		return ExecResult{Message: "tmux is not installed"}
	}

	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		a.logger.Error("could not create task-daemon session", "error", err)
		a.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux daemon: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux daemon: %s", err.Error())}
	}

	windowName := TmuxWindowName(task.ID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	// Kill ALL existing windows with this name (handles duplicates)
	KillAllWindowsByNameAllSessions(windowName)

	promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
	if err != nil {
		a.logger.Error("could not create temp file", "error", err)
		a.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create temp file: %s", err.Error())}
	}
	promptFile.WriteString(a.buildFullPrompt(prompt, feedback, isResume))
	promptFile.Close()
	defer os.Remove(promptFile.Name())

	sessionID := os.Getenv("WORKTREE_SESSION_ID")
	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", os.Getpid())
	}

	envPrefix := claudeEnvPrefix(paths.configDir)
	promptFlag := buildAntigravityPromptFlag()
	bin := antigravityBinaryPath()
	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %s%s %s"$(cat %q)"`,
		task.ID, sessionID, task.Port, task.WorktreePath, envPrefix, bin, promptFlag, promptFile.Name())

	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script, a.executor.getProjectDir(task.Project))
	if tmuxErr != nil {
		a.logger.Error("tmux new-window failed", "error", tmuxErr, "session", daemonSession)
		a.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux window: %s", tmuxErr.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux window: %s", tmuxErr.Error())}
	}

	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	time.Sleep(200 * time.Millisecond)

	if err := a.executor.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		a.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := a.executor.db.UpdateTaskWindowID(task.ID, windowID); err != nil {
			a.logger.Warn("failed to save window ID", "task", task.ID, "error", err)
		}
	}

	a.executor.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, paths.configDir)
	a.executor.configureTmuxWindow(windowTarget)

	result := a.executor.pollTmuxSession(ctx, task.ID, windowTarget)

	return ExecResult(result)
}

// buildFullPrompt assembles the prompt sent to Antigravity. Because the CLI
// has no separate system-prompt mechanism (unlike Claude's
// --append-system-prompt or Gemini's GEMINI.md), task guidance is appended to
// the end of the prompt — the same approach the OpenClaw executor uses.
func (a *AntigravityExecutor) buildFullPrompt(prompt, feedback string, isResume bool) string {
	var b strings.Builder
	b.WriteString(prompt)
	if isResume && feedback != "" {
		b.WriteString("\n\n## User Feedback\n\n")
		b.WriteString(feedback)
	}
	b.WriteString("\n\n")
	b.WriteString(a.executor.buildSystemInstructions())
	return b.String()
}

// GetProcessID returns the PID of the Antigravity process for a task.
func (a *AntigravityExecutor) GetProcessID(taskID int64) int {
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
		if strings.Contains(string(cmdOut), "agy") {
			return pid
		}
		childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "agy").Output()
		if err == nil && len(childOut) > 0 {
			childPid, err := strconv.Atoi(strings.TrimSpace(string(childOut)))
			if err == nil {
				return childPid
			}
		}
	}
	return 0
}

// Kill terminates the Antigravity process for a task.
func (a *AntigravityExecutor) Kill(taskID int64) bool {
	pid := a.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		a.logger.Debug("Failed to find Antigravity process", "pid", pid, "error", err)
		return false
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		a.logger.Debug("Failed to terminate Antigravity process", "pid", pid, "error", err)
		return false
	}
	a.logger.Info("Terminated Antigravity process", "task", taskID, "pid", pid)
	delete(a.suspendedTasks, taskID)
	return true
}

// Suspend pauses the Antigravity process for a task.
func (a *AntigravityExecutor) Suspend(taskID int64) bool {
	pid := a.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		a.logger.Debug("Failed to find process", "pid", pid, "error", err)
		return false
	}
	if err := sendSIGTSTP(proc); err != nil {
		a.logger.Debug("Failed to suspend process", "pid", pid, "error", err)
		return false
	}
	a.suspendedTasks[taskID] = time.Now()
	a.logger.Info("Suspended Antigravity process", "task", taskID, "pid", pid)
	a.executor.logLine(taskID, "system", "Antigravity suspended (idle timeout)")
	return true
}

// IsSuspended reports whether the Antigravity process is suspended for a task.
func (a *AntigravityExecutor) IsSuspended(taskID int64) bool {
	_, suspended := a.suspendedTasks[taskID]
	return suspended
}

// ResumeProcess resumes a previously suspended Antigravity process.
func (a *AntigravityExecutor) ResumeProcess(taskID int64) bool {
	if !a.IsSuspended(taskID) {
		return false
	}
	pid := a.GetProcessID(taskID)
	if pid == 0 {
		delete(a.suspendedTasks, taskID)
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		delete(a.suspendedTasks, taskID)
		return false
	}
	if err := sendSIGCONT(proc); err != nil {
		a.logger.Debug("Failed to resume process", "pid", pid, "error", err)
		return false
	}
	delete(a.suspendedTasks, taskID)
	a.logger.Info("Resumed Antigravity process", "task", taskID, "pid", pid)
	a.executor.logLine(taskID, "system", "Antigravity resumed")
	return true
}

// BuildCommand returns the shell command to start an interactive Antigravity session.
func (a *AntigravityExecutor) BuildCommand(task *db.Task, sessionID, prompt string) string {
	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	envVars := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q`,
		task.ID, worktreeSessionID, task.Port, task.WorktreePath)
	bin := antigravityBinaryPath()

	if prompt != "" {
		promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
		if err != nil {
			a.logger.Error("BuildCommand: failed to create temp file", "error", err)
			return fmt.Sprintf(`%s %s`, envVars, bin)
		}
		// Antigravity has no separate system-prompt mechanism, so task
		// guidance is appended to the prompt (see buildFullPrompt).
		promptFile.WriteString(a.buildFullPrompt(prompt, "", false))
		promptFile.Close()
		return fmt.Sprintf(`%s %s %s"$(cat %q)"; rm -f %q`,
			envVars, bin, buildAntigravityPromptFlag(), promptFile.Name(), promptFile.Name())
	}

	return fmt.Sprintf(`%s %s`, envVars, bin)
}

// ---- Session and Dangerous Mode Support ----

// SupportsSessionResume returns false - the Antigravity CLI does not expose a
// documented session-resume flag. Retries replay the full prompt instead.
func (a *AntigravityExecutor) SupportsSessionResume() bool {
	return false
}

// SupportsDangerousMode returns false - the Antigravity CLI has no command-line
// flag for auto-approve ("YOLO") mode. It is enabled in-app via /permissions.
func (a *AntigravityExecutor) SupportsDangerousMode() bool {
	return false
}

// FindSessionID returns an empty string - Antigravity sessions are not
// discoverable from disk in a documented, stable way.
func (a *AntigravityExecutor) FindSessionID(workDir string) string {
	return ""
}

// ResumeDangerous is not supported for Antigravity (no dangerous-mode flag).
func (a *AntigravityExecutor) ResumeDangerous(task *db.Task, workDir string) bool {
	a.executor.logLine(task.ID, "system", "Antigravity executor does not support dangerous mode - enable auto-approve in-app via /permissions")
	return false
}

// ResumeSafe is not supported for Antigravity (no dangerous-mode flag).
func (a *AntigravityExecutor) ResumeSafe(task *db.Task, workDir string) bool {
	a.executor.logLine(task.ID, "system", "Antigravity executor does not support dangerous mode toggle")
	return false
}
