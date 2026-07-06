package pipeline

// Phase instruction templates. Each is a full task body: the daemon wraps it in
// the usual TaskYou harness (worktree constraints, taskyou_complete guidance), so
// these only add the phase-specific job. {{goal}} and {{branch}} are substituted
// at build time.
//
// The handoff between phases is a shared git branch ({{branch}}): each phase
// commits and pushes, and the next phase checks the branch out (via SourceBranch)
// and builds on it. Non-final phases must NOT open a pull request — a PR would
// park the phase in 'blocked' awaiting a human merge and stall the chain, since
// only a task that reaches 'done' auto-queues the next phase.

const planInstruction = `This is the **PLAN** phase of an automated plan → code → review pipeline. Three agents run in sequence on the shared branch ` + "`{{branch}}`" + ` — you are the first, and you plan only.

## Goal
{{goal}}

## Your job (planning only — do not write application code)
1. Explore the codebase enough to design a concrete, staged implementation plan for the goal above.
2. Write the plan to ` + "`PLAN.md`" + ` at the repo root: the approach, the exact files to change, the ordered steps, edge cases, and how to test it.
3. Commit the plan and push the branch so the next phase can pick it up:
   ` + "`git add PLAN.md && git commit -m \"plan: {{goal}}\" && git push -u origin {{branch}}`" + `
   (If you are on a detached HEAD, run ` + "`git checkout -B {{branch}}`" + ` first.)

Do NOT implement the feature. Do NOT open a pull request.

When ` + "`PLAN.md`" + ` is committed and pushed, call ` + "`taskyou_complete`" + ` with a one-line summary. That automatically starts the CODE phase on this same branch.`

const codeInstruction = `This is the **CODE** phase of an automated plan → code → review pipeline. The PLAN phase committed an implementation plan to ` + "`PLAN.md`" + ` on this branch (` + "`{{branch}}`" + `); a separate REVIEW phase runs after you.

## Goal
{{goal}}

## Your job
1. Read ` + "`PLAN.md`" + ` and implement the plan fully. If the plan is wrong or incomplete, use your judgment and note the deviation in your commit message.
2. Keep the change focused on the goal. Add or update tests where it makes sense.
3. Commit your work and push the branch:
   ` + "`git add -A && git commit -m \"...\" && git push`" + `
   (If you are on a detached HEAD, run ` + "`git checkout -B {{branch}}`" + ` first.)

Do NOT open a pull request — the REVIEW phase does that.

When your implementation is committed and pushed, call ` + "`taskyou_complete`" + ` with a summary of what you built. That automatically starts the REVIEW phase.`

const reviewInstruction = `This is the **REVIEW** phase of an automated plan → code → review pipeline. It deliberately runs with a different model/executor than the code was written with, for a fresh set of eyes. The CODE phase implemented the plan (see ` + "`PLAN.md`" + `) on this branch (` + "`{{branch}}`" + `).

## Goal
{{goal}}

## Your job
1. Review the diff of this branch against the default branch (` + "`git diff $(git merge-base HEAD origin/HEAD)...HEAD`" + ` or similar). Look for correctness bugs, requirements from ` + "`PLAN.md`" + ` that were missed, security issues, and clear quality problems.
2. Fix what you find, keeping fixes small and safe. Commit them (` + "`git checkout -B {{branch}}`" + ` first if on a detached HEAD), then push the branch.
3. Open a pull request with ` + "`gh pr create`" + `, summarizing the change and listing anything you flagged but chose not to fix.
4. Call ` + "`taskyou_complete`" + `. Because a PR now exists, the task parks in 'blocked' for a human to review and merge — that is expected and completes the pipeline.`
