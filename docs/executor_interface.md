# Task Executor Interface

Task executors live in `internal/executor` and implement the `TaskExecutor` interface defined in `task_executor.go`. Every executor is responsible for running an AI CLI inside the Task You tmux daemon while reporting status back to the core `Executor`. This document captures the requirements for any new implementation.

## Interface Summary

| Method | Requirements |
|--------|--------------|
| `Name() string` | Return the identifier that will be stored on `db.Task.Executor` (e.g., `claude`, `codex`, `gemini`). |
| `IsAvailable() bool` | Perform a fast check (typically `exec.LookPath`) to ensure the underlying CLI is installed. Returning `false` prevents the UI/daemon from offering the executor. |
| `Execute(ctx, task, workDir, prompt)` | Start a brand-new session for the task. This must create/target the tmux daemon (`task-daemon-*`), spawn the CLI, attach the worktree environment variables (`WORKTREE_TASK_ID`, `WORKTREE_PORT`, `WORKTREE_PATH`, `WORKTREE_SESSION_ID`), ensure `ensureShellPane` and `configureTmuxWindow` are called, and then delegate polling to `Executor.pollTmuxSession`. All user-facing errors must be logged via `Executor.logLine`. |
| `Resume(ctx, task, workDir, prompt, feedback)` | Resume a previous session if the CLI supports it, otherwise rerun using the full prompt + formatted feedback (Codex/Gemini do this). Should follow the same tmux setup as `Execute`. |
| `BuildCommand(task, sessionID, prompt)` | Produce the exact shell command that the UI runs when a user manually opens the executor pane. This needs to mirror the environment + dangerous-mode behavior of `Execute` so the UI experience matches background execution. Remember to clean up temporary prompt files. |
| `IsAvailable`/`GetProcessID`/`Kill` | `GetProcessID` must introspect tmux panes and operating-system processes to find the PID tied to the executor pane. `Kill` sends a SIGTERM (or CLI-specific shutdown) and clears any suspension bookkeeping so Task You can reclaim memory. |
| `Suspend`/`IsSuspended`/`ResumeProcess` | Used by the idle-suspension logic. Implementations typically send SIGTSTP/SIGCONT and track timestamps in a `suspendedTasks` map so the executor knows whether it needs to resume. |

## Common Expectations

- **tmux integration** – Executors must create or reuse the daemon session via `ensureTmuxDaemon`, name windows with `TmuxWindowName(taskID)`, and call `killAllWindowsByNameAllSessions` before launching a new window to avoid duplicates.
- **Environment variables** – Always export `WORKTREE_TASK_ID`, `WORKTREE_SESSION_ID`, `WORKTREE_PORT`, and `WORKTREE_PATH` when invoking the CLI. Pass along `CLAUDE_CONFIG_DIR` if you rely on shared `.claude` state (this keeps attachments + MCP permissions consistent across worktrees).
- **Dangerous mode** – Honor both the persisted `task.DangerousMode` flag and the daemon-wide `WORKTREE_DANGEROUS_MODE=1`. If the CLI needs explicit flags, gate them behind helpers (see `buildGeminiDangerousFlag`) so the UI command builder and daemon execution stay in sync. Expose environment overrides (`GEMINI_DANGEROUS_ARGS` for the Gemini CLI) when the CLI uses different switches.
- **Logging** – Always send user-facing errors to `Executor.logLine` with a helpful message plus remediation link when possible. Silent failures make it hard to debug executor issues.
- **Shell pane** – Call `ensureShellPane` and `configureTmuxWindow` after launching a window so users always get the split view (executor + shell) with the correct env vars preloaded.
- **Window tracking** – Save the daemon session and tmux window IDs via `UpdateTaskDaemonSession` and `UpdateTaskWindowID`. This allows later calls (kill, retry, resume) to target the correct panes.
- **Prompt handling** – When passing prompts/feedback to CLIs, write them to temp files to avoid shell-quoting issues and remember to `defer os.Remove` once the command finishes.

## Gemini CLI Notes

The Gemini executor follows the same pattern as Codex: it starts a fresh CLI process for every run and replays the full prompt with appended feedback during retries. Gemini's dangerous-mode flag defaults to `--dangerously-allow-run` but can be overridden via the `GEMINI_DANGEROUS_ARGS` environment variable if Google updates the CLI syntax.

Review `internal/executor/gemini_executor.go` for a reference implementation that satisfies all of the above requirements.
