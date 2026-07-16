# The `rpi` workflow (Research → Plan → Implement)

`rpi` is a built-in TaskYou workflow that takes a single free-text goal and drives
it through a disciplined, human-gated engineering pipeline: it researches the
codebase, designs an approach, breaks it into a phased outline, writes a precise
plan, implements it, and opens a pull request. It is **self-contained** — it
ships compiled into the `ty` binary, inlines its own methodology, and has **zero
dependency** on HumanLayer, the `riptide-rpi` plugin, or any installed skill.

It is built entirely on TaskYou's existing primitives: a workflow is a DAG of
ordinary tasks on one shared git branch (see `internal/pipeline`), advanced by the
normal daemon. `rpi` adds only two small pieces on top: a native **artifact store**
for handing documents between phases, and a per-step **gate** for human review.

## Running it

```sh
ty pipeline -d rpi "add rate limiting to the public API"
```

or, from an agent already running as a task, the `taskyou_create_pipeline` MCP
tool with `definition: rpi`. `rpi` shows up in `ty pipeline --list` (labeled
**built-in**), in shell completion, and in the MCP `definition` enum out of the
box — there is no YAML to install and no plugin to add.

## The phases

Each phase runs as its own step, in its own isolated git worktree, on one shared
branch. The document phases produce a markdown artifact and hand it to the next
phase through the artifact store; only the last two phases touch code and git.

| # | Phase | Reads | Produces | Gate |
|---|-------|-------|----------|------|
| 1 | `research-questions` | the raw goal | a neutral list of questions the research must answer | |
| 2 | `research` | **only** the `research-questions` artifact | a factual report of how the code works today | |
| 3 | `design` | the `research` artifact | an approach with tradeoffs and a recommendation | ✅ |
| 4 | `structure-outline` | the `design` artifact | a phased, vertical-slice outline with per-phase validation | |
| 5 | `plan` | the `structure-outline` artifact | a precise, diff-level plan with success criteria | ✅ |
| 6 | `implement` | the `plan` artifact | code, committed and pushed on the shared branch | |
| 7 | `describe-pr` | the branch (+ optional artifacts) | a pull request; parks for a human to merge | |

### Research stays blind to the goal

The `research` phase is deliberately **not** given the raw goal. It works only
from the `research-questions` artifact and reports what the codebase actually
does — no recommendations, no "we should". This preserves RPI's objectivity: the
investigation isn't biased by what the goal wants to be true. `research-questions`
is the single phase that reads the goal, and its neutral questions are the
hand-off.

## Gates and approval

Steps flagged `gate: true` in the workflow — by default `design` and `plan` — do
not advance the DAG when they finish. Instead they park in `blocked` with a review
log, so a human can inspect the design or plan before the workflow spends effort
downstream (these are the high-leverage, hard-to-undo boundaries).

A human approves a parked gate with the existing close verb — there is no new
command:

```sh
ty close <task-id>          # or: POST /api/tasks/{id}/close
```

Closing the gate step to `done` fires the normal dependency cascade
(`ProcessCompletedBlocker`), which auto-queues the next phase. To revise instead
of approve, edit the phase's artifact / re-run the step before closing.

## The artifact store

Because each phase runs in its own worktree, phases can't share uncommitted files,
and committing design docs to git purely to hand them off is the wrong shape. So
document phases hand off through a **TaskYou-native artifact store** — a small
DB-backed table keyed by the shared pipeline branch, exposed as two MCP tools:

- `taskyou_set_artifact { name, content }` — save this phase's document.
- `taskyou_get_artifact { name? }` — read one document by name (or list them all
  when `name` is omitted).

The branch key is derived from the calling task, never trusted from the client, so
a phase can only read/write artifacts on its own workflow's branch. Nothing is
written to `.humanlayer/` and no documents are committed to git; only `implement`
and `describe-pr` use the normal git-push / PR path.

## Customizing or sharing it

`rpi` is a bundled default, but it is still just a workflow definition. A
same-named file on the search path (`~/.config/task/workflows/rpi.yaml` or a
project's `.taskyou/workflows/rpi.yaml`) shadows the built-in, so you can eject
and tweak it with `ty pipeline edit rpi`. The intent is that workflows like this
become shareable through a community **`taskyou/ty-workflows`** repo — `rpi` is
the seed.
