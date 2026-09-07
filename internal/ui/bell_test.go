package ui

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// recorder collects the times a debounced bell actually rang.
type recorder struct {
	mu    sync.Mutex
	times []time.Time
}

func (r *recorder) ring() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.times = append(r.times, time.Now())
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.times)
}

func TestBellRingsOnceForABurst(t *testing.T) {
	rec := &recorder{}
	b := newBell(100*time.Millisecond, rec.ring)

	// Ten events in rapid succession, the way a poll cycle picking up several
	// status changes at once would deliver them.
	for i := 0; i < 10; i++ {
		b.Ring()
	}

	if got := rec.count(); got != 0 {
		t.Fatalf("bell rang %d times during the burst, want 0 (trailing edge only)", got)
	}

	waitFor(t, func() bool { return rec.count() > 0 }, time.Second)
	time.Sleep(200 * time.Millisecond) // give any stragglers a chance to ring

	if got := rec.count(); got != 1 {
		t.Fatalf("bell rang %d times for a burst of 10 events, want 1", got)
	}
}

func TestBellRingsAfterLastEventInBurst(t *testing.T) {
	rec := &recorder{}
	delay := 100 * time.Millisecond
	b := newBell(delay, rec.ring)

	// Events spaced closer together than the debounce window keep pushing the
	// ring out, so it lands after the *last* one rather than the first.
	b.Ring()
	last := time.Now()
	for i := 0; i < 4; i++ {
		time.Sleep(delay / 4)
		b.Ring()
		last = time.Now()
	}

	waitFor(t, func() bool { return rec.count() > 0 }, time.Second)

	rec.mu.Lock()
	rang := rec.times[0]
	rec.mu.Unlock()

	if rang.Before(last.Add(delay / 2)) {
		t.Fatalf("bell rang %v after the last event, want at least %v", rang.Sub(last), delay/2)
	}
	if got := rec.count(); got != 1 {
		t.Fatalf("bell rang %d times, want 1", got)
	}
}

func TestBellRingsAgainForASeparateBurst(t *testing.T) {
	rec := &recorder{}
	delay := 20 * time.Millisecond
	b := newBell(delay, rec.ring)

	b.Ring()
	waitFor(t, func() bool { return rec.count() == 1 }, time.Second)

	// A later, unrelated event still gets its own bell — debouncing collapses
	// bursts, it does not rate-limit the bell forever.
	b.Ring()
	waitFor(t, func() bool { return rec.count() == 2 }, time.Second)
}

func TestBellFlushDeliversPendingRing(t *testing.T) {
	rec := &recorder{}
	b := newBell(10*time.Second, rec.ring)

	b.Ring()
	if rec.count() != 0 {
		t.Fatal("bell rang before its window elapsed")
	}

	b.Flush()
	if got := rec.count(); got != 1 {
		t.Fatalf("Flush delivered %d rings, want 1", got)
	}
	if b.pending() {
		t.Fatal("a ring is still pending after Flush")
	}
}

func TestBellFlushWithoutPendingRingIsSilent(t *testing.T) {
	rec := &recorder{}
	b := newBell(10*time.Millisecond, rec.ring)

	b.Flush()
	if got := rec.count(); got != 0 {
		t.Fatalf("Flush rang %d times with nothing pending, want 0", got)
	}

	b.Ring()
	waitFor(t, func() bool { return rec.count() == 1 }, time.Second)

	// The window already elapsed, so the ring was delivered by the timer and
	// Flush must not double-ring it.
	b.Flush()
	time.Sleep(30 * time.Millisecond)
	if got := rec.count(); got != 1 {
		t.Fatalf("bell rang %d times, want 1 (Flush double-rang an already-delivered bell)", got)
	}
}

func TestBellIsSafeForConcurrentCallers(t *testing.T) {
	var rings int64
	b := newBell(30*time.Millisecond, func() { atomic.AddInt64(&rings, 1) })

	// Events reach RingBell from the Bubble Tea update loop and from Flush on
	// shutdown; -race should find any unsynchronized state between them.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				b.Ring()
			}
		}()
	}
	wg.Wait()
	b.Flush()

	if got := atomic.LoadInt64(&rings); got < 1 {
		t.Fatalf("400 concurrent events produced %d rings, want at least 1", got)
	}
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

// An expired AfterFunc can be waiting for the mutex when shutdown flushes it.
func TestBellFlushDeliversExpiredPendingRing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		b := newBell(time.Second, rec.ring)
		b.Ring()
		b.mu.Lock()
		time.Sleep(time.Second)
		// The deadline has elapsed while the mutex prevents delivery.
		b.mu.Unlock()
		b.Flush()
		synctest.Wait()
		if got := rec.count(); got != 1 {
			t.Fatalf("Flush delivered %d rings after timer expiry, want 1", got)
		}
	})
}

func TestBellFlushWaitsForDelivery(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	b := newBell(time.Millisecond, func() {
		close(started)
		<-release
	})
	b.Ring()
	<-started
	flushed := make(chan struct{})
	go func() { b.Flush(); close(flushed) }()
	select {
	case <-flushed:
		t.Error("Flush returned before the bell finished ringing")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("Flush did not return after delivery finished")
	}
}
