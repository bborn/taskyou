# Inter‑Agent Collaboration — Cotal evaluation & design proposal

> **Status:** Proposal / RFC — seeking a direction decision. No runtime behavior changes
> ship in this PR; it adds this document only.
>
> **Question this answers:** *"Can we leverage [Cotal](https://github.com/Cotal-AI/Cotal)
> in a smart way to enable better inter‑agent collaboration?"*
>
> **TL;DR:** Yes — but adopt Cotal's *model*, not its *runtime*. Build native
> MCP‑first messaging + presence on the infrastructure taskyou already has
> (SQLite + the MCP server + the events bus), keep a Cotal bridge as an optional
> adapter, and learn the specific lessons from why PR #357 (`relay`) was closed.

---

## 1. Why now

Two things prompted this:

1. We have coordination primitives already — **task dependencies** (`blocked`/blocker
   graph, `internal/db/dependencies.go`) — but they're a human‑driven graph you wire up
   *before* work starts. They're not used much because they're not *organic*: agents
   discover the need to coordinate *while* working, not in advance, and a static
   dependency edge can't express "hey @reviewer, can you look at this?".

2. We already tried live inter‑task communication once — **PR #357 `feat(relay): add
   agent-to-agent messaging system`** — and it was **closed** (2026‑03‑11), not merged.
   Understanding *why* is the most important input to this design (§4).

Cotal showing up is a good forcing function to get the model right this time.

## 2. What Cotal actually is

[Cotal](https://github.com/Cotal-AI/Cotal) (Apache‑2.0) is an **open standard for agent
coordination** — a coordination *layer* that sits alongside MCP and A2A rather than
replacing them. Core ideas worth stealing:

- **Three addressing modes**, all built on a shared **presence** substrate:
  - **Multicast** — publish to named channels (`#general`, `#review`); all subscribers
    receive.
  - **Unicast** — direct peer‑to‑peer with **durable delivery**: a message to a busy or
    offline agent waits on the stream until read.
  - **Anycast** — role‑based: a message to "the reviewer role" is claimed by exactly one
    free reviewer instance.
- **Presence + AgentCard** — every agent publishes live state (`idle`/`waiting`/
  `working`/`offline`) and an A2A `AgentCard`. Anyone in the space can read the roster.
- **A2A‑compatible wire shapes** — reuses A2A `AgentCard` identity and `Message`/`Part`
  structures.
- **`cotal_spawn`** — an agent can pull in a teammate on demand ("spin up a reviewer").

These four concepts are genuinely good and map cleanly onto what taskyou needs (§5).

### What Cotal's *runtime* costs

The implementation, however, is a poor fit to drop into taskyou as a dependency:

| Cotal runtime fact | Friction for taskyou |
|---|---|
| Written in **TypeScript** (~89%); installed via `npx cotal-ai setup --full` | taskyou is a **single static, CGO‑free Go binary**. Requiring Node + npx on every host is a large install‑footprint regression. |
| Bundles / requires a running **`nats-server`** process (NATS + JetStream) | A second long‑lived daemon next to our executor + SQLite. We'd run two coordination planes. |
| **Self‑hosted infra**, JWT auth, clustering, KV buckets w/ TTL | Real operational surface for a tool whose pitch is "local‑first, zero‑dep, just a binary". |
| **Early‑stage** brand‑new standard repo | Betting our collaboration UX on a nascent external project + its Claude Code connector is schedule/maintenance risk. |

**Conclusion:** A hard dependency on Cotal's runtime contradicts taskyou's core value
prop (one binary, no external services). But the *model* is exactly what we want.

## 3. What taskyou already has (the substrate is already here)

We do **not** need NATS to get durable, presence‑aware messaging — we already have the
three things NATS would provide:

- **A durable store** — SQLite (`internal/db`). Durable delivery = a `messages` table
  with a per‑recipient read cursor. JetStream's "wait on the stream until read" is just
  a row with `status='unread'`.
- **A tool surface agents already trust** — the MCP server (`internal/mcp/server.go`),
  already injected into every running agent via `ty mcp-server --task-id <ID>` and
  `.mcp.json`. Agents already call `taskyou_complete`, `taskyou_needs_input`,
  `taskyou_create_task`, `taskyou_list_tasks`, `taskyou_show_task`. Messaging tools
  belong right here.
- **A presence signal for free** — task **status** (`backlog`/`queued`/`processing`/
  `blocked`/`done`, `internal/db/tasks.go`) *is* presence. `processing` = `working`,
  `blocked` = `waiting`, terminal = `offline`. We do not need agents to manually
  register/unregister (that manual lifecycle was a source of bugs in #357 — §4).
- **An events bus** — `internal/events/events.go` already emits `TaskBlocked`,
  `TaskCompleted`, etc. with env‑var payloads to hook scripts. A new message is just
  another event.

So the build is *additive and small*, not a new architecture.

## 4. Lessons from the closed PR #357 (`relay`)

PR #357 built a homegrown `internal/relay` package: a `relay_messages` table, in‑memory
agent registry, `*` broadcast, and CLI verbs `ty relay send/read/list`. **+1,144 lines,
closed unmerged.** The post‑review comment thread tells us exactly what to avoid:

| #357 decision | Why it hurt | What we do instead |
|---|---|---|
| **Delivery via idle‑detection** — messages injected into a running Claude session after "~1.5s no output" | Fragile, racy, depends on scraping executor output; "is the agent idle?" is a guess. This is the same hack Cotal solves with durable streams. | **Pull‑based MCP delivery.** The agent calls `taskyou_inbox` when *it* decides to. Durable rows mean nothing is lost; no injection, no idle‑guessing. Optionally *nudge* via the existing event/notification path. |
| **Manual agent register on task start / unregister on cleanup** | Caused a **memory‑leak bug** (agents not unregistered) flagged in review. Lifecycle duplicated task lifecycle. | **No separate registry.** Presence is *derived* from task status. Zero lifecycle code to leak. |
| **Agent names derived from task title** | Collisions; `relay.CleanAgentName()` band‑aid. | **Address by task ID** (stable, unique) for unicast; **named channels** for multicast; **task `type`/tag** for anycast roles. |
| **Human‑driven `ty relay send` CLI** | The *human* drives messaging → not "organic". Agents were passive recipients. | **Agents are first‑class senders** via MCP tools. CLI/TUI is for *observing* the conversation, not driving it. |

The messaging *idea* in #357 was right. The *delivery mechanism* and *identity/lifecycle*
were what made it inorganic and unmergeable. This proposal keeps the idea and replaces
those two things.

## 5. Proposed design — Cotal's model on taskyou's substrate

Map Cotal's three addressing modes onto taskyou primitives, expose them as MCP tools,
back them with SQLite + events.

### 5.1 Data model

One new table (durable inbox); presence is a *view*, not a table.

```sql
CREATE TABLE IF NOT EXISTS agent_messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    from_task   INTEGER REFERENCES tasks(id) ON DELETE SET NULL, -- NULL = human/orchestrator
    to_task     INTEGER REFERENCES tasks(id) ON DELETE CASCADE,  -- unicast target (NULL for channel)
    channel     TEXT,            -- multicast channel, e.g. 'review' (NULL for unicast)
    role        TEXT,            -- anycast role, e.g. task type 'review' (NULL otherwise)
    project     TEXT NOT NULL,   -- scope; mirrors MCP project isolation
    body        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'unread', -- unread|read|claimed
    claimed_by  INTEGER REFERENCES tasks(id),   -- anycast: which task took it
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    read_at     DATETIME
);
CREATE INDEX idx_agent_messages_to     ON agent_messages(to_task, status);
CREATE INDEX idx_agent_messages_chan   ON agent_messages(project, channel, status);
CREATE INDEX idx_agent_messages_role   ON agent_messages(project, role, status);
```

- **Unicast** = `to_task` set. Durable: the row sits `unread` until the target reads it,
  even if that task is `blocked`/not yet picked up. (This is Cotal's "waits on the stream
  until read" — for free.)
- **Multicast** = `channel` set; every reader in the project sees it (read cursor is
  per‑reader; v1 can keep it simple — see §8).
- **Anycast** = `role` set; the first eligible reader to `claim` it wins (atomic
  `UPDATE ... SET status='claimed', claimed_by=? WHERE id=? AND status='unread'`).
- **Presence/roster** = `SELECT id,title,type,status FROM tasks WHERE project=? AND
  status IN ('processing','blocked',...)` — no new state. `processing`→working,
  `blocked`→waiting.

### 5.2 MCP tools (the organic surface)

Add to `internal/mcp/server.go`, project‑scoped exactly like the existing tools:

| Tool | Params | Behavior |
|---|---|---|
| `taskyou_send_message` | `to_task?`, `channel?`, `role?`, `body` (one of to/channel/role required) | Insert a row. Unicast / multicast / anycast chosen by which field is set. Emits a `MessageSent` event. |
| `taskyou_inbox` | `mark_read?` (default true) | Return this task's unread unicast + subscribed‑channel + matching‑role messages. Pull‑based — no injection. |
| `taskyou_roster` | — | Live presence: who's in this project, their task `type` (≈ AgentCard role) and status. Answers "who can review this?" organically. |
| `taskyou_claim` | `message_id` | Anycast claim (atomic). Returns the message if won, else "already claimed". |

This is the whole point: an agent mid‑task can say *"@roster — anyone own the auth
schema? I'm about to change it"* or *"send to role=review: PR up at <url>"* — and a
**reviewer‑type task** picks it up. Coordination *emerges from the work* instead of being
pre‑wired as a dependency edge.

### 5.3 Delivery & nudging (no idle‑hacks)

- **Primary: pull.** Agents call `taskyou_inbox`. We add a one‑line nudge to the agent's
  system/skill prompt: *"If you're blocked or finishing, check `taskyou_inbox` and your
  `taskyou_roster` before stalling."* Robust, deterministic, no output scraping.
- **Optional push (later):** reuse `internal/events` to fire a `MessageSent` hook so the
  TUI surfaces a 📨 badge (the one good UI bit from #357, `internal/ui/detail.go`) and a
  desktop/PushNotification can ping. Still pull for the *content*.

### 5.4 Bridging unblocking back to dependencies

Make the *existing* dependency primitive feel organic by letting messages drive it:
when an agent sends `role=review`/`to_task` "you're unblocked", optionally call the
existing `ProcessCompletedBlocker` / auto‑queue path (`internal/db/dependencies.go`).
The dependency graph stays as the *mechanism*; messaging becomes the *interface*.

### 5.5 Where Cotal fits — optional bridge, not a dependency

Ship an **opt‑in adapter** (`internal/relay/cotal_bridge.go`, off by default, enabled by
config only when a `nats://` URL + creds are present) that mirrors `agent_messages` ⇄
Cotal subjects and maps our roster ⇄ Cotal presence/AgentCards. Benefits:

- Users *already* running a Cotal mesh (or wanting cross‑host / non‑taskyou agents) get
  interop and `cotal_spawn`‑style remote teammates.
- We stay **A2A‑shaped** in our message struct (`from`, `to`, `parts`) so the bridge is
  a thin mapping, not a translation layer.
- **Zero cost** for the 99% who just want one binary: no Node, no NATS, nothing running.

This is the "smart" use of Cotal: interoperate with the standard, don't take on its
runtime.

## 6. Recommendation

**Adopt Cotal's model; bridge to its runtime optionally.** Concretely:

1. Build §5.1–§5.4 natively (SQLite + MCP + events). Small, in‑process, CGO‑free, on‑brand.
2. Keep message structs A2A‑shaped for forward‑compat.
3. Add the Cotal bridge (§5.5) as a later, opt‑in phase once the native UX proves out.

**Reject:** making `npx cotal-ai` / a running `nats-server` a hard dependency of `ty`.

## 7. Phasing

- **Phase 0 (this PR):** this proposal — get a direction decision.
- **Phase 1:** `agent_messages` table + `taskyou_send_message` / `taskyou_inbox` /
  `taskyou_roster` MCP tools (unicast + roster). Prompt nudge. TUI 📨 badge. Tests.
- **Phase 2:** multicast channels + anycast `role`/`taskyou_claim`; wire messaging →
  dependency auto‑unblock.
- **Phase 3:** optional Cotal bridge (A2A subject mapping, presence sync), config‑gated.

Each phase is independently shippable and reversible.

## 8. Open questions (for the direction decision)

1. **Scope of presence** — project‑scoped only (matches current MCP isolation), or
   cross‑project roster for an orchestrator view?
2. **Multicast read cursors** — per‑reader cursor table in v1, or simplest "deliver to
   all currently‑live readers" and defer durability for channels?
3. **Anycast eligibility** — is "role" just task `type` (`review`, etc.), or a new
   explicit capability field on tasks / AgentCard?
4. **Push appetite** — is the prompt‑nudge pull model enough for v1, or do we want the
   event‑driven TUI badge in Phase 1?
5. **Do we want `cotal_spawn`‑equivalent** (agent spawns a teammate task) now? We already
   have `taskyou_create_task` — anycast + auto‑queue gets us most of the way without a
   new primitive.

## 9. Files this would touch (for reference, not in this PR)

- `internal/db/sqlite.go` — `agent_messages` migration.
- `internal/db/messages.go` *(new)* — store + atomic claim + roster query.
- `internal/mcp/server.go` — register the four tools.
- `internal/events/events.go` — `MessageSent` event.
- `internal/ui/detail.go` — 📨 inbox badge (reuse #357's UI touch).
- `internal/relay/cotal_bridge.go` *(new, Phase 3, opt‑in)* — Cotal/NATS adapter.
- `skills/taskyou/…` + agent prompt — the "check your inbox/roster" nudge.

---

*Authored as part of task #4521 (Cotal integration). Grounded in: closed PR #357,
`internal/db/dependencies.go`, `internal/mcp/server.go`, `internal/events/events.go`,
`internal/executor/`, and the Cotal README.*
