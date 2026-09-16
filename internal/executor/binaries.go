package executor

import (
	"os/exec"
	"sort"
)

// executorBinaries names the CLI each executor drives, in the order it looks
// for them. Cursor ships under two names and accepts either.
//
// One map, because two places need the answer and they must not drift: each
// executor's IsAvailable, and `ty doctor`, which reports whether the binaries
// for the executors this install actually uses are on PATH. A doctor that
// consulted its own list would eventually bless an executor that cannot run.
var executorBinaries = map[string][]string{
	claudeSlug: {"claude"},
	"codex":    {"codex"},
	"gemini":   {"gemini"},
	"grok":     {"grok"},
	"cursor":   {"cursor-agent", "agent"},
	"openclaw": {"openclaw"},
	"opencode": {"opencode"},
	"pi":       {"pi"},
}

// claudeSlug spells out db.ExecutorClaude rather than importing internal/db for
// one string in a var initializer. The tests assert the two are equal.
const claudeSlug = "claude"

// ExecutorBinaries returns the CLI names an executor can run as, or nil for an
// executor with no binary of its own.
func ExecutorBinaries(slug string) []string { return executorBinaries[slug] }

// ExecutorsWithBinaries lists, sorted, every executor slug this build knows how
// to look for on PATH.
func ExecutorsWithBinaries() []string {
	out := make([]string, 0, len(executorBinaries))
	for name := range executorBinaries {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// binaryOnPath reports whether any of an executor's CLI names resolves on PATH.
func binaryOnPath(slug string) bool {
	for _, bin := range executorBinaries[slug] {
		if _, err := exec.LookPath(bin); err == nil {
			return true
		}
	}
	return false
}
