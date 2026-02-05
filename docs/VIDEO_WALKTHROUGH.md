# TaskYou Video Walkthrough Script

This document provides a complete script and walkthrough for creating a screencast demo of TaskYou.

## Pre-Recording Setup

### 1. Create Demo Database

```bash
# Build the project first
make build

# Create a fresh demo database with sample data
make demo-seed
```

This creates `~/.local/share/task/demo.db` with:
- 4 sample projects (acme-webapp, mobile-app, infra, personal)
- 12 sample tasks in various states (backlog, queued, processing, blocked, done)
- Project memories demonstrating the learning feature
- Sample task logs

### 2. Terminal Setup

- Use a clean terminal with good contrast (dark theme recommended)
- Set terminal to ~120 columns x 40 rows for good visibility
- Font size: 14-16pt for readability in video
- Consider using a simple prompt like `$` or hide it entirely

### 3. Automated Recording with VHS

The easiest way to record is using [VHS](https://github.com/charmbracelet/vhs) which generates a GIF and MP4 from a scripted tape file:

```bash
# Install VHS
brew install vhs

# Record the demo (builds, seeds, and records)
make demo-record
```

This runs `demo.tape` which automates the entire walkthrough and outputs `demo.gif` and `demo.mp4`.

Edit `demo.tape` to adjust timing, scenes, or add new interactions.

### 4. Manual Recording

For a manual screencast (voiceover, custom pacing):

```bash
# Launch TaskYou with demo database
WORKTREE_DB_PATH=~/.local/share/task/demo.db ./bin/task -l

# Or use the shortcut:
make demo
```

---

## Video Script

### Opening (~15 seconds)

**[Show terminal with TaskYou logo/title if you have one, or just start with launch]**

> "TaskYou is a terminal-based task queue that lets Claude Code work on your tasks autonomously. Let me show you how it works."

---

### Scene 1: The Kanban Board (~30 seconds)

**[Launch the app - it opens in tmux with the Kanban board visible]**

> "When you open TaskYou, you see a Kanban-style board. Tasks flow from Backlog, through In Progress, and either get Blocked waiting for input, or end up Done."

**[Use arrow keys to navigate between columns]**

> "I can navigate between columns with the arrow keys. Each task shows its project, type, and when it was created."

**[Highlight the different colored project labels]**

> "Notice the color-coded project labels - this makes it easy to see what you're working on at a glance."

---

### Scene 2: Task Detail View (~45 seconds)

**[Press Enter on a task to open the detail view]**

> "Press Enter to see the full details of a task."

**[Show a task with a detailed body]**

> "Here I can see the complete task description, any tags for categorization, and the task history."

**[Navigate to a completed task with PR info if available]**

> "For completed tasks, I can see the Pull Request that was created. TaskYou automatically submits PRs when code tasks are done."

**[Press Escape to go back]**

---

### Scene 3: Creating a New Task (~45 seconds)

**[Press 'n' to open the new task form]**

> "To create a new task, I press 'n'. The form lets me specify everything Claude needs."

**[Fill in the form as you talk]**

> "I'll add a title... select my project from the dropdown... choose 'Code' as the task type since this is a development task..."

**[Type in the body field]**

> "In the body, I describe what I want done. The more detail I provide, the better Claude understands the task."

**[Show the tags field]**

> "I can add tags for organization and searching."

**[Navigate to Save with Tab, or press Ctrl+S]**

> "When I'm ready, I save the task and it goes into the Backlog."

---

### Scene 4: Starting a Task (~30 seconds)

**[Navigate to a backlog task]**

> "To start working on a task, I select it and press 's' to start. This queues it for execution."

**[The task moves to "In Progress" column]**

> "The task moves to In Progress, and the background executor picks it up. Claude Code begins working in its own git worktree, completely isolated."

---

### Scene 5: Watching Claude Work (~60 seconds)

**[Press 'w' to watch the Claude session, or show the split pane]**

> "The magic of TaskYou is watching Claude work. Press 'w' and I can see exactly what Claude is doing in real-time."

**[Show the tmux split pane with Claude's output]**

> "Claude reads files, makes edits, runs tests - all the things you'd do yourself, but automated."

**[Point out the status changes]**

> "Watch the status - when Claude needs my input, the task moves to Blocked and I get a notification."

**[Press 'q' to exit the watch view]**

---

### Scene 6: Handling Blocked Tasks (~30 seconds)

**[Navigate to a Blocked task]**

> "Sometimes Claude needs clarification or a decision. These tasks show up in the Blocked column."

**[Press Enter to see the task, then 'r' to reply]**

> "I can see what Claude is asking, type my response, and the task continues. It's like having a conversation with my AI assistant."

---

### Scene 7: Project Memories (~45 seconds)

**[Press 'm' to open the memories view]**

> "One of TaskYou's best features is Project Memories. Press 'm' to see them."

**[Show the memories list]**

> "Claude learns about your project as it works. It remembers coding patterns, architectural decisions, gotchas to avoid."

**[Navigate through different categories]**

> "Memories are categorized - patterns for coding conventions, context for project background, decisions for architectural choices, and gotchas for things to watch out for."

> "Every future task gets this context automatically. Claude gets smarter about your project over time."

---

### Scene 8: Search and Filter (~20 seconds)

**[Press '/' to open search]**

> "With lots of tasks, search helps me find what I need. Press slash and type to filter."

**[Type a search term]**

> "I can search by title, tags, or project name."

---

### Scene 9: Settings and Configuration (~20 seconds)

**[Press 'S' (shift+s) to open settings]**

> "In settings, I can configure projects, customize task types, and set up the workflow for my needs."

**[Show the projects list briefly]**

> "Each project points to a git repository and can have specific instructions for how Claude should work with it."

---

### Closing (~15 seconds)

**[Return to the Kanban board with tasks in various states]**

> "That's TaskYou - a terminal task queue that lets you delegate work to Claude Code. Create tasks, let Claude work, review the results, and ship."

> "Check out the GitHub repo for installation instructions and more documentation."

**[End on the Kanban board]**

---

## Key Moments to Capture

1. **The "aha" moment**: Watching Claude actively working in real-time
2. **Seamless flow**: Task going from Backlog → Processing → Done
3. **Interaction**: Replying to a blocked task and seeing it continue
4. **Intelligence**: Showing project memories that make Claude smarter

## Keyboard Shortcuts Reference

| Key | Action |
|-----|--------|
| `n` | New task |
| `Enter` | View task details |
| `s` | Start/queue task |
| `w` | Watch Claude session |
| `r` | Reply to blocked task |
| `m` | View memories |
| `/` | Search |
| `S` | Settings |
| `?` | Help |
| `q` | Quit / Close |
| Arrow keys | Navigate |

## Technical Tips for Recording

1. **Use demo database**: Always use `WORKTREE_DB_PATH` to keep your real tasks private
2. **Reset between takes**: Run `make demo-seed` to reset to a fresh state
3. **Pause the executor**: If you want to prevent tasks from actually running during recording, don't start the daemon
4. **Window size**: The TUI looks best at 120+ columns width
5. **Recording software**: OBS Studio or similar with terminal capture works well
6. **Audio**: Record voiceover separately for cleaner editing

## Sample Narration Timing

| Scene | Duration | Cumulative |
|-------|----------|------------|
| Opening | 15s | 0:15 |
| Kanban Board | 30s | 0:45 |
| Task Detail | 45s | 1:30 |
| Creating Task | 45s | 2:15 |
| Starting Task | 30s | 2:45 |
| Watching Claude | 60s | 3:45 |
| Blocked Tasks | 30s | 4:15 |
| Memories | 45s | 5:00 |
| Search | 20s | 5:20 |
| Settings | 20s | 5:40 |
| Closing | 15s | 5:55 |

**Total runtime: ~6 minutes**

---

## Alternative Short Version (2 minutes)

For a quick demo, focus on:
1. Opening + Kanban overview (20s)
2. Creating a task (30s)
3. Watching Claude work (45s)
4. Showing memories (20s)
5. Closing (10s)
