# External Orchestration with Task You

Task You's Kanban UI is great for humans, but every action it performs is also available through the `task` CLI. That means you can run your own always-on supervisor (Claude, Codex, bash scripts, etc.) completely outside the app by shelling into these commands.

## CLI Coverage for Automation

| Need | Command(s) |
|------|-----------|
| Snapshot the entire board | `task board` or `task board --json` |
| List/filter cards | `task list [--status <status>] [--json]` |
| Inspect a task (project, logs, attachments) | `task show <id> --json --logs` |
| Create or edit | `task create`, `task update` |
| Queue/execute or retry | `task execute <id>`, `task retry <id>` |
| Mark blocked/done/backlog/etc. | `task status <id> <status>` |
| Pin/unpin priorities | `task pin <id> [--unpin|--toggle]` |
| Close/delete | `task close <id>`, `task delete <id>` |
| Tail executor output | `task logs` |

Statuses accepted by `task status` are: `backlog`, `queued`, `processing`, `blocked`, `done`, `archived`.

## Example: Running Claude as an Orchestrator

1. **Start the daemon / UI** (locally or via SSH):
   ```bash
   task -l   # launches the TUI + daemon locally
   ```
2. **Open a second tmux pane/window** and launch Claude Code inside your project root:
   ```bash
   cd ~/Projects/workflow
   claude code
   ```
3. **Prime Claude with the following system prompt** (paste at the top of the session):
   ```text
   You are an autonomous operator that controls Task You via its CLI.
   Only interact with tasks by running shell commands that start with "task".
   Available tools:
     • task board --json            # get the full Kanban snapshot
     • task list --json --status X  # list cards in a column
     • task show --json --logs ID   # inspect a card deeply
     • task execute|retry|close ID  # run or finish cards
     • task status ID <status>      # move cards between columns
     • task pin ID [--unpin]        # prioritize/deprioritize
   Workflow:
     1. Periodically run `task board --json` to understand the queue.
     2. Decide what should happen next and run the appropriate CLI command.
     3. After taking an action, summarize what you changed before continuing.
     4. Ask the human for input when you cannot proceed.
   ```
4. **Let Claude drive**. It will now issue `task …` commands the same way the TUI would, so it can start/retry/close/pin tasks, inspect logs, or re-order the backlog – all while living completely outside Task You.

### Tips
- Use `task board --json | jq` to feed structured snapshots directly into an LLM or script.
- Combine `task board` with `watch -n30` for a rolling dashboard.
- When scripting, prefer JSON flags (`--json`, `--logs`) so the output is machine-readable.

With these primitives, you can plug in any agent or automation stack you like—all without adding bespoke orchestrators inside Task You itself.
