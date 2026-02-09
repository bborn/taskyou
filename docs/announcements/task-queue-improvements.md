# Task Queue Improvements Announcement

## Tweet

Big Task You update:

- Spotlight mode: test worktree changes without leaving your flow
- Task dependencies & auto-chaining
- Email-to-task via ty-email (Gmail OAuth)
- AI-powered command palette
- Customizable keybindings
- Race condition fixes for rock-solid parallel execution

https://github.com/bborn/workflow

## Slack Message

Hey everyone! Wanted to share a roundup of recent Task You improvements. A lot has landed:

**New Features**
- **Spotlight mode** - Sync worktree changes back to the main repo for testing without switching branches. Start with `spotlight start`, test your app, then `spotlight stop` to restore.
- **Task dependencies & auto-chaining** - Tasks can now block/unblock each other. When a dependency finishes, the next task picks up automatically.
- **Email-based task management (ty-email)** - Create and manage tasks via email. Supports Gmail OAuth and IMAP. Run `ty-email init` to set up.
- **AI-powered command palette** - The `p` command now interprets natural language. Type what you want and it figures out the right action.
- **Customizable keyboard shortcuts** - Define your own keybindings in a YAML config file. Every shortcut is rebindable.
- **Event hooks** - New `task.worktree_ready` hook fires when a worktree is set up, so you can run setup scripts automatically.
- **Project context caching** - Agents cache their codebase exploration and reuse it across tasks. No more redundant exploration on every task.

**Reliability**
- Fixed race conditions causing duplicate Claude sessions per task
- Fixed duplicate tmux windows on task retry
- Fixed tasks incorrectly auto-closing as merged
- Agents can no longer mark tasks as done - only humans close tasks
- Executor no longer restarts when you just change task status
- GitHub PR state tracking is now faster and more reliable
- Fixed unreliable Claude pane startup during create-and-execute

**UI & UX**
- Diff stats shown on task detail view
- Paste git branch names or GitHub PR URLs directly into the command palette
- Improved first-time user experience
- Task archiving now updates the UI instantly
- Proper ctrl+c handling in all confirmation dialogs
- Full CLI parity: `--executor`, `--tags`, `--pinned`, `--branch` flags all work
- Auto-allow taskyou MCP tools in Claude Code settings

A lot of these came from daily usage pain points. If you run into anything or have ideas, drop them in this channel or file an issue.
