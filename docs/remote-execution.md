# Remote execution

TaskYou runs Claude and Codex tasks over SSH in isolated remote worktrees. The
host needs a project checkout, Git, tmux and the selected executor on its login
shell PATH. Credentials and executor configuration belong on that host. When
multiple coordinators use the same host, give each coordinator its own project
checkout: task worktree paths are still relative to that checkout.

## Choose a host when creating a task

If a placement plugin offers machines for the project, the new-task forms show a
**Host** selector: `automatic` (the default — the resolver is asked at spawn, as
before), `this machine`, or one of the offered hosts. It is in the TUI form's
advanced fields, in the desktop/browser form under **Advanced**, and on the CLI
as `ty create --host <ssh-destination|local>`.

Choosing one records it as the task's placement decision before it spawns, so the
resolver is never asked for that task and every later retry reuses it. The
project's directory on that host comes from the same plugin that offered it;
`--host-dir` overrides it for a host the plugin does not know. `automatic`
records nothing at all.

The host is not contacted while the form is open: an unreachable host fails
visibly at launch, exactly as a resolver's answer does. To change a host after
the task has run, use the move controls below — they carry the work.

With no placement plugin installed, or no host serving the project, no selector
is shown and nothing changes.

## Select a destination

Use `ty place <task-id> <ssh-destination> --dir <remote-checkout>`, the **Change
host** control in desktop/browser task detail, or `@` from the TUI board/detail.
Use `local` to select this machine. Placement is sticky across retries.

These controls share the same service. A move asks the active agent for a
handoff, commits and pushes tracked work, verifies the push, then atomically
records the destination and carried branch. Git-ignored files stay on the source
machine and are reported. The next run starts at the destination; this does not
transfer a live process or the executor's native conversation history.

The CLI's existing `--force` option skips carrying work. The graphical forms use
the normal carry path. A failed carry leaves the existing placement intact.

## Require a remote host

Add this to the project's `.taskyou.yml`:

```yaml
placement:
  remote_required: true
```

A missing resolver, invalid/empty decision, unavailable eligible host or recorded
local placement stops execution before a local agent can start. Correct the
resolver or explicitly select a remote host and retry. The default remains
local fallback when no remote decision is available. This policy also prevents
an archived task from running locally; restore its work before placing it remotely.

The optional `ty-on` resolver accepts `executor:claude` and `executor:codex`
capabilities in the `on` inventory. Declaring any `executor:` entries makes that
list authoritative. Inventories without those declarations remain compatible;
the remote launch still checks that the executable exists. The inventory name
is used for probe results, while its `ssh` field is the actual SSH destination.

## It behaves like a local task

A placed task's session is shown in a pane under the TUI, and that pane is meant
to be indistinguishable from a local task's: the same keys, the same mouse, the
same chrome.

- **Keys.** The view on the host takes no tmux prefix of its own, exactly as the
  local detail view's does, so every key — including `Ctrl-a`, which is
  start-of-line in the agent's input box and in the remote shell — goes through
  to what is running over there. Layout keys (`Shift`+arrows to move between
  panes, `\` for the shell) are the TUI's, here as there.
- **Scrolling and selecting.** The mouse scrolls, selects and resizes. There is
  no separate scrollback to learn.
- **Copy and paste.** Copying in a task pane reaches the system clipboard. A view
  is a nested tmux client, and a nested client's clipboard request is an
  application request that tmux drops by default (`set-clipboard external`), so
  ty sets `set-clipboard on` on the server it opens its panes in. Without it a
  selection went into a paste buffer on the far side — for a placed task, a
  buffer on a machine you are not sitting at.
- **Size.** Remote agent sessions start at the same size as local ones (200x50),
  and the window follows whoever is looking at it, so an agent does not lay its
  screen out for tmux's 80x24 and reflow when you open the task.
- **Shell.** `\` opens a shell in the task's worktree on the host, in a window of
  its own, and closing the view leaves it running.
- **The view never shows the wrong task.** One daemon session on a host holds a
  window per placed task. If this task's window ends, the view ends with it and
  says so, rather than following the session to a neighbouring task's agent.

Actions that act on the task's code follow the task as well:

- **`o` (open in editor)** opens the worktree on its host. Editors that can do
  that over SSH — VS Code and its forks — are asked to; anything else is told
  where the code is and given the `ssh` command that gets there. It never opens
  the same path on the coordinator, which is usually a different checkout of the
  same project (and, for a task that was moved, its stale worktree).
- **`b` (open in browser)** and the `Server:` line in task detail name the host,
  not `localhost`: a placed task's dev server listens on its port over there. The
  address is the hostname `ssh -G` resolves for the destination, so a name that
  only exists in `~/.ssh/config` still produces a URL a browser can open. The
  port is probed on the host (`lsof`, falling back to `ss`).
- **Browser annotations and screenshots** are the exception. Both are staged in
  the task's worktree for the agent to read, and ty cannot yet put files in a
  worktree on another host, so it says so instead of writing them into the
  coordinator's checkout: an annotation bundle is refused, and a screenshot or
  DOM snapshot comes back with that reason in place of the payload rather than
  inlined into the agent's output. The browser actions that carry nothing bulky
  (navigate, click, eval) work as they do locally — the relay does not care which
  machine the agent is on.

## Observe and recover

The task detail shows its host, placement reason, connection state and last
successful observation. `unknown` means no observation yet; `idle` means an
observation older than 30 seconds; `reconnecting` means an error. None means
the task failed.
Failed window enumeration cannot make every task appear missing, and failed
pane capture cannot make an existing window disappear.

The browser and desktop task detail read a placed Claude task's conversation
from its host over the same outbound SSH connection used to supervise the task.
Replies from the composer are sent to that host's executor pane as well. The
browser never connects to the agent host directly. If the coordinator cannot
reach the host, the conversation reports that connection failure and retains
the last successfully loaded messages.

Completion signals are scoped to the coordinator database and launch attempt.
They remain on the remote host until persisted in SQLite. Retransmission is
deduplicated, signals from superseded attempts are discarded, and persisted
signals remain available after a daemon restart. Existing sessions launched
before this protocol continue using the previous observation fallback until
they are restarted.

## Skills, plugins and MCP on the host

Before a Claude task spawns on a host, ty brings that host's skills and plugins
in step with this machine's:

- User skills in `~/.claude/skills` are copied with `rsync -L`, so symlinked
  skills arrive as real files. `node_modules`, `.git`, virtualenvs and macOS
  binaries are not copied. When a skill with a `./setup` script changes, the
  script is started on the host in the background (log in `~/.ty-sync/`).
  Account-synced skills (`skills/synced/`) already follow the Claude login and
  are left alone.
- Enabled plugins are installed on the host from their marketplaces with
  `claude plugin install`. Caches and credentials are never copied. A plugin
  that fails to install is tried again a day later. Plugins whose marketplace
  exists only on this machine (a local directory) are skipped.
- `CLAUDE.md`, `~/.claude-shared`, settings, hooks and credentials are never
  touched. Each host keeps its own.

ty keeps a record of what it put on each host in `~/.ty-sync/state`. When
nothing changed, the check is one short command. A failed sync is logged and
the task launches anyway. A first sync that takes longer than two minutes
finishes in the background. To sync without spawning a task, run
`ty hosts sync <host>` (`--force` redoes everything).

A placed Claude task also gets its taskyou tools. The tools run on this
machine. A small MCP server in the task's worktree (`.ty/mcp-proxy`) sends each
call over the SSH connection ty already holds to the host, and nothing listens
on this machine. Calls act only on the task's current run on that host. When
this machine is asleep or offline, the tool list still loads and a tool call
fails immediately with an "unavailable" error. The agent then reports with
`.ty/signal`, which is queued until this machine is back.

MCP servers that work only on this machine can be relayed the same way. Name
them in `remote_mcp_proxy`, comma-separated:

```
ty settings set remote_mcp_proxy claude-in-chrome
```

`claude-in-chrome` is built in. Any other name must be a stdio server in
`~/.claude.json`. Each placed task that uses one gets its own server process
on this machine. The process is stopped after ten idle minutes.

## HTTP API

- `GET /api/placement/hosts?project=<name>&executor=<name>` lists the machines a
  new task in that project could be placed on (`name`, `target`, `workdir`,
  `detail`). An empty list means nothing is offering a choice.
- `POST /api/tasks` accepts `placement` (`""`/`auto` leaves it to the resolver,
  `local` pins the task to this machine, anything else is an SSH destination) and
  an optional `placement_workdir`. An unusable choice is reported and the task is
  still created.
- `GET /api/tasks/{id}/placement` returns the destination, directory, reason,
  decision state, remote worktree, health (`state`, `last_seen`, `problem`) and
  `code_uri` — the URL that opens the remote worktree in the viewer's editor over
  SSH, empty for a task on this machine.
- `POST /api/tasks/{id}/placement` accepts `target`, `workdir` and optional
  `force` (default false). It returns progress messages after the move completes.

Placement operations can take up to three minutes while waiting for the handoff
and Git transfer. Concurrent placement changes for the same task are serialized.
Remote attachments and initial repository cloning are not included.
