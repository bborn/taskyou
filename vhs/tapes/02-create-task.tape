# 02-create-task.tape — Creating a New Task
#
# Persona: User who wants to add a new task to their board.
# Goal: Walk through the new task creation form.
# Insights: Is the form intuitive? Are fields clear? Is the flow smooth?

Output vhs/output/gifs/02-create-task.gif

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

# Press 'n' to create new task
Type "n"
Sleep 1s
Screenshot vhs/output/screenshots/02-new-task-form.png

# Tab past project field
Tab
Sleep 500ms

# Type task title
Type "Fix login page responsive layout"
Sleep 500ms

# Tab to body
Tab
Sleep 500ms

Type "The login form breaks on mobile screens below 375px width. Buttons overflow and the forgot password link is hidden."
Sleep 500ms

Screenshot vhs/output/screenshots/02-form-filled.png

# Tab through remaining fields
Tab
Sleep 500ms
Tab
Sleep 500ms
Tab
Sleep 500ms

Enter
Sleep 2s

Screenshot vhs/output/screenshots/02-task-created.png

Ctrl+c
Sleep 1s
Type "y"
Sleep 1s
