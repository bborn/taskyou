# `ty move` — move a task's work and session to another host

## The problem

Placement today decides *where a task runs*. Changing it (`ty place`, `ty retry --on`)
moves the decision and nothing else: the worktree, the branch and the executor session
stay on the host that made them, and the task starts over on the far side. The command
says so and refuses without `--force`.

That is not a move. The case that matters is: a task is running on `mona`, and you want
the work here so you can run it and browser-test it yourself. A placement flip strands
exactly the thing you wanted.

## What "move" means here

**The work is lossless. The conversation is not.**

Every tracked file — staged, unstaged, or already committed — arrives on the target
host. That is a hard gate: the move does not proceed unless the work is verifiably
safe.

The Claude session does *not* travel verbatim. It could not, cheaply: a session lives at
`~/.claude/projects/<slugified-abs-cwd>/<id>.jsonl`, `cwd` is stamped on ~70% of its
records, and ~5% carry absolute paths in tool inputs and file-history snapshots. Hosts
have different `$HOME`, so carrying a session across means either rewriting the agent's
own record of what it did, or forcing every host to use an identical worktree path.
Both were rejected. Instead the agent writes a handoff document and the new agent starts
from it.

## Flow

`ty move <task-id> <host|local> [--dir <path>]`

1. **Handoff.** If the agent is alive, ask it to write `.taskyou/handoff.md` — what it
   was doing, what is done, what is next, what is half-finished and why — and wait,
   bounded. If it is dead or wedged, synthesise the same document from the task log.
   The move never blocks on an agent that cannot answer.
2. **Carry.** In the task's worktree, on whichever host holds it: `git add -A`, commit
   anything uncommitted as a WIP commit, and `git push` the branch.
3. **Verify.** Re-read `git status --porcelain` (must be empty) and confirm the local
   branch tip equals the pushed tip. **This is the gate.** If it fails, nothing else
   happens and the task stays where it is. A move that cannot prove the work is safe is
   not performed.
4. **Report what does not travel.** `git status --porcelain --ignored` lists files git
   is ignoring — `.env`, local databases, build output. These do not move. They are
   listed, and a substantive set refuses without `--force`, rather than being discovered
   as an absence on the far side.
5. **Place.** Clear the old placement, write the new decision, after the usual
   `preflightHost` check on the target.
6. **Land.** End the source session and free its worktree. The task's next spawn on the
   target clones the pushed branch and opens with `handoff.md` as its context.

## Where the code lives

`internal/executor/move.go`, written entirely against the existing `Runner` seam
(`Command(ctx, workDir, name, args...)`, satisfied by both `LocalRunner` and
`RemoteRunner`). Core learns no new ssh: the same function carries work off a remote
host and off this one, differing only in which `Runner` it is handed.

`cmd/task/move.go` is a thin command that composes `CarryWork` with the placement write
that `ty place` already performs — preflight, `--dir` resolution and stranding detection
are reused, not reimplemented.

## Not in scope

- Moving the session transcript verbatim.
- Carrying git-ignored files.
- A TUI key. That follows once the command is proven; it will call the same function.
