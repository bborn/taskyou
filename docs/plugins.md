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
```

Script paths are resolved relative to the plugin directory. Scripts must be
executable (`chmod +x`). A plugin needs at least one usable hook **or** action.

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

## Workflows and routines (by convention)

Two capabilities need no manifest entry — a plugin ships them by *convention*, just
by having the right subdirectory:

- **`workflows/*.yaml`** — each file is a workflow definition, resolvable by
  `ty pipeline -d <name>` once the plugin is installed.
- **`routines/<name>/prompt.md`** — each subdirectory is a routine: a
  named, unattended agent run, resolvable by `ty run <name>` and listed by
  `ty routines` (frontmatter for `model`/`project`/`timeout`/… works exactly as it
  does for a user routine under `~/.config/task/routines/`).

```
my-plugin/
├── plugin.yaml
├── workflows/
│   └── review.yaml            # ty pipeline -d review "<goal>"
└── routines/
    └── linear-poll/
        └── prompt.md          # ty run linear-poll   (+ ty routine schedule --every 2m)
```

A user routine of the same name **shadows** a plugin's (your
`~/.config/task/routines/` wins), so a plugin routine is a sensible default you can
override. Scheduling stays a separate step (`ty routine schedule <name> --every … |
--cron …`) — the plugin ships the *what*, you choose the cadence.

**Routine vs. service:** ship a **routine** for periodic, run-to-completion work (a
poller, a digest, a nightly sweep) — the daemon records each run, enforces a timeout,
and keeps cross-run state in `ROUTINE_STATE_DIR`. Ship a **service** (where available)
only for a process that must stay *up* (a socket connection, a listening port).

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

```bash
cp -R examples/plugins/desktop-notify ~/.config/task/plugins/
ty plugins list
```

For more, see the [plugin idea gallery](plugin-ideas.md).
