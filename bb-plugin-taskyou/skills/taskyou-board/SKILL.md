---
name: taskyou-board
description: Configure or troubleshoot the TaskYou board shown in bb. Use when working with the TaskYou bb plugin or its API connection.
---

# TaskYou board in bb

This plugin connects bb's TaskYou sidebar page to an existing TaskYou HTTP
API. Tasks, projects, worktrees, and execution belong to TaskYou. TaskYou
task IDs are integers and are unrelated to bb thread or Tasks plugin IDs.

## Connection

Inspect `bb plugin config taskyou`. Set the URL with:

```sh
bb plugin config taskyou set apiUrl http://127.0.0.1:8484
```

The address is resolved on the **bb server machine**, not the browser or
the current agent's machine. Setting changes reconnect automatically.
Use `bb plugin logs taskyou` to diagnose a failed connection.

TaskYou must already run its API (`ty serve --host 127.0.0.1 --port 8484`)
and its executor (`ty daemon`). The plugin does not start either process.
For another machine, use an address reachable from the bb server over a
trusted private connection. The TaskYou API grants control of task execution;
do not expose it to the public internet without an authenticated gateway.

## Operations

The board supports create, edit, queue, retry with feedback, and close.
Changes from the CLI, TUI, or browser appear through TaskYou's event stream.
Use the ordinary `ty` CLI for agent-driven task operations; run it on the
TaskYou machine against the same database as the configured API. Check
`ty --help` for current commands. Never write TaskYou or bb database files
directly, and do not create a second copy of a task in bb's Tasks plugin.

Terminals, session attachment, manual host placement, and bb thread linkage
are outside this first version. Use TaskYou's existing surfaces for them.
