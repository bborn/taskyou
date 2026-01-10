package ui

import (
	"os"
)

// RingBell sends the BEL character (\a) to the terminal to trigger an audible bell.
// It writes directly to /dev/tty to bypass any stdout buffering that might occur
// when running inside a TUI framework like Bubble Tea.
func RingBell() {
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
