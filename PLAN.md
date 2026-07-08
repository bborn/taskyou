# Plan: Remove redundant origin suffix from `ty pipeline --list` output

## Goal
In the `ty pipeline --list` output, remove the redundant `(custom)` / `(built-in)`
origin suffix printed after each workflow name. Tiny, focused, cosmetic change —
no behavior change to pipeline creation.

## Background / current behavior
The `--list` branch of the `pipeline` cobra command lives in `cmd/task/main.go`
(the block starting around line 843, inside `pipelineCmd.Run`). For each
definition returned by `pipeline.Definitions()` it prints:

```
<name> (built-in)      <- or (custom)
  <description>
  steps: <step> · <step> · ...
```

The relevant code (`cmd/task/main.go:845-864`):

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

**Why the suffix is redundant:** the footer line already printed after the loop
tells the user where custom workflows come from (the workflows dir), so tagging
every single line with `(built-in)`/`(custom)` adds visual noise without adding
information. The task is to drop the per-line tag.

## Approach
Delete the three-line `origin` computation and remove the `(origin)` argument
(plus its `%s`) from the `fmt.Printf`. Nothing else references `origin`. The
`d.Custom` struct field is left untouched — it stays in the `Definition` type and
is still used elsewhere; we are only removing its use in this one display line.

## Files to change
- `cmd/task/main.go` — the `--list` block inside `pipelineCmd.Run` (only file).

## Ordered steps
1. In `cmd/task/main.go`, inside the `if listDefs { ... }` block, remove:
   ```go
   origin := "built-in"
   if d.Custom {
       origin = "custom"
   }
   ```
2. Change the print line from:
   ```go
   fmt.Printf("%s %s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), dimStyle.Render("("+origin+")"), d.Description, strings.Join(stepLabels, " · "))
   ```
   to:
   ```go
   fmt.Printf("%s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), d.Description, strings.Join(stepLabels, " · "))
   ```
   (drop the leading `` %s`` for the origin and its `dimStyle.Render(...)` arg;
   keep the name, description, and steps exactly as before).
3. Leave the trailing "Custom workflows: ..." footer line unchanged.
4. Build and lint (see verification).

## Edge cases / things to watch
- **Arg/verb count must match:** after removing one `%s` and one argument, the
  format string has three `%s` verbs and exactly three arguments (`d.Name`,
  `d.Description`, `stepLabels`). `go vet` (run by the build/CI) will flag a
  mismatch, so double-check the count.
- **Unused variable:** removing the only use of `origin` means the `origin :=`
  declaration must be removed entirely, or Go will fail to compile with
  "declared and not used". Removing all three lines in step 1 handles this.
- **`d.Custom` stays defined:** do not delete the field from the `Definition`
  struct — it's still populated/used by the pipeline package; only its use in
  this print line is removed.
- **No JSON path affected:** the `--list` block does not honor `--json` (it only
  ever prints this text form), so there is no JSON branch to update.
- **Styling preserved:** `successStyle.Render(d.Name)` stays; only the
  `dimStyle`-rendered origin token is dropped. Description/steps formatting and
  the two-space indentation are unchanged.

## Manual verification (prove it works)
From the repo root:

1. Build the binary:
   ```
   make build          # or: go build -o ty ./cmd/task
   ```
2. Run the list command:
   ```
   ./ty pipeline --list
   ```
3. **Correct output:** each workflow block starts with just the workflow name
   (styled) on the first line, with **no** `(built-in)` or `(custom)` after it,
   e.g.:
   ```
   plan-code-review
     A multi-model plan → code → review pipeline
     steps: plan (claude) · code (claude) ← plan · review (claude) ← code
   ...
   Custom workflows: /…/workflows/*.yaml  ·  create with: task pipeline new "<describe it>"
   ```
   The description line, the `steps:` line, and the trailing "Custom workflows:"
   footer must be identical to before — only the `(built-in)`/`(custom)` token
   is gone.
4. (Optional) If you have a custom workflow YAML in the workflows dir, confirm
   its line also has no `(custom)` suffix — verifying the tag was removed for
   both origins, not just one.
5. Sanity checks:
   ```
   go vet ./cmd/task/...
   golangci-lint run ./cmd/task/...   # match CI's pinned version; must stay green
   ```
   `go vet` confirms the Printf verb/arg count is balanced; lint must pass (Lint
   is a required CI gate for this repo).

## Out of scope
- No new tests (this is a cosmetic one-line format change with no logic to
  assert; there is no existing test asserting the `(origin)` text).
- No changes to `pipeline.Definitions()`, the `Definition` struct, or the
  `--json` output.
