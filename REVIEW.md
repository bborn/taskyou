# Review: Remove redundant origin suffix from `ty pipeline --list`

**Verdict: Looks good — ship it.** The change is exactly what PLAN.md specifies,
compiles cleanly, and has no correctness, security, or quality concerns.

## Scope of change
Single hunk in `cmd/task/main.go` (lines ~855–862): the three-line `origin`
computation and its `(built-in)`/`(custom)` render argument were removed from the
per-workflow header `fmt.Printf`.

## Verification performed
- **Matches PLAN.md byte-for-byte.** The removed lines and the edited format
  string are precisely the ordered steps in the plan. `%s %s\n` → `%s\n`; the
  `dimStyle.Render("("+origin+")")` argument is dropped along with the leading
  space. Description line, steps line, and their two-space indents are untouched.
- **No dangling references.** `grep origin cmd/task/main.go` returns only
  unrelated words ("original", "originally") — `origin` the variable is fully
  gone, so there's no unused-variable compile error.
- **`dimStyle` still used.** The trailing "Custom workflows: …" footer
  (`cmd/task/main.go:863`) still calls `dimStyle.Render`, so no import/variable
  cleanup is needed.
- **`d.Custom` field preserved.** Only this one presentation of it was removed;
  the struct field remains for any other consumers.
- **`--json` path unaffected.** This is the text-only `--list` branch; no JSON
  rendering lives here.
- **Builds clean.** `go build ./cmd/task/` succeeds.

## Blocking issues
None.

## Nits
None. The change is appropriately surgical and the footer already communicates
where custom workflows live, so dropping the per-row origin suffix is the right
call.
