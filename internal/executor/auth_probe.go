package executor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// authState is what a probe learned about an executor's login ON THE MACHINE
// the probe ran on.
//
// Three states, and the third is the important one. A probe can fail for
// reasons that say nothing about authentication — an older CLI without the
// subcommand, a host mid-reboot, a network blip — and treating "I could not
// tell" as "logged out" would park working tasks. Only a DEFINITE no blocks
// anything; everything else falls through to the way things ran before.
type authState int

const (
	authUnknown authState = iota
	authOK
	authLoggedOut
)

// authProbeTimeout bounds one probe. It is generous because the probe may be an
// ssh round trip to a fleet host plus the CLI's own startup.
const authProbeTimeout = 30 * time.Second

// authProbe is how ty asks one executor "are you logged in here?".
//
// Each CLI answers differently, so the classification travels with the command
// rather than being inferred from an exit status ty does not control. An
// executor with no entry is simply not probed — the screen-scraping detector in
// auth_check.go remains the backstop for those, as it is for everything.
type authProbe struct {
	args     []string
	classify func(out string, err error) authState
	// hint is the command a human runs to fix it, on the host in question.
	hint string
}

var authProbes = map[string]authProbe{
	db.ExecutorClaude: {
		args: []string{"claude", "auth", "status", "--json"},
		classify: func(out string, err error) authState {
			// The JSON answer is authoritative whatever the exit status; an exit
			// status without it (an older CLI that has no `auth status`) is not.
			switch compact := compactJSON(out); {
			case strings.Contains(compact, `"loggedin":false`):
				return authLoggedOut
			case strings.Contains(compact, `"loggedin":true`):
				return authOK
			}
			return authUnknown
		},
		hint: "claude auth login",
	},
	db.ExecutorCodex: {
		args: []string{"codex", "login", "status"},
		classify: func(out string, err error) authState {
			if err != nil {
				return authLoggedOut
			}
			return authOK
		},
		hint: "codex login",
	},
}

// compactJSON lowercases and strips whitespace so a needle matches whatever
// spacing the CLI happens to print.
func compactJSON(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, strings.ToLower(s))
}

// checkExecutorAuth asks whether executorName is logged in on whichever machine
// the context's runner points at, and returns the command a human would run
// there to fix it.
//
// It is deliberately a function of the RUNNER, not of the executor object: the
// interesting question is not "is claude logged in" but "is claude logged in on
// mona", and the runner is the only thing that knows where mona is.
func checkExecutorAuth(ctx context.Context, executorName string) (authState, string) {
	probe, ok := authProbes[executorName]
	if !ok {
		return authUnknown, ""
	}

	ctx, cancel := context.WithTimeout(ctx, authProbeTimeout)
	defer cancel()

	out, err := command(ctx, "", probe.args[0], probe.args[1:]...).CombinedOutput()
	text := string(out)

	// A CLI that is not installed there, or a probe we never got to run, is a
	// different problem with a different fix — and one the launch itself reports
	// perfectly well. Never call it a logout.
	if notInstalled(text, err) || ctx.Err() != nil {
		return authUnknown, probe.hint
	}
	return probe.classify(text, err), probe.hint
}

// notInstalled recognises a shell's "no such command" so a missing binary is
// never reported as a login problem.
func notInstalled(out string, err error) bool {
	lower := strings.ToLower(out)
	if strings.Contains(lower, "command not found") || strings.Contains(lower, "no such file or directory") {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 127 {
		return true
	}
	return false
}

// remoteAuthFailure is the message a human can act on without reading code: the
// executor, the host, and the exact command to run there.
func remoteAuthFailure(executorName, host, hint string) string {
	if hint == "" {
		return fmt.Sprintf("%s on %s is not logged in", executorName, host)
	}
	return fmt.Sprintf("%s on %s is not logged in — run: ssh %s -t %s", executorName, host, host, hint)
}

// taskExecutorName is the executor a task will actually run under.
func taskExecutorName(task *db.Task) string {
	if task != nil && task.Executor != "" {
		return task.Executor
	}
	return db.DefaultExecutor()
}
