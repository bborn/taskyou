// Package config provides application configuration including keybindings.
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// KeybindingConfig represents a single keybinding configuration.
type KeybindingConfig struct {
	Keys []string `yaml:"keys"` // Key(s) that trigger the action
	Help string   `yaml:"help"` // Help text displayed in the UI
}

// KeybindingsConfig holds all customizable keybindings.
type KeybindingsConfig struct {
	Left               *KeybindingConfig `yaml:"left,omitempty"`
	Right              *KeybindingConfig `yaml:"right,omitempty"`
	Up                 *KeybindingConfig `yaml:"up,omitempty"`
	Down               *KeybindingConfig `yaml:"down,omitempty"`
	Enter              *KeybindingConfig `yaml:"enter,omitempty"`
	Back               *KeybindingConfig `yaml:"back,omitempty"`
	New                *KeybindingConfig `yaml:"new,omitempty"`
	Edit               *KeybindingConfig `yaml:"edit,omitempty"`
	Queue              *KeybindingConfig `yaml:"queue,omitempty"`
	Retry              *KeybindingConfig `yaml:"retry,omitempty"`
	Close              *KeybindingConfig `yaml:"close,omitempty"`
	Archive            *KeybindingConfig `yaml:"archive,omitempty"`
	Delete             *KeybindingConfig `yaml:"delete,omitempty"`
	Refresh            *KeybindingConfig `yaml:"refresh,omitempty"`
	Settings           *KeybindingConfig `yaml:"settings,omitempty"`
	Help               *KeybindingConfig `yaml:"help,omitempty"`
	Quit               *KeybindingConfig `yaml:"quit,omitempty"`
	ChangeStatus       *KeybindingConfig `yaml:"change_status,omitempty"`
	CommandPalette     *KeybindingConfig `yaml:"command_palette,omitempty"`
	ToggleDangerous    *KeybindingConfig `yaml:"toggle_dangerous,omitempty"`
	TogglePin          *KeybindingConfig `yaml:"toggle_pin,omitempty"`
	Filter             *KeybindingConfig `yaml:"filter,omitempty"`
	OpenWorktree       *KeybindingConfig `yaml:"open_worktree,omitempty"`
	ToggleShellPane    *KeybindingConfig `yaml:"toggle_shell_pane,omitempty"`
	JumpToNotification *KeybindingConfig `yaml:"jump_to_notification,omitempty"`
	FocusBacklog       *KeybindingConfig `yaml:"focus_backlog,omitempty"`
	FocusInProgress    *KeybindingConfig `yaml:"focus_in_progress,omitempty"`
	FocusBlocked       *KeybindingConfig `yaml:"focus_blocked,omitempty"`
	FocusDone          *KeybindingConfig `yaml:"focus_done,omitempty"`
	JumpToPinned       *KeybindingConfig `yaml:"jump_to_pinned,omitempty"`
	JumpToUnpinned     *KeybindingConfig `yaml:"jump_to_unpinned,omitempty"`
	OpenBrowser        *KeybindingConfig `yaml:"open_browser,omitempty"`
	ApprovePrompt      *KeybindingConfig `yaml:"approve_prompt,omitempty"`
	DenyPrompt         *KeybindingConfig `yaml:"deny_prompt,omitempty"`
}

// DefaultKeybindingsConfigPath returns the default path for the keybindings config file.
func DefaultKeybindingsConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "taskyou", "keybindings.yaml")
}

// LoadKeybindings loads keybindings from the default config path.
// Returns nil if the file doesn't exist (not an error - just use defaults).
func LoadKeybindings() (*KeybindingsConfig, error) {
	return LoadKeybindingsFromPath(DefaultKeybindingsConfigPath())
}

// LoadKeybindingsFromPath loads keybindings from a specific path.
// Returns nil if the file doesn't exist (not an error - just use defaults).
func LoadKeybindingsFromPath(path string) (*KeybindingsConfig, error) {
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // File doesn't exist, use defaults
		}
		return nil, err
	}

	var config KeybindingsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// GenerateDefaultKeybindingsYAML generates a YAML string with all default keybindings.
// This can be used to create an example config file.
func GenerateDefaultKeybindingsYAML() string {
	return `# TaskYou Keybindings Configuration
# Customize keyboard shortcuts by modifying the keys below.
# Each keybinding has:
#   keys: list of key(s) that trigger the action (e.g., ["n"], ["ctrl+p", "p"])
#   help: text shown in the help menu
#
# Available key formats:
#   - Single keys: "a", "n", "?"
#   - Modified keys: "ctrl+c", "ctrl+p", "shift+up"
#   - Special keys: "enter", "esc", "left", "right", "up", "down"
#
# Only include keybindings you want to customize.
# Omitted keybindings will use defaults.

# Navigation
left:
  keys: ["left"]
  help: "prev col"

right:
  keys: ["right"]
  help: "next col"

up:
  keys: ["up"]
  help: "up"

down:
  keys: ["down"]
  help: "down"

# Actions
enter:
  keys: ["enter"]
  help: "view"

back:
  keys: ["esc"]
  help: "back"

new:
  keys: ["n"]
  help: "new"

edit:
  keys: ["e"]
  help: "edit"

queue:
  keys: ["x"]
  help: "execute"

retry:
  keys: ["r"]
  help: "retry"

close:
  keys: ["c"]
  help: "close"

archive:
  keys: ["a"]
  help: "archive"

delete:
  keys: ["d"]
  help: "delete"

refresh:
  keys: ["R"]
  help: "refresh"

settings:
  keys: ["s"]
  help: "settings"

help:
  keys: ["?"]
  help: "help"

quit:
  keys: ["ctrl+c"]
  help: "quit"

change_status:
  keys: ["S"]
  help: "status"

command_palette:
  keys: ["p", "ctrl+p"]
  help: "go to task"

toggle_dangerous:
  keys: ["!"]
  help: "dangerous mode"

toggle_pin:
  keys: ["t"]
  help: "pin/unpin"

filter:
  keys: ["/"]
  help: "filter"

open_worktree:
  keys: ["o"]
  help: "open in editor"

toggle_shell_pane:
  keys: ["\\"]
  help: "toggle shell"

jump_to_notification:
  keys: ["g"]
  help: "go to notification"

# Column focus shortcuts
focus_backlog:
  keys: ["B"]
  help: "backlog"

focus_in_progress:
  keys: ["P"]
  help: "in progress"

focus_blocked:
  keys: ["L"]
  help: "blocked"

focus_done:
  keys: ["D"]
  help: "done"

# Jump to pinned/unpinned tasks
jump_to_pinned:
  keys: ["shift+up"]
  help: "jump to pinned"

jump_to_unpinned:
  keys: ["shift+down"]
  help: "jump to unpinned"

open_browser:
  keys: ["b"]
  help: "open in browser"
`
}
