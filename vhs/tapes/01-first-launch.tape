# 01-first-launch.tape — First Launch Experience
#
# Persona: Brand new user launching TaskYou for the first time.
# Goal: Capture the initial impression of the Kanban dashboard.
# Insights: Is the interface self-explanatory? Can users understand the layout?

Output vhs/output/gifs/01-first-launch.gif
Output vhs/output/screenshots/01-first-launch-final.png

Set Shell bash
Set FontSize 16
Set Width 1400
Set Height 800
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 50ms

Env WORKTREE_DB_PATH "/tmp/vhs-taskyou-test/tasks.db"
Env TMUX "vhs-testing"
Env PATH "/Users/bruno/Projects/workflow/.task-worktrees/1041-use-vhs-to-recursively-develop-taskyou/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

# Launch the TUI
Type "ty"
Enter
Sleep 6s

Screenshot vhs/output/screenshots/01-dashboard-initial.png

Sleep 2s

# Try pressing ? for help
Type "?"
Sleep 2s
Screenshot vhs/output/screenshots/01-help-overlay.png

# Dismiss help
Type "?"
Sleep 1s

# Navigate between columns
Right
Sleep 1s
Right
Sleep 1s
Right
Sleep 1s

Left
Left
Left
Sleep 1s

# Navigate down through tasks
Down
Sleep 500ms
Down
Sleep 500ms
Down
Sleep 500ms

Screenshot vhs/output/screenshots/01-task-selected.png

Ctrl+c
Sleep 1s
Type "y"
Sleep 1s
