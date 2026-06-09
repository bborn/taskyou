# Pluggable Cloud Execution for TaskYou (Isolated Agent Sandboxes)

> Status: **Design / investigation** — no implementation in this branch.
> Author: strategic-advisor pass, task #4288.
> Scope: a plan to run each agent task in its own isolated, resource-capped sandbox
> behind a swappable provider interface, reviving the prior Fly Sprites work
> (PR #287 / #286) as the first concrete cloud backend.

---

## TL;DR — Recommendation

1. **Cap the local box first (this week).** Add a concurrency semaphore + per-task
   `cgroup`/`ulimit` caps to the existing tmux executor. This is the cheapest fix for
   "a runaway task melts the shared VM" and de-risks everything else. ($0/mo.)

2. **Introduce a new `sandbox.Provider` seam — orthogonal to `TaskExecutor`.** Today
   `TaskExecutor` fuses *which CLI runs* (claude/codex/gemini) with *where/how it runs*
   (local OS process in tmux). Cloud execution needs a **separate** abstraction:
   `Create sandbox → Push worktree → Exec agent (stream stdout) → Collect result/PR → Destroy`.
   The existing local path becomes `LocalTmuxProvider` with **zero behavior change**.

3. **Revive #287 as the first cloud backend — but re-architected to one ephemeral
   sandbox _per task_**, not the prior "one persistent sprite per project." The
   per-project model does **not** deliver per-task resource isolation (the whole point of
   this task). CI failed on a *trivial* lint error (one unused mutex field), not a design
   flaw — but the integration was only ~60% wired and needs the new seam.

4. **Keep `local-tmux` (now capped) as the default/fallback.** Cloud is opt-in per project
   via config; we flip the default later once the cloud path is proven.

5. **Cost at 30 heavy tasks/day (~225 task-hrs/mo, 4 vCPU / 8 GB):** Fly Machines ~**$40–63**,
   Daytona ~**$75**, Fly Sprites ~**$60–142** (usage-metered, bursty workloads land low),
   E2B ~**$224** ($150 base fee dominates). Lead with **Fly** (stays on existing infra,
   Sprites code exists, checkpoint/restore enables a warm pool); add **Daytona** as the
   second backend to prove the interface isn't Fly-locked and to cap cost.

---

## 1. Problem & Goals

**Problem.** Every task runs as a local process in a tmux pane on one shared 8-core / 15 GB
VM. There is **no per-task resource isolation** (confirmed: no cgroups, no ulimit, no
`GOMAXPROCS`, and the daemon spawns one goroutine per queued task with **no concurrency
limit** — `internal/executor/executor.go`). Concurrent heavy tasks (Rails boot + headless
Chromium + test suite) have repeatedly OOM-killed or starved the box, taking down sibling
tasks and the daemon itself.

**Goals.**

- **G1 — Blast-radius isolation:** a runaway task cannot starve or OOM its siblings.
- **G2 — Per-task resource caps:** e.g. 4 vCPU / 8 GB / disk / wall-clock timeout per task.
- **G3 — Horizontal throughput:** capacity scales with cloud sandboxes, not one VM's cores.
- **G4 — No vendor lock-in:** a clean provider interface with ≥2 real backends + local.
- **G5 — Preserve the local path:** `local-tmux` stays as a capped, zero-cost fallback and
  remains the best dev-loop experience (instant attach, no network).

**Non-goals (for the first cut).** Moving the daemon or SQLite DB to the cloud; multi-user
fan-out; replacing the TUI's live-attach experience for local tasks.

---

## 2. Part 1 — Prior Art Review (#287, #286) and Revival Scope

The Sprites work lives on branch `task/193-use-flyio-sprites-for-task-claude-execut`
(PR #287, **CLOSED**, `CONFLICTING` with main). Companion analysis is on
`task/663-review-sprites-functionality` (PR #286, `docs/sprites-exec-api.md`).

### 2.1 What exists (and works)

| Component | File (on `task/193`) | State |
| --- | --- | --- |
| Low-level Sprites client | `internal/sprites/sprites.go` | ✅ `CreateSprite`, `ListSprites`, `Destroy`, `CreateCheckpoint`, `RestoreCheckpoint`, `ExecCommand`, `ExecCommandStreaming` (real-time stdout via pipes → line callback) |
| Sprite runner | `internal/executor/executor_sprite.go` | ⚠️ `RunClaude` execs `claude --dangerously-skip-permissions -p <prompt>` on the sprite, streams lines → `db.AppendTaskLog`; per-task `context.CancelFunc` map for cancel/shutdown |
| CLI lifecycle | `cmd/task/sprite.go` | ✅ `sprite up/down/attach/destroy`; provisions VM, installs node + claude, writes `.claude/settings.json`, takes an `initial-setup` checkpoint |
| Design docs | `docs/sprites-design.md`, `docs/sprites-discussion.md`, `docs/sprites-exec-api.md` (on #286) | ✅ Substantive — architecture, cost model, exec-API gap analysis, open questions |

**Token handling today:** `ANTHROPIC_API_KEY` is read from the daemon's env and passed as
a plain `env VAR=...` prefix on each `sprite exec`. Fly auth itself rides the `sprite` CLI's
own login state.

### 2.2 What's unfinished or wrong

1. **Not actually wired into execution.** `SpriteRunner` is constructed but the daemon's
   `executeTask` still always runs the local tmux path. The cloud route is never taken.
   *(~60% complete: plumbing built, integration not connected.)*
2. **Wrong isolation model.** The design is **one persistent sprite per project**, with
   per-task isolation via git worktrees *inside* that one VM. **This does not satisfy G1/G2** —
   multiple heavy tasks in one sprite contend for the same 4–8 cores and RAM, recreating the
   exact "melt the box" failure one level up. **The revival must be one ephemeral sandbox per task.**
3. **Code/worktree never pushed.** Setup installs tooling but there is **no step that clones
   the repo or syncs the worktree** into the sandbox; `RunClaude` doesn't set a `workDir`.
4. **Git credentials unaddressed.** No mechanism to get an SSH key / GitHub token into the
   sandbox for clone + PR push (flagged as an open question in `sprites-discussion.md`).
5. **Crash recovery is a stub.** There is in-process task tracking (`activeTasks` map +
   graceful `Shutdown`), but exec-session IDs are **not persisted**, so a daemon restart
   orphans running sandboxes. `sprites-exec-api.md` documents the reattach pattern
   (`ListSessions` → `AttachSession`) but it is **not implemented**.
6. **No network policy, no port notifications, no tests.**

### 2.3 Why CI failed (and why it's not a design problem)

**Root cause:** `golangci-lint` `unused` — the `activeTasksMu sync.RWMutex` field in
`SpriteRunner` (`internal/executor/executor_sprite.go`) was declared but never locked/unlocked.
That's it. **A 1-minute fix** (use the lock or drop the field). The PR was closed for a red
check, not a flawed design.

> ⚠️ Lint caveat for the revival: CI now pins **golangci-lint v2.8.0** and lints the
> **PR-merged-into-main** result with `goimports` local-prefix `github.com/bborn/workflow`
> (`.golangci.yml`). Reproduce locally by merging `origin/main` first. (See repo CLAUDE.md /
> memory on the lint landmine.) The branch is also `CONFLICTING` with main — expect a rebase.

### 2.4 Revival scope (the punch list)

- [ ] Fix the lint (trivial) and rebase onto current main (Go 1.25, new `TaskExecutor` shape).
- [ ] Re-architect to **per-task ephemeral sandboxes** (create → run → destroy), optional warm pool.
- [ ] Implement **Push** (worktree → sandbox) and **git credential** injection.
- [ ] Replace the hook/tmux completion model with **stream-json result parsing** (see §3.4).
- [ ] Persist sandbox/session IDs for **reattach-on-restart**.
- [ ] Add network egress policy + tests.

---

## 3. Part 2 — The Pluggable Provider Design

### 3.1 Key insight: two orthogonal axes, currently fused

The current `TaskExecutor` interface (`internal/executor/task_executor.go:25-81`) mixes two
concerns:

```go
type TaskExecutor interface {
    Name() string
    Execute(ctx, task, workDir, prompt) ExecResult   // <- "where/how it runs" (local, tmux)
    Resume(...) ExecResult
    BuildCommand(task, sessionID, prompt) string      // <- returns a *shell command for a tmux window*
    IsAvailable() bool
    GetProcessID(taskID) int                           // <- assumes a local OS PID
    Kill / Suspend / IsSuspended                        // <- assumes SIGTERM/SIGTSTP on local process
    SupportsSessionResume / SupportsDangerousMode
    FindSessionID(workDir) / ResumeDangerous / ResumeSafe // <- reads local session files
}
```

`GetProcessID`, `Suspend` (SIGTSTP), `BuildCommand`→tmux, and `FindSessionID(workDir)` all
assume **a local OS process inside tmux**. This is the wrong seam to bolt cloud onto — a
`FlyProvider` would have to stub half of it.

**Separate the axes:**

- **Axis A — Agent CLI** ("*what* runs"): claude / codex / gemini / pi / opencode. Knows how
  to build argv + env and how to interpret the CLI's output. Location-agnostic.
- **Axis B — Sandbox / Execution Provider** ("*where/how* it runs"): local-tmux / fly-sprite /
  fly-machine / e2b / daytona. Knows create → push → exec(stream) → collect → destroy +
  resource caps.

Any Agent CLI × any Sandbox composes. `local-tmux` keeps today's exact behavior; cloud
providers share one streamed model.

### 3.2 The `sandbox.Provider` interface (sketch)

```go
// Package sandbox defines WHERE an agent runs. Orthogonal to TaskExecutor (WHICH CLI).
package sandbox

type Spec struct {
    TaskID   int64
    Worktree string            // local worktree path to materialize into the sandbox
    Branch   string
    RepoURL  string            // for clone-in-sandbox path
    Caps     ResourceCaps      // per-task vCPU / RAM / disk / timeout
    Env      map[string]string // non-secret (WORKTREE_TASK_ID, WORKTREE_PORT, ...)
    Secrets  map[string]string // ANTHROPIC_API_KEY, git token — injected out-of-band
    Egress   *NetworkPolicy    // optional allowlist; nil = provider default
}

type ResourceCaps struct {
    VCPU     int
    MemoryMB int
    DiskGB   int
    Timeout  time.Duration
}

type Handle struct { ID, Provider string } // provider-native sandbox id, for reattach

type ExecRequest struct {
    Argv []string          // ["claude","--print","--output-format","stream-json", ...]
    Env  map[string]string
    Cwd  string
}

type Event struct { Stream, Line string } // "stdout" | "stderr"

type Result struct {
    ExitCode  int
    Branch    string
    DiffStat  string
    PRURL     string // if the agent opened one
    Artifacts []string
}

type Provider interface {
    Name() string
    Caps() Capabilities                                             // max caps, feature support

    Create(ctx context.Context, s Spec) (*Handle, error)            // provision, capped
    Push(ctx context.Context, h *Handle, s Spec) error              // worktree -> sandbox
    Exec(ctx context.Context, h *Handle, r ExecRequest,
         onEvent func(Event)) (exitCode int, err error)             // stream until exit
    Collect(ctx context.Context, h *Handle, s Spec) (*Result, error)// diff/branch/PR back
    Destroy(ctx context.Context, h *Handle) error                   // or checkpoint+sleep

    Reattach(ctx context.Context, id string,
             onEvent func(Event)) (exitCode int, err error)         // after daemon restart
}
```

Notes:
- **Streaming, not polling.** `Exec` streams `Event`s — the daemon writes each line via the
  existing `db.AppendTaskLog` + `broadcast()` path, so the TUI's live log keeps working
  unchanged (it already subscribes to `db.TaskLog` channels).
- **`Reattach`** is first-class because crash recovery is the #1 prior-art gap. The daemon
  persists `Handle.ID` per running task (new column, §5) and reattaches on startup.
- **`Caps()`** lets the scheduler reject specs a provider can't honor (e.g. E2B's 8 GB ceiling).

### 3.3 Composition & the integration seam

The daemon already consumes a clean, location-agnostic result type:

```go
// internal/executor/task_executor.go
type ExecResult struct { Success, NeedsInput, Interrupted bool; Message string }
```

`executeTask` calls `taskExecutor.Execute(ctx, task, workDir, prompt) ExecResult` and reacts
to it (Success → backlog for review; NeedsInput → blocked; etc.). **That is the seam.** We
introduce a single `sandboxExecutor` that implements the daemon-facing contract and delegates:

```
executeTask(task)
  └─ pick AgentCLI (task.Executor)         // claude/codex/... — reuse existing command logic
  └─ pick Provider (project/task config)   // local-tmux (default) | fly | daytona | e2b
  └─ if local-tmux:  run today's exact runClaude()/tmux path  (NO CHANGE)
     else (cloud):
        h := provider.Create(ctx, spec(caps))
        provider.Push(ctx, h, spec)         // worktree + secrets
        argv := agentCLI.Argv(task, prompt) // refactor BuildCommand -> argv + env
        code := provider.Exec(ctx, h, {argv}, onEvent)   // onEvent -> AppendTaskLog + parse
        res  := provider.Collect(ctx, h, spec)           // branch, diff, PR
        provider.Destroy(ctx, h)            // or checkpoint+sleep into warm pool
        return mapToExecResult(code, parsedSignals, res)
```

This is **additive**: `local-tmux` is the existing code wrapped as `LocalTmuxProvider`; we do
**not** rip out tmux. Only the cloud branch is new. The one refactor on the Agent-CLI side is
extracting argv/env from `BuildCommand` (which today returns a tmux shell string) into a
location-neutral `Argv(task, prompt) ([]string, env)` — local-tmux re-joins it into a shell
string, cloud passes it to `Exec`.

### 3.4 Status detection: hooks/tmux (local) → stream-json (cloud)

This is the subtle part and the biggest behavioral change.

- **Local model (today):** completion/questions are detected by *hooks* — the `claude` process
  shells out to `task claude-hook` which updates DB status — plus `pollTmuxSession` watching
  the DB + tmux window. There is **no stdout parsing**; tmux owns the terminal.
- **Cloud model:** there is no tmux pane and the subprocess-hook callback can't reach the local
  daemon directly. Drive the agent with `claude -p --output-format stream-json` and **parse the
  event stream** in `onEvent` to detect: assistant text, tool calls, the terminal `result`
  event (success/`needs_input`), and errors → map to `ExecResult`.
- **MCP / callback parity:** TaskYou injects its own MCP tools (`taskyou_complete`,
  `taskyou_needs_input`, …) so the agent can self-report. In a sandbox the agent must reach the
  daemon's MCP/HTTP endpoint over an **authenticated reverse tunnel** (provider port-forward or
  an outbound WebSocket the daemon brokers). **Decision needed (§6):** rely on stream-json
  result events only, or also tunnel the MCP callbacks. Recommendation: **stream-json as the
  source of truth for completion**, MCP tunnel as an enhancement for richer mid-run signals.
- **Simplification win:** the `worktree_guard.go` write-guard (which protects the *shared host*
  from writes outside the worktree) becomes largely moot in a per-task sandbox — the sandbox
  *is* the isolation boundary and is destroyed after. One less thing to enforce remotely.

### 3.5 Resource caps & scheduling

- Caps live in config, defaulting to **4 vCPU / 8 GB / 20 GB disk / 45 min timeout**, overridable
  per project (`.taskyou.yml`) and per task. Each provider maps `ResourceCaps` to its native
  primitive (Fly machine preset, E2B/Daytona vCPU+RAM request, or local `cgroup`/`ulimit`).
- The daemon's currently-unbounded task loop gains a **concurrency semaphore**. For local-tmux
  the limit is small (host cores ÷ per-task cores). For cloud it's a budget/quota limit, not a
  host limit (that's the throughput win, G3).

---

## 4. Part 3 — Cost + Fit Comparison

Workload modeled: **~4 vCPU / 8 GB**, ~15 min average, **30 tasks/day ≈ 225 task-hrs/mo**.
Daemon + DB stay local; only agent execution is remote. Rates are official (provider pricing
pages, 2026-06-09); `$/mo` is computed; util-dependent figures flagged.

| Provider | ~$/task-hr (4vCPU/8GB) | Est. $/mo @225 hr | Boot latency | Exec + FS API | Notable limits | Self-host |
| --- | --- | --- | --- | --- | --- | --- |
| **Fly Machines** | $0.18 (AMS)–$0.28 (BOM) | **~$40–63**, no base fee | sub-1s from stopped | `machine exec` + SSH/sftp + REST | You own lifecycle/image; not a "sandbox" SDK | No (own the image) |
| **Fly Sprites** | $0.63 full util; ~$0.27–0.49 realistic (bills actual CPU/RAM) | **~$60–142** (util-dependent), no base fee | 1–2s create, <1s cold; 300ms checkpoint | CLI/REST/Go+JS SDK, exec + persistent ext4 + **checkpoint/restore** | Newest (public ~Jan 2026); runs on Fly Machines | No |
| **Daytona** | $0.331 usage | **~$74.52**, no base fee, first $200 free | **sub-90ms** (cached) | Python/TS/**Ruby**/Go SDK; exec/files/git/ports/fork | Container (not microVM) isolation; tiered limits | **Yes — open source** |
| **E2B** | $0.331 usage | **~$224.50** ($74.52 + **$150 Pro base**) | ~150–200ms; resume 5–30ms | Mature Python/TS; streaming exec + files; 24h sessions | **8 GB is the RAM ceiling**; 4vCPU/8GB needs Pro | Enterprise only |
| **Modal** (bonus) | ~$0.48–0.60 (incl. 1.25× US) | **~$107–134**, Starter $0 + $30 credit | ~1s (1–5s) | Python-first `sandbox.exec` + Volumes + snapshots | Sandbox CPU is 3× base rate; regional multiplier | No |

**Reconciliation with prior INFRA.md framing** (Fly Machines ~$70–90, E2B ~$290, cgroups
interim): directionally consistent. My figures land Fly Machines a bit lower (region/util) and
E2B's monthly is dominated by the **$150/mo base fee** (the ~$290 figure implies ~50 tasks/day
or longer tasks). At **50 tasks/day (~375 hr/mo)**: Fly Machines ~$67–104, Daytona ~$124,
Sprites ~$100–236, E2B ~$274 — Fly stays cheapest, E2B's fixed fee matters less at scale.

**Fit read:**
- **Cheapest raw:** Fly Machines — but you build orchestration. Since the daemon already builds
  the agent command, `machine exec`/SSH covers it without an SDK.
- **Cheapest turnkey SDK + escape hatch:** Daytona — sub-90ms boot, Ruby/Go SDK, **open-source
  self-host** if you ever want to leave managed cloud.
- **E2B:** most polished microVM sandbox, but the $150/mo base ≈ 3× Daytona here, and **8 GB is
  its ceiling** — no headroom for Rails + Chromium + tests. ❌ for the heavy workload.
- **Sprites:** wildcard — usage-metered on *actual* CPU/RAM, so a bursty, often-idle agent can
  land well under full-util cost; checkpoint/restore is ideal for a warm pool. Newest, least
  battle-tested.

---

## 5. Part 4 — Recommended Path (phased)

> Honor the directive — **revive #287 (Fly) as the first cloud backend** — while fixing its
> isolation model and keeping the door open (G4) and local fallback (G5).

**Phase 0 — Cap the local box (days, $0).** Concurrency semaphore in the daemon loop +
per-task `cgroup v2`/`ulimit` (memory.max, cpu.max, pids.max) wrapping the tmux command on
Linux. Immediately addresses G1/G2 for the status quo and is the `local-tmux` fallback's
permanent safety net. *Independent of all cloud work — ship it first.*

**Phase 1 — Introduce the `sandbox.Provider` seam (1–2 wks).** Define the interface (§3.2),
implement `LocalTmuxProvider` as a thin wrapper over today's `runClaude`/tmux path (behavior
unchanged, fully regression-tested), refactor Agent-CLI `BuildCommand → Argv`, add the
`sandboxExecutor` shim at the `ExecResult` seam, add caps config + per-task sandbox-handle DB
column. **No cloud yet** — this is the de-risking refactor.

**Phase 2 — Fly backend, per-task (2–4 wks).** Port `internal/sprites` + `executor_sprite.go`
behind `FlyProvider`, re-architected to **one ephemeral sandbox per task**. Implement
`Create/Push/Exec(stream-json)/Collect/Destroy/Reattach`, git-credential + token injection,
network egress allowlist, and crash-recovery reattach. Lead with **Sprites** (managed,
checkpoint/restore → warm pool, usage billing); keep raw **Fly Machines** behind the same
`FlyProvider` as the cost-optimization escape hatch (Sprites runs on Machines anyway).
Opt-in per project; `local-tmux` stays default.

**Phase 3 — Prove pluggability with a 2nd vendor (1–2 wks).** Add `DaytonaProvider`
(cheapest turnkey SDK, open-source self-host). This validates G4 and gives a cost ceiling /
non-Fly contingency. (E2B explicitly deprioritized: $150 base + 8 GB ceiling.)

**Phase 4 — Flip defaults where it pays.** Once cloud is proven, default heavy projects to
cloud; keep `local-tmux` (capped) for light tasks and the dev inner loop.

---

## 6. Part 5 — Migration Steps & Risks

| Area | Risk | Mitigation |
| --- | --- | --- |
| **Token mgmt** | `ANTHROPIC_API_KEY` passed as plain env on each exec; leaks via process listing / logs in the sandbox | Inject as a provider **secret** (Fly secrets / E2B-Daytona env at create), never on the argv; never echo to `task_logs`; prefer short-lived/scoped keys; redact in stream parser |
| **Worktree / code transfer** | Prior art never pushed code; large worktrees (node_modules, build artifacts) are slow to ship | `Push` = `git clone --depth=1 <branch>` *inside* the sandbox (fast, needs creds) for the base, then `rsync`/tar only the worktree's uncommitted diff; cache base image per repo; measure |
| **Secrets (git)** | No mechanism to get SSH key / GH token into sandbox for clone + PR push | Inject a **short-lived, repo-scoped GitHub token** (or fine-grained PAT) as a provider secret; use HTTPS clone + `GIT_ASKPASS`; never bake into the image |
| **Network policy** | Sandbox with full egress = exfil / supply-chain surface | Default-deny egress allowlist (Anthropic API, github.com, package registries the project needs); Fly Sprites network policy / Daytona network tiers; make it config-driven per project |
| **Latency** | Cold create + clone + tool install adds seconds–minutes vs instant local tmux | **Warm pool** of pre-provisioned sandboxes (Sprites checkpoint/restore ≈ <1s; Daytona sub-90ms); pre-baked image with node + claude + common toolchain; lazy clone |
| **Status / completion** | No tmux + hooks can't call back; risk of "zombie" tasks the daemon thinks are running | Parse `--output-format stream-json` result events as source of truth (§3.4); wall-clock timeout in `Caps`; optional MCP reverse-tunnel for mid-run signals |
| **Crash recovery / reconnection** | Daemon restart orphans running cloud sandboxes (the #287 stub) | Persist `sandbox_handle`/`session_id` per task; on daemon start, `Reattach` and resume streaming; reconcile sandboxes with no live task (destroy) |
| **PR collection** | Cloud agent's branch/commits live in the sandbox, not the local worktree | `Collect` returns branch + diffstat + PR URL; agent pushes branch from sandbox using injected creds; daemon records PR via existing `internal/github` path |
| **Cost runaway** | Forgotten/looping sandboxes bill indefinitely | Hard wall-clock timeout + idle auto-sleep/destroy; daemon-side budget guard + a reaper that destroys sandboxes older than N hours; per-day spend alert |
| **Provider lock-in** | Coupling to Fly-isms (checkpoints, sprite CLI) | Keep Fly-specific features behind `Provider.Caps()`; the daemon only depends on the interface; Daytona backend in Phase 3 forces the abstraction honest |
| **Local UX regression** | TUI "attach to executor pane" / executor dock assume tmux | Cloud tasks: TUI shows the **streamed log** (already DB-backed) + a "connect" action that SSH/exec-shells into the sandbox; local tasks keep tmux attach unchanged |
| **CI to revive** | #287 closed on lint; branch conflicts with main | Fix the unused-mutex lint; rebase onto Go-1.25 main; reproduce CI with pinned golangci-lint **v2.8.0** against the **merge** result (see §2.3) |

---

## 7. Open Questions / Decisions Needed

1. **Completion signal:** stream-json result events only, or also tunnel MCP callbacks back to
   the daemon? *(Recommend: stream-json as truth, MCP tunnel as enhancement.)*
2. **Sandbox lifetime:** pure per-task ephemeral, or per-task with a **warm pool** from day one?
   *(Recommend: ephemeral first, add warm pool in Phase 2 once latency is measured.)*
3. **Fly target:** Sprites (managed, checkpoint/restore) vs raw Machines (cheaper) as the
   primary — or both behind `FlyProvider` selectable by config? *(Recommend: both; lead Sprites.)*
4. **Code transfer:** clone-in-sandbox (needs creds, fast on fat repos) vs push-worktree
   (no creds for base, slow on fat repos)? *(Recommend: clone base + push diff.)*
5. **Default flip:** which project attributes (size, heaviness) auto-route to cloud vs local?

---

## 8. Concrete Next Steps

1. **(Phase 0, this week)** Add daemon concurrency semaphore + Linux `cgroup v2`/`ulimit`
   caps to the tmux executor. Ship independently — it fixes the immediate "melted box" pain.
2. **Spike the interface** (`internal/sandbox/provider.go`) from §3.2 and wire
   `LocalTmuxProvider` + the `sandboxExecutor` shim with **zero behavior change**; land behind
   a no-op default so it's safe to merge.
3. **Refactor** Agent-CLI `BuildCommand → Argv(task, prompt) ([]string, env)`; keep the tmux
   shell-string join in `LocalTmuxProvider`.
4. **Resurrect #287:** new branch off current main, cherry-pick `internal/sprites` +
   `executor_sprite.go`, fix the lint, rebase; do **not** restore the per-project model.
5. **Build `FlyProvider`** per-task: `Create/Push/Exec(stream-json)/Collect/Destroy/Reattach`,
   secret + git-cred injection, egress allowlist; add the `sandbox_handle` DB column for reattach.
6. **Add the warm pool** (Sprites checkpoint/restore) once a cold task's create+clone latency
   is measured against the local baseline.
7. **Add `DaytonaProvider`** (Phase 3) to validate the interface isn't Fly-locked.
8. **Cost guardrails:** wall-clock timeout, idle reaper, per-day spend alert, before any
   default flip.

---

### Appendix — Key source references

- Current interface & seam: `internal/executor/task_executor.go:23-81` (`TaskExecutor`,
  `ExecResult`), `internal/executor/executor.go` (`Executor`, unbounded task loop).
- Local execution path: `runClaude` / `pollTmuxSession` / `ensureTmuxDaemon` (hook-driven
  status, tmux windows), `internal/executor/worktree_guard.go`, `internal/db` (`tasks`,
  `task_logs`).
- Prior art (branch `task/193-use-flyio-sprites-for-task-claude-execut`):
  `internal/sprites/sprites.go`, `internal/executor/executor_sprite.go`, `cmd/task/sprite.go`,
  `docs/sprites-design.md`, `docs/sprites-discussion.md`; `docs/sprites-exec-api.md` on
  `task/663-review-sprites-functionality` (PR #286).
- Pricing sources (2026-06-09): fly.io/docs/about/pricing, sprites.dev, e2b.dev/pricing,
  daytona.io/pricing, modal.com/pricing.
