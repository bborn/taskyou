package executor

import (
	"os"
	"os/exec"
	"path/filepath"
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

// TestRCFlagShellInjection verifies a task title containing shell metacharacters
// cannot inject commands. rcFlag's output is concatenated into a script that the
// executor runs via `sh -c`, and the title is arbitrary user/MCP/daemon-supplied
// text, so it must be shell-quoted (not Go-quoted with %q, which leaves $(...)
// and backticks live). We prove safety by running the flag through `sh` exactly
// the way the executor does and asserting the injected command never executes.
func TestRCFlagShellInjection(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "pwned")

	for _, title := range []string{
		"$(touch " + sentinel + ")",
		"`touch " + sentinel + "`",
		`"; touch ` + sentinel + `; echo "`,
		"normal'title$(touch " + sentinel + ")",
	} {
		got := rcFlag(&db.Task{RemoteControl: true, Title: title})

		// `echo <flag>` mirrors how the flag lands inside the executor's
		// `sh -c` script: if the title is unsafely quoted, the substitution
		// fires and creates the sentinel file.
		if out, err := exec.Command("sh", "-c", "echo "+got).CombinedOutput(); err != nil {
			t.Fatalf("sh -c failed for title %q: %v (%s)", title, err, out)
		}
		if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
			t.Fatalf("title %q injected a command: sentinel %s exists", title, sentinel)
		}
	}
}
