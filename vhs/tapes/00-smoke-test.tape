# 00-smoke-test.tape — Quick smoke test to verify VHS + ty works
#
# Minimal test: launch ty, screenshot, exit.

Output vhs/output/gifs/00-smoke-test.gif

Set Shell bash
Set FontSize 16
Set Width 1400
Set Height 800
Set Padding 10

Env WORKTREE_DB_PATH "/tmp/vhs-taskyou-test/tasks.db"
Env TMUX "vhs-testing"
Env PATH "/Users/bruno/Projects/workflow/.task-worktrees/1041-use-vhs-to-recursively-develop-taskyou/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

Type "ty"
Enter
Sleep 6s

Screenshot vhs/output/screenshots/00-smoke-test.png

Ctrl+c
Sleep 1s
Type "y"
Sleep 1s
