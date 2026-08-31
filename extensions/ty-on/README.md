# ty-on

Placement resolver for TaskYou. Decides which machine a task should run on, and
answers "this one, here" or "run it locally".

## Why this is an extension

ty can run a task on another machine. The *policy* for that — which hosts exist,
what they are provisioned for, which one to pick — is specific to whoever owns
the fleet, so it does not belong in ty. A normal ty user never sees any of it.

This extension is the policy half. It touches nothing in ty's core: ty invokes
it, and it answers.

## The contract

ty-on is a binary that reads one JSON request on stdin and writes one JSON
response on stdout. It is invoked once per task, before the executor is spawned.

Request:

```json
{
  "event": "task.placement",
  "task": {
    "id": 5225,
    "title": "Some task",
    "project": "taskyou",
    "repo_path": "/Users/bruno/Projects/workflow",
    "executor": "claude"
  }
}
```

Response:

```json
{
  "target": "ol-agents",
  "workdir": "~/projects/engineering",
  "reason": "most free memory of 2 hosts serving offerlab (ol-agents 26.5G, mona 11.3G)"
}
```

`target` names a host in the `on` inventory. `workdir` is that project's
checkout path on that host — a remote path, so a leading `~` is left alone for
the remote shell to expand.

**An empty `target` means "run locally"**, and it is the answer to every
question ty-on cannot confidently answer: unknown project, missing inventory, no
reachable host, malformed request, `on` not installed. ty-on never fails a task
and never guesses a host — it exits 0 in all cases.

`reason` is always populated and is shown to the user, so it is written to
explain a surprising placement without further digging.

## Placement rules

The inventory is the same one the [`on`](https://github.com/bborn/on) CLI reads:
`$ON_HOSTS`, else `$XDG_CONFIG_HOME/on/hosts.yaml`, else
`~/.config/on/hosts.yaml`.

```yaml
hosts:
  ol-agents:
    ssh: ol-agents
    workdir: ~/projects
    capabilities: [agent, ruby, node]
    repos:
      offerlab: ~/projects/engineering
```

Given a task's project:

1. Find the hosts whose `repos` map contains that project.
2. **None** → local. The fleet has no checkout to run in.
3. **One** → that host. No probing: this path answers from the file alone, so it
   works on a machine that does not have `on` installed at all.
4. **Several** → the one with the most free memory. `on ls` already probes the
   fleet in parallel, so ty-on shells out to it rather than reimplementing the
   probe. Hosts `on` could not reach are dropped; ties break on host name so the
   answer is stable.

`on` is an optional dependency. If it is missing, or fails, or is slow, the task
stays local with a reason saying so.

### Speed

This runs in the task spawn path, so it is built to be fast or to get out of the
way. Rules 1–3 are a single file read. Rule 4 costs one `on ls` (an SSH round
trip per host, in parallel), bounded by `TY_ON_TIMEOUT` — past that budget ty-on
prefers a local placement to a late answer.

## Usage

```console
$ go build -o ty-on ./cmd
$ echo '{"event":"task.placement","task":{"project":"taskyou"}}' | ./ty-on
{"target":"mona","workdir":"~/Projects/taskyou","reason":"only host serving taskyou"}
```

`ty-on --help` prints the same summary; `ty-on --version` prints the version.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `ON_HOSTS` | — | Host inventory path. Overrides the default lookup, and is passed through to `on ls` so both read the same file. |
| `XDG_CONFIG_HOME` | — | When set and `ON_HOSTS` is not, the inventory is `$XDG_CONFIG_HOME/on/hosts.yaml`. |
| `TY_ON_TIMEOUT` | `3s` | Budget for the `on ls` probe. Unset or unparseable falls back to the default. |

## Development

```console
$ go test ./...
$ golangci-lint run --config ../../.golangci.yml ./...
```

Tests inject a fake prober rather than shelling out, so the suite passes on a
machine with no fleet and no `on` installed. The `on ls` table parser is pinned
against real output.

## Not in scope

ty-on decides *where*; it does not move anything. Syncing the working tree,
creating worktrees, and opening SSH sessions are all `on`'s job, and invoking
this resolver is ty's.

Host `capabilities` are parsed but not yet used for filtering — the rules above
are deliberately the whole policy. Matching an executor against a host's
capabilities is the obvious next lever.
