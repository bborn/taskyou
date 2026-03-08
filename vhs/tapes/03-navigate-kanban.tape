# 03-navigate-kanban.tape — Kanban Board Navigation
#
# Persona: User managing their daily workflow on the board.
# Goal: Test column navigation, task selection, and quick-select shortcuts.
# Insights: Is navigation fluid? Are shortcuts discoverable?

Output vhs/output/gifs/03-navigate-kanban.gif

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

Type "ty"
Enter
Sleep 6s

# Navigate down through backlog tasks
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 400ms

Screenshot vhs/output/screenshots/03-scrolled-backlog.png

Up
Sleep 300ms
Up
Sleep 300ms
Up
Sleep 300ms
Up
Sleep 300ms
Up
Sleep 300ms

# Quick-select with number keys
Type "3"
Sleep 1s
Screenshot vhs/output/screenshots/03-quick-select.png

# Jump to backlog column
Type "B"
Sleep 800ms
Screenshot vhs/output/screenshots/03-backlog-column.png

# Open task detail
Enter
Sleep 2s
Screenshot vhs/output/screenshots/03-task-detail.png

Escape
Sleep 1s

Ctrl+c
Sleep 1s
Type "y"
Sleep 1s
