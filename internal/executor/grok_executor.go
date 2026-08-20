package executor

import (
	"context"
	"fmt"
	"net/url"
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

// GrokExecutor implements TaskExecutor for the Grok CLI (xAI / SpaceXAI).
// See: https://x.ai/cli/install.sh and grok --help
//
// CLI reference (grok --help):
//   - grok "prompt"              Interactive TUI with an initial prompt
//   - grok --always-approve      Auto-approve tool executions (dangerous mode)
//   - grok --permission-mode M   default | acceptEdits | auto | dontAsk | bypassPermissions | plan
//   - grok --resume [SESSION]    Resume a session by ID (or most recent)
//   - grok --continue            Continue the most recent session for this cwd
//
// TaskYou already creates an isolated git worktree per task, so we never pass
// grok's own --worktree flag (that would nest a second worktree).
type GrokExecutor struct {
	executor       *Executor
	logger         *log.Logger
	suspendedTasks map[int64]time.Time
}

// NewGrokExecutor creates a new Grok executor.
func NewGrokExecutor(e *Executor) *GrokExecutor {
	return &GrokExecutor{
		executor:       e,
		logger:         e.logger,
		suspendedTasks: make(map[int64]time.Time),
	}
}

// Name returns the executor name.
func (g *GrokExecutor) Name() string {
	return db.ExecutorGrok
}

// IsAvailable checks if the grok CLI is installed.
func (g *GrokExecutor) IsAvailable() bool {
	_, err := exec.LookPath("grok")
	return err == nil
}

// Execute runs a task using the Grok CLI.
func (g *GrokExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	return g.runGrok(ctx, task, workDir, prompt, "", false)
}

// Resume continues a previous Grok session, appending feedback as the next prompt.
func (g *GrokExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	return g.runGrok(ctx, task, workDir, prompt, feedback, true)
}

func (g *GrokExecutor) runGrok(ctx context.Context, task *db.Task, workDir, prompt, feedback string, isResume bool) ExecResult {
	paths := g.executor.claudePathsForProject(task.Project)

	if !g.IsAvailable() {
		g.executor.logLine(task.ID, "error", "grok CLI is not installed - run: curl -fsSL https://x.ai/cli/install.sh | bash")
		return ExecResult{Message: "grok CLI is not installed"}
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

	KillAllWindowsByNameAllSessions(windowName)

	// Worktree write-guard: install Grok's PreToolUse hook so writes that would
	// escape the isolated worktree are denied. Cleaned up when the session ends.
	cleanupGuard, guardErr := g.executor.setupGrokWorktreeGuard(workDir, g.executor.getProjectDir(task.Project))
	if guardErr != nil {
		g.logger.Warn("could not set up Grok worktree guard", "error", guardErr)
	}
	defer func() {
		if cleanupGuard != nil {
			cleanupGuard()
		}
	}()

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
	if _, err := promptFile.WriteString(fullPrompt); err != nil {
		promptFile.Close()
		os.Remove(promptFile.Name())
		g.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to write prompt: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to write prompt: %s", err.Error())}
	}
	promptFile.Close()
	defer os.Remove(promptFile.Name())

	sessionID := os.Getenv("WORKTREE_SESSION_ID")
	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", os.Getpid())
	}

	resumeFlag := ""
	existingSessionID := task.ClaudeSessionID
	if existingSessionID == "" && isResume {
		existingSessionID = findGrokSessionID(workDir)
	}
	if existingSessionID != "" && isResume {
		if grokSessionExists(existingSessionID) {
			resumeFlag = fmt.Sprintf("--resume %s ", existingSessionID)
			g.executor.logLine(task.ID, "system", fmt.Sprintf("Resuming Grok session %s", existingSessionID))
		} else {
			g.executor.logLine(task.ID, "system", fmt.Sprintf("Session %s no longer exists, starting fresh", existingSessionID))
			if err := g.executor.db.UpdateTaskClaudeSessionID(task.ID, ""); err != nil {
				g.logger.Warn("failed to clear stale session ID", "task", task.ID, "error", err)
			}
		}
	}

	envPrefix := grokEnvPrefix() + taskEnvPrefix(task)
	permFlag := grokLaunchFlags(task)
	modelFlag := grokModelFlag(task)
	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sgrok %s%s%s"$(cat %q)"`,
		task.ID, sessionID, task.Port, task.WorktreePath, envPrefix, permFlag, modelFlag, resumeFlag, promptFile.Name())

	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script, g.executor.getProjectDir(task.Project), task.ID)
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

	if sid := findGrokSessionID(workDir); sid != "" {
		if err := g.executor.db.UpdateTaskClaudeSessionID(task.ID, sid); err != nil {
			g.logger.Warn("failed to save grok session ID", "task", task.ID, "error", err)
		}
	}

	return ExecResult(result)
}

// GetProcessID returns the PID of the Grok process for a task.
func (g *GrokExecutor) GetProcessID(taskID int64) int {
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
		if strings.Contains(string(cmdOut), "grok") {
			return pid
		}
		childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "grok").Output()
		if err == nil && len(childOut) > 0 {
			childPid, err := strconv.Atoi(strings.TrimSpace(string(childOut)))
			if err == nil {
				return childPid
			}
		}
	}
	return 0
}

// Kill terminates the Grok process for a task.
func (g *GrokExecutor) Kill(taskID int64) bool {
	pid := g.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		g.logger.Debug("Failed to find Grok process", "pid", pid, "error", err)
		return false
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		g.logger.Debug("Failed to terminate Grok process", "pid", pid, "error", err)
		return false
	}
	g.logger.Info("Terminated Grok process", "task", taskID, "pid", pid)
	delete(g.suspendedTasks, taskID)
	return true
}

// Suspend pauses the Grok process for a task.
func (g *GrokExecutor) Suspend(taskID int64) bool {
	pid := g.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		g.logger.Debug("Failed to find process", "pid", pid, "error", err)
		return false
	}
	if err := sendSIGTSTP(proc); err != nil {
		g.logger.Debug("Failed to suspend process", "pid", pid, "error", err)
		return false
	}
	g.suspendedTasks[taskID] = time.Now()
	g.logger.Info("Suspended Grok process", "task", taskID, "pid", pid)
	g.executor.logLine(taskID, "system", "Grok suspended (idle timeout)")
	return true
}

// IsSuspended reports whether the Grok process is suspended for a task.
func (g *GrokExecutor) IsSuspended(taskID int64) bool {
	_, suspended := g.suspendedTasks[taskID]
	return suspended
}

// ResumeProcess resumes a previously suspended Grok process.
func (g *GrokExecutor) ResumeProcess(taskID int64) bool {
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
	if err := sendSIGCONT(proc); err != nil {
		g.logger.Debug("Failed to resume process", "pid", pid, "error", err)
		return false
	}
	delete(g.suspendedTasks, taskID)
	g.logger.Info("Resumed Grok process", "task", taskID, "pid", pid)
	g.executor.logLine(taskID, "system", "Grok resumed")
	return true
}

// BuildCommand returns the shell command to start an interactive Grok session.
func (g *GrokExecutor) BuildCommand(task *db.Task, sessionID, prompt string) string {
	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	envPrefix := grokEnvPrefix() + taskEnvPrefix(task)
	permFlag := grokLaunchFlags(task)
	modelFlag := grokModelFlag(task)

	resumeFlag := ""
	if sessionID != "" {
		resumeFlag = fmt.Sprintf("--resume %s ", sessionID)
	}

	if prompt != "" {
		promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
		if err != nil {
			g.logger.Error("BuildCommand: failed to create temp file", "error", err)
			return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sgrok %s%s%s`,
				task.ID, worktreeSessionID, task.Port, task.WorktreePath, envPrefix, permFlag, modelFlag, resumeFlag)
		}
		promptFile.WriteString(prompt)
		promptFile.Close()
		return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sgrok %s%s%s"$(cat %q)"; rm -f %q`,
			task.ID, worktreeSessionID, task.Port, task.WorktreePath, envPrefix, permFlag, modelFlag, resumeFlag, promptFile.Name(), promptFile.Name())
	}

	return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sgrok %s%s%s`,
		task.ID, worktreeSessionID, task.Port, task.WorktreePath, envPrefix, permFlag, modelFlag, resumeFlag)
}

// grokEnvPrefix disables folder-trust so worktree-local PreToolUse hooks (the
// write-guard) actually run. Project hooks are otherwise skipped until the user
// interactively grants /hooks-trust, which never happens in a daemon-driven TUI.
func grokEnvPrefix() string {
	return "GROK_FOLDER_TRUST=0 "
}

func grokDangerousEnabled(task *db.Task) bool {
	if task != nil && task.IsDangerous() {
		return true
	}
	return os.Getenv("WORKTREE_DANGEROUS_MODE") == "1"
}

func grokLaunchFlags(task *db.Task) string {
	if grokDangerousEnabled(task) {
		return buildGrokDangerousFlag(true)
	}
	if task == nil {
		return ""
	}
	switch task.EffectivePermissionMode() {
	case db.PermissionModeAcceptEdits:
		return "--permission-mode acceptEdits "
	case db.PermissionModeAuto:
		return "--permission-mode auto "
	default:
		return ""
	}
}

func buildGrokDangerousFlag(enabled bool) string {
	if !enabled && os.Getenv("WORKTREE_DANGEROUS_MODE") != "1" {
		return ""
	}
	flag := strings.TrimSpace(os.Getenv("GROK_DANGEROUS_ARGS"))
	if flag == "" {
		flag = "--always-approve"
	}
	if !strings.HasSuffix(flag, " ") {
		flag += " "
	}
	return flag
}

func grokModelFlag(task *db.Task) string {
	if task == nil {
		return ""
	}
	model := strings.TrimSpace(task.Model)
	if model == "" {
		return ""
	}
	return fmt.Sprintf("--model %s ", shellSingleQuote(model))
}

// ---- Session and Dangerous Mode Support ----

// SupportsSessionResume returns true — Grok supports --resume by session ID.
func (g *GrokExecutor) SupportsSessionResume() bool {
	return true
}

// SupportsDangerousMode returns true — Grok supports --always-approve.
func (g *GrokExecutor) SupportsDangerousMode() bool {
	return true
}

// FindSessionID discovers the most recent Grok session ID for the given workDir.
func (g *GrokExecutor) FindSessionID(workDir string) string {
	return findGrokSessionID(workDir)
}

// ResumeDangerous kills the current Grok process and restarts with --always-approve.
func (g *GrokExecutor) ResumeDangerous(task *db.Task, workDir string) bool {
	return g.resumeWithMode(task, workDir, true)
}

// ResumeSafe kills the current Grok process and restarts without --always-approve.
func (g *GrokExecutor) ResumeSafe(task *db.Task, workDir string) bool {
	return g.resumeWithMode(task, workDir, false)
}

func (g *GrokExecutor) resumeWithMode(task *db.Task, workDir string, dangerousMode bool) bool {
	taskID := task.ID

	sessionID := task.ClaudeSessionID
	if sessionID == "" {
		sessionID = findGrokSessionID(workDir)
	}
	if sessionID == "" || !grokSessionExists(sessionID) {
		g.executor.logLine(taskID, "system", "No Grok session found - cannot toggle mode")
		if sessionID != "" {
			if err := g.executor.db.UpdateTaskClaudeSessionID(taskID, ""); err != nil {
				g.logger.Warn("failed to clear stale session ID", "task", taskID, "error", err)
			}
		}
		return false
	}

	modeStr := "safe"
	if dangerousMode {
		modeStr = "dangerous"
	}
	g.executor.logLine(taskID, "system", fmt.Sprintf("Restarting Grok in %s mode", modeStr))

	if _, err := exec.LookPath("tmux"); err != nil {
		g.executor.logLine(taskID, "system", "Tmux not available - cannot resume")
		return false
	}

	windowName := TmuxWindowName(taskID)
	KillAllWindowsByNameAllSessions(windowName)

	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		g.logger.Warn("could not create task-daemon session", "error", err)
		return false
	}

	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	taskSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if taskSessionID == "" {
		taskSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	dangerousFlag := ""
	if dangerousMode {
		dangerousFlag = buildGrokDangerousFlag(true)
	}

	envPrefix := grokEnvPrefix() + taskEnvPrefix(task)
	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sgrok %s--resume %s`,
		taskID, taskSessionID, task.Port, task.WorktreePath, envPrefix, dangerousFlag, sessionID)

	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script, g.executor.getProjectDir(task.Project), task.ID)
	if tmuxErr != nil {
		g.logger.Warn("tmux failed to create window", "error", tmuxErr, "session", daemonSession)
		return false
	}

	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	time.Sleep(200 * time.Millisecond)

	if err := g.executor.db.UpdateTaskDaemonSession(taskID, daemonSession); err != nil {
		g.logger.Warn("failed to save daemon session", "task", taskID, "error", err)
	}
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := g.executor.db.UpdateTaskWindowID(taskID, windowID); err != nil {
			g.logger.Warn("failed to save window ID", "task", taskID, "error", err)
		}
	}

	paths := g.executor.claudePathsForTask(task)
	g.executor.ensureShellPane(windowTarget, workDir, taskID, task.Port, task.WorktreePath, paths.configDir)
	g.executor.configureTmuxWindow(windowTarget)
	return true
}

func grokHomeDir() string {
	if h := strings.TrimSpace(os.Getenv("GROK_HOME")); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".grok")
}

// grokEncodeCwd percent-encodes a working directory the way Grok names session
// group folders (~/.grok/sessions/<encoded-cwd>/). '/' becomes %2F; spaces %20.
func grokEncodeCwd(path string) string {
	return strings.ReplaceAll(url.QueryEscape(path), "+", "%20")
}

func grokSessionGroupDir(workDir string) string {
	home := grokHomeDir()
	if home == "" || workDir == "" {
		return ""
	}
	sessionsDir := filepath.Join(home, "sessions")
	encoded := grokEncodeCwd(workDir)
	direct := filepath.Join(sessionsDir, encoded)
	if info, err := os.Stat(direct); err == nil && info.IsDir() {
		return direct
	}

	// Encoded names longer than 255 bytes use a slug+hash folder with a .cwd file.
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cwdFile := filepath.Join(sessionsDir, entry.Name(), ".cwd")
		data, err := os.ReadFile(cwdFile)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == workDir {
			return filepath.Join(sessionsDir, entry.Name())
		}
	}
	return ""
}

// findGrokSessionID discovers the most recent Grok session ID for workDir.
// Grok stores sessions at ~/.grok/sessions/<urlencoded-cwd>/<session-id>/.
func findGrokSessionID(workDir string) string {
	group := grokSessionGroupDir(workDir)
	if group == "" {
		return ""
	}

	entries, err := os.ReadDir(group)
	if err != nil {
		return ""
	}

	var latestTime time.Time
	var latestID string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		mod := info.ModTime()
		// Prefer the session directory's summary.json mtime when present.
		if st, err := os.Stat(filepath.Join(group, name, "summary.json")); err == nil {
			mod = st.ModTime()
		}
		if mod.After(latestTime) {
			latestTime = mod
			latestID = name
		}
	}
	return latestID
}

// grokSessionExists reports whether a Grok session directory exists for sessionID.
func grokSessionExists(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	home := grokHomeDir()
	if home == "" {
		return false
	}
	sessionsDir := filepath.Join(home, "sessions")
	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		return false
	}

	found := false
	filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || !info.IsDir() {
			return nil
		}
		if info.Name() == sessionID {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
