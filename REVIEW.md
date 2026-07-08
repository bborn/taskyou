# Review — Unify "workflow" vs "pipeline" nomenclature (task 4664)

**Verdict:** The user-facing rename is done well and matches PLAN.md closely. The
CLI command is `ty workflow` with a working `pipeline` alias (verified at runtime:
`ty pipeline`, `ty pipeline new`, `ty pipeline edit` all resolve). Docs, MCP tool
name + dispatch alias, and help text are all consistent. `go build ./...` passes,
`gofmt` is clean, and `internal/pipeline` + `cmd` tests pass.

There is **one blocking issue**: a test was not updated to match the renamed MCP
tool, so `go test ./internal/mcp/...` fails — which the PLAN.md gate requires to be
green.

---

## Blocking

1. **`internal/mcp/server_test.go:104` — `TestToolsList` fails.**
   The `want` map still asserts that `taskyou_create_pipeline` is advertised in
   `tools/list`. The tool was renamed to `taskyou_create_workflow` and the old
   name is intentionally **not** re-advertised (PLAN.md Stage 2 / verification:
   "tools/list should advertise the new name and NOT the old one"). The test now
   fails:
   ```
   --- FAIL: TestToolsList (0.01s)
       server_test.go:130: expected tool "taskyou_create_pipeline" in tools/list
   ```
   **Fix:** change the map key on line 104 from `"taskyou_create_pipeline"` to
   `"taskyou_create_workflow"`. (Optionally add a separate assertion that a
   `tools/call` with the old `taskyou_create_pipeline` name still dispatches, to
   lock in the backward-compat alias — nice-to-have, not required.)

---

## Nits (optional)

2. **`internal/mcp/server.go:262` — MCP tool description still says "phase
   tasks" / "The first phase is queued immediately".**
   Not a "pipeline" leak, but it's inconsistent with the "step" vocabulary used
   everywhere else in this change (README, CLI help, and the docs reference all
   say *steps*). Purely cosmetic and arguably out of scope for this task; flag it
   only if the Verify step wants a fully consistent pass. Leave otherwise.

---

## Confirmed correct (no action)

- **CLI alias + subcommands** resolve via cobra as claimed — `ty pipeline`,
  `ty pipeline new`, `ty pipeline edit` all produce the renamed command's help.
- **Data/behavior left untouched** as required: the `"pipeline"` task tag, the
  `pipeline/` branch prefix, the fallback slug, and the `internal/pipeline` Go
  package/identifiers are all unchanged.
- **Backward-compat MCP dispatch** — `case "taskyou_create_workflow",
  "taskyou_create_pipeline":` keeps old callers working while advertising only
  the new name.
- **All remaining "pipeline" strings in user-facing files are intentional alias
  notes** (README.md:117, docs/mcp-tools-reference.md:135, skills/taskyou/SKILL.md:65,254).
- `docs/`, `README.md`, and code-comment wording (`definition_file.go`,
  `generate.go`) updated to `ty workflow`.
