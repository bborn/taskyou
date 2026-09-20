package ui

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// collectMsgs executes a command chain the way the bubbletea runtime would,
// flattening batches, and returns every leaf message in delivery order. It is
// the test's stand-in for execBatchMsg: it walks the batch synchronously here
// only so the test can fish out a specific message (clone A's late done) and
// hold it back until clone B is in flight.
func collectMsgs(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var out []tea.Msg
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		msg := c()
		switch typed := msg.(type) {
		case tea.BatchMsg:
			for _, sub := range typed {
				walk(sub)
			}
		default:
			out = append(out, msg)
		}
	}
	walk(cmd)
	return out
}

// TestRepoClone_LateDoneFromCanceledCloneAbortsRetry reproduces the cancel-then-
// retry race end-to-end. Clone A is canceled; before its killed goroutine
// finishes draining, the user presses enter to retry (clone B). Clone A's late
// repoCloneDoneMsg is then delivered while clone B is in flight. The model must
// ignore the stale done and let clone B run to completion.
func TestRepoClone_LateDoneFromCanceledCloneAbortsRetry(t *testing.T) {
	cloner, _ := fakeCloner(t, func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		time.Sleep(30 * time.Millisecond) // simulate git taking a moment to die
		return ctx.Err()
	})
	m := newRepoCloneModel(mustRef(t, "bborn/taskyou"), cloner, 100, 40)

	// 1. Start clone A.
	m, cmdA := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != repoCloneRunning {
		t.Fatalf("state = %v, want running", m.state)
	}

	// 2. Cancel clone A.
	if !m.Cancel() {
		t.Fatal("Cancel should report an in-flight clone")
	}
	if m.state != repoCloneFailed {
		t.Fatalf("state = %v, want failed", m.state)
	}

	// 3. Swap in a success clone with NO ctx check so the retry would succeed on its own.
	m.cloner.Run = func(_ context.Context, _, dest string) error {
		writeCheckout(t, dest, "https://github.com/bborn/taskyou.git")
		return nil
	}

	// 4. Start clone B (retry).
	m, cmdB := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != repoCloneRunning {
		t.Fatalf("retry: state = %v, want running", m.state)
	}

	// 5. Now deliver clone A's late repoCloneDoneMsg (canceled) — what the
	//    bubbletea runtime would deliver once the killed subprocess finishes.
	var lateDone repoCloneDoneMsg
	for _, msg := range collectMsgs(t, cmdA) {
		if d, ok := msg.(repoCloneDoneMsg); ok {
			lateDone = d
			break
		}
	}
	if lateDone.err == nil {
		t.Fatal("late done should carry a canceled error")
	}
	m, _ = m.Update(lateDone)

	if m.state == repoCloneFailed {
		t.Fatalf("BUG reproduced: late done from canceled clone A aborted in-flight clone B; errText=%q", m.errText)
	}
	if m.cancel == nil {
		t.Fatal("BUG reproduced: B's cancel was cleared by A's late done while B is still in flight")
	}
	// B's flow should still produce a repoClonedMsg, proving the retry would have succeeded.
	if _, ok := drain(m, cmdB).(repoClonedMsg); !ok {
		t.Fatalf("clone B should have succeeded; got %T", drain(m, cmdB))
	}
}

// TestRepoClone_RetryBypassesIsCheckoutOfViaCache shows why the race window
// above is reachable for every retry, even when clone A's partial .git/origin
// is on disk: start() memoizes isCheckoutOf per path, so the retry's start()
// returns the cached "false" without probing disk and spawns clone B instead of
// taking the "adopt existing checkout" fast-path. This test is independent of
// the seq fix and asserts the cache behavior of the unmodified model.
func TestRepoClone_RetryBypassesIsCheckoutOfViaCache(t *testing.T) {
	dest := "" // set by Run when clone A writes .git/origin
	originWritten := make(chan struct{})
	var once sync.Once
	cloner, root := fakeCloner(t, func(ctx context.Context, url, d string) error {
		writeCheckout(t, d, url)
		dest = d
		once.Do(func() { close(originWritten) })
		<-ctx.Done()
		return ctx.Err()
	})

	// Wrap RemoteURL so the test can count how many times the git origin is
	// consulted. The model's isCheckoutOf goes through this; a cache hit skips it.
	baseRemote := cloner.RemoteURL
	var remoteCalls int
	cloner.RemoteURL = func(dir string) (string, error) {
		remoteCalls++
		return baseRemote(dir)
	}

	m := newRepoCloneModel(mustRef(t, "bborn/taskyou"), cloner, 100, 40)

	// 1. Start clone A and run its command batch the way execBatchMsg does:
	//    each sub-command in its own goroutine. Clone A writes .git/origin then
	//    blocks on its context.
	m, cmdA := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != repoCloneRunning {
		t.Fatalf("state = %v, want running", m.state)
	}
	var wg sync.WaitGroup
	if batch, ok := cmdA().(tea.BatchMsg); ok {
		for _, sub := range batch {
			sub := sub
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = sub()
			}()
		}
	}

	// 2. Wait for clone A to write .git/origin so the disk would now say
	//    "this is a checkout of bborn/taskyou".
	select {
	case <-originWritten:
	case <-time.After(5 * time.Second):
		t.Fatal("clone A never wrote .git/origin")
	}

	// 3. From the main goroutine, a fresh IsCheckoutOf against the disk
	//    returns true — adoption would fire if the retry probed the disk.
	ref := mustRef(t, "bborn/taskyou")
	if !cloner.IsCheckoutOf(dest, ref) {
		t.Fatal("disk says .git/origin is a checkout of ref; adoption would fire if the cache missed")
	}
	callsAfterA := remoteCalls
	if callsAfterA == 0 {
		t.Fatal("the fresh IsCheckoutOf should have consulted the git remote at least once")
	}

	// 4. Cancel clone A (releases its context; clone A's goroutine drains).
	if !m.Cancel() {
		t.Fatal("Cancel should report an in-flight clone")
	}

	// 5. Retry with enter. Because isCheckoutOf is memoized per path, the
	//    retry's start() takes the cached "false" without probing disk, so
	//    clone B is spawned rather than the existing checkout adopted.
	m, cmdB := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != repoCloneRunning {
		t.Fatalf("retry: state = %v, want running — adoption fast-path should NOT have fired", m.state)
	}
	if m.cancel == nil {
		t.Fatal("retry should have an in-flight clone with a cancel func")
	}
	if got := remoteCalls; got != callsAfterA {
		t.Fatalf("retry consulted the git remote %d time(s), expected 0 (isCheckoutOf cache hit): before=%d after=%d",
			got-callsAfterA, callsAfterA, got)
	}

	// 6. Tear down clone B (never ran) and wait for clone A's goroutines.
	m.Cancel()
	_ = cmdB
	wg.Wait()

	// Sanity: the destination the model resolves matches what clone A wrote.
	if dest != filepath.Join(root, "taskyou") {
		t.Fatalf("clone A wrote to %q, model resolves %q", dest, filepath.Join(root, "taskyou"))
	}
}

// TestRepoClone_MultipleRetriesIgnoreAllStaleDones covers the seq contract for a
// chain of retries: start A → cancel A → start B → cancel B → start C, then
// deliver clone A's late done while clone C is in flight. Clone C must keep
// running; only the done whose seq matches the current attempt (C's) is
// honored. This guards against the variant where a closure over m.seq (rather
// than a by-value capture) would read clone C's seq at clone A's return time
// and mistakenly treat A's late done as current.
func TestRepoClone_MultipleRetriesIgnoreAllStaleDones(t *testing.T) {
	cloner, _ := fakeCloner(t, func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // simulate git taking a moment to die
		return ctx.Err()
	})
	m := newRepoCloneModel(mustRef(t, "bborn/taskyou"), cloner, 100, 40)

	// 1. Start clone A (seq becomes 1).
	m, cmdA := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != repoCloneRunning {
		t.Fatalf("A: state = %v, want running", m.state)
	}
	if m.seq != 1 {
		t.Fatalf("A: m.seq = %d, want 1", m.seq)
	}
	if !m.Cancel() {
		t.Fatal("cancel A should report an in-flight clone")
	}

	// 2. Start clone B (seq becomes 2).
	m, cmdB := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != repoCloneRunning {
		t.Fatalf("B: state = %v, want running", m.state)
	}
	if m.seq != 2 {
		t.Fatalf("B: m.seq = %d, want 2", m.seq)
	}
	if !m.Cancel() {
		t.Fatal("cancel B should report an in-flight clone")
	}

	// 3. Start clone C (seq becomes 3) with a success clone (no ctx check).
	m.cloner.Run = func(_ context.Context, _, dest string) error {
		writeCheckout(t, dest, "https://github.com/bborn/taskyou.git")
		return nil
	}
	m, cmdC := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != repoCloneRunning {
		t.Fatalf("C: state = %v, want running", m.state)
	}
	if m.seq != 3 {
		t.Fatalf("C: m.seq = %d, want 3", m.seq)
	}
	if m.cancel == nil {
		t.Fatal("C: cancel should be set")
	}

	// 4. Deliver clone A's late repoCloneDoneMsg while C is in flight. Its seq
	//    (1) must NOT match the current attempt (3) and must be ignored.
	//    Go forbids comparing funcs except to nil, so "C's cancel was not
	//    replaced" is implied by the seq guard's early return (the handler
	//    returns before touching m.cancel).
	doneA, ok := collectMsgs(t, cmdA)[0].(repoCloneDoneMsg)
	if !ok {
		t.Fatalf("A: want repoCloneDoneMsg, got %T", collectMsgs(t, cmdA)[0])
	}
	if doneA.seq != 1 {
		t.Fatalf("A: doneA.seq = %d, want 1", doneA.seq)
	}
	if doneA.err == nil {
		t.Fatal("A: late done should carry a canceled error")
	}
	m, _ = m.Update(doneA)
	if m.state == repoCloneFailed {
		t.Fatalf("BUG: A's late done aborted in-flight clone C; errText=%q", m.errText)
	}
	if m.cancel == nil {
		t.Fatal("BUG: C's cancel was cleared by A's late done while C is still in flight")
	}
	if m.seq != 3 {
		t.Fatalf("BUG: m.seq changed to %d after A's stale done; should stay 3", m.seq)
	}

	// 5. Deliver clone B's late repoCloneDoneMsg (seq 2) too; also ignored.
	doneB, ok := collectMsgs(t, cmdB)[0].(repoCloneDoneMsg)
	if !ok {
		t.Fatalf("B: want repoCloneDoneMsg, got %T", collectMsgs(t, cmdB)[0])
	}
	if doneB.seq != 2 {
		t.Fatalf("B: doneB.seq = %d, want 2", doneB.seq)
	}
	m, _ = m.Update(doneB)
	if m.state == repoCloneFailed {
		t.Fatalf("BUG: B's late done aborted in-flight clone C; errText=%q", m.errText)
	}
	if m.cancel == nil {
		t.Fatal("BUG: C's cancel was cleared by B's late done while C is still in flight")
	}
	if m.seq != 3 {
		t.Fatalf("BUG: m.seq changed to %d after B's stale done; should stay 3", m.seq)
	}

	// 6. Drain C's flow: only seq==3's done is honored and yields repoClonedMsg.
	if _, ok := drain(m, cmdC).(repoClonedMsg); !ok {
		t.Fatalf("clone C should have succeeded; got %T", drain(m, cmdC))
	}

	// 7. After C succeeds, the model hands back control (state == confirm, cancel cleared).
	if m.cancel != nil {
		t.Fatal("after C succeeds, m.cancel should be cleared")
	}
	if m.seq != 3 {
		t.Fatalf("after C succeeds, m.seq should still be 3 (not bumped by success), got %d", m.seq)
	}
}
