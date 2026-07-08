# Verify — workflow/pipeline nomenclature rename

## What was verified
The user-facing rename of "pipeline" → "workflow" (CLI command + `taskyou_create_workflow`
MCP tool, help text, docs), with the `pipeline` CLI alias kept for back-compat.

## Review finding addressed
REVIEW.md flagged one **blocking** issue: `internal/mcp/server_test.go` still asserted the
old MCP tool name `taskyou_create_pipeline` in `TestToolsList`, so `go test ./internal/mcp/...`
failed. Fixed by updating the assertion to `taskyou_create_workflow` (the tool's new name;
the old name is intentionally not re-advertised).

## Evidence (run by hand, not just trusting CI)
- `go build ./...` → clean.
- `go test ./...` → all packages PASS (previously `internal/mcp` failed on TestToolsList).
- `ty workflow --list` and `ty pipeline --list` (alias) both resolve — back-compat intact.

## Verdict
Change does what the goal asked; suite is green. Ready for human review/merge.
