# TaskYou plugins

A plugin is a **self-contained directory** that reacts to task events. Drop it
into `~/.config/task/plugins/` and it's live — no rebuild, no config edits, and
no collisions with other plugins. This is the easy on-ramp for community
integrations (notifications, proxies, trackers, chat bridges, …).

It builds on TaskYou's existing [event hooks](../README.md#event-hooks). The
difference: the legacy hooks dir allows **one script per event**, so two
integrations that both want `task.done` fight over the same file. A plugin
namespaces its scripts in its own directory and declares what it handles in a
manifest, so **any number of plugins can handle the same event** and all of them
run.

## Installing

The community collection lives at
**[github.com/taskyou/plugins](https://github.com/taskyou/plugins)** — browse it,
review a plugin, then install the whole collection (or a single plugin) with:

```bash
ty plugins add https://github.com/taskyou/plugins   # clone & install the collection
ty plugins list                                     # see what it provides
```

`ty plugins add` clones a repo into your plugins dir; a repo can hold one plugin at
its root or many nested in subdirectories, and every plugin inside becomes active.
Re-running `add` on the same source updates it in place (`git pull`). You can also
just drop a directory into `~/.config/task/plugins/` by hand — see [Examples](#examples).

## Removing

```bash
ty plugins remove <name>   # aliases: rm, uninstall
```

`remove` deletes the named plugin's directory (the name is the one from
`ty plugins list`, even if it differs from the directory name). If the plugin was
installed as part of a multi-plugin collection checkout, only its own subdirectory
is removed — the shared checkout and its sibling plugins stay, and re-running
`ty plugins add` on the collection source may restore it.

## Anatomy

```
~/.config/task/plugins/
└── my-plugin/
    ├── plugin.yaml      # manifest (required)
    └── on-done.sh       # your script(s)
```

### `plugin.yaml`

```yaml
name: my-plugin              # required, unique
version: 0.1.0               # optional
description: What it does.    # optional
hooks:                       # event -> script path (relative to this dir)
  task.done: on-done.sh
  task.blocked: on-blocked.sh
actions:                     # user-invoked commands (optional)
  - id: sync
    label: Sync to tracker
    command: sync.sh
services:                    # long-running processes (optional)
  - name: index
    command: ./search-server  # sh -c, run from the plugin dir
    cwd: ""                  # optional, relative to the plugin dir
    env: ["FOO=bar"]         # optional extra env
```

Script paths are resolved relative to the plugin directory. Scripts must be
executable (`chmod +x`). A plugin needs at least one usable hook, action,
workflow (`workflows/*.yaml`), **or** service.

### Services (long-running processes)

A **service** is a process the daemon supervises for its whole lifetime: it starts
when the daemon comes up and is stopped (SIGTERM, then SIGKILL) when the daemon exits.
Each service runs as `sh -c <command>` from the plugin dir (or `cwd`), in its own
process group. See [`examples/plugins/heartbeat/`](../examples/plugins/heartbeat/) for
a runnable example.

#### Why a service instead of a hook?

A **hook** is fired *by* the daemon on one discrete event (`task.done`), runs, and
exits — it's stateless and short-lived, and it can only ever react to a single event
in isolation. A **service** is the opposite: it owns its own loop, and it's for the
work a hook structurally can't do:

- **Hold a persistent connection** — a Slack socket-mode client, an IMAP IDLE
  connection, an MCP stdio server other tools attach to.
- **Run on its own schedule** — poll an inbox every 30s, reindex every 5min — rather
  than waiting for a ty event to fire it.
- **Serve a port** — a metrics endpoint, a small web dashboard, a search server.
- **Batch or debounce across many events** — a hook fires once per event and keeps no
  state between fires; a service can accumulate and flush.
- **Watch an external source and create tasks** — turn emails, GitHub webhooks, or a
  file watcher into ty tasks.

Rule of thumb: *react to one ty event → hook. Hold state, a connection, a schedule, or
a port → service.*

#### How a service reaches ty

A service is just a process — it has no special in-process access to ty. The daemon
hands it, via the environment, a stable way to find the running instance:

| env var | value | use it for |
| --- | --- | --- |
| `TY_API_URL` | `http://127.0.0.1:8080` (the daemon's HTTP API; absent if the API is disabled) | Read/write tasks over HTTP — the decoupled, recommended path. |
| `TY_DB_PATH` | the SQLite file (respects `WORKTREE_DB_PATH`) | Open the DB read-only for queries the API doesn't expose yet. Couples you to the schema — prefer the API. |

Beyond those, a service can subscribe to ty's **event feed** (the SSE stream on the
HTTP API) to react to task changes in real time, or simply shell out to the **`ty`
CLI**, which is often the least-effort option for a small shell service.

#### Example use cases

- **Email/chat bridge** — hold an IMAP IDLE or Slack socket connection; turn incoming
  messages into tasks and post task updates back out. (This is the shape of the
  existing `ty-email` extension — the kind of thing that used to run and be babysat on
  its own.)
- **Semantic search / index server** — keep an embeddings index warm and answer
  queries over a port or via MCP. (The shape of `ty-qmd`.)
- **Webhook listener** — bind a port, accept GitHub/Linear webhooks, and create tasks
  from them.
- **Metrics / dashboard** — serve board stats read from `TY_API_URL` on a small HTTP
  endpoint for a status display.
- **Reactive reindexer / cache warmer** — subscribe to the event feed and rebuild a
  derived view whenever tasks change.

> Note: declaring a service does **not** migrate any of the existing in-repo
> extensions — they stay where they are. This is simply the capability so a *new*
> plugin can ship a long-running helper if it wants one.

## Events

Plugins handle the same events as the [event hooks](../README.md#event-hooks)
system. The ones dispatched today:

| Event | When |
|-------|------|
| `task.started` | Execution begins |
| `task.done` | Agent finished successfully |
| `task.blocked` | Task needs input |
| `task.failed` | Agent execution failed |
| `task.auth_required` | Executor session needs re-authentication |

A plugin may declare any event string; it only runs for events TaskYou actually
emits, so unknown events are harmless.

## Environment

Every hook receives the standard task variables:

```
TASK_ID TASK_TITLE TASK_STATUS TASK_PROJECT TASK_TYPE
TASK_MESSAGE TASK_EVENT WORKTREE_PATH
```

Plugin hooks additionally receive:

```
TASK_PLUGIN_NAME    # this plugin's name
TASK_PLUGIN_DIR     # absolute path to this plugin's directory
```

The script's working directory is set to the plugin directory, so it can read
its own bundled files (config, templates, helper binaries) with relative paths.

## Actions (user-invoked)

Hooks fire automatically on events. **Actions** are commands the user triggers on
demand. Each action has an `id`, an optional `label`, and a `command` (a script
path relative to the plugin dir).

Run one from the CLI, optionally against a task:

```bash
ty plugins run my-plugin sync          # no task context
ty plugins run my-plugin sync 42       # with task #42's env
```

An action script receives `TASK_PLUGIN_NAME` / `TASK_PLUGIN_DIR` always, and the
`TASK_*` variables (`TASK_ID`, `TASK_TITLE`, `TASK_STATUS`, `TASK_PROJECT`,
`TASK_TYPE`, `WORKTREE_PATH`) when a task is in context. Unlike hooks, actions
run synchronously (up to 60s) and their output is shown to the caller (the CLI
prints it; the TUI shows the first line in the notification banner).

Actions are reachable from every surface, all running the same command:

- **CLI:** `ty plugins run <plugin> <action> [task-id]`.
- **TUI — detail view:** press `A` on a task to open a picker of that task's
  plugin actions; the chosen one runs with the task's env.
- **TUI — command palette:** open it (`p` / `Ctrl+P`) and type a leading `>` to
  switch from task search to action search.
- **GUI / API:** `GET /api/plugins/actions` lists them; `POST
  /api/plugins/actions/run` (`{plugin, action, task_id?}`) runs one. The desktop
  app and any agent use these.

## Behavior & guarantees

- **Fan-out**: for each event, the legacy single-script hook *and* every plugin
  that declares the event run, concurrently and in the background. A slow or
  failing plugin never blocks task execution or the other plugins.
- **Isolation**: a malformed manifest, a missing script, or a plugin with no
  name is skipped (surfaced via `ty plugins list` and the daemon log) — one bad
  community plugin can't break your pipeline.
- **Timeout**: each hook is given 30s before it's killed.
- **Deterministic order**: plugins run sorted by name.

## Inspecting

```bash
ty plugins list   # what's installed and which events each handles
ty plugins dir    # the plugins directory path
```

Set `TY_PLUGINS_DIR` to use a directory other than `~/.config/task/plugins/`.

## Examples

Complete, copy-pasteable plugins live in [`examples/plugins/`](../examples/plugins/):

| Plugin | Kind | What it shows |
|---|---|---|
| [`desktop-notify`](../examples/plugins/desktop-notify/) | hooks + action | native notifications; a `test` action |
| [`slack`](../examples/plugins/slack/) | hooks | webhook integration; bundled `config.env` |
| [`worktree`](../examples/plugins/worktree/) | actions | task-scoped `diff` / `test` using `WORKTREE_PATH` |
| [`heartbeat`](../examples/plugins/heartbeat/) | service | a daemon-supervised long-running process |

```bash
cp -R examples/plugins/desktop-notify ~/.config/task/plugins/
ty plugins list
```

For ready-made, reviewable plugins, see the community collection at
[github.com/taskyou/plugins](https://github.com/taskyou/plugins) (see
[Installing](#installing)). For more ideas, see the
[plugin idea gallery](plugin-ideas.md).
