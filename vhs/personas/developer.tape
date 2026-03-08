# developer.tape — Developer Daily Workflow
#
# Persona: Mike, a developer who uses TaskYou to manage his daily coding tasks.
# Scenario: Pick a task, view details, attempt to execute it, check status.
# Key questions:
#   - Is the task-to-execution flow smooth?
#   - Is the detail view informative enough for starting work?
#   - Does status management feel intuitive?
#   - Can developers quickly context-switch between tasks?

Output vhs/output/gifs/persona-developer.gif

Set Shell bash
Set FontSize 16
Set Width 1400
Set Height 800
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 45ms

Env WORKTREE_DB_PATH "/tmp/vhs-taskyou-test/tasks.db"
Env TMUX "vhs-testing"
Env PATH "/Users/bruno/Projects/workflow/.task-worktrees/1041-use-vhs-to-recursively-develop-taskyou/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

Type "ty"
Enter
Sleep 6s

Screenshot vhs/output/screenshots/persona-dev-01-board.png
Sleep 1s

# Check first task
Type "1"
Sleep 500ms
Enter
Sleep 3s
Screenshot vhs/output/screenshots/persona-dev-02-task-detail.png

Sleep 2s
Escape
Sleep 1s

# Try another task
Type "4"
Sleep 500ms
Enter
Sleep 2s
Screenshot vhs/output/screenshots/persona-dev-03-alt-task.png

Escape
Sleep 1s

# Change status
Type "4"
Sleep 500ms
Type "S"
Sleep 1s
Screenshot vhs/output/screenshots/persona-dev-04-status-change.png

Escape
Sleep 1s

# Search for task
Ctrl+p
Sleep 500ms
Type "database"
Sleep 1s
Screenshot vhs/output/screenshots/persona-dev-05-search.png

Enter
Sleep 2s
Screenshot vhs/output/screenshots/persona-dev-06-search-result.png

Escape
Sleep 1s
Screenshot vhs/output/screenshots/persona-dev-07-final.png

Ctrl+c
Sleep 1s
Type "y"
Sleep 1s
