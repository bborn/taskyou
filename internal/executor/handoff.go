package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// The conversation does not travel across a move, so something has to. Asking
// the outgoing agent to write it is better than anything ty can synthesise: the
// agent knows which of the three things it tried actually worked and which file
// it was halfway through, and none of that is in the task log.
//
// The ask is made through the file system rather than by reading the agent's
// reply. ty tells the agent to write HandoffPath and then watches for the file
// to appear, which means no scraping of pane output, no prompt-format coupling,
// and a clean answer to "did it work": the file is there or it is not.

// HandoffRequest is the prompt sent to a live agent before its task moves.
func HandoffRequest(destination string) string {
	where := "another host"
	if destination != "" {
		where = destination
	}
	return fmt.Sprintf("This task is being moved to %s and your session will not come with it. "+
		"Write %s: what you were doing, what is done, what is left, and anything half-finished "+
		"that the next agent would otherwise have to rediscover. Be concrete — name files and "+
		"commands. Commit nothing; ty will carry the work. Write only that file, then stop.",
		where, HandoffPath)
}

// DefaultHandoffTimeout bounds the wait for an agent to write its handoff. A
// move is interactive and the agent may be mid-tool-call, so this is generous —
// but it is bounded, because the most common reason to move a task is that the
// agent is stuck, and a stuck agent must not block the move that rescues it.
const DefaultHandoffTimeout = 90 * time.Second

// handoffPollInterval is how often the worktree is checked for the file. Each
// check is an ssh round trip on a remote task, so it is not tight. It is a
// variable so tests can wait milliseconds instead of seconds; a test suite that
// sleeps for real makes every timing-sensitive test around it flakier.
var handoffPollInterval = 3 * time.Second

// RequestHandoff asks a live agent for a handoff and waits for it to appear.
//
// send delivers the prompt to the agent; a nil send (or one that fails) means
// there is no agent to ask, which is not an error — a task whose agent has died
// is exactly the task you most want to move. In every failing case this returns
// a synthesised handoff instead, so the target host always gets something.
func RequestHandoff(ctx context.Context, src WorkSource, task HandoffTask, destination string,
	send func(string) error, timeout time.Duration) (string, bool) {

	if timeout <= 0 {
		timeout = DefaultHandoffTimeout
	}
	if send == nil {
		return SyntheticHandoff(task, destination, "its agent was not running"), false
	}

	// Clear any handoff from an earlier move BEFORE asking. Only a file written
	// after the ask is an answer to it — and doing this after the send races the
	// agent, deleting the very file it was told to write.
	_ = src.removeFile(ctx, HandoffPath)

	if err := send(HandoffRequest(destination)); err != nil {
		return SyntheticHandoff(task, destination, "its agent could not be reached"), false
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return SyntheticHandoff(task, destination, "the move was interrupted while waiting for it"), false
		case <-time.After(handoffPollInterval):
		}
		if body, ok := src.readFile(ctx, HandoffPath); ok && strings.TrimSpace(body) != "" {
			return body, true
		}
	}
	return SyntheticHandoff(task, destination, "its agent did not answer in time"), false
}

// HandoffTask is the task detail a synthesised handoff can honestly state. It is
// deliberately small: everything else ty knows about a task is already in the
// task's own description on the far side.
type HandoffTask struct {
	ID    int64
	Title string
	Host  string
}

// SyntheticHandoff is the handoff written when the agent could not supply one.
//
// It says plainly that it is not the agent's account, and why. The failure mode
// worth avoiding is a confident-looking summary that the next agent trusts: ty
// does not know what the previous session was thinking, and pretending otherwise
// would send the new agent down a path nobody actually took.
func SyntheticHandoff(task HandoffTask, destination, because string) string {
	var b strings.Builder
	b.WriteString("# Handoff\n\n")
	fmt.Fprintf(&b, "Task #%d — %s\n\n", task.ID, task.Title)

	from := "another host"
	if task.Host != "" {
		from = task.Host
	}
	to := "this host"
	if destination != "" {
		to = destination
	}
	fmt.Fprintf(&b, "This task was moved from %s to %s.\n\n", from, to)
	fmt.Fprintf(&b, "**No handoff was written by the previous agent** because %s.\n\n", because)
	b.WriteString("So this document says nothing about what the last session was thinking, " +
		"and you should not assume it got anywhere in particular. What you can rely on:\n\n")
	b.WriteString("- Every tracked file it had, committed or not, is on this branch. " +
		"The last commit may be a `wip:` commit ty made to carry uncommitted work.\n")
	b.WriteString("- Files git ignores did NOT travel. If the task needs a `.env`, " +
		"a local database or built assets, they are not here.\n\n")
	b.WriteString("Read the diff on this branch before doing anything else — that is the " +
		"only reliable record of where the work got to.\n")
	return b.String()
}

// readFile reads a path inside the worktree on whichever host holds it.
func (s WorkSource) readFile(ctx context.Context, relPath string) (string, bool) {
	cmd := s.Runner.Command(ctx, s.WorkDir, "cat", relPath)
	out, err := runCapturingStdout(cmd)
	if err != nil {
		return "", false
	}
	return out, true
}

// removeFile deletes a path inside the worktree. Best effort: the file usually
// does not exist, which is not a failure.
func (s WorkSource) removeFile(ctx context.Context, relPath string) error {
	cmd := s.Runner.Command(ctx, s.WorkDir, "rm", "-f", relPath)
	_, err := runCapturingStdout(cmd)
	return err
}

// AgentSender returns a send function for a task's live agent, or nil when
// there is no agent to talk to.
//
// The target is built from what ty recorded when it started the session —
// daemon session plus the task's window name — rather than by asking tmux which
// pane is "current", which is the read that has cross-wired two tasks onto one
// pane before. Sending is best effort by design: every failure path here ends in
// a synthesised handoff, never in a failed move, because the tasks most worth
// moving are the ones whose agent has stopped answering.
func AgentSender(src WorkSource, daemonSession string, taskID int64) func(string) error {
	if strings.TrimSpace(daemonSession) == "" {
		return nil
	}
	target := daemonSession + ":" + TmuxWindowName(taskID)
	return func(prompt string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// Confirm the window is actually there before typing into it. Without
		// this, send-keys against a dead session "succeeds" quietly and the move
		// then waits out the full timeout for an answer that cannot come.
		if _, err := runCapturingStdout(src.Runner.Command(ctx, "", "tmux", "has-session", "-t", target)); err != nil {
			return fmt.Errorf("no live agent window at %s: %w", target, err)
		}
		if _, err := runCapturingStdout(src.Runner.Command(ctx, "", "tmux", "send-keys", "-t", target, prompt, "Enter")); err != nil {
			return fmt.Errorf("could not send the handoff request to %s: %w", target, err)
		}
		return nil
	}
}

// handoffSection is the handoff left by a moved session, ready to be put at the
// front of the new agent's prompt.
//
// It is read from the worktree rather than from the database because that is
// where it necessarily is: the handoff travelled as a committed file on the
// task's branch, which is the only channel a move has between two hosts.
//
// The file is consumed, not deleted — a later move overwrites it, and leaving it
// in place keeps the history of a task that has moved twice readable.
func (e *Executor) handoffSection(task *db.Task) string {
	if task == nil || strings.TrimSpace(task.WorktreePath) == "" {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(task.WorktreePath, HandoffPath))
	if err != nil || strings.TrimSpace(string(body)) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Handoff from the previous session\n\n")
	b.WriteString("This task was moved here from another host. Its conversation did not come\n")
	b.WriteString("with it — this is what the previous agent left you. Read it, and read the\n")
	b.WriteString("diff on this branch, before starting anything.\n\n")
	b.WriteString(strings.TrimSpace(string(body)))
	b.WriteString("\n\n---\n\n")
	return b.String()
}
