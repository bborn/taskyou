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
type fakeTmux struct {
	mu    sync.Mutex
	calls [][]string
	panes string // list-panes output
	err   error
	delay time.Duration
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
	f.record(name, args)
	return f.err
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

// sends returns the recorded calls with the pane lookups filtered out.
func (f *fakeTmux) sends() [][]string {
	var out [][]string
	for _, c := range f.snapshot() {
		if len(c) > 1 && c[1] == "list-panes" {
			continue
		}
		out = append(out, c)
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
// without the per-pane lock the second send's set-buffer lands between the
// first's paste and its Enter, and this fails.
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

	// Four delayed calls each. Serialized end to end that is 8 delays; run in
	// parallel it is about 4.
	if elapsed := time.Since(start); elapsed > 7*60*time.Millisecond {
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
