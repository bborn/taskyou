# Push notifications & one-tap unblock

TaskYou can push a notification to your phone when a task needs you — and let
you act on it with one tap, without opening a laptop. Notifications fire on task
lifecycle events and are delivered through [ntfy](https://ntfy.sh) (simplest)
or a Telegram bot.

**Notifications are OFF by default.** Nothing is sent until you turn them on and
configure a provider.

## What fires a push

| Event | When | Push |
|-------|------|------|
| `task.blocked` | A task calls `taskyou_needs_input`, or is otherwise blocked waiting on you | 🔔 "Needs input" + one-tap reply action |
| `task.auth_required` | An executor session needs re-authentication (e.g. expired login) | 🔐 "Auth required" + one-tap reply action |
| `task.completed` | A task finishes (or its PR is up for review) | ✅ "Completed" |
| `task.failed` | A task fails | ❌ "Failed" |

The body always includes the **task title**, **project**, and a **short
reason** — for needs-input pushes that reason is the actual question the agent
asked.

These hook into the existing event system (`internal/events`), so every code
path that changes task state — the executor, MCP (`taskyou_needs_input`,
`taskyou_complete`), CLI, and the TUI — produces a push. No parallel path.

## Quick start (ntfy)

1. Install the [ntfy app](https://ntfy.sh/app) on your phone and subscribe to a
   private, hard-to-guess topic (e.g. `ty-bruno-7f3a9c`).

2. Configure TaskYou:

   ```sh
   ty settings set notify_enabled true
   ty settings set notify_ntfy_topic ty-bruno-7f3a9c
   ```

3. To make the **one-tap reply** button work from your phone, the daemon's HTTP
   API must be reachable from the internet (or your VPN/tailnet). Point
   TaskYou at that reachable base URL:

   ```sh
   ty settings set notify_base_url https://ty.my-tailnet.ts.net:8080
   ```

   If you don't set `notify_base_url`, action links fall back to
   `http://localhost:<http_api_port>`, which only works on the same machine.

That's it. Block a task (or have an agent call `taskyou_needs_input`) and you'll
get a push within a few seconds.

## One-tap unblock — how it works

A needs-input / auth-required push carries two action buttons:

- **Reply "continue"** — an ntfy `http` action that POSTs to the existing web
  API, `POST /api/tasks/{id}/input`, with `{"message":"continue"}`. That types
  the reply into the agent's session and presses Enter, resuming the task. The
  canned reply is configurable:

  ```sh
  ty settings set notify_unblock_reply "yes, go ahead"
  ```

- **Open task** — a `view` action that opens the web UI so you can type a
  full custom reply.

The round trip is: push → tap → `POST /api/tasks/{id}/input` → `tmux
send-keys` into the executor pane → agent resumes.

## Protected ntfy topics

If your topic requires auth, set an access token (stored as a secret and hidden
from `ty settings` / the settings API):

```sh
ty settings set notify_ntfy_token tk_xxxxxxxxxxxxxxxx
```

You can also self-host ntfy and point at it:

```sh
ty settings set notify_ntfy_server https://ntfy.example.com
```

## Telegram (optional second provider)

[Create a bot](https://core.telegram.org/bots#how-do-i-create-a-bot) via
@BotFather, then find your chat ID (e.g. message
[@userinfobot](https://t.me/userinfobot)):

```sh
ty settings set notify_telegram_token 123456:ABC-DEF...
ty settings set notify_telegram_chat_id 987654321
```

Telegram inline buttons can only navigate (not POST), so Telegram pushes get an
**Open task** deep link to the web UI rather than a true one-tap reply. Use ntfy
for one-tap unblock.

If both ntfy and Telegram are configured, pushes go to both.

## All settings

| Key | Default | Notes |
|-----|---------|-------|
| `notify_enabled` | `false` | Master on/off switch |
| `notify_base_url` | `http://localhost:<port>` | Externally reachable HTTP API base for action links |
| `notify_unblock_reply` | `continue` | Canned reply for the one-tap action |
| `notify_ntfy_server` | `https://ntfy.sh` | ntfy server base URL |
| `notify_ntfy_topic` | — | ntfy topic; setting it enables the ntfy provider |
| `notify_ntfy_token` | — | ntfy access token for protected topics (secret) |
| `notify_telegram_token` | — | Telegram bot token (secret); setting it + chat ID enables Telegram |
| `notify_telegram_chat_id` | — | Telegram chat ID |

Settings whose names contain `token`/`key`/`secret`/`password` are never shown
by `ty settings` or returned by the settings API.
