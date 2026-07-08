# Plan: Remove the origin `(custom)`/`(built-in)` suffix from `ty pipeline --list`

## Goal
`ty pipeline --list` currently prints an origin label — `(custom)` or `(built-in)` —
after every workflow name. Since there are no built-in workflows to distinguish
from anymore, that suffix is redundant noise on every line. Remove it. Keep the
rest of the listing (name, description, steps line, and the trailing "Custom
workflows: …" hint) byte-for-byte identical.

## Scope
- **One file, one command handler:** `cmd/task/main.go`, inside the `pipeline`
  cobra command's `Run` func, in the `if listDefs { … }` block (currently
  lines ~843–866).
- **No other files change.** In particular, the `Definition.Custom` field
  (`internal/pipeline/pipeline.go:51`) is **left in place** — it is still set in
  `internal/pipeline/definition_file.go` and asserted in
  `internal/pipeline/custom_test.go`. We are only removing its use in the
  `--list` display, not the field itself.

## Current code (for reference)
```go
origin := "built-in"
if d.Custom {
    origin = "custom"
}
fmt.Printf("%s %s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), dimStyle.Render("("+origin+")"), d.Description, strings.Join(stepLabels, " · "))
```

## The change
1. Delete the four `origin` lines:
   ```go
   origin := "built-in"
   if d.Custom {
       origin = "custom"
   }
   ```
2. Drop the origin argument from the `Printf`: remove the leading `%s ` (the
   `%s` plus the single space that separated the name from the suffix) and the
   corresponding `dimStyle.Render("("+origin+")")` argument. Result:
   ```go
   fmt.Printf("%s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), d.Description, strings.Join(stepLabels, " · "))
   ```

That is the entire change. The format string goes from `"%s %s\n  %s\n  steps: %s\n"`
(4 verbs) to `"%s\n  %s\n  steps: %s\n"` (3 verbs), and the arg count drops from
4 to 3 accordingly — they must stay matched or `go vet` will flag it.

## Ordered steps
1. Open `cmd/task/main.go`, locate the `if listDefs {` block in the `pipeline`
   command `Run` func (~line 843).
2. Remove the `origin := …` / `if d.Custom { … }` block.
3. Edit the `fmt.Printf` to drop the ` %s` name-suffix verb and its
   `dimStyle.Render("("+origin+")")` argument (per above).
4. Build: `go build ./cmd/task` (must compile — `origin` is now unused, so it
   must be fully removed, not just unreferenced).
5. `go vet ./cmd/task` (confirms Printf verb/arg count still matches).
6. `gofmt -l cmd/task/main.go` returns nothing (already formatted); run
   `golangci-lint run` if available to stay green with CI (pinned v2.8.0).

## Edge cases / things to watch
- **Unused variable:** Go will not compile if `origin` is declared but unused,
  so it must be deleted entirely (this is a guardrail, not a risk).
- **Printf verb/arg mismatch:** the number of `%s` verbs must drop from 4 to 3
  in lockstep with dropping one argument, or `go vet` fails. Double-check.
- **Trailing hint line unchanged:** the
  `fmt.Println(dimStyle.Render("Custom workflows: …"))` line stays exactly as-is.
- **`--json` path untouched:** this block only runs for `--list` text output;
  there is no JSON branch here to keep in sync for this flag.
- **Spacing:** removing the suffix means each entry's first line is just the
  styled name followed by a newline — no dangling trailing space after the name.
- **Do not touch** the `Custom` field, `definition_file.go`, or any test that
  reads `Custom`; those are unrelated to display and must keep working.

## How to exercise it by hand (proof it works)
1. Build the binary: `go build -o /tmp/ty ./cmd/task`
2. Run the list command: `/tmp/ty pipeline --list`
3. **Correct output:** every workflow entry looks like

   ```
   plan-code-review
     Multi-model plan → code → review pipeline for a goal
     steps: plan (claude) · code (claude) · review (claude) ← plan+code
   Custom workflows: /Users/<you>/.../workflows/*.yaml  ·  create with: task pipeline new "<describe it>"
   ```

   with **no `(custom)` or `(built-in)`** anywhere after any workflow name.
4. Grep-assert absence explicitly:
   `/tmp/ty pipeline --list | grep -E '\((custom|built-in)\)'`
   should print nothing and exit non-zero (grep found no match) — proving the
   suffix is gone.
5. Sanity-check nothing else regressed: confirm the description line, the
   `steps:` line, and the trailing "Custom workflows:" hint are all still
   present (`/tmp/ty pipeline --list` visually, or
   `/tmp/ty pipeline --list | grep -c 'steps:'` returns the workflow count).
6. If a custom YAML workflow exists in the workflows dir, it should list the
   same way as any other — indistinguishable by suffix, which is the point.

## Handoff
- Commit and push the shared branch:
  `git add -A && git commit -m "plan: remove origin suffix from pipeline --list" && git push origin HEAD:pipeline/4674-in-the-cli-ty-pipeline-list-output-every`
- Do NOT open a PR (a later workflow step does that).
- Then call `taskyou_complete`.
