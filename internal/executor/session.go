package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executorlock"
)

// spawnLockTimeout bounds how long a spawner waits for the per-task executor
// spawn lock before proceeding best-effort. The critical section it guards (an
// existing-window check plus a single tmux new-window) is fast, so a holder
// should release well within this; the timeout only preserves liveness if a
// spawner wedges.
const spawnLockTimeout = 15 * time.Second

// executorSpawnLockDir is the directory the per-task spawn lock files live in.
// Co-located with the task DB so isolated instances (custom DB path) get their
// own lock namespace and don't contend with the real daemon.
func executorSpawnLockDir() string {
	return filepath.Dir(db.DefaultPath())
}

// sessionValidator is an optional capability for executors that can verify
// whether a stored session still exists. EnsureTaskWindow uses it to avoid
// resuming into a dead session (see the "lost executor pane" recovery there).
// Executors that don't implement it are always treated as resumable, preserving
// their existing behavior.
type sessionValidator interface {
	SessionExists(task *db.Task, sessionID string) bool
}

// EnsureTaskWindow ensures a tmux window running the task's executor session
// exists in a task-daemon session, creating the daemon session and window if
// needed. It is shared by the TUI detail view and the HTTP API so the
// interactive-session bootstrap logic lives in exactly one place.
//
// sessionID optionally resumes an existing executor session (e.g. a stored
// Claude session ID). handoffContext is prepended to the prompt when starting
// fresh (used when switching executors mid-task).
//
// Returns the tmux window target and whether a new window was created. For an
// existing window the target is "session:index"; for a freshly created one it
// is "session:windowName".
func (e *Executor) EnsureTaskWindow(ctx context.Context, task *db.Task, sessionID, handoffContext string) (string, bool, error) {
	if task == nil {
		return "", false, fmt.Errorf("task not available")
	}

	windowName := TmuxWindowName(task.ID)

	// Reuse an existing window in any task-daemon session to avoid duplicates.
	if target := findExistingTaskWindow(ctx, windowName); target != "" {
		return target, false, nil
	}

	// Serialize the check-then-create against the daemon executor and any other
	// caller so two spawners can't both observe "no window yet" and each create
	// one — the double-spawn that leaves two executor sessions in one worktree with
	// clobbered pane ids. Held only around the spawn decision; released as soon as
	// the window (or shell pane) is created. Best-effort: on timeout we proceed
	// rather than wedge the UI, but re-check under whatever lock we did get.
	if release, lerr := executorlock.AcquireSpawn(executorSpawnLockDir(), task.ID, spawnLockTimeout); lerr == nil {
		defer release()
		// Another spawner may have created the window while we waited for the lock.
		if target := findExistingTaskWindow(ctx, windowName); target != "" {
			return target, false, nil
		}
	} else {
		e.logger.Warn("could not acquire executor spawn lock; proceeding best-effort", "task", task.ID, "error", lerr)
	}

	daemonSession, err := findOrCreateDaemonSession(ctx)
	if err != nil {
		return "", false, err
	}

	workDir := e.taskWorkdir(task)

	taskExecutor := e.GetTaskExecutor(task)
	if taskExecutor == nil {
		return "", false, fmt.Errorf("no executor configured for task")
	}

	// Don't resume a session that no longer exists on disk. Claude prunes session
	// JSONLs after cleanupPeriodDays (default 30), so a long-parked task's stored
	// session ID usually points at a deleted file. Resuming it runs
	// `claude --resume <id>` against a missing session; claude exits immediately,
	// leaving a dead "lost executor pane". Fall back to a fresh session and clear
	// the stale ID — the same recovery the daemon launch path already performs.
	if sessionID != "" {
		if v, ok := taskExecutor.(sessionValidator); ok && !v.SessionExists(task, sessionID) {
			// Record which config dir we checked. A mismatch here between processes
			// used to silently destroy live sessions (see NormalizeClaudeConfigEnv);
			// logging the dir makes any future divergence diagnosable, not invisible.
			checkedDir := e.claudePathsForTask(task).configDir
			e.db.AppendTaskLog(task.ID, "system", fmt.Sprintf("Session %s no longer exists in %s, starting fresh", sessionID, checkedDir))
			e.logger.Warn("stored session not found; starting fresh", "task", task.ID, "session", sessionID, "configDir", checkedDir, "project", task.Project)
			if err := e.db.UpdateTaskClaudeSessionID(task.ID, ""); err != nil {
				e.logger.Warn("failed to clear stale session ID", "task", task.ID, "error", err)
			}
			task.ClaudeSessionID = "" // keep the caller's in-memory copy consistent
			sessionID = ""
		}
	}

	// Build prompt with task details when starting fresh (no session to resume).
	var prompt string
	if sessionID == "" {
		var b strings.Builder
		if handoffContext != "" {
			b.WriteString(handoffContext)
		}
		b.WriteString(fmt.Sprintf("# Task: %s\n\n", task.Title))
		if task.Body != "" {
			b.WriteString(task.Body)
			b.WriteString("\n")
		}
		prompt = b.String()
	}

	script := taskExecutor.BuildCommand(task, sessionID, prompt)

	executorName := taskExecutor.Name()
	if sessionID != "" {
		e.db.AppendTaskLog(task.ID, "system", fmt.Sprintf("Reconnecting to %s session %s", executorName, sessionID))
	} else {
		e.db.AppendTaskLog(task.ID, "system", fmt.Sprintf("Starting new %s session", executorName))
	}

	err = exec.CommandContext(ctx, "tmux", "new-window", "-d",
		"-t", daemonSession,
		"-n", windowName,
		"-c", workDir,
		"sh", "-c", script).Run()
	if err != nil {
		return "", false, fmt.Errorf("tmux new-window failed: %w", err)
	}

	// Give tmux a moment to create the window before splitting it.
	time.Sleep(100 * time.Millisecond)

	windowTarget := daemonSession + ":" + windowName

	// Create the shell pane alongside the executor pane, in the user's shell
	// so it doesn't exit immediately.
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	if err := exec.CommandContext(ctx, "tmux", "split-window",
		"-h",
		"-t", windowTarget+".0",
		"-c", workDir,
		shell).Run(); err != nil {
		e.logger.Warn("split-window for shell pane failed", "window", windowTarget, "error", err)
	}

	exec.CommandContext(ctx, "tmux", "select-pane", "-t", windowTarget+".0", "-T", formatExecutorDisplayName(executorName, executorName)).Run()
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", windowTarget+".1", "-T", "Shell").Run()

	// Persist pane IDs so other clients (HTTP API, TUI) can target the panes.
	e.savePaneIDs(ctx, windowTarget, task.ID)
	if err := e.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		e.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}

	return windowTarget, true, nil
}

// taskWorkdir returns the directory an interactive session should start in:
// the task worktree when present, then the project directory, then home.
func (e *Executor) taskWorkdir(task *db.Task) string {
	if task.WorktreePath != "" {
		return task.WorktreePath
	}
	if task.Project != "" {
		if dir := e.GetProjectDir(task.Project); dir != "" {
			return dir
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

// findExistingTaskWindow looks for windowName in any task-daemon session and
// returns its "session:index" target, or "" when absent.
func findExistingTaskWindow(ctx context.Context, windowName string) string {
	out, err := exec.CommandContext(ctx, "tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_index}:#{window_name}").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[2] == windowName && strings.HasPrefix(parts[0], "task-daemon-") {
			return parts[0] + ":" + parts[1]
		}
	}
	return ""
}

// findOrCreateDaemonSession returns the name of an existing task-daemon
// session, creating one (with a placeholder window) when none exists.
func findOrCreateDaemonSession(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err == nil {
		for _, session := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.HasPrefix(session, "task-daemon-") {
				return session, nil
			}
		}
	}

	daemonSession := fmt.Sprintf("task-daemon-%d", os.Getpid())
	// "tail -f /dev/null" keeps the placeholder window alive (empty windows exit immediately).
	if err := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", daemonSession, "-n", "_placeholder", "tail", "-f", "/dev/null").Run(); err != nil {
		return "", fmt.Errorf("tmux new-session failed: %w", err)
	}
	return daemonSession, nil
}
