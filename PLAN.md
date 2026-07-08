# Plan: Remove redundant origin suffix from `ty pipeline --list`

## Goal
In the `ty pipeline --list` output, stop printing the `(custom)` / `(built-in)`
origin suffix on each workflow's header line. Keep everything else about the
listing byte-for-byte identical.

## Background / current behavior
The listing is rendered in `cmd/task/main.go`, inside the `pipeline` command's
`Run` handler, guarded by the `--list` flag. The relevant block (currently
lines ~845–865):

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
    origin := "built-in"
    if d.Custom {
        origin = "custom"
    }
    fmt.Printf("%s %s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), dimStyle.Render("("+origin+")"), d.Description, strings.Join(stepLabels, " · "))
}
fmt.Println(dimStyle.Render("Custom workflows: " + pipeline.WorkflowsDir() + "/*.yaml  ·  create with: task pipeline new \"<describe it>\""))
```

Each line looks like:

```
plan-code-review (built-in)
  Plan → code → review pipeline
  steps: plan (claude) · code (claude) ← plan · review (claude) ← code
```

The `(built-in)` / `(custom)` is redundant — the trailing "Custom workflows:
…/*.yaml" footer already tells the user where custom workflows live, and origin
adds noise per row.

## Approach
Delete the `origin` computation and drop the `%s` that renders it from the
`fmt.Printf`. This is the entire change — one file, ~4 lines removed, one format
string edited. No behavior other than the printed suffix changes.

## Exact file to change
- `cmd/task/main.go` — the `--list` branch of the `pipeline` command's `Run`
  handler (the block shown above, currently lines ~845–865).

## Ordered steps
1. In `cmd/task/main.go`, remove the three lines that compute `origin`:
   ```go
   origin := "built-in"
   if d.Custom {
       origin = "custom"
   }
   ```
2. Change the `fmt.Printf` from:
   ```go
   fmt.Printf("%s %s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), dimStyle.Render("("+origin+")"), d.Description, strings.Join(stepLabels, " · "))
   ```
   to (drop the ` %s` after the name and the corresponding `dimStyle.Render(...)`
   argument):
   ```go
   fmt.Printf("%s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), d.Description, strings.Join(stepLabels, " · "))
   ```
   Note the leading space that separated name and origin (`%s %s`) goes away too —
   the header is now just the styled name.
3. Leave the trailing `fmt.Println(dimStyle.Render("Custom workflows: …"))`
   footer untouched.
4. Build to confirm it compiles: `make build-ty` (produces `bin/ty`).

## Edge cases / things to watch
- **`origin` is used nowhere else.** Verified: the only references to `origin`
  in `cmd/task/main.go` are the three lines being removed (other `origin*`
  matches in the file are unrelated words like "original"). Removing it leaves no
  dangling references — the build would fail on an unused variable otherwise, so
  a clean compile is the guard here.
- **`d.Custom` field stays as-is.** We're not removing the struct field, just
  this one presentation of it. Other code may still use `Custom` (e.g. shadowing
  logic); don't touch it.
- **`--json` path is unaffected.** The `--list` branch here is text-only; there
  is no JSON rendering inside it, so no JSON output needs updating.
- **Don't reflow the other lines.** The description line (`  %s`) and steps line
  (`  steps: %s`) and their two-space indents must stay identical.
- **`dimStyle` may become unused? No** — `dimStyle` is still used by the footer
  `fmt.Println` on the next line, so no import/variable cleanup is needed.

## How to exercise the finished change by hand (proof it works)
1. Build the CLI:
   ```
   make build-ty
   ```
2. Run the list command:
   ```
   ./bin/ty pipeline --list
   ```
3. **Correct output:** each workflow's first line is now just the (green) name
   with no ` (built-in)` / ` (custom)` suffix, e.g.:
   ```
   plan-code-review
     Plan → code → review pipeline
     steps: plan (claude) · code (claude) ← plan · review (claude) ← code
   Custom workflows: <workflows dir>/*.yaml  ·  create with: task pipeline new "<describe it>"
   ```
   The description line, the `steps:` line, and the trailing `Custom workflows:`
   footer must be unchanged from before.
4. **Regression check with a custom workflow present** (optional but ideal, since
   `custom` was the other origin value): create a custom workflow so at least one
   definition has `Custom == true`, then re-run `--list` and confirm that
   definition also renders with no suffix (previously it showed `(custom)`):
   ```
   ./bin/ty pipeline new "plan then build then test" --name tmp-verify
   ./bin/ty pipeline --list        # tmp-verify appears, no "(custom)" suffix
   ```
   Clean up the temp file afterward from the workflows dir printed in the footer.
5. **Before/after diff sanity:** capture `--list` output before and after the
   change; the only difference should be the removal of the ` (built-in)` /
   ` (custom)` tokens on the header lines — nothing else.

## Out of scope
- No changes to `internal/pipeline` (the `Definitions()` / `Custom` data model).
- No changes to JSON output, the `pipeline new`/`edit` subcommands, or the footer.
- No new tests — this is a pure presentation tweak with no unit-test harness
  around CLI stdout in this area; hand-verification above is the acceptance check.
