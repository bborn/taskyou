package executor

import (
	"context"
	"crypto/md5" //nolint:gosec // G501: Cursor names CLI chat folders MD5(cwd)
	"encoding/hex"
	"encoding/json"
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

// CursorExecutor implements TaskExecutor for the Cursor Agent CLI.
// See: https://cursor.com/docs/cli and https://cursor.com/install
//
// CLI reference (agent --help / cursor.com/docs/cli/reference/parameters):
//   - agent "prompt"            Interactive agent with an initial prompt
//   - agent --force / --yolo    Force-allow commands (dangerous mode)
//   - agent --resume [chatId]   Resume a chat session
//   - agent --continue          Continue the most recent session
//   - agent --model <model>     Model to use
//   - agent --approve-mcps      Auto-approve project MCP servers
//
// TaskYou already creates an isolated git worktree per task, so we never pass
// Cursor's own --worktree flag (that would nest a second worktree).
type CursorExecutor struct {
	executor       *Executor
	logger         *log.Logger
	suspendedTasks map[int64]time.Time
}

// NewCursorExecutor creates a new Cursor executor.
func NewCursorExecutor(e *Executor) *CursorExecutor {
	return &CursorExecutor{
		executor:       e,
		logger:         e.logger,
		suspendedTasks: make(map[int64]time.Time),
	}
}

// Name returns the executor name.
func (c *CursorExecutor) Name() string {
	return db.ExecutorCursor
}

// cursorLaunchBin is the Cursor Agent CLI binary. Official installs put
// `agent` on PATH (https://cursor.com/install); some builds also ship
// `cursor-agent`. Prefer the more specific name when both exist.
func cursorLaunchBin() string {
	if _, err := exec.LookPath("cursor-agent"); err == nil {
		return "cursor-agent"
	}
	return "agent"
}

// IsAvailable checks if the Cursor Agent CLI is installed.
func (c *CursorExecutor) IsAvailable() bool {
	if _, err := exec.LookPath("cursor-agent"); err == nil {
		return true
	}
	_, err := exec.LookPath("agent")
	return err == nil
}

// Execute runs a task using the Cursor CLI.
func (c *CursorExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	return c.runCursor(ctx, task, workDir, prompt, "", false)
}

// Resume continues a previous Cursor session, appending feedback as the next prompt.
func (c *CursorExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	return c.runCursor(ctx, task, workDir, prompt, feedback, true)
}

func (c *CursorExecutor) runCursor(ctx context.Context, task *db.Task, workDir, prompt, feedback string, isResume bool) ExecResult {
	paths := c.executor.claudePathsForProject(task.Project)

	if !c.IsAvailable() {
		c.executor.logLine(task.ID, "error", "Cursor CLI is not installed - run: curl https://cursor.com/install -fsS | bash")
		return ExecResult{Message: "Cursor CLI is not installed"}
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		c.executor.logLine(task.ID, "error", "tmux is not installed - required for task execution")
		return ExecResult{Message: "tmux is not installed"}
	}

	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		c.logger.Error("could not create task-daemon session", "error", err)
		c.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux daemon: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux daemon: %s", err.Error())}
	}

	windowName := TmuxWindowName(task.ID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	KillAllWindowsByNameAllSessions(windowName)

	cleanupGuard, guardErr := c.executor.setupCursorWorktreeGuard(workDir, c.executor.getProjectDir(task.Project))
	if guardErr != nil {
		c.logger.Warn("could not set up Cursor worktree guard", "error", guardErr)
	}
	defer func() {
		if cleanupGuard != nil {
			cleanupGuard()
		}
	}()

	promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
	if err != nil {
		c.logger.Error("could not create temp file", "error", err)
		c.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create temp file: %s", err.Error())}
	}
	fullPrompt := prompt
	if isResume && feedback != "" {
		fullPrompt = prompt + "\n\n## User Feedback\n\n" + feedback
	}
	if _, err := promptFile.WriteString(fullPrompt); err != nil {
		promptFile.Close()
		os.Remove(promptFile.Name())
		c.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to write prompt: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to write prompt: %s", err.Error())}
	}
	promptFile.Close()
	defer os.Remove(promptFile.Name())

	sessionID := os.Getenv("WORKTREE_SESSION_ID")
	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", os.Getpid())
	}

	resumeSessionID := ""
	existingSessionID := task.ClaudeSessionID
	if existingSessionID == "" && isResume {
		existingSessionID = findCursorSessionID(workDir)
	}
	if existingSessionID != "" && isResume {
		if cursorSessionExists(existingSessionID) {
			resumeSessionID = existingSessionID
			c.executor.logLine(task.ID, "system", fmt.Sprintf("Resuming Cursor session %s", existingSessionID))
		} else {
			c.executor.logLine(task.ID, "system", fmt.Sprintf("Session %s no longer exists, starting fresh", existingSessionID))
			if err := c.executor.db.UpdateTaskClaudeSessionID(task.ID, ""); err != nil {
				c.logger.Warn("failed to clear stale session ID", "task", task.ID, "error", err)
			}
		}
	}

	if err := ensureCursorWorktreeMCPConfig(workDir, task.ID); err != nil {
		c.logger.Warn("could not write cursor taskyou MCP config", "error", err)
	}

	script := cursorLaunchScript(task, sessionID, resumeSessionID, nil, fmt.Sprintf(`"$(cat %q)"`, promptFile.Name()))

	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script, c.executor.getProjectDir(task.Project), task.ID)
	if tmuxErr != nil {
		c.logger.Error("tmux new-window failed", "error", tmuxErr, "session", daemonSession)
		c.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux window: %s", tmuxErr.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux window: %s", tmuxErr.Error())}
	}

	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	time.Sleep(200 * time.Millisecond)

	if err := c.executor.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		c.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := c.executor.db.UpdateTaskWindowID(task.ID, windowID); err != nil {
			c.logger.Warn("failed to save window ID", "task", task.ID, "error", err)
		}
	}

	c.executor.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, paths.configDir)
	c.executor.configureTmuxWindow(windowTarget)

	result := c.executor.pollTmuxSession(ctx, task.ID, windowTarget)

	if sid := findCursorSessionID(workDir); sid != "" {
		if err := c.executor.db.UpdateTaskClaudeSessionID(task.ID, sid); err != nil {
			c.logger.Warn("failed to save cursor session ID", "task", task.ID, "error", err)
		}
	}

	return ExecResult(result)
}

func cursorProcessName(comm string) bool {
	comm = strings.TrimSpace(comm)
	return strings.Contains(comm, "cursor-agent") || comm == "agent" || strings.HasSuffix(comm, "/agent")
}

// GetProcessID returns the PID of the Cursor process for a task.
func (c *CursorExecutor) GetProcessID(taskID int64) int {
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
		if cursorProcessName(string(cmdOut)) {
			return pid
		}
		for _, name := range []string{"cursor-agent", "agent"} {
			childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), name).Output()
			if err == nil && len(childOut) > 0 {
				childPid, err := strconv.Atoi(strings.TrimSpace(string(childOut)))
				if err == nil {
					return childPid
				}
			}
		}
	}
	return 0
}

// Kill terminates the Cursor process for a task.
func (c *CursorExecutor) Kill(taskID int64) bool {
	pid := c.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		c.logger.Debug("Failed to find Cursor process", "pid", pid, "error", err)
		return false
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		c.logger.Debug("Failed to terminate Cursor process", "pid", pid, "error", err)
		return false
	}
	c.logger.Info("Terminated Cursor process", "task", taskID, "pid", pid)
	delete(c.suspendedTasks, taskID)
	return true
}

// Suspend pauses the Cursor process for a task.
func (c *CursorExecutor) Suspend(taskID int64) bool {
	pid := c.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		c.logger.Debug("Failed to find process", "pid", pid, "error", err)
		return false
	}
	if err := sendSIGTSTP(proc); err != nil {
		c.logger.Debug("Failed to suspend process", "pid", pid, "error", err)
		return false
	}
	c.suspendedTasks[taskID] = time.Now()
	c.logger.Info("Suspended Cursor process", "task", taskID, "pid", pid)
	c.executor.logLine(taskID, "system", "Cursor suspended (idle timeout)")
	return true
}

// IsSuspended reports whether the Cursor process is suspended for a task.
func (c *CursorExecutor) IsSuspended(taskID int64) bool {
	_, suspended := c.suspendedTasks[taskID]
	return suspended
}

// ResumeProcess resumes a previously suspended Cursor process.
func (c *CursorExecutor) ResumeProcess(taskID int64) bool {
	if !c.IsSuspended(taskID) {
		return false
	}
	pid := c.GetProcessID(taskID)
	if pid == 0 {
		delete(c.suspendedTasks, taskID)
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		delete(c.suspendedTasks, taskID)
		return false
	}
	if err := sendSIGCONT(proc); err != nil {
		c.logger.Debug("Failed to resume process", "pid", pid, "error", err)
		return false
	}
	delete(c.suspendedTasks, taskID)
	c.logger.Info("Resumed Cursor process", "task", taskID, "pid", pid)
	c.executor.logLine(taskID, "system", "Cursor resumed")
	return true
}

// BuildCommand returns the shell command to start an interactive Cursor session.
func (c *CursorExecutor) BuildCommand(task *db.Task, sessionID, prompt string) string {
	if err := ensureCursorWorktreeMCPConfig(task.WorktreePath, task.ID); err != nil {
		c.logger.Error("BuildCommand: failed to write cursor MCP config", "error", err)
	}

	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	if prompt != "" {
		promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
		if err != nil {
			c.logger.Error("BuildCommand: failed to create temp file", "error", err)
			return cursorLaunchScript(task, worktreeSessionID, sessionID, nil, "")
		}
		promptFile.WriteString(prompt)
		promptFile.Close()
		return cursorLaunchScript(task, worktreeSessionID, sessionID, nil, fmt.Sprintf(`"$(cat %q)"; rm -f %q`, promptFile.Name(), promptFile.Name()))
	}

	return cursorLaunchScript(task, worktreeSessionID, sessionID, nil, "")
}

func cursorDangerousEnabled(task *db.Task) bool {
	if task != nil && task.IsDangerous() {
		return true
	}
	return os.Getenv("WORKTREE_DANGEROUS_MODE") == "1"
}

func cursorLaunchFlags(task *db.Task) string {
	if cursorDangerousEnabled(task) {
		return buildCursorDangerousFlag(true)
	}
	return ""
}

func buildCursorDangerousFlag(enabled bool) string {
	if !enabled && os.Getenv("WORKTREE_DANGEROUS_MODE") != "1" {
		return ""
	}
	flag := strings.TrimSpace(os.Getenv("CURSOR_DANGEROUS_ARGS"))
	if flag == "" {
		flag = "--force"
	}
	if !strings.HasSuffix(flag, " ") {
		flag += " "
	}
	return flag
}

func cursorLaunchEnv(task *db.Task) string {
	return dbPathEnvPrefix() + taskEnvPrefix(task)
}

// cursorCLIFlags returns Cursor CLI flags (each with a trailing space).
// dangerousOverride is used by ResumeDangerous/ResumeSafe to force bypass on
// or off; nil honors the task. --approve-mcps is always included so the
// worktree-local taskyou MCP server is not blocked on a TUI prompt.
func cursorCLIFlags(task *db.Task, sessionID string, dangerousOverride *bool) string {
	var perm string
	if dangerousOverride != nil {
		if *dangerousOverride {
			perm = buildCursorDangerousFlag(true)
		}
	} else {
		perm = cursorLaunchFlags(task)
	}
	model := ""
	if task != nil {
		model = modelFlag(task.Model)
	}
	resume := ""
	if sessionID != "" {
		resume = fmt.Sprintf("--resume %s ", sessionID)
	}
	return perm + "--approve-mcps " + model + resume
}

func cursorLaunchScript(task *db.Task, worktreeSessionID, resumeSessionID string, dangerousOverride *bool, promptArg string) string {
	bin := cursorLaunchBin()
	if task == nil {
		return bin
	}
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}
	flags := cursorCLIFlags(task, resumeSessionID, dangerousOverride)
	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %s%s %s%s`,
		task.ID, worktreeSessionID, task.Port, task.WorktreePath, cursorLaunchEnv(task), bin, flags, promptArg)
	return strings.TrimSpace(script)
}

type cursorMCPFile struct {
	MCPServers map[string]cursorMCPServer `json:"mcpServers"`
}

type cursorMCPServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// ensureCursorWorktreeMCPConfig merges a taskyou stdio server into the
// worktree's `.cursor/mcp.json`. Cursor has no --mcp-config flag; it reads
// project MCP from that file. Existing servers (often committed in the repo)
// are preserved.
func ensureCursorWorktreeMCPConfig(workDir string, taskID int64) error {
	if strings.TrimSpace(workDir) == "" || taskID == 0 {
		return nil
	}
	dir := filepath.Join(workDir, ".cursor")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "mcp.json")

	cfg := cursorMCPFile{MCPServers: map[string]cursorMCPServer{}}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			cfg.MCPServers = map[string]cursorMCPServer{}
		}
		if cfg.MCPServers == nil {
			cfg.MCPServers = map[string]cursorMCPServer{}
		}
	}
	cfg.MCPServers["taskyou"] = cursorMCPServer{
		Command: resolveTaskExecutable(),
		Args:    []string{"mcp-server", "--task-id", fmt.Sprintf("%d", taskID)},
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0644)
}

// ---- Session and Dangerous Mode Support ----

// SupportsSessionResume returns true — Cursor supports --resume by chat ID.
func (c *CursorExecutor) SupportsSessionResume() bool {
	return true
}

// SupportsDangerousMode returns true — Cursor supports --force / --yolo.
func (c *CursorExecutor) SupportsDangerousMode() bool {
	return true
}

// FindSessionID discovers the most recent Cursor session ID for the given workDir.
func (c *CursorExecutor) FindSessionID(workDir string) string {
	return findCursorSessionID(workDir)
}

// ResumeDangerous kills the current Cursor process and restarts with --force.
func (c *CursorExecutor) ResumeDangerous(task *db.Task, workDir string) bool {
	return c.resumeWithMode(task, workDir, true)
}

// ResumeSafe kills the current Cursor process and restarts without --force.
func (c *CursorExecutor) ResumeSafe(task *db.Task, workDir string) bool {
	return c.resumeWithMode(task, workDir, false)
}

func (c *CursorExecutor) resumeWithMode(task *db.Task, workDir string, dangerousMode bool) bool {
	taskID := task.ID

	sessionID := task.ClaudeSessionID
	if sessionID == "" {
		sessionID = findCursorSessionID(workDir)
	}
	if sessionID == "" || !cursorSessionExists(sessionID) {
		c.executor.logLine(taskID, "system", "No Cursor session found - cannot toggle mode")
		if sessionID != "" {
			if err := c.executor.db.UpdateTaskClaudeSessionID(taskID, ""); err != nil {
				c.logger.Warn("failed to clear stale session ID", "task", taskID, "error", err)
			}
		}
		return false
	}

	modeStr := "safe"
	if dangerousMode {
		modeStr = "dangerous"
	}
	c.executor.logLine(taskID, "system", fmt.Sprintf("Restarting Cursor in %s mode", modeStr))

	if _, err := exec.LookPath("tmux"); err != nil {
		c.executor.logLine(taskID, "system", "Tmux not available - cannot resume")
		return false
	}

	windowName := TmuxWindowName(taskID)
	KillAllWindowsByNameAllSessions(windowName)

	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		c.logger.Warn("could not create task-daemon session", "error", err)
		return false
	}

	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	taskSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if taskSessionID == "" {
		taskSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	if err := ensureCursorWorktreeMCPConfig(workDir, taskID); err != nil {
		c.logger.Warn("could not write cursor taskyou MCP config", "error", err)
	}
	override := dangerousMode
	script := cursorLaunchScript(task, taskSessionID, sessionID, &override, "")

	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script, c.executor.getProjectDir(task.Project), task.ID)
	if tmuxErr != nil {
		c.logger.Warn("tmux failed to create window", "error", tmuxErr, "session", daemonSession)
		return false
	}

	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	time.Sleep(200 * time.Millisecond)

	if err := c.executor.db.UpdateTaskDaemonSession(taskID, daemonSession); err != nil {
		c.logger.Warn("failed to save daemon session", "task", taskID, "error", err)
	}
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := c.executor.db.UpdateTaskWindowID(taskID, windowID); err != nil {
			c.logger.Warn("failed to save window ID", "task", taskID, "error", err)
		}
	}

	paths := c.executor.claudePathsForTask(task)
	c.executor.ensureShellPane(windowTarget, workDir, taskID, task.Port, task.WorktreePath, paths.configDir)
	c.executor.configureTmuxWindow(windowTarget)
	return true
}

func cursorHomeDir() string {
	if h := strings.TrimSpace(os.Getenv("CURSOR_CONFIG_DIR")); h != "" {
		return h
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "cursor")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cursor")
}

func cursorWorkspaceHash(path string) string {
	// Cursor names CLI chat folders with MD5(cwd). This is a filesystem lookup
	// key, not a security digest.
	sum := md5.Sum([]byte(path)) //nolint:gosec // G401
	return hex.EncodeToString(sum[:])
}

func cursorChatsDir() string {
	home := cursorHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "chats")
}

func latestCursorSessionInGroup(group string) string {
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
		metaPath := filepath.Join(group, name, "meta.json")
		if data, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				UpdatedAtMs int64 `json:"updatedAtMs"`
			}
			if json.Unmarshal(data, &meta) == nil && meta.UpdatedAtMs > 0 {
				mod = time.UnixMilli(meta.UpdatedAtMs)
			} else if st, err := os.Stat(metaPath); err == nil {
				mod = st.ModTime()
			}
		}
		if mod.After(latestTime) {
			latestTime = mod
			latestID = name
		}
	}
	return latestID
}

// findCursorSessionID discovers the most recent Cursor CLI chat ID for workDir.
// Cursor stores CLI chats at ~/.cursor/chats/<md5(cwd)>/<chat-id>/ (see
// https://cursor.com/docs/cli). When the hash folder is missing, we fall back
// to meta.json files that record cwd.
func findCursorSessionID(workDir string) string {
	if workDir == "" {
		return ""
	}
	chats := cursorChatsDir()
	if chats == "" {
		return ""
	}

	direct := filepath.Join(chats, cursorWorkspaceHash(workDir))
	if info, err := os.Stat(direct); err == nil && info.IsDir() {
		if id := latestCursorSessionInGroup(direct); id != "" {
			return id
		}
	}

	entries, err := os.ReadDir(chats)
	if err != nil {
		return ""
	}
	var latestTime time.Time
	var latestID string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		group := filepath.Join(chats, entry.Name())
		sessions, err := os.ReadDir(group)
		if err != nil {
			continue
		}
		for _, session := range sessions {
			if !session.IsDir() || strings.HasPrefix(session.Name(), ".") {
				continue
			}
			metaPath := filepath.Join(group, session.Name(), "meta.json")
			data, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var meta struct {
				Cwd         string `json:"cwd"`
				UpdatedAtMs int64  `json:"updatedAtMs"`
			}
			if json.Unmarshal(data, &meta) != nil {
				continue
			}
			if strings.TrimSpace(meta.Cwd) != workDir {
				continue
			}
			mod := time.UnixMilli(meta.UpdatedAtMs)
			if meta.UpdatedAtMs == 0 {
				if st, err := os.Stat(metaPath); err == nil {
					mod = st.ModTime()
				}
			}
			if mod.After(latestTime) {
				latestTime = mod
				latestID = session.Name()
			}
		}
	}
	return latestID
}

// cursorSessionExists reports whether a Cursor chat directory exists for sessionID.
func cursorSessionExists(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	chats := cursorChatsDir()
	if chats == "" {
		return false
	}
	if _, err := os.Stat(chats); os.IsNotExist(err) {
		return false
	}

	found := false
	filepath.Walk(chats, func(path string, info os.FileInfo, err error) error {
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
