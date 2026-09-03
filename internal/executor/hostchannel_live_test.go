package executor

import (
	"os"
	"testing"
	"time"
)

// A live end-to-end check against a real fleet host, opt-in via TY_LIVE_HOST.
// It proves the thing unit tests cannot: that the script survives ty's own
// quoting, the host's login shell, and a real tmux with real task windows.
func TestHostChannelAgainstALiveHost(t *testing.T) {
	host := os.Getenv("TY_LIVE_HOST")
	if host == "" {
		t.Skip("set TY_LIVE_HOST to run against a real host")
	}

	c := startHostChannel(host)
	defer c.Close()

	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		snap := c.snap
		c.mu.RUnlock()
		if !snap.At.IsZero() {
			t.Logf("snapshot from %s: %d task windows", host, len(snap.Windows))
			for target, win := range snap.Windows {
				t.Logf("  %-28s sum=%s pane=%d bytes", target, win.Sum, len(win.Content))
			}
			if len(snap.Windows) == 0 {
				t.Logf("(host has no live ty task windows right now)")
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("no snapshot from %s within the deadline", host)
}
