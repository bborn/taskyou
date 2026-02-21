# Multiple Tmux Panes Feature

## Overview

The task detail view now supports creating multiple tmux panes for a single task. This allows you to have multiple shell sessions or Claude instances running simultaneously within a task's context.

## Usage

### Keybindings

When viewing a task in detail view:

- **Ctrl+N**: Create a new shell pane
- **Ctrl+Shift+N** (or **Ctrl+N**): Create a new Claude pane
- **\\**: Toggle the primary shell pane (existing functionality)

### Creating New Panes

1. Open a task in detail view (press Enter on a task)
2. Press **Ctrl+N** to create a new shell pane
   - The new pane will be created next to the Claude pane
   - It will have the task's environment variables set (WORKTREE_TASK_ID, WORKTREE_PORT, WORKTREE_PATH)
3. Press **Ctrl+Shift+N** to create a new Claude pane
   - A new Claude session will start in the new pane
   - This allows you to have multiple AI assistants working on different aspects of the task

### Cleanup

All panes (including extra panes) are automatically cleaned up when:
- You navigate away from the task detail view
- You switch to a different task
- You close the application

The panes are "broken" (moved to their own tmux windows) so they continue running in the background and can be manually attached to later if needed.

## Technical Details

### Database Schema

The feature uses a new `task_panes` table to track all panes associated with a task:

```sql
CREATE TABLE task_panes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    pane_id TEXT NOT NULL,
    pane_type TEXT NOT NULL,
    title TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
)
```

### Pane Types

- `claude`: Primary Claude/executor pane
- `shell`: Primary shell pane
- `claude-extra`: Additional Claude panes
- `shell-extra`: Additional shell panes

### Backward Compatibility

The existing `claude_pane_id` and `shell_pane_id` columns on the `tasks` table are maintained for backward compatibility and continue to track the primary panes.

## Use Cases

### Multiple Shell Sessions

You might want multiple shell panes to:
- Run a development server in one pane
- Have a shell for git operations in another
- Monitor logs in a third pane

### Multiple Claude Sessions

Multiple Claude panes can be useful for:
- Having one Claude focused on implementation
- Another Claude reviewing code or documentation
- A third Claude investigating a specific problem or researching best practices

## Implementation Notes

- All extra panes are stored in the `task_panes` table
- Panes are automatically tracked and cleaned up when leaving the detail view
- The feature integrates seamlessly with the existing tmux-based task isolation system
