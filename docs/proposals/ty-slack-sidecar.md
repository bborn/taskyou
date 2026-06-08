# Proposal: manage TaskYou from Slack (and other chat channels)

> Status: exploration / recommendation
> Origin: Task #3723 — "Explore taskyou MCP sidecar extension for Slack"
> TL;DR: **Yes, this makes sense — and we have already built the exact pattern
> twice** (the `ty-email` sidecar here, and the **Linear poller module** in
> [`taskyou-os`](https://github.com/taskyou/taskyou-os)). A Slack integration is
> a third instance of a well-trodden path. Recommended home: a **`taskyou-os`
> module** (`modules/slack/`, modeled on `modules/linear/`), with `ty-email` as
> the fallback template if we want a standalone Go sidecar instead.

---

## 1. What prompted this

The screenshot attached to the task shows a Slack thread in `#product` where
Bruno asks Claude (the cloud "Claude in Slack" integration) whether it can reach
the InfluenceKit TaskYou agents server. Claude answers **no**:

> "I don't have access to a TaskYou agents server in this session… that's a
> separate integration running in your Slack workspace — not something I can
> access from this remote execution environment."

The friction is concrete: **from Slack there's no way to see, create, or unblock
TaskYou tasks.** The question is whether a sidecar/integration (like the ones we
already ship) should close that gap.

## 2. Did we already build this? (No Slack — but we built the pattern twice)

No Slack control surface exists. But the relevant prior art is bigger than I
first thought — once you include `taskyou-os`, we have **two** working
external-channel → TaskYou bridges:

| What exists | Where | Relevance |
|---|---|---|
| **`ty-email` sidecar** (PR #371 + #385) | `extensions/ty-email` | Channel-control sidecar: email in → LLM classify intent → `ty` CLI → reply/notify. The architectural twin of a Slack bot. |
| **Linear poller module** (`linear-poll.mjs`) | `taskyou-os/modules/linear` | **The closest precedent.** Polls Linear for `@agent` comments → `ty create/execute` → routes to projects by issue label → **posts diffs/results back** as Linear comments. State in `.linear-poll-state.json`, `LINEAR_TOKEN` auth, `.auth-failed` graceful degradation. A Slack module is a near-exact analog. |
| **`taskyou-os`** (the remote "OS") | whole repo | Local Claude Code **GM** session ↔ SSH ↔ remote server running `ty daemon` / `ty serve` / `ty-web` as systemd services. Agents write lifecycle events to **`notifications.jsonl`**, which the GM monitors to know when tasks complete or block. |
| Task event hooks (`task.blocked/completed/started.tmpl`) | `taskyou-os/templates/hooks` | The push substrate. Each hook appends a JSON record to `notifications.jsonl`. Mirrors `internal/events` + `internal/hooks` in this repo. |
| PR #433 / task #1172 — "tweet and Slack message" | this repo | **Not** an integration — marketing copy *about* TaskYou. The only thing `grep -ri slack` finds here. |
| PR #402 (closed) — "Review Cloudflare Code Mode MCP" | this repo | Adjacent: explored exposing TaskYou's MCP server remotely (see Pattern B). |

So the honest answer: **we have not built Slack, but we've solved this exact
shape twice.** That makes a Slack integration low-risk and mostly a
copy-adapt-and-configure job, not new architecture.

## 3. The two things "Slack integration" could mean

These are genuinely different, and the screenshot depicts the *harder* one.

### Pattern A — a chat → TaskYou control bridge — RECOMMENDED

You DM or @-mention a bot in Slack; it classifies intent and drives `ty`, posts
replies in-thread, and pings you when a task needs input or finishes. This is the
literal analog of **both** `ty-email` and the **Linear poller**, and the most
direct reading of "manage their taskyou remotely via Slack."

```
Slack user ──@mention/DM──▶ Slack (Socket Mode / Events API)
                                   │
                                   ▼
                            ┌──────────────┐
                            │ slack module │──▶ LLM classify intent
                            │  (poller/    │──▶ ty CLI / ty serve API
                            │   socket)    │──▶ chat.postMessage (reply)
                            └──────────────┘
                                   ▲
              notifications.jsonl (task.blocked / completed) ──┘  push
```

### Pattern B — hosted/remote MCP endpoint (what the screenshot literally shows)

The cloud "Claude in Slack" wants to call `taskyou_*` MCP tools directly, but
TaskYou's MCP server (`internal/mcp/server.go`) is **stdio-only** — it reads
JSON-RPC off `os.Stdin`, is spawned per-task by Claude Code via `.mcp.json`
(`ty mcp-server --task-id`), and is not network-reachable. Letting a remote
Claude use it needs a **network-exposed, authenticated MCP server** (HTTP/SSE
transport) wrapping the existing `ty serve` REST API, plus hosting/tunnel/auth.
This is the bigger lift PR #402 circled, and it overlaps with the
`taskyou-os` **credential-proxy** design
(`docs/plans/2026-03-11-credential-proxy-design.md`).

**Recommendation:** ship Pattern A first. It delivers the user value with
infrastructure we already have, and exposes nothing to the public internet.
Pattern B is a worthwhile follow-up but is an infra/security project, not a
sidecar.

## 4. Why Pattern A is cheap: every piece already exists

A Slack bridge reuses proven seams:

1. **A working channel-bridge template.** `modules/linear/linear-poll.mjs`
   already does: detect new external messages → create `ty` task → route to
   project → execute → post results back. Swap "Linear comment" for "Slack
   message" and "post Linear comment" for "`chat.postMessage`".
2. **Intent classification** is solved in `ty-email/internal/classifier`
   (direct Claude API call, no permission prompts). Slack messages classify the
   same way.
3. **The action surface** is the `ty` CLI (what both precedents use) or the
   `ty serve` HTTP API (`internal/web/server.go`: `/api/tasks`, `/tasks/{id}/input`,
   `/execute`, `/logs`, `/stream` SSE, `/board`). Use the API if the bridge runs
   off-box from the daemon.
4. **The push half is already aggregated.** `notifications.jsonl` (written by the
   `task.blocked` / `task.completed` hooks) is a single tail-able stream of
   "needs attention" events. A Slack module tails it → `chat.postMessage`. That
   gives "ping me in Slack when a task blocks/finishes" almost for free, and the
   GM keeps using the same file.

Genuinely new code: a Slack **adapter** (Socket Mode or Events API +
`chat.postMessage`), thread↔task state (mirror `.linear-poll-state.json` or
`ty-email/internal/state`), and a few config flags.

## 5. Where should it live?

| Option | Shape | Best when |
|---|---|---|
| **`taskyou-os/modules/slack/`** (recommended) | `slack-poll.mjs` mirroring `linear-poll.mjs`; enabled via `SLACK_ENABLED=true` in `config.env`; tails `notifications.jsonl` for push | We want it to ship with the remote server, reuse the GM's notification stream, and match the existing module convention (Linear/R2/GitHub/nono are all env-flag modules bundled in the repo). |
| **`extensions/ty-slack/`** (fallback) | Go sidecar mirroring `ty-email` (adapter + classifier + bridge + state) | We want a standalone, runs-anywhere binary independent of the taskyou-os server stack, consistent with the other `extensions/` sidecars. |

Recommended: **build it as a `taskyou-os` module.** It's the smaller diff, slots
into an established convention, and inherits `notifications.jsonl` + the GM. If a
standalone Go sidecar is preferred for distribution, `ty-email` is a clean
template — in that case, lift `ty-email`'s `bridge` and `classifier` into a
shared package both sidecars consume.

### Interaction examples

- `@taskyou fix the checkout 500s and run it` → creates + executes a task,
  replies in-thread with the task id and a board link.
- Task hits a decision point → bridge reads `task.blocked` from
  `notifications.jsonl` → posts *"Task #312 needs input: which migration
  strategy?"* to the channel. Reply in-thread → routed to `ty input 312 …`.
- `@taskyou what's happening with 312?` → posts recent `ty output 312`.

## 6. Security (same model the precedents use)

- **Allowlist by Slack user ID**, not just channel membership — only listed
  users can create/execute tasks (cf. `ty-email`'s `allowed_senders`).
- **Verify request authenticity** — Slack signing-secret check (Events API) or
  Socket Mode's authenticated socket; never act on unverified payloads.
- **No code execution from chat** — the LLM only *classifies*; the bridge only
  calls `ty` subcommands. `--dangerous` stays opt-in via config.
- **Local secrets** — bot/app tokens via env / `*_cmd` shell-outs (like
  `LINEAR_TOKEN`), never sent to the LLM. `.auth-failed`-style degradation keeps
  polling alive when tokens lapse, as the Linear module already does.

## 7. Recommendation

1. **Build Pattern A as `taskyou-os/modules/slack/`**, closely modeled on
   `modules/linear/linear-poll.mjs`, enabled via `SLACK_ENABLED=true`, tailing
   `notifications.jsonl` for push. (Fallback: `extensions/ty-slack` Go sidecar
   modeled on `ty-email`.)
2. **Defer Pattern B** (network-exposed MCP for cloud Claude-in-Slack) to a
   separate spike; it's a hosting/auth project that should build on the
   credential-proxy design and where PR #402 left off.
3. The **"ping me in Slack when a task blocks/finishes"** half can ship
   immediately: a `task.blocked`/`task.completed` hook (or a tiny tail of
   `notifications.jsonl`) POSTing to a Slack incoming webhook — independent of
   the full bridge.

## 8. Open questions for Bruno

- **Home:** `taskyou-os` module (ships with the server, reuses the GM's
  `notifications.jsonl`) vs. standalone `ty-email`-style sidecar (runs anywhere)?
  I lean module.
- **Shared bot vs per-user:** one InfluenceKit-workspace bot (multi-user routing
  + per-user allowlists become the main design work) vs. a personal bot per
  operator like `ty-email`?
- **Socket Mode vs Events API:** Socket Mode needs no public URL (simplest on a
  laptop/agent server); Events API suits an always-on shared bot but needs an
  HTTPS endpoint.
- **Also pursue Pattern B?** Do we want the *existing* cloud Claude-in-Slack to
  call `taskyou_*` tools directly (remote MCP), or is a dedicated TaskYou bot
  enough?
