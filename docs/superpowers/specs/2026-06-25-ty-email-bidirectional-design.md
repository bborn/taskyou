# ty-email: Bidirectional, thread-per-task email — in-daemon, on the notify framework

**Status:** Design approved · **Date:** 2026-06-25 · **Component:** ty daemon (`internal/notify`, `internal/web`, new IMAP poller) · **Depends on:** PR #621 (`internal/notify` framework)

## Goal

Make each TaskYou task a single ongoing **email thread**: the daemon emails you
when a task needs input, completes, fails, or the agent wants to tell you
something; you reply in the thread to unblock, reopen, or comment. Queue-level
model (C1) — replies resume tasks through the existing input/resume machinery,
never injected live into an arbitrary running agent.

## How we got here (decision log)

- **A + C1.** Extend the email channel into a two-way protocol; queue-level
  park/resume, not live session injection.
- **One thread per task.** Replies route by thread identity to a task.
- **Outbound on:** needs-input, completion, agent-initiated FYI (+ failure, which
  #621 already emits). No daily digest.
- **Architecture pivoted on PR #621.** #621 introduces `internal/notify` — an
  in-daemon, provider-agnostic notifier on the existing `events.Emitter`, already
  firing on `task.blocked`/`completed`/`failed` with **ntfy** + **Telegram**
  providers, and already doing one-tap unblock via `POST /api/tasks/{id}/input`.
  Email is the same job (notify on an event) with a different transport, so:
  - **Option A:** email becomes a **provider in `internal/notify`**, not a
    parallel system. (Chosen over a standalone sidecar that would double-fire on
    every event alongside #621's push.)
  - **A1 (chosen):** go **fully in-daemon** — outbound provider *and* the inbound
    IMAP poller live in the daemon; thread-map is a table in the main ty DB; one
    process, one source of truth, no cross-process split-brain. The existing
    `extensions/ty-email` sidecar is **retired**, its logic ported in.

## Dependencies & grounding (verified on `main`, 2026-06-25)

- **`internal/events/events.go` `Emitter`** exists on `main`; every mutation path
  (executor, MCP `taskyou_needs_input`/`taskyou_complete`, CLI, TUI) routes
  through it. #621 plugs `notify` into it.
- **`POST /api/tasks/{id}/input` → `handleTaskInput`** exists on `main`
  (`internal/web/server.go:92`); it forwards the reply via `tmux send-keys` into
  the executor pane (`internal/web/terminal.go`) and resumes the agent. This is
  the unified resume path push/web/TUI already use; email reuses it.
- **`internal/notify`** does **not** exist on `main` — it arrives with #621.
  **This feature is built on top of #621's branch and lands after it.**
- **`extensions/ty-email`** (current standalone sidecar) is the **reference
  implementation** for IMAP/Gmail adapters, exact-match allowlist, SQLite dedup,
  and the PR #596 loop protections. We port these into the daemon and retire the
  sidecar/its separate `go.mod`.

## Architecture

Everything in-daemon. Two halves sharing one DB.

```
OUTBOUND:  mutation ──▶ events.Emitter ──▶ internal/notify
              (task.blocked / completed / failed / notify)      ──▶ email provider
                                                                       │ compose in
                                                                       │ task's thread
                                                                       ▼  SMTP ──▶ inbox

INBOUND:   your reply ──IMAP poll (daemon goroutine)──▶ match In-Reply-To/[ty#id]
                                          │ resolve task_id (thread-map table)
                                          ▼
                              POST /api/tasks/{id}/input  (existing handler:
                              tmux send-keys → resume)   ── unified w/ push/web/TUI
```

### Thread-map (new table in the main ty DB)

| column | purpose |
|---|---|
| `task_id` | the ty task |
| `root_message_id` | `Message-ID` of the first email we sent — thread anchor |
| `last_message_id` | most recent message in the thread, for `In-Reply-To` chaining |
| `subject` | cached, so replies keep `Re: [ty#42] …` |

Binding: first outbound for a task creates the row, stamps `[ty#<id>]` in the
subject, sets `Message-ID`/`References`. Later outbound sets `In-Reply-To:
last_message_id`. Inbound matches `In-Reply-To`/`References` → task, with the
`[ty#<id>]` subject token as the client-proof fallback.

## Outbound — email provider in `internal/notify`

A new provider alongside `ntfy.go`/`telegram.go`, fired by the same event
allow-list #621 uses. Email-specific behavior layered on top:

| Event | Email |
|---|---|
| `task.blocked` (true `needs_input`) | "Needs your input" — question + log tail; reply unblocks. |
| `task.blocked` (PR / awaiting-merge) | "Done — PR up" template (disambiguated from needs-input via block reason / fresh `"question"` log line). |
| `task.completed` | "Done" — summary + PR link; reply = reopen. |
| `task.failed` | "Failed" — error tail; reply = guidance/retry. |
| `task.notify` (new) | "FYI" — agent-initiated, non-blocking; reply = best-effort comment. |

**`task.notify` is new:** add MCP tool `taskyou_notify(message)` (`internal/mcp/
server.go`) that appends a log line and emits a `task.notify` event so every
provider (email *and* ntfy/Telegram) can surface it.

**Shared anatomy:** subject `[ty#<id>] <title>`; body = event message + last ~5
log lines; footer states what a reply does (`unblock | reopen | comment`) +
`ty#<id> · <status>`.

**Volume control:** one email per event transition, deduped on `(task_id,
event_id)`; per-task outbound rate cap (default **6 / task / hour**, defer not
drop) to survive a flapping task. (Reuse #621's notifier throttle if it provides
one; otherwise add it in the email provider.)

## Inbound — IMAP poller (daemon goroutine)

Ports the sidecar's poll loop into the daemon. Every cycle, for each unseen
message:

1. Exact `net/mail` allowlist check. Fail → drop + mark seen.
2. Resolve `task_id`: `In-Reply-To`/`References` → thread-map; fallback `[ty#NN]`
   subject token; no match → **new task** (create path), not a reply.
3. Read current task status from the DB (in-process now — no `ty` shell-out).
4. Route by status (matrix below).
5. Mark processed (state + `\Seen`) **only after** the action succeeds.

### Status routing matrix

| status at reply time | action |
|---|---|
| **blocked (question)** | `POST /api/tasks/{id}/input` with cleaned reply body → send-keys resume. The win condition. |
| **blocked (PR/awaiting-merge)** | same input handler; reply reopens/continues. |
| **completed / done** | reopen via the input/continuation path with reply as feedback. |
| **running** | best-effort: queue as feedback; auto-ack "agent still working." No live race. |
| **not found / archived** | bounce a friendly "that task is gone"; no phantom task. |

**Reply cleaning:** strip quoted history (`>` trailer, `On <date>, X wrote:`) and
signatures before the body becomes input; keep raw copy in the task log. The one
fiddly parser — use a known quoted-text stripper.

## Safety, loops & abuse (ported from PR #596, adapted in-daemon)

- **Crash/retry boundedness:** mark processed only after success; classify/route
  retry cap (3 → giveup) so a poison message can't loop or re-charge. Invariant:
  *no inbound path retries unbounded; no outbound path re-emits* (idempotent on
  `(task_id, event_id)`).
- **Auto-reply ping-pong:** outbound stamped `Auto-Submitted: auto-replied` +
  `X-TY-Email`; inbound with `Auto-Submitted`/`Precedence`/`X-Autoreply` ignored.
- **Prompt injection via reply body** (sharpest risk — body becomes input):
  exact-match allowlist (only you reply-route); body injected as delimited *user
  input*, never system/tool content; destructive capability not granted over
  email. Accepted residual risk for trusted-sender-only operation.
- **From-spoofing:** allowlist + no destructive-over-email.

## Testing

- **Unit:** thread-map bind/resolve (header chain + `[ty#id]` fallback +
  broken-chain); quoted-text stripper (Gmail/Apple Mail/Outlook + signature);
  event→template mapping incl. PR-blocked vs question-blocked fork; `(task_id,
  event_id)` dedup; rate cap defer.
- **Routing matrix:** one test per row.
- **Loop/safety:** event replay no double-send; auto-reply headers drop;
  non-allowlisted sender drops; poison message stops at 3 attempts.
- **End-to-end through real wiring** (mirrors #621's notify e2e): `UpdateTaskStatus
  (blocked)` → `events.Emitter` → email provider → SMTP publish (mock); then feed
  a reply through the IMAP poller → assert `POST /api/tasks/{id}/input` called
  with cleaned body → assert marked seen. Proves C1 end-to-end.
- Runs in the daemon's normal `go test` (no separate sidecar module).

## Migration / retirement

- Port from `extensions/ty-email`: IMAP/Gmail adapters, allowlist, SQLite dedup,
  #596 loop protections, config wizard fields.
- New config lives under the same `notify_*` namespace #621 introduces (e.g.
  `notify_email_*`), read live from the DB.
- Retire `extensions/ty-email` (separate binary + `go.mod`) once parity is
  reached; leave a deprecation note pointing at the in-daemon feature.

## Out of scope (YAGNI)

- Live injection into an arbitrary running agent (C2).
- Daily/periodic digest thread.
- Keeping the standalone sidecar as a long-term parallel path.
