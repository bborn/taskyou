package github

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// recordingFetcher answers from prs and records each call's branches.
type recordingFetcher struct {
	prs   map[string]*PRInfo
	err   error
	calls [][]string
	rate  int
	reset time.Time
}

func (f *recordingFetcher) fetch(ctx context.Context, repoDir string, branches []string) (*BranchPRs, error) {
	sorted := append([]string(nil), branches...)
	sort.Strings(sorted)
	f.calls = append(f.calls, sorted)
	if f.err != nil {
		return nil, f.err
	}
	res := &BranchPRs{PRs: map[string]*PRInfo{}, RateRemaining: -1}
	if f.rate != 0 {
		res.RateRemaining, res.RateResetAt = f.rate, f.reset
	}
	for _, b := range branches {
		if pr := f.prs[b]; pr != nil {
			res.PRs[b] = pr
		}
	}
	return res, nil
}

func newTestPoller(f *recordingFetcher) (*PRPoller, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)}
	p := NewPRPollerWithFetcher(f.fetch)
	p.now = clock.now
	return p, clock
}

func TestPollInterval(t *testing.T) {
	cases := []struct {
		name string
		info *PRInfo
		want time.Duration
	}{
		{"no PR yet", nil, PRPollNoPR},
		{"merged never again", &PRInfo{State: PRStateMerged}, 0},
		{"closed never again", &PRInfo{State: PRStateClosed}, 0},
		{"checks running", &PRInfo{State: PRStateOpen, CheckState: CheckStatePending, Mergeable: "MERGEABLE"}, PRPollActive},
		{"mergeability still computing", &PRInfo{State: PRStateOpen, CheckState: CheckStatePassing, Mergeable: "UNKNOWN"}, PRPollActive},
		{"settled open", &PRInfo{State: PRStateOpen, CheckState: CheckStatePassing, Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN"}, PRPollSettled},
		{"settled draft", &PRInfo{State: PRStateDraft, CheckState: CheckStatePassing}, PRPollSettled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PollInterval(tc.info); got != tc.want {
				t.Errorf("PollInterval = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPoll_OneFetchPerRepoAndOnlyDueTargets(t *testing.T) {
	f := &recordingFetcher{prs: map[string]*PRInfo{
		"a": {Number: 1, State: PRStateOpen, CheckState: CheckStatePassing, Mergeable: "MERGEABLE"},
	}}
	p, clock := newTestPoller(f)
	settled := &PRInfo{Number: 1, State: PRStateOpen, CheckState: CheckStatePassing, Mergeable: "MERGEABLE"}
	targets := []PRTarget{
		{TaskID: 1, RepoDir: "/r1", Branch: "a", Known: settled},
		{TaskID: 2, RepoDir: "/r1", Branch: "b"},
		{TaskID: 3, RepoDir: "/r2", Branch: "c"},
		{TaskID: 4, RepoDir: "/r2", Branch: "d", Known: &PRInfo{State: PRStateMerged}},
	}

	results := p.Poll(context.Background(), targets)
	if len(f.calls) != 2 {
		t.Fatalf("fetch calls = %v, want one per repo", f.calls)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3 (merged task never asked)", len(results))
	}
	for _, r := range results {
		if r.Target.Branch == "d" {
			t.Error("merged PR was polled")
		}
	}

	// Nothing is due a moment later.
	clock.advance(10 * time.Second)
	f.calls = nil
	if got := p.Poll(context.Background(), targets); len(got) != 0 || len(f.calls) != 0 {
		t.Fatalf("re-polled too soon: calls %v", f.calls)
	}

	// After the no-PR interval, only the PR-less branches are due; the settled PR waits longer.
	clock.advance(PRPollNoPR)
	p.Poll(context.Background(), targets)
	want := [][]string{{"b"}, {"c"}}
	if len(f.calls) != 2 || f.calls[0][0] != want[0][0] || f.calls[1][0] != want[1][0] {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}
}

func TestPoll_FailureYieldsNoResultsAndBacksOff(t *testing.T) {
	f := &recordingFetcher{err: errors.New("network down")}
	p, clock := newTestPoller(f)
	targets := []PRTarget{{TaskID: 1, RepoDir: "/r", Branch: "a", Known: &PRInfo{State: PRStateOpen, CheckState: CheckStatePending}}}

	if got := p.Poll(context.Background(), targets); len(got) != 0 {
		t.Fatalf("a failed lookup must produce no results, got %+v", got)
	}

	// Inside the backoff window: no retry.
	clock.advance(prBackoffBase - time.Second)
	p.Poll(context.Background(), targets)
	if len(f.calls) != 1 {
		t.Fatalf("retried during backoff: %d calls", len(f.calls))
	}

	// After it: retry, and the next window doubles.
	clock.advance(2 * time.Second)
	p.Poll(context.Background(), targets)
	if len(f.calls) != 2 {
		t.Fatalf("did not retry after backoff: %d calls", len(f.calls))
	}
	clock.advance(prBackoffBase + time.Second)
	p.Poll(context.Background(), targets)
	if len(f.calls) != 2 {
		t.Fatal("second backoff should be twice as long")
	}

	// Recovery clears the backoff.
	f.err = nil
	clock.advance(prBackoffBase)
	if got := p.Poll(context.Background(), targets); len(got) != 1 {
		t.Fatalf("expected recovery result, got %d", len(got))
	}
}

func TestPoll_BackoffIsCapped(t *testing.T) {
	f := &recordingFetcher{err: errors.New("boom")}
	p, clock := newTestPoller(f)
	targets := []PRTarget{{TaskID: 1, RepoDir: "/r", Branch: "a"}}
	for i := 0; i < 20; i++ {
		p.Poll(context.Background(), targets)
		clock.advance(prBackoffMax + time.Second)
	}
	if len(f.calls) != 20 {
		t.Fatalf("calls = %d, want 20 (backoff must cap at %s)", len(f.calls), prBackoffMax)
	}
}

func TestPoll_RateLimitPausesEverything(t *testing.T) {
	reset := time.Date(2026, 9, 15, 12, 30, 0, 0, time.UTC)
	f := &recordingFetcher{err: &RateLimitError{ResetAt: reset}}
	p, clock := newTestPoller(f)
	targets := []PRTarget{
		{TaskID: 1, RepoDir: "/r1", Branch: "a"},
		{TaskID: 2, RepoDir: "/r2", Branch: "b"},
	}
	p.Poll(context.Background(), targets)
	if len(f.calls) != 1 {
		t.Fatalf("kept querying other repos after a rate limit: %v", f.calls)
	}
	clock.advance(29 * time.Minute)
	p.Poll(context.Background(), targets)
	if len(f.calls) != 1 {
		t.Fatal("polled before the rate limit reset")
	}
	f.err = nil
	clock.advance(2 * time.Minute)
	p.Poll(context.Background(), targets)
	if len(f.calls) != 3 {
		t.Fatalf("did not resume after reset: %v", f.calls)
	}
}

func TestPoll_LowBudgetPausesUntilReset(t *testing.T) {
	f := &recordingFetcher{rate: prRateFloor - 1}
	p, clock := newTestPoller(f)
	f.reset = clock.t.Add(10 * time.Minute)
	targets := []PRTarget{
		{TaskID: 1, RepoDir: "/r1", Branch: "a"},
		{TaskID: 2, RepoDir: "/r2", Branch: "b"},
	}
	if got := p.Poll(context.Background(), targets); len(got) != 1 {
		t.Fatalf("results = %d, want the one repo fetched before pausing", len(got))
	}
	clock.advance(5 * time.Minute)
	p.Poll(context.Background(), targets)
	if len(f.calls) != 1 {
		t.Fatal("polled while budget was low")
	}
}
