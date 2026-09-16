// Package agentsend delivers a prompt to a task's live agent.
//
// Every surface that talks to a running agent — the TUI's retry-with-feedback,
// the web API's task input, the browser-annotation nudge, `ty input` — used to
// type into tmux itself, and each one had the same two holes:
//
//   - It aimed at the pane id stored on the task row. tmux reuses pane ids, so a
//     row whose agent has since gone away can name a pane belonging to another
//     task — which has cross-wired two tasks onto one pane before. The pane is
//     found here by asking tmux which pane carries the task's tag
//     (tmuxctl.PaneTaskOption), and a task with no tagged pane is refused rather
//     than typed at.
//   - It sent the text and its Enter as two unsynchronised calls, so two nudges
//     arriving together interleaved into one garbled line, and multi-line text
//     submitted itself a line at a time. Here the text goes in as one bracketed
//     paste and the Enter follows it under a per-pane lock.
package agentsend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/tmuxctl"
)

// Runner runs a command. It is deliberately the same shape as
// web.CommandRunner, so the web server hands over the runner it already has —
// one that knows which tmux server TaskYou's agents live on — and its tests
// hand over the fake they already have.
type Runner interface {
	Run(name string, args ...string) error
	Output(name string, args ...string) ([]byte, error)
}

// Store is the slice of the database this package needs. An interface so a test
// can drive the busy check and the turn wait without a real database, and so it
// is obvious that nothing here writes to the task row.
type Store interface {
	GetTask(id int64) (*db.Task, error)
	AgentTurnState(taskID int64) (db.AgentTurn, error)
	WaitForAgentReply(ctx context.Context, taskID int64, since db.AgentTurn, timeout time.Duration) (db.AgentTurn, error)
}

// NoPaneError means no pane on the agent server carries this task's tag: its
// agent is not running, or is running somewhere this process cannot see. The
// stored pane id is deliberately NOT used as a fallback — whatever it names now
// is not known to be this task's agent.
type NoPaneError struct{ TaskID int64 }

func (e *NoPaneError) Error() string {
	return fmt.Sprintf("task #%d has no live agent pane", e.TaskID)
}

// ErrNoPane matches any NoPaneError under errors.Is.
var ErrNoPane = errors.New("no live agent pane")

func (e *NoPaneError) Is(target error) bool { return target == ErrNoPane }

// BusyError means the agent is mid-turn. Surfaces show this to the user rather
// than dropping the text into a pane that is already producing output, and can
// resend with Force when interrupting is the point.
type BusyError struct {
	TaskID int64
	Status string
}

func (e *BusyError) Error() string {
	return fmt.Sprintf("task #%d's agent is busy (status %s)", e.TaskID, e.Status)
}

// ErrBusy matches any BusyError under errors.Is.
var ErrBusy = errors.New("agent busy")

func (e *BusyError) Is(target error) bool { return target == ErrBusy }

// Sender delivers prompts to agents. The zero value is unusable; see New.
type Sender struct {
	runner Runner
	store  Store
	// host namespaces the per-pane lock. Pane ids are only unique within one
	// tmux server, so a remote host's "%3" and this machine's "%3" must not
	// queue behind each other.
	host string
}

// New returns a Sender for this machine's agent server. store may be nil, which
// disables the busy check and the turn wait — for callers that have a tmux
// runner and nothing else.
func New(runner Runner, store Store) *Sender {
	return &Sender{runner: runner, store: store}
}

// NewForHost returns a Sender whose runner reaches another machine's tmux, for a
// task placed on a remote host. Its panes are resolved there by whoever holds
// the connection (see SendToPane); everything else — the busy check, the single
// paste, the lock — is the same.
func NewForHost(runner Runner, store Store, host string) *Sender {
	return &Sender{runner: runner, store: store, host: host}
}

// Prompt is one delivery.
type Prompt struct {
	TaskID int64
	// Text is pasted as-is. Newlines and shell metacharacters survive: it
	// travels as a tmux buffer, never as key names.
	Text string
	// Force sends even while the agent is working. For surfaces that interrupt
	// on purpose — a user who has just been told the agent is busy and said to
	// send anyway.
	Force bool
	// Submit presses Enter after the paste. False leaves the text sitting in the
	// agent's input box, which is what `ty input --no-submit` is for.
	Submit bool
}

// Send delivers p to the task's tagged agent pane.
//
// Returns *NoPaneError when no pane carries the task's tag, and *BusyError when
// the agent is working and Force is not set.
func (s *Sender) Send(p Prompt) error {
	pane, err := s.AgentPane(p.TaskID)
	if err != nil {
		return err
	}
	return s.SendToPane(pane, p)
}

// SendToPane is Send for a pane the caller has already resolved. It exists for
// remote tasks, whose pane lives on another machine's tmux server and is found
// by the code that owns that connection — the busy check, the single paste and
// the per-pane lock are the same, so a remote task gets the same guarantees as a
// local one instead of its own hand-rolled send.
func (s *Sender) SendToPane(pane string, p Prompt) error {
	if pane == "" {
		return &NoPaneError{TaskID: p.TaskID}
	}
	if err := s.checkIdle(p); err != nil {
		return err
	}
	return s.deliver(pane, p.Text, p.Submit)
}

// SendAndWait delivers p and waits for the agent's answer to it.
//
// The turn counters are read BEFORE the prompt goes out, so the Stop hook of
// whatever the agent was doing a moment ago cannot be mistaken for the reply:
// only a turn that starts after this send, and then finishes, counts. Returns
// db.ErrReplyTimeout if the agent does not answer in time — the prompt was still
// delivered.
func (s *Sender) SendAndWait(ctx context.Context, p Prompt, timeout time.Duration) (db.AgentTurn, error) {
	return s.sendAndWait(ctx, "", p, timeout)
}

// SendToPaneAndWait is SendAndWait for a pane the caller has already resolved.
// See SendToPane.
func (s *Sender) SendToPaneAndWait(ctx context.Context, pane string, p Prompt, timeout time.Duration) (db.AgentTurn, error) {
	return s.sendAndWait(ctx, pane, p, timeout)
}

func (s *Sender) sendAndWait(ctx context.Context, pane string, p Prompt, timeout time.Duration) (db.AgentTurn, error) {
	if s.store == nil {
		return db.AgentTurn{}, errors.New("agentsend: waiting for a reply needs a store")
	}
	before, err := s.store.AgentTurnState(p.TaskID)
	if err != nil {
		return db.AgentTurn{}, err
	}
	send := s.Send
	if pane != "" {
		send = func(p Prompt) error { return s.SendToPane(pane, p) }
	}
	if err := send(p); err != nil {
		return db.AgentTurn{}, err
	}
	return s.store.WaitForAgentReply(ctx, p.TaskID, before, timeout)
}

// SendKeys presses tmux key names in the task's tagged agent pane — "Enter",
// "Escape", "Up". Keystrokes are not prompts: they are how a human answers a
// menu the agent is showing, so there is no busy check. The pane is still
// resolved by tag, because a keypress into a stranger's pane is no better than
// a prompt into one.
func (s *Sender) SendKeys(taskID int64, keys ...string) error {
	pane, err := s.AgentPane(taskID)
	if err != nil {
		return err
	}
	return s.SendKeysToPane(pane, keys...)
}

// SendKeysToPane is SendKeys for a pane the caller has already resolved. See
// SendToPane.
func (s *Sender) SendKeysToPane(pane string, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if pane == "" {
		return &NoPaneError{}
	}
	unlock := lockPane(s.host, pane)
	defer unlock()
	return s.runner.Run("tmux", append([]string{"send-keys", "-t", pane}, keys...)...)
}

// checkIdle refuses a prompt aimed at an agent that is mid-turn.
func (s *Sender) checkIdle(p Prompt) error {
	if p.Force || s.store == nil {
		return nil
	}
	task, err := s.store.GetTask(p.TaskID)
	if err != nil {
		return fmt.Errorf("read task #%d: %w", p.TaskID, err)
	}
	if task != nil && task.Status == db.StatusProcessing {
		return &BusyError{TaskID: p.TaskID, Status: task.Status}
	}
	return nil
}

// AgentPane returns the pane tagged as this task's agent, or *NoPaneError.
func (s *Sender) AgentPane(taskID int64) (string, error) {
	return TaggedPane(s.runner, taskID, tmuxctl.RoleAgent)
}

// TaggedPane asks tmux which pane on the agent server carries this task's tag in
// the given role. Every pane on the server is listed, not just the ones in the
// task's window: the window may have been renamed, moved between daemon
// generations, or joined into a view, and the tag survives all three.
func TaggedPane(runner Runner, taskID int64, role string) (string, error) {
	out, err := runner.Output("tmux", "list-panes", "-a", "-F",
		"#{pane_id} #{"+tmuxctl.PaneTaskOption+"} #{"+tmuxctl.PaneRoleOption+"}")
	if err != nil {
		// No server, or tmux refused to answer. Either way there is no pane this
		// send can be proved to belong in.
		return "", &NoPaneError{TaskID: taskID}
	}
	want := strconv.FormatInt(taskID, 10)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 {
			continue
		}
		if fields[1] == want && fields[2] == role {
			return fields[0], nil
		}
	}
	return "", &NoPaneError{TaskID: taskID}
}

// pasteSettleDelay is the pause between the paste and its Enter. Agent TUIs
// debounce their input, and an Enter that arrives in the same breath as the text
// is swallowed as a newline instead of submitting. A variable so tests do not
// wait in real time.
var pasteSettleDelay = 100 * time.Millisecond

// bufferSeq keeps concurrent sends from naming the same tmux buffer.
var bufferSeq atomic.Int64

// deliver pastes text into pane and presses Enter, with nothing else allowed
// into that pane in between.
//
// The text travels as a tmux buffer pasted in bracketed-paste mode, not as
// send-keys arguments: a paste arrives as one input event, so multi-line text
// does not submit itself line by line, and nothing in it is read as a key name
// (a message of "Enter" used to press Enter). set-buffer rather than
// load-buffer because the buffer's contents arrive as an argument — the Runner
// seam every surface shares has no stdin, and both commands feed the same
// paste.
func (s *Sender) deliver(pane, text string, submit bool) error {
	unlock := lockPane(s.host, pane)
	defer unlock()

	if text != "" {
		buf := fmt.Sprintf("ty-prompt-%d-%d", os.Getpid(), bufferSeq.Add(1))
		if err := s.runner.Run("tmux", "set-buffer", "-b", buf, "--", text); err != nil {
			return fmt.Errorf("stage prompt for pane %s: %w", pane, err)
		}
		// -d drops the buffer afterwards so a long prompt is not left on the
		// server's paste stack; -p wraps it in bracketed-paste markers.
		if err := s.runner.Run("tmux", "paste-buffer", "-d", "-p", "-b", buf, "-t", pane); err != nil {
			// The buffer outlives a failed paste; take it back so a failed send
			// leaves nothing behind.
			_ = s.runner.Run("tmux", "delete-buffer", "-b", buf)
			return fmt.Errorf("paste prompt into pane %s: %w", pane, err)
		}
	}
	if !submit {
		return nil
	}
	if text != "" && pasteSettleDelay > 0 {
		time.Sleep(pasteSettleDelay)
	}
	if err := s.runner.Run("tmux", "send-keys", "-t", pane, "Enter"); err != nil {
		return fmt.Errorf("submit prompt in pane %s: %w", pane, err)
	}
	return nil
}

// Per-pane serialization. A prompt is several tmux calls that must not have
// another prompt's calls threaded through them; two prompts to DIFFERENT panes
// have no reason to wait for each other, so the lock is per pane rather than one
// global mutex.
var (
	paneLocksMu sync.Mutex
	paneLocks   = map[string]*paneLock{}
)

type paneLock struct {
	mu   sync.Mutex
	refs int
}

// lockPane takes the pane's lock and returns the release. host namespaces the
// key, because pane ids are only unique within one tmux server. The entry is
// dropped once the last waiter is gone, so a long-lived process does not
// accumulate one mutex per pane id tmux has ever issued.
func lockPane(host, pane string) func() {
	key := host + "\x00" + pane
	paneLocksMu.Lock()
	l := paneLocks[key]
	if l == nil {
		l = &paneLock{}
		paneLocks[key] = l
	}
	l.refs++
	paneLocksMu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		paneLocksMu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(paneLocks, key)
		}
		paneLocksMu.Unlock()
	}
}
