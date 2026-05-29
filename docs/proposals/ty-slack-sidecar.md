# Proposal: `ty-slack` — manage TaskYou from Slack (and other chat channels)

> Status: exploration / recommendation
> Origin: Task #3723 — "Explore taskyou MCP sidecar extension for Slack"
> TL;DR: **Yes, this makes sense, and ~90% of it already exists.** Build a
> `ty-slack` sidecar in `extensions/`, modeled almost exactly on `ty-email`.
> We have **not** built this before — the only prior "Slack" work was a
> marketing message, not an integration.

---

## 1. What prompted this

The screenshot attached to the task shows a Slack thread in `#product` where
Bruno asks Claude (running in the cloud "Claude in Slack" integration) whether
it has access to the InfluenceKit TaskYou agents server. Claude answers **no**:

> "I don't have access to a TaskYou agents server in this session. The MCP
> servers available to me are: GitHub MCP... that's a separate integration
> running in your Slack workspace — not something I can access from this remote
> execution environment."

So the friction is concrete: **from Slack there is no way to see, create, or
unblock TaskYou tasks.** The question is whether a sidecar (like the ones we
already ship) should close that gap.

## 2. Did we already build this? (short answer: no)

| What I found | Relevance |
|---|---|
| PR #433 (closed) / commit `8b67f59` / archived task #1172 — *"Add tweet and Slack message for task queue improvements"* | **Not an integration.** This was marketing copy *about* TaskYou to post to Slack, not a way to drive TaskYou *from* Slack. |
| **PR #371 (merged)** + #385 — `ty-email` sidecar | **The direct precedent.** A full channel-based remote-control sidecar. A Slack version is a near-clone with a different adapter. |
| PR #402 (closed) — *"Review Cloudflare Code Mode MCP for TaskYou"* | Adjacent: explored exposing TaskYou's MCP server remotely. Relevant to "Pattern B" below. |
| `extensions/ty-web`, `extensions/ty-qmd` | Other sidecars showing the established `extensions/` pattern and the `ty serve` HTTP API + MCP-proxy patterns. |

There is **no existing Slack control surface** and **no open issue/PR** for one.
`grep -ri slack` across the repo returns only the marketing commit.

## 3. The two things "Slack integration" could mean

These are genuinely different and worth separating, because the screenshot
actually depicts the *harder* one.

### Pattern A — `ty-slack` sidecar (chat → TaskYou control channel) — RECOMMENDED

A small daemon you run next to TaskYou. You DM or @-mention a Slack bot; it
classifies intent with an LLM and drives the `ty` CLI. It posts replies in-thread
and pushes a message when a task needs input or finishes. **This is the literal
analog of `ty-email`** and the most direct reading of "manage their taskyou
remotely via Slack."

```
Slack user ──@mention/DM──▶ Slack (Events API / Socket Mode)
                                   │
                                   ▼
                            ┌────────────┐
                            │  ty-slack  │──▶ LLM API (classify intent)
                            │            │──▶ ty CLI / ty serve API (act)
                            │            │──▶ chat.postMessage (reply)
                            └────────────┘
                                   ▲
                  task.blocked / task.completed hook ──┘  (push notifications)
```

### Pattern B — hosted/remote MCP endpoint (what the screenshot shows)

The cloud "Claude in Slack" wants to call `taskyou_*` MCP tools directly, but
TaskYou's MCP server (`internal/mcp/server.go`) is **stdio-only** — it reads
JSON-RPC off `os.Stdin`, is spawned per-task by Claude Code via `.mcp.json`
(`ty mcp-server --task-id`), and is not network-reachable. To let a remote
Claude use it you'd need a **network-exposed, authenticated MCP server**
(HTTP/SSE transport) wrapping the existing `ty serve` REST API, plus hosting and
tunneling. This is the bigger lift PR #402 was circling.

**Recommendation:** ship Pattern A first. It delivers the user value ("manage
TaskYou from Slack") with infrastructure we already have, and it does not require
exposing anything to the public internet. Pattern B is a worthwhile follow-up but
is an infra/security project, not a sidecar.

## 4. Why Pattern A is cheap: almost all the pieces exist

`ty-slack` reuses TaskYou's existing seams. Concretely:

1. **The CLI bridge already exists.** `extensions/ty-email/internal/bridge`
   wraps `ty list/show/create/input/execute/output` with `--json`. It is not
   email-specific — it should be lifted into a shared package (e.g.
   `extensions/internal/bridge` or a `ty-core` module) and reused verbatim.

2. **The intent classifier already exists.** `ty-email/internal/classifier`
   uses a direct Claude API call (no permission prompts) to map a free-text
   message → `{create | input | execute | query}`. Slack messages classify the
   same way. Reuse it.

3. **The HTTP API already exists.** `ty serve` (`internal/web/server.go`)
   exposes the full surface — `GET/POST /api/tasks`, `/tasks/{id}/input`,
   `/execute`, `/logs`, `/stream` (SSE), `/board`, projects, types. `ty-slack`
   can call the CLI (like `ty-email`) *or* this API. The API is the better
   choice if `ty-slack` runs on a different host than the daemon.

4. **The push mechanism already exists.** `internal/events` emits
   `task.blocked`, `task.completed`, `task.failed`, etc., and `internal/hooks`
   runs a script named after each event with `TASK_EVENT` + task metadata in the
   environment. A `task.blocked` hook that POSTs to `ty-slack` (or straight to a
   Slack webhook) gives "ping me in Slack when a task needs input" for free — no
   core changes.

So the genuinely new code is small: a Slack **adapter** (Socket Mode or Events
API + `chat.postMessage`), thread↔task state tracking (mirror
`ty-email/internal/state`), and an `init` wizard for the bot token / signing
secret / allowed users.

## 5. Proposed shape

```
extensions/ty-slack/
  cmd/main.go              # cobra: init | serve | process | test | status | send
  internal/adapter/        # Slack Socket Mode + Events API; chat.postMessage
  internal/state/          # thread_ts ↔ task_id mapping (SQLite), dedupe
  config.example.yaml
  README.md
# shared (lifted out of ty-email so both reuse it):
extensions/internal/bridge/      # ty CLI / ty serve client
extensions/internal/classifier/  # LLM intent classification
```

Config mirrors `ty-email` (`~/.config/ty-slack/config.yaml`):

```yaml
slack:
  mode: socket            # socket (no public URL) | events (needs HTTPS endpoint)
  bot_token_cmd: echo $SLACK_BOT_TOKEN
  app_token_cmd: echo $SLACK_APP_TOKEN     # socket mode
  signing_secret_cmd: echo $SLACK_SIGNING_SECRET  # events mode
  notify_channel: "#taskyou"               # where blocked/done pings go
classifier:
  provider: claude
  api_key_cmd: echo $ANTHROPIC_API_KEY
taskyou:
  cli: ty                  # or: api: http://localhost:8080
  dangerous: false
security:
  allowed_users:           # Slack user IDs allowed to drive TaskYou
    - U012ABCDEF
```

### Interaction examples

- `@taskyou fix the checkout 500s and run it` → creates + executes a task,
  replies in-thread with the task id and a board link.
- Task hits a decision point → `ty-slack` posts *"Task #312 needs input: which
  migration strategy?"* to `#taskyou`. Reply in that thread → routed to
  `ty input 312 …`.
- `@taskyou what's happening with 312?` → posts recent `ty output 312`.

## 6. Security notes (same model as `ty-email`)

- **Allowlist by Slack user ID**, not just channel membership — only listed
  users can create/execute tasks.
- **Verify request authenticity** — Slack signing-secret check (Events API) or
  Socket Mode's authenticated socket; never act on unverified payloads.
- **No code execution from chat** — the LLM only *classifies*; the sidecar only
  calls `ty` subcommands. `--dangerous` stays opt-in via config, exactly as in
  `ty-email`.
- **Local secrets** — bot/app tokens resolved via `*_cmd` shell-outs, never sent
  to the LLM.

## 7. Recommendation

1. **Build `ty-slack` (Pattern A)** as a sidecar in `extensions/`, refactoring
   `ty-email`'s `bridge` and `classifier` into a shared package both consume.
   This is a focused, low-risk follow-up — most of the surface area is proven.
2. **Defer Pattern B** (network-exposed MCP for cloud Claude) to a separate
   spike; it's a hosting/auth project (pick up where PR #402 left off) and is not
   required to let people drive TaskYou from Slack today.
3. The "ping me when a task is blocked/done" half can ship **immediately** as a
   documented `task.blocked` / `task.completed` hook posting to a Slack incoming
   webhook — independent of the full sidecar.

## 8. Open questions for Bruno

- **Hosting:** is `ty-slack` a thing each user runs next to their daemon (like
  `ty-email`), or a single shared bot for the InfluenceKit workspace? Shared →
  multi-user routing + per-user allowlists become the main design work.
- **Socket Mode vs Events API:** Socket Mode needs no public URL (simplest for a
  laptop/agent server). Events API is better for an always-on shared bot but
  needs an HTTPS endpoint.
- **Do we also want Pattern B** (remote MCP) so the *existing* cloud Claude-in-
  Slack can call `taskyou_*` tools, or is a dedicated TaskYou bot enough?
