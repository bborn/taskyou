package ui

import (
	"os"
	"sync"
	"time"
)

// BellDebounce is the quiet period used to coalesce terminal bells.
//
// Task transitions frequently arrive in bursts: a single poll cycle can pick up
// several status changes at once, and the executor's event channel delivers
// back-to-back events when a batch of tasks finishes. Ringing once per event
// turns that into a machine-gun of beeps. Instead each bell restarts this
// window and only the last event in the burst actually rings.
//
// The window is deliberately shorter than the board's 1s poll interval, so
// genuinely separate events still get their own bell — only same-burst events
// collapse.
const BellDebounce = 500 * time.Millisecond

// bell coalesces rapid Ring calls into a single trailing ring.
type bell struct {
	mu    sync.Mutex
	delay time.Duration
	ring  func()
	timer *time.Timer
	gen   uint64 // incremented on every Ring/Flush, invalidates in-flight timers
}

func newBell(delay time.Duration, ring func()) *bell {
	return &bell{delay: delay, ring: ring}
}

// Ring schedules a ring for delay from now. Each call restarts the window, so a
// burst of calls produces exactly one ring once the burst settles.
func (b *bell) Ring() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.gen++
	gen := b.gen
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer = time.AfterFunc(b.delay, func() {
		b.mu.Lock()
		if gen != b.gen {
			// Superseded by a later Ring (or a Flush) while the timer was
			// firing — that call owns the ring now.
			b.mu.Unlock()
			return
		}
		b.timer = nil
		b.mu.Unlock()
		b.ring()
	})
}

// Flush rings immediately if a ring is still pending, and does nothing
// otherwise. Used on shutdown so a bell scheduled moments before the user quits
// isn't silently dropped.
func (b *bell) Flush() {
	b.mu.Lock()
	if b.timer == nil {
		b.mu.Unlock()
		return
	}
	pending := b.timer.Stop()
	b.timer = nil
	b.gen++
	b.mu.Unlock()

	if pending {
		b.ring()
	}
}

// pending reports whether a ring is scheduled but not yet delivered.
func (b *bell) pending() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.timer != nil
}

var defaultBell = newBell(BellDebounce, ringBellNow)

// RingBell asks the terminal for an audible bell, debounced by BellDebounce so
// a burst of task events makes one sound rather than one per event.
func RingBell() {
	defaultBell.Ring()
}

// FlushBell delivers a pending debounced bell right away. Call it before the
// process exits so a bell scheduled just before quitting still reaches the
// terminal.
func FlushBell() {
	defaultBell.Flush()
}

// ringBellNow sends the BEL character (\a) to the terminal to trigger an
// audible bell. It writes directly to /dev/tty to bypass any stdout buffering
// that might occur when running inside a TUI framework like Bubble Tea.
func ringBellNow() {
	// Open /dev/tty directly to write to the actual terminal
	// This bypasses Bubble Tea's alternate screen buffer and stdout capture
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		// Fallback to stderr if /dev/tty is not available
		// (stderr is less likely to be captured than stdout)
		os.Stderr.WriteString("\a")
		return
	}
	defer tty.Close()

	tty.WriteString("\a")
}
