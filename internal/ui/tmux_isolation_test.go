package ui

import (
	"os"
	"testing"

	"github.com/bborn/workflow/internal/tmuxtest"
)

// TestMain keeps every test in this package off the live tmux server; see
// internal/tmuxtest for why moving TMUX_TMPDIR alone was not enough.
func TestMain(m *testing.M) { os.Exit(tmuxtest.Main(m)) }
