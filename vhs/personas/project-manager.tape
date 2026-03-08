# project-manager.tape — Project Manager Organizing Work
#
# Persona: Jordan, a PM who oversees multiple project streams.
# Scenario: Weekly review - filter by project, check status distribution, organize.
# Key questions:
#   - Can a PM get a quick project-level overview?
#   - Is filtering by project efficient for multi-project management?
#   - Does the board provide enough visibility for planning?
#   - Are task counts and distributions easy to read?

Output vhs/output/gifs/persona-project-manager.gif

Set Shell bash
Set FontSize 16
Set Width 1400
Set Height 800
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Env WORKTREE_DB_PATH "/tmp/vhs-taskyou-test/tasks.db"
Env TMUX "vhs-testing"
Env PATH "/Users/bruno/Projects/workflow/.task-worktrees/1041-use-vhs-to-recursively-develop-taskyou/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

Type "ty"
Enter
Sleep 6s

Screenshot vhs/output/screenshots/persona-pm-01-overview.png
Sleep 1s

# Review each project
Type "/"
Sleep 500ms
Type "[backend"
Sleep 1s
Screenshot vhs/output/screenshots/persona-pm-02-backend.png

Escape
Sleep 800ms

Type "/"
Sleep 500ms
Type "[frontend"
Sleep 1s
Screenshot vhs/output/screenshots/persona-pm-03-frontend.png

Escape
Sleep 800ms

Type "/"
Sleep 500ms
Type "[infrastructure"
Sleep 1s
Screenshot vhs/output/screenshots/persona-pm-04-infrastructure.png

Escape
Sleep 800ms

Type "/"
Sleep 500ms
Type "[product"
Sleep 1s
Screenshot vhs/output/screenshots/persona-pm-05-product.png

Escape
Sleep 800ms

# Check project settings
Type "s"
Sleep 1s
Tab
Sleep 1s
Screenshot vhs/output/screenshots/persona-pm-06-project-settings.png

Escape
Sleep 1s

# Review a task
Type "2"
Sleep 500ms
Enter
Sleep 2s
Screenshot vhs/output/screenshots/persona-pm-07-task-review.png

Escape
Sleep 1s
Screenshot vhs/output/screenshots/persona-pm-08-final.png

Ctrl+c
Sleep 1s
Type "y"
Sleep 1s
