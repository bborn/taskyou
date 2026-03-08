# 04-filter-search.tape — Filtering and Search
#
# Persona: User with many tasks who needs to find specific ones.
# Goal: Test the filter bar and command palette search.
# Insights: Is filtering discoverable? Does search feel fast and accurate?

Output vhs/output/gifs/04-filter-search.gif

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

# Activate filter
Type "/"
Sleep 1s
Screenshot vhs/output/screenshots/04-filter-active.png

# Filter by project
Type "[backend"
Sleep 1s
Screenshot vhs/output/screenshots/04-filter-backend.png

Escape
Sleep 1s

# Filter by text pattern
Type "/"
Sleep 500ms
Type "dark mode"
Sleep 1s
Screenshot vhs/output/screenshots/04-filter-text.png

Escape
Sleep 1s

# Command palette
Ctrl+p
Sleep 1s
Screenshot vhs/output/screenshots/04-command-palette.png

Type "CI/CD"
Sleep 1s
Screenshot vhs/output/screenshots/04-palette-search.png

Enter
Sleep 2s
Screenshot vhs/output/screenshots/04-palette-result.png

Escape
Sleep 1s

Ctrl+c
Sleep 1s
Type "y"
Sleep 1s
