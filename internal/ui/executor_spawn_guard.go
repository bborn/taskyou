package ui

import (
	"sync"
	"time"
)

// Executor spawn circuit breaker.
//
// The detail view starts an executor when a task has no tmux window, and re-runs
// that setup whenever its pane health poll finds no pane. Both halves are
// reasonable on their own; together they can form a loop, because a spawn does
// not guarantee an adoptable pane. When a task's worktree had been reaped, tmux
// silently started the agent in $HOME, the ownership check refused to adopt a
// pane outside the worktree, the poll saw no pane, and setup started another
// one — 178 Claude sessions and $43 in 30 minutes on task 5040.
//
// The specific cause of that loop is fixed elsewhere (a missing worktree now
// stops the start path outright). This breaker is the backstop for the shape of
// the bug rather than its cause: whatever the reason, a task that keeps starting
// executors without ever adopting a pane stops starting them.
const (
	// maxExecutorSpawns is how many executor launches one task may make inside
	// executorSpawnWindow without a pane ever being adopted.
	maxExecutorSpawns = 3
	// executorSpawnWindow is the sliding window the launches are counted over.
	// Well above the ~10s cadence of the observed loop, and short enough that a
	// user who fixes the underlying problem isn't locked out for long.
	executorSpawnWindow = 2 * time.Minute
)

// spawnBreaker counts executor launches per task and refuses further launches
// once a task exceeds its limit inside the window without a successful adopt.
//
// It is keyed by task and lives for the process, not on DetailModel: reopening
// the detail view builds a brand new model, so a breaker that lived on the model
// would reset every time the loop came back around — which is exactly when it
// needs to remember.
type spawnBreaker struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	attempts map[int64][]time.Time
}

// executorSpawns is the process-wide breaker consulted before any UI-initiated
// executor launch.
var executorSpawns = newSpawnBreaker(maxExecutorSpawns, executorSpawnWindow)

func newSpawnBreaker(limit int, window time.Duration) *spawnBreaker {
	return &spawnBreaker{limit: limit, window: window, attempts: map[int64][]time.Time{}}
}

// allow reports whether a launch for taskID may proceed, recording it when it
// may. A refused launch is deliberately NOT recorded, so the window slides shut
// on its own and the task becomes startable again once the burst ages out.
func (b *spawnBreaker) allow(taskID int64, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	recent := pruneSpawnAttempts(b.attempts[taskID], now, b.window)
	if len(recent) >= b.limit {
		b.attempts[taskID] = recent
		return false
	}
	b.attempts[taskID] = append(recent, now)
	return true
}

// adopted records that a launch finally produced a usable pane, clearing the
// task's history so a long-lived healthy view never trips the breaker.
func (b *spawnBreaker) adopted(taskID int64) { b.reset(taskID) }

// reset forgets a task's launch history (used by explicit user recovery, e.g.
// recreating a missing worktree).
func (b *spawnBreaker) reset(taskID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.attempts, taskID)
}

// pruneSpawnAttempts drops launch timestamps that have fallen out of the window.
func pruneSpawnAttempts(attempts []time.Time, now time.Time, window time.Duration) []time.Time {
	cutoff := now.Add(-window)
	kept := attempts[:0:0]
	for _, at := range attempts {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	return kept
}
