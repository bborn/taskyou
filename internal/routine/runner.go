package routine

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/events"
)

// outputTailLimit bounds how much run output is stored in the DB row; the
// full output always lands in the log file.
const outputTailLimit = 16 * 1024

// RunResult describes a finished routine run.
type RunResult struct {
	RunID    int64
	Status   string
	ExitCode int
	Output   string
	LogPath  string
	Duration time.Duration
}

// Runner executes routines and records their runs.
type Runner struct {
	DB      *db.DB
	Emitter *events.Emitter

	// ClaudeBin overrides the agent binary (tests use a stub). Empty = "claude".
	ClaudeBin string
	// Stdout, when set, receives a live copy of the run output (in addition to
	// the log file) so `ty run` invoked by hand shows progress.
	Stdout io.Writer
}

// Run executes the routine and records the run. A non-nil error means the run
// itself failed (agent exit != 0, env.sh failure, or timeout); the run row and
// failure alerting have already been handled when it returns.
func (r *Runner) Run(ctx context.Context, rt *Routine) (*RunResult, error) {
	stateDir := StateDir(rt.Name)
	logsDir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}

	runID, err := r.DB.CreateRoutineRun(rt.Name)
	if err != nil {
		return nil, fmt.Errorf("record run: %w", err)
	}
	logPath := filepath.Join(logsDir, fmt.Sprintf("run-%d.log", runID))
	if err := r.DB.SetRoutineRunLogPath(runID, logPath); err != nil {
		return nil, fmt.Errorf("record log path: %w", err)
	}

	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}
	defer logFile.Close()

	var tail tailBuffer
	out := io.MultiWriter(logFile, &tail)
	if r.Stdout != nil {
		out = io.MultiWriter(logFile, &tail, r.Stdout)
	}

	start := time.Now()
	exitCode, runErr := r.exec(ctx, rt, stateDir, out)
	result := &RunResult{
		RunID:    runID,
		Status:   db.RoutineRunStatusOK,
		ExitCode: exitCode,
		Output:   tail.String(),
		LogPath:  logPath,
		Duration: time.Since(start),
	}
	if runErr != nil {
		result.Status = db.RoutineRunStatusFailed
		fmt.Fprintf(logFile, "\n[ty] run failed: %v\n", runErr)
	}

	if err := r.DB.FinishRoutineRun(runID, result.Status, exitCode, result.Output); err != nil {
		return result, fmt.Errorf("record run result: %w", err)
	}

	if runErr != nil {
		r.alertFailure(rt, result, runErr)
		return result, runErr
	}
	return result, nil
}

// exec runs the agent process: bash sources env.sh (if present) for secrets
// and fail-fast checks, then execs the claude binary in headless print mode
// with the prompt on stdin. cwd is the routine's state directory.
func (r *Runner) exec(ctx context.Context, rt *Routine, stateDir string, out io.Writer) (int, error) {
	claudeBin := r.ClaudeBin
	if claudeBin == "" {
		claudeBin = "claude"
	}

	script := `set -euo pipefail
if [ -f "$TY_ROUTINE_DIR/env.sh" ]; then
  set -a
  . "$TY_ROUTINE_DIR/env.sh"
  set +a
fi
exec "$TY_ROUTINE_CLAUDE_BIN" -p --model "$TY_ROUTINE_MODEL" $TY_ROUTINE_PERMISSION_FLAGS`

	ctx, cancel := context.WithTimeout(ctx, rt.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Dir = stateDir
	cmd.Stdin = strings.NewReader(rt.Prompt)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Env = append(os.Environ(),
		"ROUTINE_NAME="+rt.Name,
		"ROUTINE_STATE_DIR="+stateDir,
		"TY_ROUTINE_DIR="+rt.Dir,
		"TY_ROUTINE_CLAUDE_BIN="+claudeBin,
		"TY_ROUTINE_MODEL="+rt.Model,
		"TY_ROUTINE_PERMISSION_FLAGS="+permissionFlags(rt.PermissionMode),
	)
	// Run in its own process group and kill the whole group on timeout so
	// agent child processes don't outlive a cancelled run.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	err := cmd.Run()
	exitCode := cmd.ProcessState.ExitCode()
	if ctx.Err() == context.DeadlineExceeded {
		return exitCode, fmt.Errorf("timed out after %s", rt.Timeout)
	}
	if err != nil {
		return exitCode, fmt.Errorf("agent exited with code %d", exitCode)
	}
	return exitCode, nil
}

func permissionFlags(mode string) string {
	switch db.NormalizePermissionMode(mode) {
	case db.PermissionModeAuto:
		return "--permission-mode auto"
	case db.PermissionModeAcceptEdits:
		return "--permission-mode acceptEdits"
	case db.PermissionModeDefault:
		return ""
	default:
		// Headless runs can't answer permission prompts; dangerous is the
		// routine default and the fallback for anything unrecognized.
		return "--dangerously-skip-permissions"
	}
}

// alertFailure surfaces a failed run where Bruno actually looks: as a pinned
// task on the board, plus a routine.failed event for hooks. The task is
// deduped by title so a routine failing every cycle doesn't flood the board.
func (r *Runner) alertFailure(rt *Routine, result *RunResult, runErr error) {
	if r.Emitter != nil {
		r.Emitter.Emit(events.Event{
			Type:    events.RoutineFailed,
			Message: fmt.Sprintf("Routine %q failed: %v", rt.Name, runErr),
			Metadata: map[string]interface{}{
				"routine":   rt.Name,
				"run_id":    result.RunID,
				"exit_code": result.ExitCode,
				"log_path":  result.LogPath,
			},
		})
	}

	title := fmt.Sprintf("Routine failed: %s", rt.Name)
	exists, err := r.DB.HasOpenTaskWithTitle(title)
	if err != nil || exists {
		return
	}

	tailLines := lastLines(result.Output, 20)
	body := fmt.Sprintf(`Routine `+"`%s`"+` failed: %v (exit code %d).

Last output:
`+"```\n%s\n```"+`

Full log: %s

- Inspect: `+"`ty routines show %s`"+`
- Re-run:  `+"`ty run %s`"+`
- Pause:   `+"`ty routines disable %s`"+`

This task was created automatically; it won't be recreated while it stays open.`,
		rt.Name, runErr, result.ExitCode, tailLines, result.LogPath, rt.Name, rt.Name, rt.Name)

	task := &db.Task{
		Title:   title,
		Body:    body,
		Status:  db.StatusBacklog,
		Project: rt.Project,
		Type:    "thinking",
		Pinned:  true,
	}
	// Alerting is best-effort: a failed insert (e.g. unknown project) must not
	// mask the original run error, which the caller still returns.
	_ = r.DB.CreateTask(task)
}

// tailBuffer keeps the last outputTailLimit bytes written to it.
type tailBuffer struct {
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > outputTailLimit {
		t.buf = t.buf[len(t.buf)-outputTailLimit:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
