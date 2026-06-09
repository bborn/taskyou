package ui

import "testing"

// TestLiveExecutorInPaneCommands is the core of the "lost executor pane" self-heal.
// When the detail view opens a task that already has a daemon tmux window, it must
// rejoin that window ONLY if the window still holds a live executor pane. A window
// left with just its keep-alive `tail` placeholder and/or a plain shell (e.g. an
// executor that exited before the CLAUDE_CONFIG_DIR fix) must be treated as dead so
// the open path rebuilds it instead of rejoining a corpse forever.
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
