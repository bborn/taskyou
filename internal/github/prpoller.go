package github

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Poll cadence. A PR whose state is still settling is asked about often; a
// settled one rarely; a merged or closed one never again.
const (
	// PRPollActive applies while checks run or GitHub is still computing
	// mergeability — the states a human is watching change.
	PRPollActive = 30 * time.Second
	// PRPollSettled applies to an open PR nothing is currently moving on.
	PRPollSettled = 3 * time.Minute
	// PRPollNoPR applies to a branch with no PR yet, waiting for one to appear.
	PRPollNoPR = 2 * time.Minute
)

// Failure backoff, per repository.
const (
	prBackoffBase = 30 * time.Second
	prBackoffMax  = 5 * time.Minute
	// prRateLimitPause is used when GitHub rate limits without saying until when.
	prRateLimitPause = 15 * time.Minute
	// prRateFloor is the GraphQL budget below which polling pauses until the
	// reset, leaving the rest for the agents' own gh usage.
	prRateFloor = 200
)

// PollInterval reports how long a PR state stays fresh. Zero means the state is
// terminal and never needs asking again.
func PollInterval(info *PRInfo) time.Duration {
	if info == nil {
		return PRPollNoPR
	}
	switch info.State {
	case PRStateMerged, PRStateClosed:
		return 0
	}
	if info.CheckState == CheckStatePending || mergeabilityUnknown(info) {
		return PRPollActive
	}
	return PRPollSettled
}

// mergeabilityUnknown reports that GitHub hasn't finished computing whether the
// PR merges; it resolves within seconds of being asked, so it's worth a quick
// second look.
func mergeabilityUnknown(info *PRInfo) bool {
	if info.State != PRStateOpen {
		return false
	}
	return info.Mergeable == "" || info.Mergeable == "UNKNOWN" || info.MergeStateStatus == "UNKNOWN"
}

// PRTarget is one branch whose PR status someone wants kept current.
type PRTarget struct {
	TaskID  int64
	RepoDir string
	Branch  string
	// Known is the last stored state, nil if none. It drives the cadence.
	Known *PRInfo
}

// PRResult is a successful lookup. Info nil means the branch has no PR.
// Targets whose lookup failed produce no result, so their last good state stands.
type PRResult struct {
	Target PRTarget
	Info   *PRInfo
}

type repoBackoff struct {
	failures int
	until    time.Time
}

// PRPoller decides which branches are due and asks GitHub about them, one query
// per repository. It is the single background owner of PR status: the daemon
// runs one, and every surface reads what it stores.
type PRPoller struct {
	mu          sync.Mutex
	fetch       BranchFetcher
	now         func() time.Time
	checked     map[int64]time.Time
	backoff     map[string]*repoBackoff
	pausedUntil time.Time // account-wide: GraphQL budget is per user, not per repo
}

// BranchFetcher looks up the PRs for a set of branches in one repository.
type BranchFetcher func(ctx context.Context, repoDir string, branches []string) (*BranchPRs, error)

// NewPRPoller returns a poller that talks to GitHub through gh.
func NewPRPoller() *PRPoller {
	return NewPRPollerWithFetcher(FetchPRsForBranches)
}

// NewPRPollerWithFetcher returns a poller that looks PRs up with fetch, for
// callers that need to stand in for GitHub.
func NewPRPollerWithFetcher(fetch BranchFetcher) *PRPoller {
	return &PRPoller{
		fetch:   fetch,
		now:     time.Now,
		checked: make(map[int64]time.Time),
		backoff: make(map[string]*repoBackoff),
	}
}

// Poll looks up every due target and returns what it learned. It never blocks
// on a repository that is backing off, and never returns a result for a lookup
// that failed.
func (p *PRPoller) Poll(ctx context.Context, targets []PRTarget) []PRResult {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	if now.Before(p.pausedUntil) {
		return nil
	}
	p.forgetMissing(targets)

	byRepo := make(map[string][]PRTarget)
	var order []string
	for _, t := range targets {
		if t.RepoDir == "" || t.Branch == "" || !p.due(t, now) {
			continue
		}
		if b := p.backoff[t.RepoDir]; b != nil && now.Before(b.until) {
			continue
		}
		if _, seen := byRepo[t.RepoDir]; !seen {
			order = append(order, t.RepoDir)
		}
		byRepo[t.RepoDir] = append(byRepo[t.RepoDir], t)
	}

	var results []PRResult
	for _, repoDir := range order {
		if ctx.Err() != nil {
			break
		}
		repoTargets := byRepo[repoDir]
		branches := make([]string, len(repoTargets))
		for i, t := range repoTargets {
			branches[i] = t.Branch
		}

		res, err := p.fetch(ctx, repoDir, branches)
		now = p.now()
		if err != nil {
			p.recordFailure(repoDir, err, now)
			if now.Before(p.pausedUntil) {
				// Rate limited: the budget is account-wide, so every other
				// repo would be refused too.
				break
			}
			continue
		}
		delete(p.backoff, repoDir)
		for _, t := range repoTargets {
			p.checked[t.TaskID] = now
			results = append(results, PRResult{Target: t, Info: res.PRs[t.Branch]})
		}
		if res.RateRemaining >= 0 && res.RateRemaining < prRateFloor && res.RateResetAt.After(now) {
			p.pausedUntil = res.RateResetAt
			break
		}
	}
	return results
}

func (p *PRPoller) due(t PRTarget, now time.Time) bool {
	last, ok := p.checked[t.TaskID]
	if !ok {
		// Never asked in this process. A terminal PR stored by an earlier run
		// still needs no question.
		return t.Known == nil || PollInterval(t.Known) > 0
	}
	interval := PollInterval(t.Known)
	return interval > 0 && now.Sub(last) >= interval
}

func (p *PRPoller) recordFailure(repoDir string, err error, now time.Time) {
	var rl *RateLimitError
	if errors.As(err, &rl) {
		until := rl.ResetAt
		if !until.After(now) {
			until = now.Add(prRateLimitPause)
		}
		p.pausedUntil = until
		return
	}
	b := p.backoff[repoDir]
	if b == nil {
		b = &repoBackoff{}
		p.backoff[repoDir] = b
	}
	delay := prBackoffBase << b.failures
	if delay <= 0 || delay > prBackoffMax {
		delay = prBackoffMax
	} else {
		b.failures++
	}
	b.until = now.Add(delay)
}

// forgetMissing drops bookkeeping for tasks no longer being tracked.
func (p *PRPoller) forgetMissing(targets []PRTarget) {
	live := make(map[int64]bool, len(targets))
	for _, t := range targets {
		live[t.TaskID] = true
	}
	for id := range p.checked {
		if !live[id] {
			delete(p.checked, id)
		}
	}
}
