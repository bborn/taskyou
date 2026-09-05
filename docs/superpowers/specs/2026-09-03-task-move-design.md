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

`ty place <task-id> <host|local> [--dir <path>]`

There is no flag. Moving a task moves its work; that is what the word means.
`--force` is the single opt-out and means "move without the work" — for a source
host that cannot be reached, or a wrong turn worth abandoning.

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
   named in the output so their absence is not a surprise on the far side. They do not
   block the move: leaving them on the old host destroys nothing.
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

## Correction

An earlier draft made carrying opt-in, on the reasoning that most placements happen to
tasks that have never run and so have nothing to carry. That reasoning was invented, not
measured. Counting a real board: 31 of 31 placement decisions were on tasks that had
already run; none on a task that never had. Carrying is the default.

## Correction: step 6 was never implemented

"Land" was written as a sentence about what would happen on its own — "the task's next
spawn on the target clones the pushed branch" — and no code did it. Task 5286 found all
three ways that failed.

The branch existed only as prose. `CarryWork` returned it, the placement reason quoted it
("moved here by hand, carrying task/5286-…"), and nothing wrote it to a field. Worktree
setup reads `SourceBranch` and `BranchName`, so it saw a task with no branch and did what
that means: cut a new one from the default branch.

That made both landings wrong, in opposite ways:

- **Local** never provisioned at all. The task arrived with an empty `worktree_path`, and
  every start path but the daemon's refuses that — "task has no worktree yet: refusing to
  start outside an isolated worktree". The guard was right; nothing did the provisioning
  it was guarding.
- **Remote** provisioned cleanly and silently dropped the work. Its script asked only
  whether a *local* branch of that name existed; a carried branch is on origin and
  nowhere else, so it fell through to a fresh branch off main. Correct name, clean
  checkout, none of the work — immediately after the carry gate finished proving that
  work was safe. This is the more dangerous of the two, because nothing looks wrong.

So step 6 is now code, and it runs at move time rather than being left to the next spawn:

6. **Land.** Write the carried branch to the task (`SourceBranch`, `BranchName`), so the
   receiving host can find the work as data rather than as English. Provision the local
   worktree immediately when the target is here; remote targets still provision at spawn,
   which is the only moment that host is reachable, but now attach to `origin/<branch>`
   when there is no local ref.

Landing is best effort by design. It runs *after* the carry gate and the placement write,
so a landing failure means a task that starts a little later — never a task that reports
"NOT been moved" when it has in fact moved.

Re-running `ty place <id> local` on a task that is already placed here but has no worktree
repairs it instead of reporting a no-op, and finds the work by looking for a branch named
for that task on origin. A task's branch name is derived from its id, so a branch by that
name is that task's own work and not a coincidence. That lookup fetches, which is why it
hangs off the landing path and not off `setupWorktree`, which every ordinary task start
goes through.
