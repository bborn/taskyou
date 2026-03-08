# newcomer.tape — Complete Newcomer Journey
#
# Persona: Sarah, a developer who just installed TaskYou.
# Scenario: First launch, exploring the interface, creating her first task.
# Key questions:
#   - Can she understand the Kanban layout without instructions?
#   - Is the help system accessible and useful?
#   - Is creating a task intuitive on first try?
#   - Does the interface communicate available actions?

Output vhs/output/gifs/persona-newcomer.gif

Set Shell bash
Set FontSize 16
Set Width 1400
Set Height 800
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 75ms

Env WORKTREE_DB_PATH "/tmp/vhs-taskyou-test/tasks.db"
Env TMUX "vhs-testing"
Env PATH "/Users/bruno/Projects/workflow/.task-worktrees/1041-use-vhs-to-recursively-develop-taskyou/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

Type "ty"
Enter
Sleep 6s

Screenshot vhs/output/screenshots/persona-newcomer-01-first-look.png
Sleep 2s

# Try help
Type "?"
Sleep 3s
Screenshot vhs/output/screenshots/persona-newcomer-02-help.png

Type "?"
Sleep 1s

# Explore
Down
Sleep 800ms
Down
Sleep 800ms
Up
Sleep 800ms

Right
Sleep 1s
Screenshot vhs/output/screenshots/persona-newcomer-03-empty-column.png

Left
Sleep 1s

# View a task
Type "1"
Sleep 500ms
Enter
Sleep 3s
Screenshot vhs/output/screenshots/persona-newcomer-04-first-detail.png

Escape
Sleep 1s

# Create a task
Type "n"
Sleep 2s
Screenshot vhs/output/screenshots/persona-newcomer-05-new-form.png

Tab
Sleep 500ms
Type "My first task"
Sleep 500ms
Tab
Sleep 500ms
Type "Testing out TaskYou for the first time. Excited to see what this can do!"
Sleep 1s
Tab
Sleep 500ms
Tab
Sleep 500ms
Tab
Sleep 500ms

Enter
Sleep 2s
Screenshot vhs/output/screenshots/persona-newcomer-06-created.png

Ctrl+c
Sleep 1s
Type "y"
Sleep 1s
