# Plan: Remove redundant origin suffix from `ty pipeline --list` output

## Goal
In `ty pipeline --list`, each workflow line currently prints a dim `(built-in)` /
`(custom)` suffix after the workflow name. This is redundant — the footer already
tells the user where custom workflows live, and the suffix adds noise to every
line. Remove the suffix while keeping the rest of the listing byte-for-byte
identical.

## Where the code lives
`cmd/task/main.go`, inside the `pipeline` cobra command's `Run`, in the
`if listDefs { ... }` branch. The relevant block today (around lines 843–865):

```go
if listDefs {
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
        origin := "built-in"                                   // <- remove
        if d.Custom {                                          // <- remove
            origin = "custom"                                  // <- remove
        }                                                      // <- remove
        fmt.Printf("%s %s\n  %s\n  steps: %s\n",               // <- change
            successStyle.Render(d.Name),
            dimStyle.Render("("+origin+")"),                   // <- remove this arg
            d.Description,
            strings.Join(stepLabels, " · "))
    }
    fmt.Println(dimStyle.Render("Custom workflows: " + pipeline.WorkflowsDir() + "/*.yaml  ·  create with: task pipeline new \"<describe it>\""))
    return
}
```

## Approach
Delete the 4 lines that compute `origin`, and drop the origin format verb +
argument from the `fmt.Printf`. Everything else — the name line, the description
line, the steps line, and the trailing footer — stays exactly as is.

## Exact change (single file: `cmd/task/main.go`)
Replace this block:

```go
        origin := "built-in"
        if d.Custom {
            origin = "custom"
        }
        fmt.Printf("%s %s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), dimStyle.Render("("+origin+")"), d.Description, strings.Join(stepLabels, " · "))
```

with:

```go
        fmt.Printf("%s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), d.Description, strings.Join(stepLabels, " · "))
```

Key details:
- Drop the leading `%s ` (verb **and** the following space) from the format
  string so there is no trailing space after the rendered name.
- Remove the `dimStyle.Render("("+origin+")")` argument so the remaining args
  line up with the two remaining `%s` verbs (`d.Description`,
  `strings.Join(...)`).
- Leave the `stepLabels` loop, the footer `fmt.Println`, and the `return`
  untouched.

## Ordered steps
1. Open `cmd/task/main.go`, locate the `if listDefs {` block in the `pipeline`
   command `Run`.
2. Delete the four `origin` lines (`origin := "built-in"` … closing `}`).
3. Edit the `fmt.Printf` format string and args as shown above.
4. Confirm `d.Custom` is not referenced anywhere else that would now be dead
   (it is still a legitimate field on the definition struct used elsewhere; we
   are only removing this one display use, so no struct/field change is needed).
5. Build and lint (see below).

## Edge cases / things to watch
- **No unused-variable / import fallout.** We remove a local (`origin`), not an
  import. `dimStyle` is still used by the footer `fmt.Println`, so its import
  chain stays live. `d.Custom` remains a valid field elsewhere — do not delete
  it from the pipeline package.
- **Format-verb/arg count must stay balanced.** After the edit there are exactly
  two `%s` verbs and two trailing args. `go vet` (run by lint) will catch a
  mismatch — make sure the count matches.
- **No trailing space after the name.** Verify the new format string is
  `"%s\n  ..."`, not `"%s \n  ..."`.
- **Custom workflows still discoverable.** The footer line
  (`Custom workflows: <dir>/*.yaml ...`) is intentionally kept — that is where
  the "where do custom workflows come from" information now lives, so removing
  the per-line badge does not hide anything.
- Purely a stdout formatting change; no tests assert on this output (a quick
  `grep` for `built-in`/`(custom)` in `*_test.go` should confirm none exist —
  if one does, update it to the new format).

## How to exercise by hand (proof it works)
From the worktree root:

1. Build the binary:
   ```
   make build
   ```
   (or `go run ./cmd/task pipeline --list` to skip building).

2. Run the list command:
   ```
   ./ty pipeline --list
   ```
   or, if `make build` outputs elsewhere / for a one-shot:
   ```
   go run ./cmd/task pipeline --list
   ```

3. **Correct output:** each workflow prints three lines with **no** `(built-in)`
   or `(custom)` suffix on the name line, e.g.:
   ```
   plan-code-verify
     Plan → implement → verify on a shared branch
     steps: Plan (claude/opus) · Code (claude) · Verify (claude) ← Code
   Custom workflows: /Users/<you>/.taskyou/workflows/*.yaml  ·  create with: task pipeline new "<describe it>"
   ```
   The name line must have no trailing badge and no trailing space; the
   description line, steps line, and the final "Custom workflows:" footer must be
   unchanged from before.

4. **Regression check:** confirm the footer still appears and that both built-in
   and any custom workflow definitions still list (create a custom one first
   with `ty pipeline new "..."` if you want to prove a custom def also renders
   without the badge). Diff the before/after output; the only difference should
   be the removed ` (built-in)` / ` (custom)` token on each name line.

## Verification gates before commit
- `go build ./...` (or `make build`) succeeds.
- `golangci-lint run` is clean (matches CI; catches vet format-arg mismatch and
  unused vars).
- `./ty pipeline --list` output matches the expected shape above.
