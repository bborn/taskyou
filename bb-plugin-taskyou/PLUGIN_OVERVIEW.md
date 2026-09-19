Keep your existing TaskYou queue beside your agent conversations in bb.

## Your board, in bb

Browse Backlog, In progress, Blocked, and Done. Filter by project or search
for a task, inspect its details and recent logs, then create, edit, queue,
retry with feedback, or close it. Changes made through TaskYou's CLI, TUI,
or browser appear through the same live event stream.

## One source of truth

Tasks and projects stay in TaskYou's database. TaskYou still owns agent
execution and worktrees. This plugin does not copy tasks into bb's Tasks
plugin or start bb threads for them.

## Requirements

Install TaskYou separately and run its HTTP API and executor daemon. Set
the API URL in the plugin settings; the address must be reachable from the
bb server machine. The default is `http://127.0.0.1:8484`.

Requires bb 0.43 or newer with Plugin SDK 0.4.87+. Git installation requires
npm. No additional account or subscription is required by the plugin.

The first version displays up to 200 tasks per status and reports when that
limit is reached. Terminals, attachments, manual host placement, and bb thread
linkage remain outside this version; use TaskYou's existing interfaces.
