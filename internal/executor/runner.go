package executor

import (
	"context"
	"os/exec"
)

// Runner builds commands for execution in some location.
//
// Every command ty runs to provision a task's workspace (git worktree, clone)
// or to create and drive the tmux session its agent lives in is built through a
// Runner rather than by calling exec.Command directly. That is the whole point
// of the type: without it, "this runs on the local machine" is spelled out at
// ~90 call sites and cannot be varied; with it, the assumption lives in exactly
// one implementation.
//
// Today there is one implementation, LocalRunner, and it is exactly the
// behaviour ty has always had. Nothing selects a different one, and nothing
// user-facing knows the concept exists.
type Runner interface {
	// Command builds a command to run in workDir.
	Command(ctx context.Context, workDir, name string, args ...string) *exec.Cmd
	// Target is "" for local execution.
	Target() string
}

// LocalRunner runs commands on this machine, in this process's environment.
//
// Command is deliberately a one-liner: it must stay byte-for-byte what the call
// sites used to do inline, so that routing them through the seam cannot change
// what ty does.
type LocalRunner struct{}

// Command builds a local command, run in workDir. An empty workDir leaves the
// process in the caller's working directory, matching a bare exec.Command.
func (LocalRunner) Command(ctx context.Context, workDir, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

// Target is "" — local execution has no remote to name.
func (LocalRunner) Target() string { return "" }

// defaultRunner is the runner the whole execution path resolves to.
//
// It is a package-level var rather than a constructor argument on purpose: the
// point of this change is that execution location is decided in ONE place, so
// that a later change can vary it there without threading a new parameter
// through the ~90 call sites that build commands.
var defaultRunner Runner = LocalRunner{}

// DefaultRunner returns the Runner used to build every command in the execution
// path. It is local, always, and there is no configuration that changes that.
//
// This is the seam: choosing where a task runs is a matter of returning a
// different Runner from here.
func DefaultRunner() Runner { return defaultRunner }

// runnerKey scopes a Runner to a context.
type runnerKey struct{}

// WithRunner returns a context whose commands are built through r.
//
// This is how a per-task placement decision reaches the ~90 call sites that build
// commands without any of them growing a parameter: the spawn path attaches the
// runner it chose to the task's context, and every command built from that context
// (or one derived from it) lands wherever the runner points. A context with no
// runner attached — which is every context in ty today — resolves to the local
// runner, so nothing changes for anyone who has not installed a placement handler.
func WithRunner(ctx context.Context, r Runner) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, runnerKey{}, r)
}

// RunnerFrom returns the Runner attached to ctx, or the default (local) runner.
func RunnerFrom(ctx context.Context) Runner {
	if ctx != nil {
		if r, ok := ctx.Value(runnerKey{}).(Runner); ok && r != nil {
			return r
		}
	}
	return DefaultRunner()
}

// detachedRunnerCtx returns a background context carrying ctx's runner.
//
// Several call sites deliberately give a tmux probe its own short timeout instead
// of inheriting the task's context, so that cancelling the task doesn't race the
// probe. Those contexts must still know WHERE to run, or a remotely-placed task
// would be polled against the local tmux server and look like it had vanished.
func detachedRunnerCtx(ctx context.Context) context.Context {
	return WithRunner(context.Background(), RunnerFrom(ctx))
}

// command builds a command in workDir through the context's runner, which is the
// local one unless a placement decision put another there.
func command(ctx context.Context, workDir, name string, args ...string) *exec.Cmd {
	return RunnerFrom(ctx).Command(ctx, workDir, name, args...)
}

// gitCmd builds `git <args>` to run in dir. An empty dir inherits the caller's
// working directory, as a bare exec.Command("git", ...) did.
func gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	return command(ctx, dir, "git", args...)
}

// tmuxCmd builds `tmux <args>`. A tmux command addresses a server rather than a
// directory, so it carries no workDir; the location it implies is the machine
// whose tmux server the runner reaches.
func tmuxCmd(ctx context.Context, args ...string) *exec.Cmd {
	return command(ctx, "", "tmux", args...)
}
