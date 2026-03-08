# 05-settings.tape — Settings View
#
# Persona: User customizing their TaskYou experience.
# Goal: Navigate through settings sections (themes, projects, types).
# Insights: Is the settings layout clear? Can users find what they need?

Output vhs/output/gifs/05-settings.gif

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

Type "s"
Sleep 1s
Screenshot vhs/output/screenshots/05-settings-themes.png

Tab
Sleep 1s
Screenshot vhs/output/screenshots/05-settings-projects.png

Tab
Sleep 1s
Screenshot vhs/output/screenshots/05-settings-types.png

Escape
Sleep 1s

Ctrl+c
Sleep 1s
Type "y"
Sleep 1s
