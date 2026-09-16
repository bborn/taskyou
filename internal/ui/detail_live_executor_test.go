package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestLiveExecutorInPaneCommands is the core of the "lost executor pane" self-heal.
// When the detail view opens a task that already has a daemon tmux window, it must
// rejoin that window ONLY if the window still holds a live executor pane. A window
// left with just its keep-alive `tail` placeholder and/or a plain shell (e.g. an
// executor that exited before the CLAUDE_CONFIG_DIR fix) must be treated as dead so
// the open path rebuilds it instead of rejoining a corpse forever.
// TestLiveExecutorInPanes covers the rule that stopped two TUIs killing each
// other's freshly launched agent: a starting agent reports `sh`, exactly as a
// dead window's shell does, so launch provenance decides, not the command.
func TestLiveExecutorInPanes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	recent := strconv.FormatInt(now.Add(-5*time.Second).Unix(), 10)
	old := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
	pane := func(dead, spawned, cur, start string) string {
		return strings.Join([]string{dead, spawned, cur, start}, "\t")
	}
	tests := []struct {
		name  string
		lines []string
		want  bool
	}{
		{"starting agent still in sh -c, beside a shell",
			[]string{pane("0", "", "sh", `sh -c "WORKTREE_TASK_ID=5436 claude"`), pane("0", "", "zsh", "/bin/zsh")}, true},
		{"just spawned window whose agent shows nothing yet",
			[]string{pane("0", recent, "zsh", "/bin/zsh")}, true},
		{"running agent", []string{pane("0", old, "2.1.272", `sh -c "claude"`)}, true},
		{"agent exited: only the shell is left", []string{pane("0", old, "zsh", "/bin/zsh")}, false},
		{"agent pane dead (remain-on-exit)", []string{pane("1", old, "sh", `sh -c "claude"`), pane("0", old, "zsh", "/bin/zsh")}, false},
		{"legacy placeholder window", []string{pane("0", "", "tail", "tail -f /dev/null"), pane("0", "", "zsh", "")}, false},
		{"legacy untagged live agent", []string{pane("0", "", "2.1.169", "")}, true},
		{"nothing listed", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := liveExecutorInPanes(tt.lines, now); got != tt.want {
				t.Errorf("liveExecutorInPanes(%q) = %v, want %v", tt.lines, got, tt.want)
			}
		})
	}
}

func TestLiveExecutorInPaneCommands(t *testing.T) {
	tests := []struct {
		name string
		cmds []string
		want bool
	}{
		{"live claude (version) beside shell", []string{"2.1.169", "zsh"}, true},
		{"live codex beside shell", []string{"codex", "zsh"}, true},
		{"live subprocess (agent spawned node)", []string{"node"}, true},
		{"dead: placeholder + shell", []string{"tail", "zsh"}, false},
		{"dead: placeholder only", []string{"tail"}, false},
		{"dead: interactive shell only", []string{"zsh"}, false},
		{"dead: login shell only", []string{"-zsh"}, false},
		{"dead: login bash + placeholder", []string{"-bash", "tail"}, false},
		{"dead: empty list", nil, false},
		{"dead: blank entries", []string{"", "  "}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := liveExecutorInPaneCommands(tt.cmds); got != tt.want {
				t.Errorf("liveExecutorInPaneCommands(%q) = %v, want %v", tt.cmds, got, tt.want)
			}
		})
	}
}
