# Plan: Remove the origin `(custom)`/`(built-in)` suffix from `ty pipeline --list`

## Goal
In the CLI `ty pipeline --list` output, every workflow line currently prints an
origin label like `(custom)` or `(built-in)`. Since there are no built-in
workflows to distinguish against anymore, that suffix is redundant on every
line. Remove it from the `--list` output. Keep everything else identical.

## Context / findings
- The `--list` rendering lives in **`cmd/task/main.go`**, inside the `pipeline`
  cobra command's `Run` func, in the `if listDefs {` block (currently lines
  ~845–865).
- The relevant lines today:
  ```go
  origin := "built-in"
  if d.Custom {
      origin = "custom"
  }
  fmt.Printf("%s %s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), dimStyle.Render("("+origin+")"), d.Description, strings.Join(stepLabels, " · "))
  ```
- `origin` and `d.Custom` are used **only** here for this output. Verified:
  - `grep -rn "\.Custom" cmd/ internal/pipeline/` → this block + one place in
    `internal/pipeline/custom_test.go` (unrelated: asserts the struct field is
    set) + the field definition in `internal/pipeline/pipeline.go:51`.
  - `grep -rn "origin" cmd/task/main.go` → only these three lines relate to the
    suffix.
- The `Definition.Custom` struct field (`internal/pipeline/pipeline.go:51`)
  stays — it is still meaningful metadata (YAML vs. built-in) and is asserted by
  a test. We are only changing what `--list` prints, not the data model.
- The final footer line (`Custom workflows: <dir>/*.yaml  ·  create with: …`)
  and the `--json` path are **out of scope** and must stay untouched.

## Approach
Delete the three-line `origin` computation and drop the origin `%s` field from
the `fmt.Printf` format string and its argument. Nothing else changes: the name
(green), description, and steps line all stay exactly as they are.

## Files to change
- `cmd/task/main.go` — the `if listDefs {` block only.

## Ordered steps
1. In `cmd/task/main.go`, inside the `--list` loop, remove:
   ```go
   origin := "built-in"
   if d.Custom {
       origin = "custom"
   }
   ```
2. Change the `Printf` from:
   ```go
   fmt.Printf("%s %s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), dimStyle.Render("("+origin+")"), d.Description, strings.Join(stepLabels, " · "))
   ```
   to:
   ```go
   fmt.Printf("%s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), d.Description, strings.Join(stepLabels, " · "))
   ```
   (Drop the leading `%s ` and its `dimStyle.Render(...)` argument. Keep the
   `\n  %s\n  steps: %s\n` tail exactly.)
3. Build to confirm no unused-variable / unused-import fallout:
   `go build ./cmd/task` (this also catches an accidentally-orphaned `origin`).
4. Run gofmt/goimports and the pinned linter (`golangci-lint run`) — the repo's
   CI Lint gate must stay green.

## Edge cases / risks
- **Unused variable**: removing the `Printf` arg without removing the `origin`
  declaration would leave `origin` unused → compile error. Both must go together
  (step 1 + step 2). `go build` catches this.
- **`dimStyle` still used?** Yes — `dimStyle` is used by the footer line
  (`dimStyle.Render("Custom workflows: …")`) and elsewhere in the file, so it
  will not become unused. No import/var cleanup needed.
- **`d.Custom` / struct field**: leave intact — still used by
  `internal/pipeline/custom_test.go` and is legitimate metadata.
- **`--json` output**: not touched by this block (the `--list` path returns
  before any JSON handling); leave as-is.
- **Spacing**: the new format begins the line with just the styled name and a
  newline — no trailing space before the newline. Confirm no stray double space.

## How to exercise it by hand (proof it works)
From the repo root:

1. Build the CLI:
   ```sh
   go build -o /tmp/ty ./cmd/task
   ```
2. Run the list command:
   ```sh
   /tmp/ty pipeline --list
   ```
3. **Correct output** — each workflow is three lines, with **no** `(custom)` or
   `(built-in)` suffix after the name. For example:
   ```
   plan-code-review
     Plan → code → review pipeline for a goal
     steps: plan (claude) · code (claude) ← plan · review (claude) ← code
   Custom workflows: /Users/<you>/.taskyou/workflows/*.yaml  ·  create with: task pipeline new "<describe it>"
   ```
   Specifically verify:
   - The name line no longer has a trailing ` (custom)` / ` (built-in)`.
   - Description and `steps:` lines are unchanged.
   - The trailing "Custom workflows: …" footer is still printed.
4. **Before/after diff check** (optional but convincing): stash the change,
   capture `ty pipeline --list` output, unstash, capture again, and diff — the
   only difference should be the removed ` (custom)`/`(built-in)` tokens.
5. Sanity-check an authored custom workflow too (if one exists in the workflows
   dir, or create one with `ty pipeline new "..."`) to confirm it also renders
   without a suffix — proving the removal is uniform, not just for built-ins.

## Verification checklist before handoff
- [ ] `go build ./cmd/task` succeeds (no unused `origin`).
- [ ] `golangci-lint run` clean.
- [ ] `ty pipeline --list` prints names with no origin suffix; description,
      steps, and footer unchanged.
