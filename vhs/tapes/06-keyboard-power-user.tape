# 06-keyboard-power-user.tape — Power User Keyboard Workflow
#
# Persona: Experienced user who never touches the mouse.
# Goal: Demonstrate rapid keyboard-driven task management.
# Insights: Do shortcuts feel natural? Is the keyboard flow efficient?

Output vhs/output/gifs/06-keyboard-power-user.gif

Set Shell bash
Set FontSize 16
Set Width 1400
Set Height 800
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 30ms

Env WORKTREE_DB_PATH "/tmp/vhs-taskyou-test/tasks.db"
Env TMUX "vhs-testing"
Env PATH "/Users/bruno/Projects/workflow/.task-worktrees/1041-use-vhs-to-recursively-develop-taskyou/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

Type "ty"
Enter
Sleep 6s

# Quick select and open task 1
Type "1"
Sleep 500ms
Enter
Sleep 2s
Screenshot vhs/output/screenshots/06-detail-view.png

Escape
Sleep 1s

# Change status
Type "5"
Sleep 500ms
Type "S"
Sleep 1s
Screenshot vhs/output/screenshots/06-change-status.png

Escape
Sleep 1s

# Pin a task
Type "3"
Sleep 500ms
Type "t"
Sleep 1s
Screenshot vhs/output/screenshots/06-pinned-task.png

Type "t"
Sleep 1s

# Rapid column jumping
Type "P"
Sleep 500ms
Type "L"
Sleep 500ms
Type "D"
Sleep 500ms
Type "B"
Sleep 500ms

Type "R"
Sleep 1s

Screenshot vhs/output/screenshots/06-rapid-nav.png

Ctrl+c
Sleep 1s
Type "y"
Sleep 1s
