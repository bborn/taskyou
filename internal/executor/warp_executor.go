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

// WarpExecutor implements TaskExecutor for the Warp Agent CLI — the agent from
// the Warp terminal, shipped as a standalone TUI that runs in any terminal.
// See: https://www.warp.dev/agent-cli
//
// CLI Reference (warp --help):
//   - warp                     Start the interactive agent TUI
//   - warp --auto-approve      Approve agent actions without prompting
//   - warp --resume <TOKEN>    Resume a conversation by server token
//   - warp --api-key <KEY>     Non-interactive auth (also WARP_API_KEY)
//
// Unlike every other executor ty drives, the Warp CLI accepts no prompt on the
// command line: it is a TUI with a single input box and nothing else. So the
// prompt is delivered the way a human would deliver it — a tmux bracketed paste
// into the agent pane once the TUI owns it, followed by Enter. Warp advertises
// bracketed paste (DECSET 2004) on startup, so a multi-line markdown prompt
// arrives as one paste rather than as one submission per line.
type WarpExecutor struct {
	executor       *Executor
	logger         *log.Logger
	suspendedTasks map[int64]time.Time
}

// NewWarpExecutor creates a new Warp executor.
func NewWarpExecutor(e *Executor) *WarpExecutor {
	return &WarpExecutor{
		executor:       e,
		logger:         e.logger,
		suspendedTasks: make(map[int64]time.Time),
	}
}

// Name returns the executor name.
func (w *WarpExecutor) Name() string {
	return db.ExecutorWarp
}

// IsAvailable checks if the warp CLI is installed.
func (w *WarpExecutor) IsAvailable() bool {
	_, err := exec.LookPath("warp")
	return err == nil
}

// Execute runs a task using the Warp Agent CLI.
func (w *WarpExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	return w.runWarp(ctx, task, workDir, prompt, "", false)
}

// Resume replays the full prompt plus feedback. Warp conversations are resumable
// only by a server-side token the CLI does not hand back to us, so a retry starts
// a fresh conversation (see SupportsSessionResume).
func (w *WarpExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	return w.runWarp(ctx, task, workDir, prompt, feedback, true)
}

func (w *WarpExecutor) runWarp(ctx context.Context, task *db.Task, workDir, prompt, feedback string, isResume bool) ExecResult {
	paths := w.executor.claudePathsForProject(task.Project)

	if !w.IsAvailable() {
		w.executor.logLine(task.ID, "error", "warp CLI is not installed - run: curl -fsSL https://app.warp.dev/download/agent-cli | bash")
		return ExecResult{Message: "warp CLI is not installed"}
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		w.executor.logLine(task.ID, "error", "tmux is not installed - required for task execution")
		return ExecResult{Message: "tmux is not installed"}
	}

	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		w.logger.Error("could not create task-daemon session", "error", err)
		w.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux daemon: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux daemon: %s", err.Error())}
	}

	windowName := TmuxWindowName(task.ID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	// Kill ALL existing windows with this name (handles duplicates)
	KillAllWindowsByNameAllSessions(windowName)

	promptFile, err := writeWarpPromptFile(buildWarpPrompt(workDir, prompt, feedback, isResume))
	if err != nil {
		w.logger.Error("could not create temp file", "error", err)
		w.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create temp file: %s", err.Error())}
	}
	// The launch script removes the prompt file once it has been pasted; this is
	// the backstop for the paths where the window never starts.
	defer os.Remove(promptFile)

	script := warpLaunchScript(task, workDir, promptFile, claudeEnvPrefix(paths.configDir))

	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script, w.executor.getProjectDir(task.Project), task.ID)
	if tmuxErr != nil {
		w.logger.Error("tmux new-window failed", "error", tmuxErr, "session", daemonSession)
		w.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux window: %s", tmuxErr.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux window: %s", tmuxErr.Error())}
	}

	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	time.Sleep(200 * time.Millisecond)

	if err := w.executor.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		w.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := w.executor.db.UpdateTaskWindowID(task.ID, windowID); err != nil {
			w.logger.Warn("failed to save window ID", "task", task.ID, "error", err)
		}
	}

	w.executor.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, paths.configDir)
	w.executor.configureTmuxWindow(windowTarget)

	result := w.executor.pollTmuxSession(ctx, task.ID, windowTarget)

	return ExecResult(result)
}

// buildWarpPrompt assembles the text pasted into the Warp TUI.
//
// The working-directory preamble matches the other TUI executors. The tooling
// note is Warp-specific: ty injects its MCP server into Claude Code's config,
// but has no equivalent hook into Warp, so the agent has to reach ty through the
// `ty` CLI instead of the taskyou_* tools the shared prompt tells it to expect.
func buildWarpPrompt(workDir, prompt, feedback string, isResume bool) string {
	var b strings.Builder
	b.WriteString("## Working Directory\n\n")
	b.WriteString(fmt.Sprintf("You are working in a git worktree at: `%s`\n\n", workDir))
	b.WriteString("IMPORTANT: All file operations (reading, writing, creating files) MUST be done within this directory. ")
	b.WriteString("Do NOT use your default workspace. Always use absolute paths or paths relative to this working directory.\n\n")
	b.WriteString("## TaskYou Tooling\n\n")
	b.WriteString("The taskyou MCP server is NOT connected to this session, so the taskyou_* tools described below are unavailable to you. ")
	b.WriteString("Use the `ty` CLI instead — it is on PATH and resolves the task from WORKTREE_TASK_ID:\n")
	b.WriteString("- Finish the task:      ty complete --summary \"<summary>\"   (equivalent to taskyou_complete)\n")
	b.WriteString("- Ask for input:        ty needs-input \"<question>\"          (equivalent to taskyou_needs_input)\n")
	b.WriteString("- Workflow documents:   ty artifact get|set|list\n\n")
	b.WriteString("---\n\n")
	b.WriteString(prompt)
	if isResume && feedback != "" {
		b.WriteString("\n\n## User Feedback\n\n")
		b.WriteString(feedback)
	}
	return b.String()
}

// writeWarpPromptFile stores the prompt in a temp file so it can be handed to
// tmux load-buffer verbatim, with no shell quoting in the path.
func writeWarpPromptFile(prompt string) (string, error) {
	f, err := os.CreateTemp("", "task-prompt-*.txt")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(prompt); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// warpReadyPollAttempts and warpReadyPollInterval bound how long the prompt
// feeder waits for the TUI to take over the pane before pasting anyway.
const (
	warpReadyPollAttempts = 60
	warpReadyPollInterval = "0.25"
	// warpSettleDelay gives the TUI time to finish its first draw and put the
	// cursor in the input box after it becomes the pane's foreground process.
	warpSettleDelay = "2"
)

// warpLaunchScript builds the shell script for a task's Warp session. It is the
// single source of truth for how ty starts Warp, shared by the daemon path
// (runWarp) and the interactive path (BuildCommand), so the two can't drift.
//
// promptFile may be empty to start Warp with no prompt (a bare interactive
// session). Warp's `--resume` is deliberately never passed: it takes a Warp
// conversation token, and the only session ID ty stores for a task
// (ClaudeSessionID) may hold another agent's identifier entirely.
func warpLaunchScript(task *db.Task, workDir, promptFile, envPrefix string) string {
	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	envVars := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q`,
		task.ID, worktreeSessionID, task.Port, task.WorktreePath)

	flags := ""
	if task.DangerousMode {
		flags += " --auto-approve"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("cd %q\n", workDir))
	if promptFile != "" {
		b.WriteString(warpPromptFeeder(task.ID, promptFile))
		b.WriteString("\n")
	}
	// exec so the Warp binary — not the wrapping shell — becomes the pane's
	// foreground process: that is what the feeder above waits on, and what
	// GetProcessID matches.
	b.WriteString(fmt.Sprintf("exec env %s %s warp%s\n", envVars, envPrefix, flags))
	return b.String()
}

// warpPromptFeeder returns a backgrounded POSIX-sh block that pastes promptFile
// into this pane once the Warp TUI owns it, then presses Enter to submit.
//
// tmux paste-buffer -p wraps the text in bracketed-paste markers, which Warp
// enables at startup, so an embedded newline does not submit a partial prompt.
// Everything is best-effort: a failed paste must not take the session down, and
// the prompt file is removed either way.
func warpPromptFeeder(taskID int64, promptFile string) string {
	buffer := fmt.Sprintf("ty-warp-%d", taskID)
	return fmt.Sprintf(`(
  i=0
  while [ $i -lt %d ]; do
    case "$(tmux display-message -p -t "$TMUX_PANE" '#{pane_current_command}' 2>/dev/null)" in
      *warp*) break ;;
    esac
    i=$((i + 1))
    sleep %s
  done
  sleep %s
  if tmux load-buffer -b %s %q 2>/dev/null; then
    tmux paste-buffer -d -p -b %s -t "$TMUX_PANE" 2>/dev/null && \
      sleep 0.5 && tmux send-keys -t "$TMUX_PANE" Enter 2>/dev/null
  fi
  rm -f %q
) >/dev/null 2>&1 &`,
		warpReadyPollAttempts, warpReadyPollInterval, warpSettleDelay,
		buffer, promptFile, buffer, promptFile)
}

// GetProcessID returns the PID of the Warp process for a task.
func (w *WarpExecutor) GetProcessID(taskID int64) int {
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
		// The launch script execs warp, so the pane process itself is normally
		// the agent. The binary is installed as warp-tui-<channel> behind a
		// `warp` symlink, so match on the prefix rather than an exact name.
		cmdOut, _ := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
		if strings.Contains(string(cmdOut), "warp") {
			return pid
		}
		// Fall back to a child process for setups where a wrapper shell survives.
		childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "-f", "warp").Output()
		if err == nil && len(childOut) > 0 {
			childPid, err := strconv.Atoi(strings.TrimSpace(strings.Split(string(childOut), "\n")[0]))
			if err == nil {
				return childPid
			}
		}
	}
	return 0
}

// Kill terminates the Warp process for a task.
func (w *WarpExecutor) Kill(taskID int64) bool {
	pid := w.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		w.logger.Debug("Failed to find Warp process", "pid", pid, "error", err)
		return false
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		w.logger.Debug("Failed to terminate Warp process", "pid", pid, "error", err)
		return false
	}
	w.logger.Info("Terminated Warp process", "task", taskID, "pid", pid)
	delete(w.suspendedTasks, taskID)
	return true
}

// Suspend pauses the Warp process for a task.
func (w *WarpExecutor) Suspend(taskID int64) bool {
	pid := w.GetProcessID(taskID)
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		w.logger.Debug("Failed to find process", "pid", pid, "error", err)
		return false
	}
	if err := sendSIGTSTP(proc); err != nil {
		w.logger.Debug("Failed to suspend process", "pid", pid, "error", err)
		return false
	}
	w.suspendedTasks[taskID] = time.Now()
	w.logger.Info("Suspended Warp process", "task", taskID, "pid", pid)
	w.executor.logLine(taskID, "system", "Warp suspended (idle timeout)")
	return true
}

// IsSuspended reports whether the Warp process is suspended for a task.
func (w *WarpExecutor) IsSuspended(taskID int64) bool {
	_, suspended := w.suspendedTasks[taskID]
	return suspended
}

// ResumeProcess resumes a previously suspended Warp process.
func (w *WarpExecutor) ResumeProcess(taskID int64) bool {
	if !w.IsSuspended(taskID) {
		return false
	}
	pid := w.GetProcessID(taskID)
	if pid == 0 {
		delete(w.suspendedTasks, taskID)
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		delete(w.suspendedTasks, taskID)
		return false
	}
	if err := sendSIGCONT(proc); err != nil {
		w.logger.Debug("Failed to resume process", "pid", pid, "error", err)
		return false
	}
	delete(w.suspendedTasks, taskID)
	w.logger.Info("Resumed Warp process", "task", taskID, "pid", pid)
	w.executor.logLine(taskID, "system", "Warp resumed")
	return true
}

// BuildCommand returns the shell command to start an interactive Warp session.
// sessionID is ignored: Warp conversations are not resumable from ty (see
// SupportsSessionResume), so a reconnect always starts a fresh conversation.
func (w *WarpExecutor) BuildCommand(task *db.Task, sessionID, prompt string) string {
	promptFile := ""
	if prompt != "" {
		var err error
		promptFile, err = writeWarpPromptFile(prompt)
		if err != nil {
			w.logger.Error("BuildCommand: failed to create temp file", "error", err)
			promptFile = ""
		}
	}
	return warpLaunchScript(task, w.executor.taskWorkdir(task), promptFile, "")
}

// ---- Session and Dangerous Mode Support ----

// SupportsSessionResume returns false. Warp's `--resume` takes a server-side
// conversation token, and the CLI does not expose that token to the process that
// launched it, so ty has nothing to store or rediscover.
func (w *WarpExecutor) SupportsSessionResume() bool {
	return false
}

// SupportsDangerousMode returns true - Warp supports --auto-approve.
func (w *WarpExecutor) SupportsDangerousMode() bool {
	return true
}

// FindSessionID returns empty - Warp conversation tokens are not discoverable
// from the filesystem.
func (w *WarpExecutor) FindSessionID(workDir string) string {
	return ""
}

// ResumeDangerous is not supported: toggling the flag means restarting Warp, and
// without a resumable session that would silently discard the conversation.
func (w *WarpExecutor) ResumeDangerous(task *db.Task, workDir string) bool {
	w.executor.logLine(task.ID, "system", "Warp cannot switch to auto-approve mid-session (no session resume); restart the task with dangerous mode enabled")
	return false
}

// ResumeSafe is not supported for the same reason as ResumeDangerous.
func (w *WarpExecutor) ResumeSafe(task *db.Task, workDir string) bool {
	w.executor.logLine(task.ID, "system", "Warp cannot leave auto-approve mid-session (no session resume); restart the task with dangerous mode disabled")
	return false
}
