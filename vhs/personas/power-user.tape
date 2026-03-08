# power-user.tape — Experienced Power User Workflow
#
# Persona: Alex, a senior developer who uses TaskYou daily.
# Scenario: Morning routine - triage tasks, reassign priorities, use shortcuts.
# Key questions:
#   - Is the keyboard-driven workflow efficient?
#   - Can rapid column switching keep up?
#   - Is the command palette fast enough for real-time use?
#   - Do status transitions feel smooth?

Output vhs/output/gifs/persona-power-user.gif

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

Screenshot vhs/output/screenshots/persona-power-01-morning.png

# Pin important tasks
Type "1"
Sleep 300ms
Type "t"
Sleep 800ms

Type "5"
Sleep 300ms
Type "t"
Sleep 800ms
Screenshot vhs/output/screenshots/persona-power-02-pinned.png

# Quick column scan
Type "P"
Sleep 500ms
Type "L"
Sleep 500ms
Type "D"
Sleep 500ms
Type "B"
Sleep 500ms

# Command palette search
Ctrl+p
Sleep 800ms
Type "API doc"
Sleep 1s
Screenshot vhs/output/screenshots/persona-power-03-palette.png
Enter
Sleep 2s

Screenshot vhs/output/screenshots/persona-power-04-detail.png

Escape
Sleep 500ms

# Filter by project
Type "/"
Sleep 300ms
Type "[frontend"
Sleep 1s
Screenshot vhs/output/screenshots/persona-power-05-filtered.png

Escape
Sleep 500ms

# Quick task creation
Type "n"
Sleep 800ms
Tab
Sleep 300ms
Type "Hotfix: production auth token expiry"
Tab
Sleep 300ms
Type "Users getting logged out unexpectedly. JWT refresh not working."
Tab
Sleep 300ms
Tab
Sleep 300ms
Tab
Sleep 300ms
Enter
Sleep 2s
Screenshot vhs/output/screenshots/persona-power-06-quick-create.png

Ctrl+c
Sleep 1s
Type "y"
Sleep 1s
