# Diagnostics: the build handshake and `ty doctor`

A ty install is several processes of the same program — the daemon that runs
agents, the TUI, and every short-lived CLI invocation. They are supposed to be
the same build, reading the same database, talking to the same tmux server,
resolving the same Claude config dir.

When they are not, the symptom is never "version mismatch". It is an agent that
resumes into an empty session, a task stuck on `processing` with a finished
agent in it, or a fix you built, installed and apparently watched get ignored —
because the daemon from twenty minutes ago is still the one executing tasks.

Two things address that: a handshake that makes the disagreement say so, and a
doctor that checks the rest of the install without touching it.

## The build handshake

### What the daemon writes down

At startup the daemon writes a record beside its pid file — `daemon.pid.info`
in the same directory as the database, alongside the existing `.mode` and
`.lock` siblings:

```json
{
  "version": "0.9.3",
  "protocol": 1,
  "pid": 41231,
  "started_at": "2026-09-16T09:02:11Z",
  "executable": "/home/you/go/bin/ty",
  "claude_config_dir": "/home/you/.claude",
  "tmux_socket": "taskyou",
  "db_path": "/home/you/.local/share/task/tasks.db"
}
```

The last three fields are the environment two ty processes must agree on to see
the same world. They are resolved by the same function on both sides, so a
difference in the record is a real difference in the processes.

The record is removed when the daemon stops, and a record whose `pid` is not the
live daemon's is ignored — a stale one would report a mismatch against a daemon
that already restarted.

### The protocol number, and how it is derived

`version` is the release. `protocol` is the version of the daemon↔client
*contract*, and it moves only when the two must agree on something that changed:

- **`db.SchemaVersion`** — the daemon writes rows an older client cannot read,
  or reads columns a newer client has stopped writing.
- **`executor.ClaudeHookEvents`** — the set of Claude hook events ty installs
  into a task worktree, or the payload they send back to `ty claude-hook`. A
  daemon that installs hooks a client does not understand silently loses status
  transitions.
- **the tmux conventions in `internal/tmuxctl`** — the agent server socket, the
  pane tags, the daemon session and window naming. Two builds that disagree here
  cannot find each other's agents.

It is a hand-written integer, bumped by one and never reused. It is *not*
derived automatically: a hash of those inputs would move on harmless refactors
and would make a useless error message.

Instead, `TestContractFingerprint` in `internal/handshake` fingerprints exactly
those inputs. Change one without bumping `Protocol` and the test fails, printing
the new fingerprint to paste back in:

```
the daemon↔client contract changed.

The contract inputs now fingerprint as "4c1e…", but internal/handshake pins "ddb3…".

If the change means a daemon and a client of different builds would now
disagree with each other, bump handshake.Protocol by one. Either way, set:

    const ContractFingerprint = "4c1e…"
```

Two deliberate edits, which is the point: the bump is an act, not something you
have to remember.

### What clients do with it

At startup the TUI and every CLI command compare their own record against the
daemon's:

| Situation | Severity | What you see |
| --- | --- | --- |
| Same protocol, same build, same environment | ok | nothing |
| Different protocol | **error** | `daemon is build X, this is build Y — run \`ty restart\`` |
| Same protocol, different build | warning | `… run \`ty restart\` when convenient` |
| Different claude config dir, database or tmux server | warning | both values, named |
| Daemon left no record | info | nothing on the CLI; reported by `ty doctor` |

The CLI prints to stderr. The TUI does too, but stderr is wiped by the alt
screen on the first frame, so it also raises the board's notification banner —
this is exactly the class of problem someone stares past for an hour.

Nothing here changes an exit code or refuses a command. A mismatched daemon is
something you fix with `ty restart`, not a reason to reject what you typed. The
hidden internal commands (`claude-hook`, `worktree-guard`, `mcp-server`) stay
silent, as do `completion`, `daemon` and `doctor`.

### Remote and placed hosts

**The handshake is local.** A client compares itself only against the daemon on
its own machine — the one behind its own pid file. A host you have placed tasks
on runs its own ty, its own daemon and its own handshake, and nothing about that
is gated by this machine's build.

A daemon that left no record is reported as **info**, never as an error, and
never blocks anything. That is what every ty from before the handshake looks
like, and what a remote or placed host running a slightly older ty looks like.
Both keep working exactly as they did.

The `Record` JSON is append-only for the same reason: fields are added, never
renamed or removed, so an older ty reading a newer record picks up the fields it
knows and ignores the rest.

## `ty doctor`

`ty doctor` checks the whole install and **changes none of it**. It opens the
database read-only (so it cannot migrate it), never starts or stops a daemon or
a tmux server, and never writes a config. When something is wrong it says so and
leaves the fix to you.

```
TaskYou Doctor
build 0.9.3 · protocol 1 · tasks.db

✓ github-auth        Authenticated as GitHub App / bot identity "agents[bot]"; GraphQL bucket: 5000/5000
✓ daemon             daemon is running (pid 41231)
✓ daemon-handshake   daemon and this binary are both build 0.9.3 (protocol 1)
✓ daemon-env         daemon and this binary resolve the same config dir, database and tmux server
✓ tmux               tmux 3.4
✓ tmux-agent-server  agent tmux server "taskyou" is reachable (3 session(s))
✓ database           task database opens read-only
✓ db-schema          database schema is current (version 1)
✓ status-consistency every task's status matches its log (1284 event(s))
✓ claude-hooks       task #5469's generated hooks cover every expected event
✓ claude-mcp-config  task #5469's MCP config parses and wires the taskyou server
✓ executors          every configured executor is on PATH (claude, codex)

All checks passed.
```

| Check | What it answers |
| --- | --- |
| `github-auth` | gh installed, logged in, token valid, and not sharing a personal account's 5,000 pt/hr GraphQL bucket across servers |
| `daemon` | is a daemon running, and which process |
| `daemon-handshake` | its build and protocol against this binary |
| `daemon-env` | whether it resolved the same claude config dir, database and tmux server |
| `tmux` | tmux present, and its version |
| `tmux-agent-server` | the agent server (`tmux -L taskyou`) is reachable, and holds only one daemon session |
| `claude-hooks` | a live task's generated settings carry every expected hook event |
| `claude-mcp-config` | that task's MCP config exists, parses, and wires the taskyou server |
| `database` | the task database opens |
| `db-schema` | its schema version matches this build |
| `status-consistency` | every task's cached status matches the fold over its status log |
| `executors` | the CLI binaries for configured executors are on PATH |

"Configured executors" means the ones this install will really try to run: the
default executor plus every executor named by an unfinished task. Reporting on
all eight built-ins would warn about tools you have deliberately never
installed.

### Statuses and exit codes

Each check is `ok`, `info`, `warning` or `error`. `info` is a fact you might
want and nothing to do — no daemon record because the daemon is older, no active
task to inspect, no agent tmux server because nothing has started one.

The top-level status is the worst check that needs attention, so it is only ever
`ok`, `warning` or `error`: a healthy machine can always reach `ok`.

`ty doctor` exits non-zero on errors. `--strict` also exits non-zero on
warnings, for a fleet sweep:

```bash
for s in host1 host2 host3; do
  ssh "$s" ty doctor --strict || echo "$s unhealthy"
done
```

### JSON

`--json` prints a stable report. Check ids never change meaning; new checks are
appended.

```bash
ty doctor --json | jq -r '.checks[] | select(.status=="error") | "\(.id): \(.summary)"'
```

```json
{
  "status": "warning",
  "checks": [
    {
      "id": "daemon-handshake",
      "status": "warning",
      "summary": "daemon is build 0.9.2, this is build 0.9.3 — run `ty restart` when convenient",
      "details": "Both speak protocol 1, so they still understand each other. …"
    }
  ]
}
```

Every check carries all four keys; `details` is present even when empty.
