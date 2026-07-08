# Verify — remove redundant origin suffix from `ty pipeline --list`

## Change
Code removed the `(custom)`/`(built-in)` origin suffix from each line of the
`ty pipeline --list` output (`cmd/task/main.go`), leaving `<name>` + description +
steps. Review approved it (see REVIEW.md).

## Evidence (exercised by hand)
- `go build ./...` → clean.
- Ran the rebuilt `ty pipeline --list`: each entry now prints just the name (no
  trailing `(custom)`), as intended.
- `go test ./...`: the only failures are `TestBuiltinUsesInstructionVerbatim` and
  `TestCreateBuildsDAG`, which fail **identically on clean `main`** (pre-existing;
  caused by a `plan-code-review.yaml` in the user's global workflows dir shadowing
  the built-in during the test) — **not** caused by this diff.

## Verdict
Change does what the goal asked; no regression attributable to it. Ready to merge.
