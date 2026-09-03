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
| `task.route` | **Consulted** before a task spawns: which profile runs it. See [Routing](#routing-pre-spawn) |
| `task.placement` | **Consulted** before a task spawns: which machine runs it. See below |

A plugin may declare any event string; it only runs for events TaskYou actually
emits, so unknown events are harmless.

## Routing (pre-spawn)

Every other hook is a notification — it fires after the fact and nothing waits for
it. `task.route` fires *before* a task spawns, TaskYou waits for it, and reads the
script's **stdout back as a decision**. Stdout is the decision channel; put
diagnostics on stderr.

```sh
#!/bin/sh
echo "CLAUDE_CONFIG_DIR=$HOME/.claude-work"   # run this task under that profile
echo "REASON=7% of its limits used"           # optional, for the task log
```

| Key | Effect |
|-----|--------|
| `CLAUDE_CONFIG_DIR` | Run the task under that Claude profile (config dir) |
| `HOLD=1` | Don't start yet; leave it queued and reconsider next tick |
| `REASON=…` | Free text for the task log |

Unrecognized lines are ignored. Timeout is 15s. Plugins are consulted in name
order and the first non-empty decision wins.

Failing is safe: no router, a script that errors or prints nothing, or a timeout
all spawn the task exactly as it would have. A config dir already set by hand, by
a workflow step, or on the **project** is never overruled — pinning a project's
config dir is how you opt it out of routing, which matters because a config dir
carries that account's MCP connectors and their per-profile OAuth logins, and a routed task keeps its profile on
resume (its Claude session lives in that config dir). `HOLD` leaves a task
**queued**, never blocked, and is ignored for a manually started task. Only
Claude tasks are routed — `CLAUDE_CONFIG_DIR` means nothing to the other
executors.

Routing hooks also get `TASK_EXECUTOR` and `TASK_CLAUDE_CONFIG_DIR`.

Worked example: **claude-profile-router** in the
[community collection](https://github.com/taskyou/plugins).

## `task.placement` — which machine a task runs on

Most events are fire-and-forget: the script runs in the background and nothing
waits for it or reads what it says. `task.placement` is one of the two
exceptions, alongside `task.route` below — ty asks it a question and uses the
answer.
Where a task runs has to be decided **before** the executor spawns, and the
answer has to come back — so this hook is synchronous, bounded by a short
timeout, and its stdout is parsed.

ty writes the request to the handler's **stdin**:

```json
{"event":"task.placement",
 "task":{"id":5228,"title":"Add a consulted task.placement hook",
         "project":"taskyou","repo_path":"/Users/you/Projects/workflow",
         "executor":"claude"}}
```

and reads the answer from its **stdout**:

```json
{"target":"ol-agents","workdir":"~/projects/engineering",
 "reason":"most free memory of 2 hosts serving offerlab"}
```

- **`target`** — the ssh destination to run on. **Empty means run locally.**
- **`workdir`** — the task's directory *on that host*. A remote path, so a
  leading `~` is passed through for the remote shell to expand.
- **`reason`** — why. Always shown to the user (`ty show`, the task log), so
  write it to explain a surprising placement without further digging.

ty never learns what a host *is*. It asks the question, and runs the answer:
an empty target uses the local runner — byte for byte what ty has always done —
and a named target starts the task's tmux session on that host over ssh, in that
directory. Where it ran is recorded on the task and shown by `ty show` and on the
board, so a result can be traced back to the machine that produced it.

### Failure behaviour

Failing to *decide* where to run falls back to local. Failing to *run* where you
were told does not.

| Situation | What ty does |
|-----------|--------------|
| No `task.placement` plugin installed | Local. Silent. Nothing is asked, logged or recorded. |
| Handler answers with an empty target | Local, and the reason is recorded so you can see why. |
| Handler is slow (over 5s), crashes, exits non-zero, or writes malformed JSON | Local, logged loudly. A hook in the spawn path must never hang a task. |
| Handler names a host ty cannot reach | **The task fails, visibly.** It is *not* quietly run locally — that would put the load straight back on the machine placement exists to unload, on the days you are least likely to notice. |

If several plugins declare `task.placement` they are consulted in name order and
the first to name a host wins; a handler that answers "local" lets the next one
try.

### The reference handler

[`extensions/ty-on`](../extensions/ty-on/README.md) is a working resolver: it
reads the [`on`](https://github.com/bborn/on) CLI's host inventory and answers
with the host that serves the task's project (picking the one with the most free
memory when several do). Build and install it with:

```bash
make install-ty-on     # builds the binary and installs it as a plugin
make uninstall-ty-on   # every task goes back to running locally
```

### How ty watches a placed task

ty holds **one standing connection per host**, not one per task. A small POSIX
shell agent runs on the host, walks every ty window each tick, and streams back a
single snapshot covering all of them, so the cost of watching a fleet is
`O(hosts)` rather than `O(tasks)`. Before this, each task cost two ssh round
trips per tick — one for its window, one to capture its pane — which is a
thousand ssh spawns every fifteen seconds at a few hundred agents.

The agent is a shell script rather than a ty binary on purpose: it needs no
install, no cross-compilation for the host's architecture, and no version
agreement between the two ends. It requires only `tmux`.

The direction matters as much as the cost. **ty holds the connection outbound**,
exactly as it does for every other remote command. Nothing listens on your
machine, no port is opened, no reverse tunnel exists, and no host is given a way
to reach back on its own initiative — the agent speaks only by writing to the
stdout of a process ty started.

If the channel cannot speak for a host — none started yet, or its last snapshot
is stale — it reports "I don't know" and ty falls back to probing that task
directly. It can make watching cheaper, never wrong.

### How a remote agent reports it finished

The `taskyou_*` MCP tools are stdio, so a remotely placed agent's MCP server
would talk to its own host's database rather than to ty. Instead, ty installs
`.ty/signal` in the task's worktree and tells the agent about it in the prompt:

```bash
.ty/signal done        "<one line saying what you did>"
.ty/signal needs-input "<the question you need answered>"
.ty/signal failed      "<what stopped you>"
```

The script drops a file in a spool directory; the host agent drains it onto the
connection ty already holds open. The sentence the agent writes reaches the board
as the task's message — usually the only explanation of how a task on another
machine ended.

Without this ty had to infer completion from the screen: an agent that stopped
repainting for two minutes was called finished. That is wrong in both directions
— an agent pausing to think looks done, and an agent that finished in six seconds
is parked two minutes later labelled "needs input" when nothing was asked. The
idle heuristic is still there underneath, for a host where the script could not
be installed, and ty says so when it falls back.

### Current boundaries

- Only the `claude` executor can be launched remotely so far. A task using
  another executor fails visibly rather than quietly running here.
- Attachments are staged in the local workspace, so they are not available to a
  remotely-placed task. A file you drop into a remote agent's pane is a path on
  *your* machine, and the agent cannot open it.
- A host must already have a checkout of the project, mapped in the resolver's
  inventory. ty creates the task's worktree there, but does not clone a project
  the host has never seen.

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
| [`heartbeat`](../examples/plugins/heartbeat/) | service | a daemon-supervised long-running process |

```bash
cp -R examples/plugins/desktop-notify ~/.config/task/plugins/
ty plugins list
```

For ready-made, reviewable plugins, see the community collection at
[github.com/taskyou/plugins](https://github.com/taskyou/plugins) (see
[Installing](#installing)). For more ideas, see the
[plugin idea gallery](plugin-ideas.md).
