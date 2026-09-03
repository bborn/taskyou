package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Moving a task means moving its WORK. Placement alone has never done that:
// `ty place` changes where the next spawn happens and says, correctly, that the
// worktree and the session stay on the host that made them. That is the right
// warning and the wrong feature for the case people actually hit — a task is
// running on another host and you want the code here, now, to run and look at.
//
// So the work travels through git, which is the only thing that can carry it
// between two machines that share nothing else: commit everything, push, and
// refuse to go any further unless both are verifiably true. The conversation
// does not travel; a handoff document takes its place. See
// docs/superpowers/specs/2026-09-03-task-move-design.md for why carrying the
// session verbatim was rejected.

// HandoffPath is where the outgoing agent's handoff lands, relative to the
// worktree. It is committed with the rest of the work, so it arrives on the
// target host by the same route everything else does.
const HandoffPath = ".taskyou/handoff.md"

// WorkSource is a task's work as it exists right now: a directory on a host,
// reachable through a Runner. The zero Host means this machine.
type WorkSource struct {
	Runner  Runner
	Host    string
	WorkDir string
}

// Where names the source host for a human.
func (s WorkSource) Where() string {
	if s.Host == "" {
		return "this machine"
	}
	return s.Host
}

// CarryReport is what a carry did, for the human who asked for it.
type CarryReport struct {
	Branch string
	Commit string
	// WIPCommit is true when uncommitted work had to be committed to travel.
	WIPCommit bool
	// LeftBehind are git-ignored files present in the worktree. They do NOT
	// move. Naming them is the whole point: an ignored file is discovered as an
	// absence on the far side, hours later, and never as an error.
	LeftBehind []string
}

// CarryWork puts every tracked file in the task's worktree onto its branch and
// pushes it, so the work exists somewhere both hosts can reach.
//
// It is deliberately paranoid at the end. Committing and pushing can each fail
// in ways that leave a plausible-looking worktree behind — a pre-commit hook
// that exits non-zero, a push rejected for being behind, a detached HEAD that
// swallows the commit where no branch will ever find it. So the last thing this
// does is re-read the state and prove the two facts that matter: nothing is
// uncommitted, and the branch tip is on the remote. Callers must treat an error
// here as "the move does not happen", because the alternative is a move that
// silently drops the work it promised to carry.
func CarryWork(ctx context.Context, src WorkSource, handoff, destination string) (CarryReport, error) {
	var rep CarryReport
	if src.Runner == nil {
		return rep, fmt.Errorf("no runner for %s", src.Where())
	}
	if strings.TrimSpace(src.WorkDir) == "" {
		return rep, fmt.Errorf("task has no worktree on %s to carry", src.Where())
	}

	branch, err := src.git(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return rep, fmt.Errorf("cannot read the branch in %s on %s: %w", src.WorkDir, src.Where(), err)
	}
	branch = strings.TrimSpace(branch)
	// A detached worktree is exactly how ty has lost work before: commits land on
	// no branch, the push has nothing to push, and the next host clones a branch
	// that never saw them. Refuse rather than "succeed".
	if branch == "" || branch == "HEAD" {
		return rep, fmt.Errorf("the worktree on %s is on a detached HEAD, so its commits belong to no branch; "+
			"check out the task's branch there before moving it", src.Where())
	}
	rep.Branch = branch

	if strings.TrimSpace(handoff) != "" {
		if err := src.writeFile(ctx, HandoffPath, handoff); err != nil {
			return rep, fmt.Errorf("could not write the handoff on %s: %w", src.Where(), err)
		}
	}

	if _, err := src.git(ctx, "add", "-A"); err != nil {
		return rep, fmt.Errorf("could not stage the work on %s: %w", src.Where(), err)
	}

	// "Is there anything staged" is a question git answers with an exit code, not
	// with output: 1 means there are differences. Anything else is a real error.
	staged, err := src.gitStatus(ctx, "diff", "--cached", "--quiet")
	if err != nil {
		return rep, err
	}
	if staged == 1 {
		msg := "wip: carry work to " + destination
		if destination == "" {
			msg = "wip: carry work"
		}
		if _, err := src.git(ctx, "commit", "--no-verify", "-m", msg); err != nil {
			return rep, fmt.Errorf("could not commit the work on %s: %w", src.Where(), err)
		}
		rep.WIPCommit = true
	}

	if _, err := src.git(ctx, "push", "--set-upstream", "origin", branch); err != nil {
		return rep, fmt.Errorf("could not push %s from %s, so the work cannot travel: %w", branch, src.Where(), err)
	}

	if err := src.verifyCarried(ctx, branch); err != nil {
		return rep, err
	}

	head, err := src.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return rep, err
	}
	rep.Commit = strings.TrimSpace(head)
	rep.LeftBehind = src.ignoredFiles(ctx)
	return rep, nil
}

// verifyCarried proves the work is somewhere the target host can reach it.
//
// Both halves matter. A clean worktree with an unpushed branch is work that
// exists only on a host we are about to stop using; a pushed branch with a dirty
// worktree is a move that leaves edits behind. Neither is a move.
func (s WorkSource) verifyCarried(ctx context.Context, branch string) error {
	dirty, err := s.git(ctx, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(dirty) != "" {
		return fmt.Errorf("the worktree on %s still has uncommitted changes after committing them, "+
			"so the move would leave work behind:\n%s", s.Where(), strings.TrimSpace(dirty))
	}

	local, err := s.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	remote, err := s.git(ctx, "rev-parse", "origin/"+branch)
	if err != nil {
		return fmt.Errorf("%s is not on origin after pushing it, so the target host cannot fetch the work: %w", branch, err)
	}
	if strings.TrimSpace(local) != strings.TrimSpace(remote) {
		return fmt.Errorf("origin/%s is at %s but the worktree on %s is at %s, "+
			"so the push did not carry everything",
			branch, short(remote), s.Where(), short(local))
	}
	return nil
}

// ignoredFiles lists what git is ignoring in the worktree — the files that do
// NOT travel. Best effort: failing to list them must not fail a move whose work
// is already safely pushed.
func (s WorkSource) ignoredFiles(ctx context.Context) []string {
	out, err := s.git(ctx, "status", "--porcelain", "--ignored")
	if err != nil {
		return nil
	}
	var found []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "!! ") {
			found = append(found, strings.TrimSpace(strings.TrimPrefix(line, "!!")))
		}
	}
	return found
}

// git runs a git command in the worktree and returns its stdout.
func (s WorkSource) git(ctx context.Context, args ...string) (string, error) {
	cmd := s.Runner.Command(ctx, s.WorkDir, "git", args...)
	return runCapturingStdout(cmd)
}

// gitStatus runs a git command whose ANSWER is its exit code, and returns that
// code. A non-zero exit is not an error here, so it must not be reported as one.
func (s WorkSource) gitStatus(ctx context.Context, args ...string) (int, error) {
	cmd := s.Runner.Command(ctx, s.WorkDir, "git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
}

// writeFile writes content to a path inside the worktree, on whichever host the
// worktree is on. It goes through the runner's stdin rather than os.WriteFile so
// that the local and remote paths are the same code.
func (s WorkSource) writeFile(ctx context.Context, relPath, content string) error {
	dir := "."
	if i := strings.LastIndex(relPath, "/"); i > 0 {
		dir = relPath[:i]
	}
	script := fmt.Sprintf("mkdir -p %s && cat > %s", shellQuote(dir), shellQuote(relPath))
	cmd := s.Runner.Command(ctx, s.WorkDir, "sh", "-c", script)
	cmd.Stdin = strings.NewReader(content)
	_, err := runCapturingStdout(cmd)
	return err
}

// runCapturingStdout runs a command and returns its stdout ALONE.
//
// Not CombinedOutput: ssh interleaves the remote process's stdout and stderr
// into one local stream in arrival order, so a parseable answer can end up with
// a progress line ("Preparing worktree...") in front of it or spliced into it.
// Every value this file reads back — a branch name, a commit sha — is parsed,
// so every one of them reads from its own buffer.
func runCapturingStdout(cmd *exec.Cmd) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg != "" {
			return stdout.String(), fmt.Errorf("%w: %s", err, msg)
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}

func short(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
