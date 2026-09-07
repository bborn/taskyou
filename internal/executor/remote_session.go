package executor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// runRemoteSession starts a task's agent inside a tmux session on the host a
// task.placement handler chose, and then watches it exactly as the local path
// watches a local one.
//
// Everything it runs is built through the placement's Runner, so the tmux server
// it talks to, the window it creates and the pane it polls are all on that host.
// Nothing here knows what a host IS: the name and the directory came from the
// handler's answer, and were checked once by Preflight before we got here.
func (e *Executor) runRemoteSession(ctx context.Context, task *db.Task, r RemoteRunner, executorName, prompt string) execResult {
	if !SupportsRemoteExecutor(executorName) {
		return execResult{Message: "Unsupported remote executor: " + executorName}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	probe := r.Command(probeCtx, "", "sh", "-c", "command -v "+shellQuote(executorName)+" >/dev/null")
	err := probe.Run()
	cancel()
	if err != nil {
		return execResult{Message: fmt.Sprintf("%s is not available on %s: %v", executorName, r.Host, err)}
	}
	coordinator, err := e.db.CoordinatorID()
	if err != nil {
		return execResult{Message: err.Error()}
	}
	runID, err := e.db.BeginRemoteRun(task.ID, r.Host)
	if err != nil {
		return execResult{Message: err.Error()}
	}
	// Every command built from this context lands on the placed host.
	ctx = WithRunner(ctx, r)

	// The agent gets a way to say it has finished, and its prompt gets told about
	// it. Without both halves the remote path is back to inferring completion from
	// a silent screen. A failure to install is not fatal — the idle heuristic is
	// still there underneath — but it is worth saying out loud, because the task
	// will then finish two minutes late for a reason nothing else would explain.
	if err := e.installSignalScript(ctx, r.WorkDir); err != nil {
		e.logLine(task.ID, "system", fmt.Sprintf(
			"Could not install the completion signal on %s (%v); falling back to idle detection, "+
				"so this task will park a couple of minutes after it actually finishes.", r.Host, err))
	} else {
		prompt += signalInstructions()
	}

	script, err := remoteLaunchScript(task, executorName, r.WorkDir, prompt, runID)
	script = "export WORKTREE_COORDINATOR_ID=" + shellQuote(coordinator) + " WORKTREE_RUN_ID=" + shellQuote(runID) + "; " + script
	if err != nil {
		e.logLine(task.ID, "error", err.Error())
		return execResult{Message: err.Error()}
	}

	// The prompt travels as a file, for the same reason it does locally: it is
	// arbitrary multi-line text and has no business being shell-quoted through
	// two shells.
	if err := e.writeRemotePrompt(ctx, task.ID, prompt, runID); err != nil {
		msg := fmt.Sprintf("Could not stage the prompt on %s: %v", r.Host, err)
		e.logLine(task.ID, "error", msg)
		return execResult{Message: msg}
	}

	daemonSession, err := findOrCreateRemoteDaemonSession(ctx, coordinator)
	if err != nil {
		msg := fmt.Sprintf("Could not create a tmux session on %s: %v", r.Host, err)
		e.logLine(task.ID, "error", msg)
		return execResult{Message: msg}
	}

	// Adopt-or-replace: a window left behind by an earlier run of this task on
	// this host would otherwise shadow the new one.
	windowName := TmuxWindowName(task.ID)
	if windows, listErr := tmuxCmd(ctx, "list-windows", "-t", daemonSession, "-F", "#{window_name}").Output(); listErr == nil {
		for _, name := range strings.Split(strings.TrimSpace(string(windows)), "\n") {
			if name == windowName {
				tmuxCmd(ctx, "kill-window", "-t", daemonSession+":"+windowName).Run()
			}
		}
	}

	out, err := tmuxCmd(ctx, "new-window", "-d",
		"-t", daemonSession,
		"-n", windowName,
		"-c", r.WorkDir,
		// A LOGIN shell: tmux execs this directly on the remote host, so nothing
		// else would read ~/.profile and the agent binary (claude lives in
		// ~/.local/bin on ol-agents) would simply not be on PATH. The window then
		// runs "claude: not found", exits in under a second, and the task parks as
		// "needs review" with nothing to explain it.
		"sh", "-lc", script).CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("Could not start the session on %s: %v (%s)", r.Host, err, strings.TrimSpace(string(out)))
		e.logLine(task.ID, "error", msg)
		return execResult{Message: msg}
	}

	windowTarget := daemonSession + ":" + windowName
	if err := e.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		e.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}
	e.logLine(task.ID, "system", fmt.Sprintf(
		"Running on %s in %s — attach with: ssh %s -t tmux attach -t %s",
		r.Host, r.WorkDir, r.Host, windowTarget))
	e.logger.Info("task placed remotely", "task", task.ID, "host", r.Host,
		"workdir", r.WorkDir, "window", windowTarget)

	return e.pollTmuxSession(ctx, task.ID, windowTarget)
}

// remotePromptPath is where a task's prompt is staged on the placed host.
func remotePromptPath(taskID int64, runs ...string) string {
	if len(runs) > 0 {
		return fmt.Sprintf("/tmp/ty-task-%d-%s-prompt.txt", taskID, runs[0])
	}
	return fmt.Sprintf("/tmp/ty-task-%d-prompt.txt", taskID)
}

// writeRemotePrompt streams the prompt to a file on the placed host.
func (e *Executor) writeRemotePrompt(ctx context.Context, taskID int64, prompt string, runs ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	path := remotePromptPath(taskID, runs...)
	cmd := command(ctx, "", "sh", "-c", "umask 077; set -C; cat > "+shellQuote(path))
	cmd.Stdin = strings.NewReader(prompt)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// remoteLaunchScript builds the shell line that starts the agent on the placed
// host.
//
// It deliberately does NOT reuse TaskExecutor.BuildCommand. That builder bakes
// in paths that only exist on this machine — the MCP config it writes, the
// CLAUDE_CONFIG_DIR it points at, the temp file it stages the prompt in — and
// every one of them would be a path the remote host does not have. What travels
// well is the task's identity, the permission/effort/model flags, and a prompt
// file we put there ourselves.
//
// Claude and Codex have remote launch adapters. Another executor is not silently
// downgraded to a local run: it fails, visibly, with a message saying why.
func remoteLaunchScript(task *db.Task, executorName, workDir, prompt string, runs ...string) (string, error) {
	if !SupportsRemoteExecutor(executorName) {
		return "", fmt.Errorf(
			"placement chose a remote host, but ty can launch Claude and Codex remotely, not %q (this task uses %q); "+
				"run it locally by removing the placement handler's answer for this project",
			executorName, executorName)
	}

	sessionID := os.Getenv("WORKTREE_SESSION_ID")
	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", os.Getpid())
	}

	env := fmt.Sprintf("WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%s",
		task.ID, sessionID, task.Port, shellQuote(workDir))
	flags := claudePermissionFlag(task) + effortFlag(task.EffortLevel) + modelFlag(task.Model)
	if executorName == "codex" {
		flags = ""
		if task.DangerousMode || os.Getenv("WORKTREE_DANGEROUS_MODE") == "1" {
			flags += "--dangerously-bypass-approvals-and-sandbox "
		}
		if task.Model != "" {
			flags += "--model " + shellQuote(task.Model) + " "
		}
	}

	if prompt == "" {
		return fmt.Sprintf("%s %s %s", env, executorName, flags), nil
	}
	promptFile := shellQuote(remotePromptPath(task.ID, runs...))
	return fmt.Sprintf(`%s %s %s"$(cat %s)"; rm -f %s`, env, executorName, flags, promptFile, promptFile), nil
}

// SupportsRemoteExecutor reports whether a remote launch adapter is available.
func SupportsRemoteExecutor(name string) bool { return name == "claude" || name == "codex" }

// A coordinator owns its session namespace even when task IDs overlap on a host.
func findOrCreateRemoteDaemonSession(ctx context.Context, coordinator string) (string, error) {
	session := "task-daemon-remote-" + coordinator
	if err := tmuxCmd(ctx, "has-session", "-t", "="+session).Run(); err != nil {
		if err := tmuxCmd(ctx, "new-session", "-d", "-s", session, "-n", "_placeholder", "tail", "-f", "/dev/null").Run(); err != nil {
			// Another task from this coordinator may have created it concurrently.
			if check := tmuxCmd(ctx, "has-session", "-t", "="+session).Run(); check != nil {
				return "", fmt.Errorf("create remote session: %w", err)
			}
		}
	}
	tagSessionOwner(ctx, session)
	return session, nil
}
