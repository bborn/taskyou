package executor

import "testing"

// TestWindowMissTracker covers the transient-failure debounce used by
// pollTmuxSession (PR #558). The whole point of the change is that a single
// failed `tmux list-panes` — which also happens when the shared tmux server is
// briefly busy or the 3s timeout trips under load — must NOT immediately block a
// healthy task. Only `threshold` consecutive misses should be treated as
// "window genuinely gone".
func TestWindowMissTracker(t *testing.T) {
	const threshold = 3

	t.Run("single transient miss does not block", func(t *testing.T) {
		w := windowMissTracker{threshold: threshold}
		if gone := w.record(false); gone {
			t.Fatalf("one miss reported window gone; want healthy (transient blip must not block)")
		}
	})

	t.Run("misses below threshold do not block", func(t *testing.T) {
		w := windowMissTracker{threshold: threshold}
		for i := 1; i < threshold; i++ {
			if gone := w.record(false); gone {
				t.Fatalf("miss #%d reported window gone; want still healthy until threshold (%d)", i, threshold)
			}
		}
	})

	t.Run("threshold consecutive misses block", func(t *testing.T) {
		w := windowMissTracker{threshold: threshold}
		var gone bool
		for i := 0; i < threshold; i++ {
			gone = w.record(false)
		}
		if !gone {
			t.Fatalf("%d consecutive misses did not report window gone; want gone", threshold)
		}
	})

	t.Run("a success resets the run of misses", func(t *testing.T) {
		w := windowMissTracker{threshold: threshold}
		// Two misses, then a healthy check — the streak must reset so we are
		// nowhere near blocking.
		w.record(false)
		w.record(false)
		if gone := w.record(true); gone {
			t.Fatalf("healthy check reported window gone")
		}
		if w.consecutive != 0 {
			t.Fatalf("consecutive = %d after success; want 0", w.consecutive)
		}
		// After the reset it must take a *fresh* full run of misses to block —
		// proving the failures are required to be consecutive, not cumulative.
		if gone := w.record(false); gone {
			t.Fatalf("first miss after reset blocked; want threshold misses required again")
		}
		if gone := w.record(false); gone {
			t.Fatalf("second miss after reset blocked; want threshold misses required again")
		}
		if gone := w.record(false); !gone {
			t.Fatalf("third consecutive miss after reset did not block; want gone")
		}
	})

	t.Run("flapping never accumulates to a block", func(t *testing.T) {
		w := windowMissTracker{threshold: threshold}
		// miss, ok, miss, ok, ... a server that intermittently recovers must
		// never trip the threshold because the run never reaches `threshold`.
		for i := 0; i < 10; i++ {
			if gone := w.record(false); gone {
				t.Fatalf("flapping miss tripped block at iteration %d; want never", i)
			}
			if gone := w.record(true); gone {
				t.Fatalf("flapping success reported gone at iteration %d", i)
			}
		}
	})
}
