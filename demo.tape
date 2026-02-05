# TaskYou Demo Recording
#
# Prerequisites:
#   brew install vhs    (or: go install github.com/charmbracelet/vhs@latest)
#   make build
#   make demo-seed
#
# Record:
#   vhs demo.tape
#
# Output: demo.gif (and demo.mp4)

Require tmux
Require go

Output demo.gif
Output demo.mp4

Set Shell bash
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 1400
Set Height 800
Set Padding 20
Set TypingSpeed 50ms
Set PlaybackSpeed 1

# Seed the demo database fresh
Hide
Type "go run ./cmd/demoseed ~/.local/share/task/demo.db 2>/dev/null"
Enter
Sleep 2s
Type "clear"
Enter
Sleep 500ms
Show

# -----------------------------------------------------------
# Scene 1: Launch TaskYou
# -----------------------------------------------------------
Type "WORKTREE_DB_PATH=~/.local/share/task/demo.db ./bin/task -l"
Sleep 1s
Enter
Sleep 3s

# -----------------------------------------------------------
# Scene 2: Browse the Kanban Board
# -----------------------------------------------------------
# Navigate to In Progress column
Right
Sleep 1.5s

# Navigate to Blocked column
Right
Sleep 1.5s

# Navigate to Done column
Right
Sleep 1.5s

# Back to Backlog
Left
Left
Left
Sleep 1s

# Move down through backlog tasks
Down
Sleep 800ms
Down
Sleep 800ms
Down
Sleep 800ms

# -----------------------------------------------------------
# Scene 3: View Task Detail
# -----------------------------------------------------------
# Go back to first task
Up
Up
Up
Sleep 500ms
Enter
Sleep 3s

# Scroll down in detail view
Down
Sleep 1s
Down
Sleep 1s

# Return to board
Escape
Sleep 1.5s

# -----------------------------------------------------------
# Scene 4: View a completed task
# -----------------------------------------------------------
# Jump to Done column
Type "D"
Sleep 1.5s

# View first done task
Enter
Sleep 3s

# Back to board
Escape
Sleep 1s

# -----------------------------------------------------------
# Scene 5: Project Memories
# -----------------------------------------------------------
Type "m"
Sleep 3s

# Browse memories
Down
Sleep 800ms
Down
Sleep 800ms
Down
Sleep 800ms

# Back to board
Escape
Sleep 1.5s

# -----------------------------------------------------------
# Scene 6: Create a New Task
# -----------------------------------------------------------
Type "n"
Sleep 2s

# Type the task title
Type "Add CSV export to analytics dashboard"
Sleep 500ms

# Tab to next field (body)
Tab
Sleep 500ms
Type "Users need to export their analytics data as CSV files. Add an export button to the dashboard that generates a CSV with the current view's data."
Sleep 500ms

# Tab to project (should be a dropdown)
Tab
Sleep 500ms

# Tab to type
Tab
Sleep 500ms

# Tab to tags
Tab
Sleep 500ms
Type "feature,analytics,export"
Sleep 500ms

# Save the task (Ctrl+S)
Ctrl+S
Sleep 2s

# -----------------------------------------------------------
# Scene 7: Filter Tasks
# -----------------------------------------------------------
Type "/"
Sleep 1s
Type "analytics"
Sleep 2s

# Clear filter
Escape
Sleep 1.5s

# -----------------------------------------------------------
# Scene 8: Quick column jumps
# -----------------------------------------------------------
Type "B"
Sleep 1s
Type "P"
Sleep 1s
Type "L"
Sleep 1s
Type "D"
Sleep 1s
Type "B"
Sleep 1s

# -----------------------------------------------------------
# Scene 9: Help overlay
# -----------------------------------------------------------
Type "?"
Sleep 4s
Escape
Sleep 1s

# -----------------------------------------------------------
# Done - pause on the board
# -----------------------------------------------------------
Sleep 3s

# Exit
Ctrl+C
Sleep 1s
