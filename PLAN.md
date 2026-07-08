# Plan — Unify "workflow" vs "pipeline" nomenclature (task 4664)

## Goal & decision

The same concept is called a **workflow** in the README, docs, and TUI, but a
**pipeline** in the CLI command (`ty pipeline`), the MCP tool
(`taskyou_create_pipeline`), the `internal/pipeline` package, and assorted
help/flag text. Settle on **`workflow`** as the single user-facing term and make
the user-facing surface consistent — **while keeping `pipeline` working as a
backward-compatible alias** so existing scripts and muscle memory don't break.

This is a **naming/wording-only** change. **No behavior changes.**

---

## Scope

### In scope (user-facing surface → say "workflow")

1. **CLI command** `cmd/task/main.go` — rename the `pipeline` command to
   `workflow`, register `pipeline` as a cobra alias, and reword its Short/Long
   help, examples, and flag descriptions.
2. **MCP tool** `internal/mcp/server.go` — advertise `taskyou_create_workflow`,
   keep `taskyou_create_pipeline` as an (unadvertised) working alias in the call
   dispatch, and reword the description + a couple of user-facing strings.
3. **Docs** — `README.md`, `docs/mcp-tools-reference.md`, `skills/taskyou/SKILL.md`
   — switch example command names to `ty workflow …` and the MCP tool name to
   `taskyou_create_workflow`, noting the `pipeline` alias still works.

### Explicitly NOT changed — these are **data/behavior**, not wording

Changing any of these would alter runtime behavior or break existing tasks/branches,
which the task forbids:

- **The `"pipeline"` task tag** that marks a task as belonging to a workflow —
  `internal/pipeline/pipeline.go:275` (`Tags: "pipeline"`) and the matcher
  `hasPipelineTag` in `internal/pipeline/group.go:37-45`. Existing workflow tasks
  in the DB are tagged `pipeline`; renaming the tag would orphan them from board
  grouping.
- **The `pipeline/` git branch prefix** — `internal/pipeline/pipeline.go:285`
  (`fmt.Sprintf("pipeline/%d-%s", …)`). This is the shared-branch namespace every
  step checks out (this very task runs on `pipeline/4664-…`). Leave it.
- **The `"pipeline"` fallback branch slug** — `internal/pipeline/pipeline.go:434`.
- **The `internal/pipeline` Go package name and all exported identifiers**
  (`pipeline.Create`, `Options`, `Definitions`, `DefaultDefinition`, `Group`,
  `GroupWorkflows`, `WorkflowsDir`, etc.) and TUI internal identifiers
  (`FieldPipeline`, `m.pipeline`, `pipelineOptions()`, `pendingPipeline`). Internal
  renames are **optional and lower priority** per the task; skipping them keeps the
  diff focused and low-risk. (See Follow-ups.)
- **The `ty-email` extension** (`extensions/ty-email/**`) — its "pipeline" wording
  refers to the unrelated email-processing pipeline concept. Out of scope.
- Internal code comments that happen to say "pipeline" (e.g.
  `internal/executor/executor.go:5718`). Not user-facing; leave unless trivially
  adjacent to an edit.

### Already correct (no change needed)

- TUI new-task form selector already renders the label **"Workflow"**
  (`internal/ui/form.go:2074`) with a "runs as a workflow…" hint (line 2077).
- Board/notification strings already say "workflow" (`internal/ui/app.go:1180`,
  `internal/ui/kanban.go`).
- CLI success/notification strings already say "workflow"
  (`cmd/task/main.go:950`, `1051`).
- `docs/mcp-tools-reference.md` body prose already says "workflow"; only the tool
  **name** and the JSON example need updating.

---

## Exact changes, staged

### Stage 1 — CLI command (`cmd/task/main.go`, ~lines 814–1128)

Primary edit target: the `pipelineCmd` definition.

- **Line 815–816**: change `Use: "pipeline [goal]"` → `Use: "workflow [goal]"`
  and add `Aliases: []string{"pipeline"}` to the `cobra.Command` literal.
- **Line 817** `Short`: "Create a multi-model plan → code → review **pipeline**
  for a goal" → "…**workflow** for a goal".
- **Long (818–840)**: replace the remaining "pipeline" wording and switch the
  example verb from `task pipeline …` to `ty workflow …`. Add one line noting the
  alias, e.g. `(the 'pipeline' command name still works as an alias)`. Keep the
  step/DAG explanation as-is.
- **Line 864** (the `--list` footer): `task pipeline new "<describe it>"` →
  `ty workflow new "<describe it>"`.
- **Line 972** flag help: `"Pipeline definition to use"` → `"Workflow definition to use"`.
- Comment on **814** (`// Pipeline subcommand …`) → "Workflow subcommand …" (cheap alignment).
- **`pipelineNewCmd` (984–1070)**: reword `Long`/examples (`task pipeline new` →
  `ty workflow new`; line 1064 run-hint `task pipeline "<goal>"` → `ty workflow "<goal>"`).
- **`pipelineEditCmd` (1073–1126)**: reword `Long`/examples (`task pipeline edit`
  → `ty workflow edit`; line 1122 run-hint likewise).

Leave the Go variable names (`pipelineCmd`, `pipelineNewCmd`, `pipelineEditCmd`)
as-is — they're internal. Do **not** rename flags (`--definition`/`-d`, `--list`,
`--no-execute`, `--body`, `--project`, `--json`, `--permission-mode`,
`--dangerous`): renaming flags would break scripts and isn't required.

**Why an alias, not a rename:** cobra resolves `Aliases` during command traversal,
so `ty pipeline`, `ty pipeline --list`, `ty pipeline new …`, and `ty pipeline edit …`
all continue to resolve to the renamed command and its subcommands unchanged.

### Stage 2 — MCP tool (`internal/mcp/server.go`)

- **Line 261**: tool `Name: "taskyou_create_pipeline"` → `"taskyou_create_workflow"`.
- **Line 262**: reword the `Description` to lead with "workflow" and reference
  `ty workflow new` instead of `ty pipeline new`.
- **Line 276**: param `definition` description `"Pipeline definition name …"` →
  `"Workflow definition name …"`.
- **Line 580 (dispatch switch)**: change
  `case "taskyou_create_pipeline":` → `case "taskyou_create_workflow", "taskyou_create_pipeline":`
  so old callers keep working (backward-compatible, unadvertised alias — mirrors
  the CLI alias).
- **Line 606**: error string `"Failed to create pipeline: %v"` → `"Failed to create workflow: %v"`.

### Stage 3 — Docs

- **`README.md`** (lines ~116–150): `ty pipeline …` → `ty workflow …` throughout
  the "Running a workflow" / "Custom workflows" blocks. Add a short note after the
  first example: `# 'ty pipeline' still works as an alias`.
- **`docs/mcp-tools-reference.md`** (heading line 131 + JSON example ~line 144):
  `taskyou_create_pipeline` → `taskyou_create_workflow`; optionally note the old
  name is accepted for compatibility.
- **`skills/taskyou/SKILL.md`** (lines 65–66, 252): `ty pipeline …` → `ty workflow …`
  and `taskyou_create_pipeline` → `taskyou_create_workflow` (mention alias).

---

## Ordered steps

1. Stage 1 — edit `cmd/task/main.go` (command rename + alias + help wording).
2. `make build` and smoke-test the CLI help + alias (see Verification).
3. Stage 2 — edit `internal/mcp/server.go` (tool name + dispatch alias + strings).
4. Stage 3 — edit the three docs files.
5. `gofmt`/`goimports` (`.golangci.yml` uses goimports w/ local-prefix
   `github.com/bborn/workflow`) and run `golangci-lint run` — **Lint must stay green**.
   Watch the gofmt smart-quote landmine: don't put adjacent quotes in doc comments.
6. `go build ./...` and targeted tests: `go test ./internal/mcp/... ./internal/pipeline/... ./cmd/...`
   (run in small batches — `go test ./...` all-at-once can OOM/exit 137 locally).
7. Manual verification (below), then commit and push the shared branch.

---

## Edge cases

- **Cobra alias + subcommands:** verify `ty pipeline new` and `ty pipeline edit`
  still resolve (they do — alias is matched before subcommand lookup). Include in
  the smoke test.
- **Shell completion:** completion is generated for the canonical name `workflow`;
  `ty pipeline …` still executes but may not tab-complete. Acceptable — note it,
  don't chase it.
- **MCP old-name callers:** any agent/script still sending `taskyou_create_pipeline`
  must keep working → guaranteed by the dual `case`. The old name is intentionally
  *not* re-advertised in `tools/list` to avoid a confusing duplicate.
- **Don't touch the `pipeline` tag / `pipeline/` branch prefix / fallback slug** —
  re-read the "NOT changed" list before editing `internal/pipeline/*`.
- **Behavior invariance:** the `workflow` command must produce byte-identical
  behavior to the old `pipeline` command — same branch name, same tags, same task
  rows. Only strings change.

---

## Manual verification (prove it works by hand)

Build first:

```bash
make build     # produces ./ty (and ./taskd)
```

### CLI — new name is primary, old name still works

```bash
./ty workflow --help        # EXPECT: Usage "ty workflow [goal]"; an "Aliases:
                            #   workflow, pipeline" line; help text says "workflow",
                            #   examples read "ty workflow …"; no stray "pipeline"
                            #   except the alias note.
./ty pipeline --help        # EXPECT: identical help (alias resolves to same command).
./ty workflow --list        # EXPECT: lists definitions (e.g. plan-code-review …).
./ty pipeline --list        # EXPECT: same list — alias works.
./ty workflow new "spike three approaches, build the best, then test" --print
                            # EXPECT: prints generated YAML (needs anthropic_api_key
                            #   set; otherwise an auth error is fine — proves routing).
./ty pipeline new "…" --print   # EXPECT: same behavior via alias.
./ty workflow edit --print      # EXPECT: prints the plan-code-review YAML.
./ty pipeline edit --print      # EXPECT: same via alias.
```

Confirm **no other** top-level command regressed:

```bash
./ty --help | grep -A1 -i workflow   # EXPECT: "workflow" listed among commands.
```

### CLI — behavior unchanged (branch/tag invariance)

In a throwaway git-worktree project registered with `ty`:

```bash
./ty workflow "tiny no-op goal" --project <proj> --no-execute --json | jq .
# EXPECT: JSON with "branch":"pipeline/<id>-tiny-no-op-goal" (prefix UNCHANGED),
#   one step per definition step, statuses present. Then confirm the created
#   tasks are tagged "pipeline" and the board folds them into a single ⇄ card:
./ty   # open TUI; EXPECT the workflow shows as one "⇄ … · N/M" lead card.
```

The `pipeline/` branch prefix and `pipeline` tag appearing here is **correct and
intended** — that's the data layer we deliberately did not rename.

### MCP — new tool advertised, old name still dispatches

Drive the internal MCP server over stdio (JSON-RPC). Use a real task ID from your
DB (`./ty list` / TUI) for `--task-id`:

```bash
# tools/list should advertise the new name and NOT the old one:
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  | ./ty mcp-server --task-id <id> 2>/dev/null | grep -o 'taskyou_create_[a-z]*'
# EXPECT: "taskyou_create_workflow" present; "taskyou_create_pipeline" absent
#   from the advertised list.
```

Backward-compat dispatch (old name still handled) is guaranteed by the
`case "taskyou_create_workflow", "taskyou_create_pipeline":` line; confirm with
`go test ./internal/mcp/...` (green) and, optionally, a `tools/call` using the old
name against a git-worktree project returns "Created … workflow on branch …"
rather than "method not found".

### Docs

```bash
grep -rn "ty pipeline\|taskyou_create_pipeline" README.md docs/mcp-tools-reference.md skills/taskyou/SKILL.md
# EXPECT: only intentional "alias still works" mentions remain; primary examples
#   now read "ty workflow" / "taskyou_create_workflow".
```

### Gate

```bash
golangci-lint run          # EXPECT: clean (Lint must stay green)
go build ./...             # EXPECT: builds
go test ./internal/mcp/... ./internal/pipeline/... ./cmd/...   # EXPECT: pass
```

---

## Follow-ups (optional, out of scope for this task)

- Rename the `internal/pipeline` package → `internal/workflow` and its exported
  identifiers, plus TUI internals (`FieldPipeline`, `pipelineOptions`,
  `pendingPipeline`). Purely internal; larger churn; deliberately deferred to keep
  this diff focused and behavior-safe.
- Optionally migrate the task tag `pipeline` → `workflow` **with a
  read-both-write-new compatibility shim + data migration** — a behavior change,
  so it needs its own task, not this one.
