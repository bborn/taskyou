package agentsend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// fakeTmux records what was run and, importantly, takes its time doing it: a
// zero-cost fake cannot show that two concurrent sends stay out of each other's
// way, because nothing ever overlaps.
//
// It also simulates tmux's per-pane wait-for lock — the one the production
// deliver now holds while it issues set-buffer / paste-buffer / send-keys
// Enter. wait-for -L <channel> blocks on a per-channel sync.Mutex; -U
// <channel> releases it. Two Senders (two ty processes' worth) sharing one
// fakeTmux therefore serialize on the same channel the way two real ty
// processes serialize on the same tmux server, which is the case the bug is
// about: there is no longer any in-process lock to do the job for free.
type fakeTmux struct {
	mu    sync.Mutex
	calls [][]string
	panes string // list-panes output
	err   error
	delay time.Duration

	// locks maps each wait-for channel to a real mutex that simulates the
	// tmux server's lock state for that channel.
	lockMu sync.Mutex
	locks  map[string]*sync.Mutex
}

func (f *fakeTmux) record(name string, args []string) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	delay := f.delay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
}

func (f *fakeTmux) Run(name string, args ...string) error {
	// wait-for is simulated first: a real -L blocks until the channel is free,
	// then claims it; -U releases. The recorded order only reflects what an
	// outside observer (the tmux server) actually saw, so a -L is logged AFTER
	// the lock is held and a -U AFTER it has been let go.
	if len(args) > 0 && args[0] == "wait-for" {
		if len(args) >= 2 && args[1] == "-L" {
			if f.err != nil {
				return f.err
			}
			f.lockAcquire(args[len(args)-1])
			f.record(name, args)
			return f.err
		}
		if len(args) >= 2 && args[1] == "-U" {
			f.record(name, args)
			f.lockRelease(args[len(args)-1])
			return f.err
		}
	}
	f.record(name, args)
	return f.err
}

// lockAcquire claims the named wait-for channel, blocking while another caller
// holds it, the way `tmux wait-for -L` does on a real server.
func (f *fakeTmux) lockAcquire(ch string) {
	f.lockMu.Lock()
	m, ok := f.locks[ch]
	if !ok {
		m = &sync.Mutex{}
		if f.locks == nil {
			f.locks = map[string]*sync.Mutex{}
		}
		f.locks[ch] = m
	}
	f.lockMu.Unlock()
	m.Lock()
}

// lockRelease frees the named wait-for channel so the next waiter can proceed.
func (f *fakeTmux) lockRelease(ch string) {
	f.lockMu.Lock()
	m, ok := f.locks[ch]
	f.lockMu.Unlock()
	if ok {
		m.Unlock()
	}
}

func (f *fakeTmux) Output(name string, args ...string) ([]byte, error) {
	f.record(name, args)
	if len(args) > 0 && args[0] == "list-panes" {
		if f.panes == "" {
			return nil, errors.New("no server running")
		}
		return []byte(f.panes), nil
	}
	return nil, nil
}

func (f *fakeTmux) snapshot() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.calls...)
}

// sends returns the recorded calls with the pane lookups (list-panes) and the
// per-pane lock (wait-for) filtered out, so the structure of one prompt's
// paste/Enter sequence is easy to check on its own.
func (f *fakeTmux) sends() [][]string {
	var out [][]string
	for _, c := range f.snapshot() {
		if len(c) > 1 && (c[1] == "list-panes" || c[1] == "wait-for") {
			continue
		}
		out = append(out, c)
	}
	return out
}

// lockCalls returns just the recorded wait-for calls, in order, so a test can
// check the per-pane lock is acquired and released around a delivery without
// picking through the paste/Enter calls. Each entry is `[tmux wait-for -L|-U
// <channel>]`.
func (f *fakeTmux) lockCalls() [][]string {
	var out [][]string
	for _, c := range f.snapshot() {
		if len(c) > 1 && c[1] == "wait-for" {
			out = append(out, c)
		}
	}
	return out
}

// fakeStore answers the two questions a send asks the database.
type fakeStore struct {
	task *db.Task
	turn db.AgentTurn
}

func (s *fakeStore) GetTask(int64) (*db.Task, error) { return s.task, nil }

func (s *fakeStore) AgentTurnState(int64) (db.AgentTurn, error) { return s.turn, nil }

func (s *fakeStore) WaitForAgentReply(ctx context.Context, _ int64, since db.AgentTurn, _ time.Duration) (db.AgentTurn, error) {
	return since, nil
}

func idleStore() *fakeStore {
	return &fakeStore{task: &db.Task{ID: 7, Status: db.StatusBlocked}}
}

func init() { pasteSettleDelay = 0 }

// The pane id on the task row is the one thing that must not decide where a
// prompt goes: tmux hands a dead task's id to the next pane it opens.
func TestSendGoesToTheTaggedPaneNotTheStoredOne(t *testing.T) {
	tmux := &fakeTmux{panes: strings.Join([]string{
		"%3 9 agent", // another task's agent — the id task 7 used to hold
		"%8 7 agent", // task 7's agent, where this must land
		"%9 7 shell", // task 7's shell
	}, "\n")}
	store := idleStore()
	store.task.ClaudePaneID = "%3" // stale: that pane belongs to task 9 now

	if err := New(tmux, store).Send(Prompt{TaskID: 7, Text: "ship it", Submit: true}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	for _, call := range tmux.sends() {
		for i, arg := range call {
			if arg == "-t" && i+1 < len(call) && call[i+1] != "%8" {
				t.Fatalf("prompt aimed at %q, want the tagged pane %%8: %v", call[i+1], call)
			}
		}
	}
}

func TestSendRefusesWhenNoPaneCarriesTheTag(t *testing.T) {
	tmux := &fakeTmux{panes: "%3 9 agent\n%4 9 shell"}
	store := idleStore()
	store.task.ClaudePaneID = "%3"

	err := New(tmux, store).Send(Prompt{TaskID: 7, Text: "ship it", Submit: true})
	if !errors.Is(err, ErrNoPane) {
		t.Fatalf("err = %v, want ErrNoPane", err)
	}
	if got := tmux.sends(); len(got) != 0 {
		t.Fatalf("refused send still typed into tmux: %v", got)
	}
}

func TestSendRefusesWhenNoTmuxServerIsThere(t *testing.T) {
	tmux := &fakeTmux{} // Output fails, as it does with no server running
	if err := New(tmux, idleStore()).Send(Prompt{TaskID: 7, Text: "hi", Submit: true}); !errors.Is(err, ErrNoPane) {
		t.Fatalf("err = %v, want ErrNoPane", err)
	}
}

func TestSendRefusesAWorkingAgentUnlessForced(t *testing.T) {
	tmux := &fakeTmux{panes: "%8 7 agent"}
	store := &fakeStore{task: &db.Task{ID: 7, Status: db.StatusProcessing}}
	sender := New(tmux, store)

	err := sender.Send(Prompt{TaskID: 7, Text: "stop that", Submit: true})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if got := tmux.sends(); len(got) != 0 {
		t.Fatalf("refused send still typed into tmux: %v", got)
	}

	if err := sender.Send(Prompt{TaskID: 7, Text: "stop that", Force: true, Submit: true}); err != nil {
		t.Fatalf("forced Send: %v", err)
	}
	if got := tmux.sends(); len(got) == 0 {
		t.Fatal("forced send delivered nothing")
	}
}

// Multi-line text and tmux key names have to arrive as text. A prompt sent with
// send-keys per line submits itself line by line, and "Enter" typed as a
// message used to press Enter.
func TestPromptTravelsAsOneBracketedPaste(t *testing.T) {
	tmux := &fakeTmux{panes: "%8 7 agent"}
	text := "line one\nline two — $HOME; `date`\nEnter"

	if err := New(tmux, idleStore()).Send(Prompt{TaskID: 7, Text: text, Submit: true}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	calls := tmux.sends()
	if len(calls) != 3 {
		t.Fatalf("want set-buffer, paste-buffer, Enter; got %v", calls)
	}
	if calls[0][1] != "set-buffer" || calls[0][len(calls[0])-1] != text {
		t.Errorf("text not staged verbatim as one buffer: %v", calls[0])
	}
	if calls[1][1] != "paste-buffer" || !hasArg(calls[1], "-p") {
		t.Errorf("not pasted in bracketed-paste mode: %v", calls[1])
	}
	if fmt.Sprint(calls[2][1:]) != fmt.Sprint([]string{"send-keys", "-t", "%8", "Enter"}) {
		t.Errorf("Enter call = %v", calls[2])
	}
	for _, c := range calls {
		if c[1] == "send-keys" && hasArg(c, "-l") {
			t.Errorf("prompt text went through send-keys: %v", c)
		}
	}
}

func TestNoSubmitLeavesTheTextUnsent(t *testing.T) {
	tmux := &fakeTmux{panes: "%8 7 agent"}
	if err := New(tmux, idleStore()).Send(Prompt{TaskID: 7, Text: "draft"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	for _, c := range tmux.sends() {
		if c[1] == "send-keys" {
			t.Fatalf("no-submit send pressed a key: %v", c)
		}
	}
}

// Two prompts to one pane must not interleave. The fake is slow on purpose:
// without serialization the second send's set-buffer lands between the first's
// paste and its Enter, and this fails. The serialization is the per-pane
// tmux wait-for lock (simulated by fakeTmux), which is the same channel every
// ty process reaches — so this test already exercises the cross-process
// guarantee the package now relies on, not the in-process one it used to.
func TestConcurrentSendsToOnePaneDoNotInterleave(t *testing.T) {
	tmux := &fakeTmux{panes: "%8 7 agent", delay: 20 * time.Millisecond}
	sender := New(tmux, idleStore())

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := sender.Send(Prompt{TaskID: 7, Text: fmt.Sprintf("prompt %d", i), Submit: true}); err != nil {
				t.Errorf("Send %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	calls := tmux.sends()
	if len(calls) != 12 {
		t.Fatalf("want 4 sends of 3 calls, got %v", calls)
	}
	for i := 0; i < len(calls); i += 3 {
		if calls[i][1] != "set-buffer" || calls[i+1][1] != "paste-buffer" || calls[i+2][1] != "send-keys" {
			t.Fatalf("sends interleaved at call %d: %v", i, calls)
		}
		// The paste must carry the buffer its own set-buffer staged.
		if argAfter(calls[i], "-b") != argAfter(calls[i+1], "-b") {
			t.Fatalf("paste used another send's buffer: %v / %v", calls[i], calls[i+1])
		}
	}
}

// Prompts to different tasks have no reason to queue behind each other.
func TestSendsToDifferentPanesRunConcurrently(t *testing.T) {
	tmux := &fakeTmux{panes: "%8 7 agent\n%9 8 agent", delay: 60 * time.Millisecond}
	sender := New(tmux, &fakeStore{task: &db.Task{Status: db.StatusBlocked}})

	start := time.Now()
	var wg sync.WaitGroup
	for _, id := range []int64{7, 8} {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			if err := sender.Send(Prompt{TaskID: id, Text: "hi", Submit: true}); err != nil {
				t.Errorf("Send %d: %v", id, err)
			}
		}(id)
	}
	wg.Wait()

	// The pane lookup itself is recorded, so each Send issues several delayed
	// calls (the list-panes lookup, the per-pane wait-for acquire, set-buffer,
	// paste-buffer, send-keys, and the wait-for release). Serialized end to
	// end that is well over ten delays; run in parallel it is about six, so a
	// generous cutoff distinguishes the two.
	if elapsed := time.Since(start); elapsed > 9*60*time.Millisecond {
		t.Errorf("sends to different panes serialized: took %v", elapsed)
	}
}

func TestSendKeysGoesToTheTaggedPane(t *testing.T) {
	tmux := &fakeTmux{panes: "%3 9 agent\n%8 7 agent"}
	if err := New(tmux, idleStore()).SendKeys(7, "Escape"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	got := tmux.sends()
	if len(got) != 1 || fmt.Sprint(got[0][1:]) != fmt.Sprint([]string{"send-keys", "-t", "%8", "Escape"}) {
		t.Fatalf("SendKeys = %v", got)
	}
}

// A keypress is how a human answers a menu the agent is showing, so it is not
// held back by the busy check the way a prompt is.
func TestSendKeysIsNotBusyChecked(t *testing.T) {
	tmux := &fakeTmux{panes: "%8 7 agent"}
	store := &fakeStore{task: &db.Task{ID: 7, Status: db.StatusProcessing}}
	if err := New(tmux, store).SendKeys(7, "Enter"); err != nil {
		t.Fatalf("SendKeys while busy: %v", err)
	}
}

func hasArg(call []string, want string) bool {
	for _, a := range call {
		if a == want {
			return true
		}
	}
	return false
}

func argAfter(call []string, flag string) string {
	for i, a := range call {
		if a == flag && i+1 < len(call) {
			return call[i+1]
		}
	}
	return ""
}

// Each delivery is held by a per-pane lock that lives on the tmux server: every
// ty process acquires `tmux wait-for -L <channel>` before its set-buffer /
// paste-buffer / send-keys Enter and releases with `-U`. The channel is per
// pane; here the pane "%8" becomes the channel suffix "8". The acquire has to
// come before the paste and the release after the Enter, so two of these
// delivers through one tmux server cannot interleave.
func TestDeliverAcquiresTheTmuxPaneLockAroundItsPaste(t *testing.T) {
	tmux := &fakeTmux{panes: "%8 7 agent"}
	if err := New(tmux, idleStore()).Send(Prompt{TaskID: 7, Text: "hi", Submit: true}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	lockCalls := tmux.lockCalls()
	if len(lockCalls) != 2 {
		t.Fatalf("want one acquire and one release, got %v", lockCalls)
	}
	acq, rel := lockCalls[0], lockCalls[1]
	if acq[2] != "-L" {
		t.Errorf("first lock call = %v, want wait-for -L", acq)
	}
	if rel[2] != "-U" {
		t.Errorf("release call = %v, want wait-for -U", rel)
	}
	ch := acq[len(acq)-1]
	if ch != rel[len(rel)-1] {
		t.Errorf("acquire and release on different channels: %v / %v", acq, rel)
	}
	wantCh := paneLockChannelPrefix + "8"
	if ch != wantCh {
		t.Errorf("channel = %q, want %q (the pane id %q stripped of %%)", ch, wantCh, "%8")
	}

	// The acquire precedes the set-buffer, the Enter precedes the release.
	calls := tmux.snapshot()
	var acqIdx, sbIdx, enterIdx, relIdx int
	for i, c := range calls {
		switch {
		case len(c) >= 2 && c[1] == "wait-for" && len(c) >= 3 && c[2] == "-L":
			acqIdx = i
		case len(c) >= 2 && c[1] == "set-buffer":
			sbIdx = i
		case len(c) >= 2 && c[1] == "send-keys":
			enterIdx = i
		case len(c) >= 2 && c[1] == "wait-for" && len(c) >= 3 && c[2] == "-U":
			relIdx = i
		}
	}
	if acqIdx >= sbIdx || sbIdx >= enterIdx || enterIdx >= relIdx {
		t.Fatalf("lock doesn't bracket the paste: acq=%d set=%d enter=%d rel=%d\ncalls=%v",
			acqIdx, sbIdx, enterIdx, relIdx, calls)
	}
}

// If the tmux server cannot give out the lock (it is gone, refused, etc.) the
// prompt is not delivered at all: typing half of a paste into a pane and then
// failing would be strictly worse than refusing the send, and the release is
// never issued because there was nothing to release.
func TestDeliverRefusesIfTheTmuxLockCannotBeAcquired(t *testing.T) {
	tmux := &fakeTmux{panes: "%8 7 agent", err: errors.New("tmux not running")}
	err := New(tmux, idleStore()).Send(Prompt{TaskID: 7, Text: "hi", Submit: true})
	if err == nil {
		t.Fatal("Send returned nil; want a lock-acquire error")
	}
	if got := tmux.sends(); len(got) != 0 {
		t.Errorf("paste ran with no lock held: %v", got)
	}
	if got := tmux.lockCalls(); len(got) != 0 {
		t.Errorf("lock calls recorded though acquire failed: %v", got)
	}
}

// A no-submit delivery still acquires and releases the lock — multi-line text
// sitting unsent in the pane's input box still has to arrive as one paste and
// not be split into pieces by another sender's paste.
func TestNoSubmitDeliveryStillHoldsThePerPaneLock(t *testing.T) {
	tmux := &fakeTmux{panes: "%8 7 agent"}
	if err := New(tmux, idleStore()).Send(Prompt{TaskID: 7, Text: "draft"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	lockCalls := tmux.lockCalls()
	if len(lockCalls) != 2 {
		t.Fatalf("want acquire+release even without Enter, got %v", lockCalls)
	}
	if lockCalls[0][2] != "-L" || lockCalls[1][2] != "-U" {
		t.Errorf("lock calls not -L then -U: %v", lockCalls)
	}
	// No send-keys ran (the text was not submitted); only set-buffer + paste-buffer.
	for _, c := range tmux.sends() {
		if c[1] == "send-keys" {
			t.Errorf("no-submit send pressed Enter: %v", c)
		}
	}
}

// SendKeys rides the same per-pane lock — a press of "Escape" between two
// prompts to the same pane must not thread through the prompts' calls.
func TestSendKeysHoldsThePerPaneTmuxLock(t *testing.T) {
	tmux := &fakeTmux{panes: "%8 7 agent"}
	if err := New(tmux, idleStore()).SendKeys(7, "Escape"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	lockCalls := tmux.lockCalls()
	if len(lockCalls) != 2 {
		t.Fatalf("want acquire+release around SendKeys, got %v", lockCalls)
	}
	if lockCalls[0][2] != "-L" || lockCalls[1][2] != "-U" {
		t.Errorf("lock calls not -L then -U: %v", lockCalls)
	}
	if ch := lockCalls[0][len(lockCalls[0])-1]; ch != paneLockChannelPrefix+"8" {
		t.Errorf("SendKeys lock channel = %q, want %q", ch, paneLockChannelPrefix+"8")
	}
	// The send-keys is inside the acquire/release.
	calls := tmux.snapshot()
	var acqIdx, sendIdx, relIdx int
	for i, c := range calls {
		switch {
		case len(c) >= 2 && c[1] == "wait-for" && len(c) >= 3 && c[2] == "-L":
			acqIdx = i
		case len(c) >= 2 && c[1] == "send-keys":
			sendIdx = i
		case len(c) >= 2 && c[1] == "wait-for" && len(c) >= 3 && c[2] == "-U":
			relIdx = i
		}
	}
	if acqIdx >= sendIdx || sendIdx >= relIdx {
		t.Fatalf("SendKeys not inside the lock: acq=%d send=%d rel=%d\ncalls=%v",
			acqIdx, sendIdx, relIdx, calls)
	}
}

// Two Senders stand in for two ty processes: they share the tmux server (the
// same fakeTmux, whose wait-for is a real per-channel mutex) and nothing else —
// no in-process mutex, no shared map, no Sender state. The bug was that two
// such processes used to interleave because their in-process locks didn't see
// each other; with the lock now on the tmux server they must serialize, and
// each prompt must arrive whole.
func TestTwoSendersToTheSamePaneDoNotInterleave(t *testing.T) {
	tmux := &fakeTmux{panes: "%8 7 agent", delay: 20 * time.Millisecond}
	a := New(tmux, idleStore())
	b := New(tmux, idleStore())

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			if err := s.Send(Prompt{TaskID: 7, Text: fmt.Sprintf("prompt %d", i), Submit: true}); err != nil {
				t.Errorf("Send %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// Each prompt's set-buffer/paste-buffer/send-keys arrive as one block; no
	// call from one prompt is interleaved between another's set-buffer and its
	// send-keys.
	calls := tmux.sends()
	if len(calls) != 12 {
		t.Fatalf("want 4 sends of 3 calls, got %v", calls)
	}
	for i := 0; i < len(calls); i += 3 {
		if calls[i][1] != "set-buffer" || calls[i+1][1] != "paste-buffer" || calls[i+2][1] != "send-keys" {
			t.Fatalf("prompts interleaved across two senders at call %d: %v", i, calls)
		}
		if argAfter(calls[i], "-b") != argAfter(calls[i+1], "-b") {
			t.Fatalf("paste used another sender's buffer: %v / %v", calls[i], calls[i+1])
		}
	}
	// Every one of those 4 sends acquired the same per-pane channel.
	channels := map[string]int{}
	for _, c := range tmux.lockCalls() {
		if c[2] == "-L" {
			channels[c[len(c)-1]]++
		}
	}
	if len(channels) != 1 {
		t.Errorf("want one lock channel shared by both senders; got %v", channels)
	}
	for ch, n := range channels {
		if ch != paneLockChannelPrefix+"8" {
			t.Errorf("lock channel = %q, want %q", ch, paneLockChannelPrefix+"8")
		}
		if n != 4 {
			t.Errorf("expected 4 acquires on the shared channel; got %d", n)
		}
	}
}

// The per-pane lock channels are per pane: prompts to DIFFERENT panes use
// different channels, so they can be delivered in parallel and never contend on
// the tmux server.
func TestPerPaneLockChannelsArePerPaneNotGlobal(t *testing.T) {
	tmux := &fakeTmux{panes: "%8 7 agent\n%9 8 agent"}
	var wg sync.WaitGroup
	for _, id := range []int64{7, 8} {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			if err := New(tmux, &fakeStore{task: &db.Task{Status: db.StatusBlocked}}).
				Send(Prompt{TaskID: id, Text: "hi", Submit: true}); err != nil {
				t.Errorf("Send %d: %v", id, err)
			}
		}(id)
	}
	wg.Wait()

	channels := map[string]bool{}
	for _, c := range tmux.lockCalls() {
		if c[2] == "-L" {
			channels[c[len(c)-1]] = true
		}
	}
	if len(channels) != 2 {
		t.Errorf("want two distinct per-pane lock channels; got %v", channels)
	}
	if !channels[paneLockChannelPrefix+"8"] || !channels[paneLockChannelPrefix+"9"] {
		t.Errorf("expected channels for panes %%8 and %%9; got %v", channels)
	}
}

// paneLockChannel keeps only tmux-safe characters in the channel name and
// prefixes it so a pane id like "%8" cannot be misread as a tmux format
// specifier or collide with the project's other wait-for channels.
func TestPaneLockChannelStripsUnsafeCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"%8":      paneLockChannelPrefix + "8",
		"@5":      paneLockChannelPrefix + "5",
		"%9@":     paneLockChannelPrefix + "9", // both % and @ are dropped
		"a-b_1%2": paneLockChannelPrefix + "a-b_12",
	} {
		if got := paneLockChannel(in); got != want {
			t.Errorf("paneLockChannel(%q) = %q, want %q", in, got, want)
		}
	}
	for _, ch := range []string{paneLockChannelPrefix + "8", paneLockChannelPrefix + "9"} {
		if strings.HasPrefix(ch, "@") || strings.ContainsAny(ch, "%@:/.") {
			t.Errorf("channel %q contains a tmux format sigil or path char", ch)
		}
	}
}
