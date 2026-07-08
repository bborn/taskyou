# Plan: Remove redundant origin suffix from `ty pipeline --list`

## Goal
In the `ty pipeline --list` output, drop the per-line origin suffix
(`(custom)` / `(built-in)`) that is printed after each workflow name in
`cmd/task/main.go`. Keep everything else in the listing identical.

## Why it's redundant
Each listed line already prints the workflow name, description, and steps. The
final footer line still tells the user where custom workflows live and how to
create them:

```
Custom workflows: <dir>/*.yaml  ·  create with: task pipeline new "<describe it>"
```

So the `(built-in)`/`(custom)` tag on every line adds visual noise without
adding information the user needs at a glance.

## The exact code (today)
`cmd/task/main.go`, inside the `pipeline` command's `--list` branch
(around lines 843–866):

```go
for _, d := range pipeline.Definitions() {
    stepLabels := make([]string, 0, len(d.Steps))
    for _, s := range d.Steps {
        label := s.Name + " (" + s.Executor
        if s.Model != "" {
            label += "/" + s.Model
        }
        label += ")"
        if len(s.Deps) > 0 {
            label += " ← " + strings.Join(s.Deps, "+")
        }
        stepLabels = append(stepLabels, label)
    }
    origin := "built-in"          // <-- remove
    if d.Custom {                 // <-- remove
        origin = "custom"         // <-- remove
    }                             // <-- remove
    fmt.Printf("%s %s\n  %s\n  steps: %s\n",
        successStyle.Render(d.Name),
        dimStyle.Render("("+origin+")"),   // <-- remove this arg + its %s
        d.Description,
        strings.Join(stepLabels, " · "))
}
fmt.Println(dimStyle.Render("Custom workflows: " + pipeline.WorkflowsDir() + "/*.yaml  ·  create with: task pipeline new \"<describe it>\""))
```

## Approach
Delete the three-line `origin` computation and remove the origin argument from
the `Printf`, collapsing the format string from `"%s %s\n  ..."` to
`"%s\n  ..."`. Nothing else in the loop or the footer changes.

## Files to change
- `cmd/task/main.go` — the single `Printf` block in the `pipeline --list`
  branch. This is the only file touched.

## Ordered steps
1. In `cmd/task/main.go`, remove the four lines that compute `origin`:
   ```go
   origin := "built-in"
   if d.Custom {
       origin = "custom"
   }
   ```
2. Change the `Printf` to drop the origin. From:
   ```go
   fmt.Printf("%s %s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), dimStyle.Render("("+origin+")"), d.Description, strings.Join(stepLabels, " · "))
   ```
   to:
   ```go
   fmt.Printf("%s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), d.Description, strings.Join(stepLabels, " · "))
   ```
   Note the format string loses the leading `%s ` (the name's `%s` plus the
   space and the origin's `%s`) → it becomes just `%s` for the name.
3. Build: `go build ./cmd/task` — must compile clean.
4. Lint: `golangci-lint run` (CI pins v2.8.0) — must be clean.

## Edge cases / gotchas
- **Declared-but-unused variable.** Go will fail to compile if `origin` is
  removed from the `Printf` but the `origin :=` declaration is left behind
  (`declared and not used`). Both must go together. Likewise `d.Custom` is only
  read to set `origin`; once `origin` is gone, that `if` block must be deleted
  entirely — do not leave a dangling `if d.Custom {}`.
- **`dimStyle` stays in use.** `dimStyle` is still referenced by the footer
  `fmt.Println(...)` on the line after the loop, so removing its use inside the
  loop will NOT make it an unused import/variable. No import changes needed.
- **`pipeline.Definition.Custom` field stays.** Only this one display site is
  removed; the `Custom` field on the definition is presumably used elsewhere
  (e.g. shadowing logic). Do not delete the field itself — just stop reading it
  here. (Verify with `grep -rn "\.Custom" cmd internal` before/after; expect
  other references to remain untouched.)
- **Spacing.** After the edit the name line is just the rendered name with no
  trailing space where `(built-in)` used to be. That's the intended result.
- **Footer unchanged.** The `Custom workflows: ...` footer line is a separate
  `Println` and must remain exactly as-is.

## How to exercise it by hand (proof it works)
1. Build the binary:
   ```
   go build -o /tmp/ty ./cmd/task
   ```
2. Run the list command:
   ```
   /tmp/ty pipeline --list
   ```
3. **Correct output:** each workflow block shows the name on its own line with
   **no** `(built-in)` or `(custom)` tag after it, e.g.:
   ```
   plan-code-review
     Plan, code, two parallel reviews, then collect and open a PR
     steps: Plan (claude/opus) · Code (claude/sonnet) · ...
   ```
   The description line and `steps:` line are unchanged from before. The final
   footer line (`Custom workflows: .../*.yaml  ·  create with: ...`) is still
   present.
4. **Before/after comparison (optional but convincing).** Stash the branch,
   build the current `main` version to `/tmp/ty-old`, and diff:
   ```
   /tmp/ty-old pipeline --list > /tmp/before.txt
   /tmp/ty     pipeline --list > /tmp/after.txt
   diff /tmp/before.txt /tmp/after.txt
   ```
   The only differences should be the removal of the ` (built-in)` / ` (custom)`
   tag from each name line — nothing else.
5. If a custom workflow exists (or create one with
   `ty pipeline new "..."`), confirm its line also no longer carries `(custom)`,
   proving both branches of the old `if d.Custom` are gone cleanly.

## Out of scope
- No changes to the footer, to `pipeline.Definitions()`, or to the `Custom`
  field itself.
- No changes to any other command or to the TUI.
- No new tests: this is a cosmetic single-line output change with no logic to
  unit-test; the manual `--list` run above is the verification.
