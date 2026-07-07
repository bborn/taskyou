package pipeline

// Step instruction templates. Each is a full task body: the daemon wraps it in
// the usual TaskYou harness (worktree constraints, taskyou_complete guidance), so
// these only add the step-specific job. {{goal}}, {{branch}}, {{step}} and
// {{stepslug}} are substituted at build time.
//
// Steps hand work forward through one shared git branch ({{branch}}): each step
// commits and pushes, and the next step checks the branch out (via SourceBranch).
// Only the terminal Collect step opens a pull request — earlier steps must NOT,
// because a PR parks a task in 'blocked' and would stall the DAG (a step only
// advances the workflow when it reaches 'done').

const planInstruction = `This is the **Plan** step of an automated plan → code → review → collect workflow. Later steps run on the shared branch ` + "`{{branch}}`" + ` — you are the root, and you plan only.

## Goal
{{goal}}

## Your job (planning only — do not write application code)
1. Explore the codebase enough to design a concrete, staged implementation plan for the goal above.
2. Write the plan to ` + "`PLAN.md`" + ` at the repo root: the approach, the exact files to change, the ordered steps, edge cases, and how to test it.
3. Commit and push so the next step can pick it up:
   ` + "`git add PLAN.md && git commit -m \"plan: {{goal}}\" && git push -u origin {{branch}}`" + `
   (If you are on a detached HEAD, run ` + "`git checkout -B {{branch}}`" + ` first.)

Do NOT implement the feature. Do NOT open a pull request.

When ` + "`PLAN.md`" + ` is committed and pushed, call ` + "`taskyou_complete`" + ` with a one-line summary. That automatically starts the Code step.`

const codeInstruction = `This is the **Code** step of an automated plan → code → review → collect workflow. The Plan step committed ` + "`PLAN.md`" + ` on this branch (` + "`{{branch}}`" + `); two independent reviewers run after you.

## Goal
{{goal}}

## Your job
1. Read ` + "`PLAN.md`" + ` and implement the plan fully. If the plan is wrong or incomplete, use your judgment and note the deviation in your commit message.
2. Keep the change focused on the goal. Add or update tests where it makes sense.
3. Commit and push:
   ` + "`git add -A && git commit -m \"...\" && git push`" + `
   (If you are on a detached HEAD, run ` + "`git checkout -B {{branch}}`" + ` first.)

Do NOT open a pull request — the Collect step does that.

When your implementation is committed and pushed, call ` + "`taskyou_complete`" + ` with a summary of what you built. That automatically starts the review steps.`

const reviewInstruction = `This is the **{{step}}** step of an automated plan → code → review → collect workflow. You are ONE of two reviewers running in parallel with independent context — that independence is the point, so review the change on your own merits and don't assume the other reviewer will catch what you miss.

## Goal
{{goal}}

## Your job (review only — do not change application code)
1. Review the diff of this branch against the default branch:
   ` + "`git fetch origin && git diff origin/HEAD...HEAD`" + ` (or ` + "`git diff $(git merge-base HEAD origin/HEAD)...HEAD`" + `).
   Check for correctness bugs, missed requirements from ` + "`PLAN.md`" + `, security issues, and clear quality problems.
2. Write your findings to ` + "`review-{{stepslug}}.md`" + ` at the repo root — a short prioritized list (blocking issues first, then nits), each with file:line and a concrete suggestion. If it looks good, say so explicitly.
3. Commit that file and push it to YOUR OWN review branch. You each use a separate branch, so there is no rebase and no way to clobber the other reviewer — just one commit and one push:
   ` + "`git add review-{{stepslug}}.md && git commit -m \"review: {{stepslug}}\" && git push origin HEAD:{{branch}}-{{stepslug}}`" + `
   (If you are on a detached HEAD, that push command still works as-is.)

Do NOT modify application code. Do NOT push to ` + "`{{branch}}`" + ` itself. Do NOT open a pull request.

When your review branch is pushed, call ` + "`taskyou_complete`" + ` with a one-line verdict. The Collect step runs once BOTH reviewers finish.`

const collectInstruction = `This is the **Collect** step of an automated plan → code → review → collect workflow, and the terminal step. Two independent reviewers have each pushed their review to a separate branch.

## Goal
{{goal}}

## Your job
1. Get onto the code branch and fetch the reviews:
   ` + "`git fetch origin && git checkout -B {{branch}} origin/{{branch}}`" + `
   The reviewers pushed their reviews to these branches:
{{reviews}}
   Read each one, e.g. ` + "`git show origin/{{branch}}-review-a:review-review-a.md`" + `.
2. Address the findings: apply the fixes you agree are correct and safe, keeping changes focused on the code branch. If the reviewers disagree, use your judgment and note the call. Skip anything out of scope and record why.
3. Commit your fixes and push ` + "`{{branch}}`" + `.
4. Open a pull request with ` + "`gh pr create`" + `. In the body, summarize the change, then list what each review flagged and how you resolved it (fixed / deferred / disagreed).
5. Call ` + "`taskyou_complete`" + `. Because a PR now exists, the workflow parks in 'blocked' for a human to review and merge — that is expected and completes the workflow.`
