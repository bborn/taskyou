package executor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/tmuxctl"
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
	paths, stageErr := StageAttachments(ctx, e.db, task.ID, r.WorkDir, &r, nil)
	if stageErr != nil {
		return execResult{Message: "Could not stage attachments: " + stageErr.Error()}
	}
	prompt += AttachmentPrompt(paths)
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

	// The host's standing connection carries the agent's relayed MCP calls as
	// well as its screen, so it is opened now rather than at the first poll: an
	// agent that calls a taskyou tool the moment it starts must not find this
	// machine "offline" only because nothing had dialled the host yet.
	e.hostChannelFor(r.Host)

	// The agent gets a way to say it has finished, and its prompt gets told about
	// it. Without both halves the remote path is back to inferring completion from
	// a silent screen. A failure to install is not fatal — the idle heuristic is
	// still there underneath — but it is worth saying out loud, because the task
	// will then finish two minutes late for a reason nothing else would explain.
	signalReady := true
	if err := e.installSignalScript(ctx, r.WorkDir); err != nil {
		signalReady = false
		e.logLine(task.ID, "system", fmt.Sprintf(
			"Could not install the completion signal on %s (%v); falling back to idle detection, "+
				"so this task will park a couple of minutes after it actually finishes.", r.Host, err))
	}

	// Claude also gets its taskyou tools, relayed to this machine (mcpproxy.go).
	// Without them it still has the signal script, so a failure here costs the
	// tools, not the task.
	mcpConfig := ""
	if executorName == "claude" {
		if install, err := e.installMCPProxy(ctx, task, r.WorkDir); err != nil {
			e.logLine(task.ID, "system", fmt.Sprintf(
				"Could not give this task its taskyou tools on %s (%v); it will report through .ty/signal instead.", r.Host, err))
		} else {
			mcpConfig = install.ConfigPath
			e.logger.Info("relaying MCP for placed task", "task", task.ID, "host", r.Host, "servers", install.Servers)
		}
	}
	switch {
	case mcpConfig != "":
		prompt += proxyInstructions(controllerName())
	case signalReady:
		prompt += signalInstructions()
	default:
		prompt += fallbackFinishInstructions()
	}

	script, err := remoteLaunchScriptWith(task, executorName, r.WorkDir, prompt, mcpConfig, runID)
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
	return remoteLaunchScriptWith(task, executorName, workDir, prompt, "", runs...)
}

// remoteLaunchScriptWith is remoteLaunchScript plus the --mcp-config file that
// installMCPProxy staged ON THE HOST, when there is one. Unlike the local
// config, that file and everything it names exist where the agent runs.
func remoteLaunchScriptWith(task *db.Task, executorName, workDir, prompt, mcpConfig string, runs ...string) (string, error) {
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
	// Remote Control is Claude-only; for codex the flags are reset below, so
	// rcFlag is a no-op there. Threaded into flags before the prompt == "" early
	// return so both the prompt-bearing and empty-prompt branches emit it,
	// mirroring the local fresh-launch and resume paths (executor.go).
	flags := claudePermissionFlag(task) + rcFlag(task) + effortFlag(task.EffortLevel) + modelFlag(task.Model)
	if mcpConfig != "" {
		flags = "--mcp-config " + shellQuote(mcpConfig) + " " + flags
	}
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
	// Suppress the staged prompt arg for Remote Control so claude starts with a
	// blank, drivable session instead of running the staged prompt — matching the
	// local launch/resume sites. The flag itself is added above via rcFlag.
	// Remote Control is Claude-only (see rcFlag and the codex flags reset above):
	// codex never reads RemoteControl, so a codex task with Remote Control on must
	// still stage and clean up its prompt exactly like a Remote Control-off one.
	promptArg := fmt.Sprintf(`"$(cat %s)"; rm -f %s`, promptFile, promptFile)
	if task.RemoteControl && executorName == "claude" {
		promptArg = ""
	}
	return fmt.Sprintf(`%s %s %s%s`, env, executorName, flags, promptArg), nil
}

// SupportsRemoteExecutor reports whether a remote launch adapter is available.
func SupportsRemoteExecutor(name string) bool { return name == "claude" || name == "codex" }

// A coordinator owns its session namespace even when task IDs overlap on a host.
func findOrCreateRemoteDaemonSession(ctx context.Context, coordinator string) (string, error) {
	session := "task-daemon-remote-" + coordinator
	if err := tmuxCmd(ctx, "has-session", "-t", "="+session).Run(); err != nil {
		// Same size as a local agent session, for the same reason (see
		// tmuxctl.DefaultWidth): a detached session otherwise starts at tmux's
		// 80x24, the agent lays its whole session out for 80 columns, and the
		// first thing the user sees when they open the task is a screen written
		// for a terminal a third the width of theirs, reflowing as it attaches.
		args := append([]string{"new-session", "-d", "-s", session}, tmuxctl.DefaultSizeArgs()...)
		if err := tmuxCmd(ctx, append(args, "-n", "_placeholder", "tail", "-f", "/dev/null")...).Run(); err != nil {
			// Another task from this coordinator may have created it concurrently.
			if check := tmuxCmd(ctx, "has-session", "-t", "="+session).Run(); check != nil {
				return "", fmt.Errorf("create remote session: %w", err)
			}
		}
	}
	// Set on every launch, not only at creation: the windows of tasks placed
	// later must start at this size too, including in a session an older ty left
	// behind at 80x24.
	_ = tmuxCmd(ctx, "set-option", "-t", session, "default-size", tmuxctl.DefaultSize()).Run()
	tagSessionOwner(ctx, session)
	return session, nil
}
