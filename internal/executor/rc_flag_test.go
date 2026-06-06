package executor

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// TestRCFlag verifies the rcFlag helper produces the expected --remote-control
// flag, including the task-title fallback to task-<id>.
func TestRCFlag(t *testing.T) {
	// Remote Control disabled => empty string.
	if got := rcFlag(&db.Task{RemoteControl: false}); got != "" {
		t.Errorf("rcFlag(disabled) = %q, want %q", got, "")
	}

	// Remote Control enabled with a title => flag contains --remote-control and the title.
	got := rcFlag(&db.Task{RemoteControl: true, Title: "My Task"})
	if !strings.Contains(got, "--remote-control") {
		t.Errorf("rcFlag(enabled) = %q, want it to contain --remote-control", got)
	}
	if !strings.Contains(got, "My Task") {
		t.Errorf("rcFlag(enabled) = %q, want it to contain the task title", got)
	}

	// Remote Control enabled with an empty title => falls back to task-<id>.
	got = rcFlag(&db.Task{RemoteControl: true, ID: 42, Title: ""})
	if !strings.Contains(got, "--remote-control") {
		t.Errorf("rcFlag(empty title) = %q, want it to contain --remote-control", got)
	}
	if !strings.Contains(got, "task-42") {
		t.Errorf("rcFlag(empty title) = %q, want it to contain the task-<id> fallback", got)
	}
}
