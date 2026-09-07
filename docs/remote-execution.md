# Remote execution

TaskYou runs Claude and Codex tasks over SSH in isolated remote worktrees. The
host needs a project checkout, Git, tmux and the selected executor on its login
shell PATH. Credentials and executor configuration belong on that host. When
multiple coordinators use the same host, give each coordinator its own project
checkout: task worktree paths are still relative to that checkout.

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

## Observe and recover

The task detail shows its host, placement reason, connection state and last
successful observation. `unknown` means no observation yet; `reconnecting` means
an error or an observation older than 30 seconds. Neither means the task failed.
Failed window enumeration cannot make every task appear missing, and failed
pane capture cannot make an existing window disappear.

Completion signals are scoped to the coordinator database and launch attempt.
They remain on the remote host until persisted in SQLite. Retransmission is
deduplicated, signals from superseded attempts are discarded, and persisted
signals remain available after a daemon restart. Existing sessions launched
before this protocol continue using the previous observation fallback until
they are restarted.

## HTTP API

- `GET /api/tasks/{id}/placement` returns the destination, directory, reason,
  decision state, remote worktree and health (`state`, `last_seen`, `problem`).
- `POST /api/tasks/{id}/placement` accepts `target`, `workdir` and optional
  `force` (default false). It returns progress messages after the move completes.

Placement operations can take up to three minutes while waiting for the handoff
and Git transfer. Concurrent placement changes for the same task are serialized.
Remote attachments and initial repository cloning are not included.
