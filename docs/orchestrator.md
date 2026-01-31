# External Orchestration with Task You

Task You's Kanban UI is great for humans, but every action it performs is also available through the `ty` CLI (or `taskyou`). That means you can run your own always-on supervisor (Claude, Codex, Gemini, bash scripts, etc.) completely outside the app by shelling into these commands.

## Using the Claude Code Skill

Task You includes a `/taskyou` skill that teaches Claude Code (or any compatible agent) how to manage your task queue via CLI.

**Automatic availability:** When working inside the Task You project, the skill is automatically loaded from `skills/taskyou/`.

**Global installation:** To use the skill from any project, run:

```bash
./scripts/install-skill.sh
```

This symlinks the skill to `~/.claude/skills/taskyou/` for personal access across all projects.

Once available, you can say things like:

- "Show me my task board"
- "Execute the top priority task"
- "What's blocked right now?"
- "Create a task to fix the login bug"

The skill provides structured guidance for common orchestration patterns, JSON output handling, and best practices.

## CLI Coverage for Automation

| Need | Command(s) |
|------|-----------|
| Snapshot the entire board | `ty board` or `ty board --json` |
| List/filter cards | `ty list [--status <status>] [--json]` |
| Inspect a task (project, logs, attachments) | `ty show <id> --json --logs` |
| Create or edit | `ty create`, `ty update` |
| Queue/execute or retry | `ty execute <id>`, `ty retry <id>` |
| Mark blocked/done/backlog/etc. | `ty status <id> <status>` |
| Pin/unpin priorities | `ty pin <id> [--unpin|--toggle]` |
| Close/delete | `ty close <id>`, `ty delete <id>` |
| Tail executor output | `ty logs` |

Statuses accepted by `ty status` are: `backlog`, `queued`, `processing`, `blocked`, `done`, `archived`.

## Example: Running Claude as an Orchestrator

1. **Start the daemon / UI** (locally or via SSH):
   ```bash
   ty   # launches the TUI + daemon locally
   ```
2. **Open a second tmux pane/window** and launch Claude Code inside your project root:
   ```bash
   cd ~/Projects/workflow
   claude code
   ```
3. **Prime Claude with the following system prompt** (paste at the top of the session):
   ```text
   You are an autonomous operator that controls Task You via its CLI.
   Only interact with tasks by running shell commands that start with "ty".
   Available tools:
     - ty board --json            # get the full Kanban snapshot
     - ty list --json --status X  # list cards in a column
     - ty show --json --logs ID   # inspect a card deeply
     - ty execute|retry|close ID  # run or finish cards
     - ty status ID <status>      # move cards between columns
     - ty pin ID [--unpin]        # prioritize/deprioritize
   Workflow:
     1. Periodically run `ty board --json` to understand the queue.
     2. Decide what should happen next and run the appropriate CLI command.
     3. After taking an action, summarize what you changed before continuing.
     4. Ask the human for input when you cannot proceed.
   ```
4. **Let Claude drive**. It will now issue `ty …` commands the same way the TUI would, so it can start/retry/close/pin tasks, inspect logs, or re-order the backlog – all while living completely outside Task You.

### Tips
- Use `ty board --json | jq` to feed structured snapshots directly into an LLM or script.
- Combine `ty board` with `watch -n30` for a rolling dashboard.
- When scripting, prefer JSON flags (`--json`, `--logs`) so the output is machine-readable.

## Using with Other AI Agents

The skill and CLI work with any agent that can execute shell commands:

### Codex
```bash
codex exec "Run ty board --json and tell me what needs attention"
```

### Gemini
```bash
gemini code "Check my task queue with 'ty board --json' and summarize"
```

### OpenCode / Pi
Same pattern - instruct the agent to shell out to `ty` CLI commands.

## Scripted Automation

For fully automated orchestration without human intervention:

```bash
#!/bin/bash
# Auto-execute queued tasks every 5 minutes

while true; do
    # Get first queued task
    TASK_ID=$(ty list --status queued --json | jq -r '.[0].id // empty')

    if [ -n "$TASK_ID" ]; then
        echo "Executing task $TASK_ID..."
        ty execute "$TASK_ID"
    fi

    sleep 300
done
```

With these primitives, you can plug in any agent or automation stack you like—all without adding bespoke orchestrators inside Task You itself.
