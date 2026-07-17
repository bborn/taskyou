# ty-chrome — TaskYou in Chrome

Three things, all tied to a specific taskyou task, all in the Chrome side panel:

1. **Create a task.** Pick a project/repo, describe the work, and hit
   **New task** — the panel creates it via the daemon and binds the terminal to
   it right away, execute-now optional. No terminal needed to get started.
2. **A full terminal.** A real xterm.js terminal wired straight into the task's
   tmux session — the same Claude Code (Agent) pane the TUI/desktop app show,
   plus a workdir **Shell** pane. Type into it, run commands, drive Claude Code,
   do anything you want, without leaving the browser.
3. **Live annotate.** Point, click, and annotate any page served by the task's
   dev server, and deliver that feedback — selectors, DOM excerpts, your
   comments, and a screenshot with numbered markers — straight into the task's
   executor.

The extension is a thin client for a locally-running `ty serve` daemon; see
[`docs/extension-first.md`](../../docs/extension-first.md) for the plan to make
it a no-terminal primary entry point (and the Chrome Web Store checklist).

| Annotate the live page | Side panel: task + embedded terminal |
|---|---|
| ![Annotating a page: numbered marker, draggable region box, comment popover](screenshots/annotate.png) | ![Side panel with the matched task and an interactive Agent/Shell terminal](screenshots/sidepanel.png) |

## How it works

```
annotate page ──► POST /api/tasks/{id}/annotations (ty serve)
                        │ writes <worktree>/.taskyou/annotations/<ts>/
                        │   annotation.md + screenshot.png
                        └► tmux send-keys nudge → Claude executor pane
                              executor reads bundle, edits code
                              dev server reloads → you see the change
```

Tab→task matching is automatic: each taskyou task has a dedicated port
(3100–4099, exposed as `$WORKTREE_PORT` in the task shell). If the tab's URL is
on a loopback host with that port — `localhost`, `127.0.0.1`, or any `.localhost`
/ `.test` subdomain (e.g. `qa-brand.influencekit.test:<port>`) — the extension
finds the processing/blocked task with that port. No match? Pick the task
manually in the side panel.

## Install

1. Get the extension: either clone this repo, or download `ty-chrome.zip`
   from the [latest release](https://github.com/bborn/taskyou/releases) and
   unzip it
2. Chrome → `chrome://extensions` → enable **Developer mode** → **Load
   unpacked** → select the `ty-chrome` directory
3. Run the API server: `ty serve` (the extension finds it automatically)
4. Pin the extension. Click it to open the side panel.

Requires a `ty` build that has the `/api/tasks/{id}/annotations` and
`/api/tasks/{id}/browser` endpoints (any build including this extension does).

## Use

1. Start a task whose executor runs your app on the task port
   (e.g. `rails s -p $WORKTREE_PORT` in the task shell).
2. Browse `http://localhost:<port>`. The extension badge shows the task id.
3. Open the side panel → **Annotate this page** (or use the on-page toolbar):
   - **Select** — click any element, describe what should change
   - **Box** — drag a rectangle over a region
   - **Note** — page-level comment

   Select/Box/region drags use pointer events, so they work the same whether
   you're on a real pointer or in Chrome DevTools' mobile/responsive
   (touch-emulation) mode.
4. Optionally add an overall instruction, then **Send**.
5. Watch the executor pick it up in the side panel's live console. When it
   finishes its turn, the page auto-reloads (toggle in the Executor header) —
   no hot-reload setup needed in your app. Annotate again. Loop.

## Shortcuts

- **⌥A** — annotate the current page (global, configurable at
  `chrome://extensions/shortcuts`)
- **⌥S** — send annotations to the executor (global)
- On page while annotating: **S** select · **B** box · **N** note ·
  **Esc** exit mode · **⌘↩** send (also saves an open comment)

## Terminal (Agent + Shell)

The side panel embeds a real terminal into the matched task — not a read-only
log, an interactive xterm.js terminal you can type into:

- **Agent** — the task's Claude Code executor pane. Send follow-ups, answer
  prompts, `/`-commands, interrupt with `Esc`, scroll history — exactly what you
  see in the TUI.
- **Shell** — the task's workdir shell (created on demand beside the executor),
  for `git`, `rails c`, `ls`, running tests, whatever you need.

If the task has no live session yet, the terminal offers a **Start session**
button; queued/processing tasks attach automatically once their executor comes
up. When the executor finishes a turn, **auto-reload** still reloads the app tab
(unless you have unsent annotations pinned).

Transport is the daemon's capture-pane WebSocket
(`GET /api/tasks/{id}/terminal[?pane=shell]`) — the same browser-fallback bridge
the desktop and web GUIs use: keystrokes flow out via tmux `send-keys`, the
visible pane streams back. It works by pane ID, so it keeps rendering even while
the TUI has the pane borrowed. `ty serve` must be localhost (the panel opens a
`ws://` socket, which browsers only allow to loopback from a secure context).

xterm.js is vendored under `vendor/` (`xterm.js`, `addon-fit.js`,
`addon-unicode11.js`, `xterm.css`) — same pinned versions the desktop app uses;
no build step.

## Browser bridge (executor → your browser window)

While the side panel is open on a matched task, the executor can see and drive
your live browser instead of launching its own — the same "control a group of
tabs" model as the Claude in Chrome extension. On first connect the daemon
drops `.taskyou/browser/HOWTO.md` into the worktree with ready-to-use curl
commands, and annotation nudges mention the bridge — so the executor discovers
it on its own:

- `screenshot` / `snapshot` — written into the worktree as PNG/HTML files the
  executor can Read directly (the PNG is its "eyes")
- `console` — real page console logs + uncaught JS errors (MAIN-world tap)
- `click` / `type` — interact via CSS selectors
- `navigate` (any http/https site) / `reload`

**The tab group.** The executor isn't limited to the matched tab — it can drive
every tab in this task's **tab group**, plus tabs it opens itself, so it can
pull up docs or an issue tracker alongside your app:

- `tabs` — list the tabs in the group `[{tab,url,title,active,primary}]`
- `open` — open a new tab at any URL (docs, external sites) → returns its `tab` id
- `activate` — bring a tab to the foreground
- `close` — close a tab

When the bridge connects, the matched tab is placed in a real Chrome tab group
labeled **`ty #<id>`** (orange). That group *is* the boundary: the executor
can't see or touch your other tabs, and you can drag a tab in or out of the
group to grant or revoke access. Any see/act command takes an optional
`"tab":<id>` to target a specific tab in the group (default: the matched app
tab). Screenshotting a background tab brings it to the foreground first.

The bridge pins to the (task, tab) it started on, so the executor navigating the
app tab to an external site — or foregrounding another tab to screenshot it —
won't tear the session down. It stops when you close the panel or the pinned
tab.

Transport: the panel long-polls `GET /api/tasks/{id}/browser/poll`; the
executor POSTs to `/api/tasks/{id}/browser` and blocks until the result comes
back. Panel closed → executor gets a fast 503 telling it to ask you to open
the panel.

## Toolbar badge

An orange **✓** on the extension icon means this tab is matched to a task (by
dev-server port). Hover the icon for the task id and title; the side panel
shows the full task.

## Auto-reload

The panel watches the executor pane for the working→idle transition (the
`esc to interrupt` marker disappearing) and reloads the matched tab when the
executor finishes a turn — the right moment for apps that don't hot-reload.
It never reloads while you still have unsent annotations pinned.

## Configuration

None needed in the common case: the extension defaults to
`http://127.0.0.1:8080` (where `ty serve` comes up) and, if that isn't
answering, auto-probes common ports and adopts the first responding server.
The gear icon in the side panel overrides it manually
(`chrome.storage.local`).

## API endpoints used

- `GET  /api/status` — connection check
- `GET  /api/tasks?status=processing|blocked` — tab→task matching, task picker
- `POST /api/tasks/{id}/annotations` — annotation bundle delivery
- `GET  /api/tasks/{id}/terminal-info` — resolve the task's tmux window/panes
- `POST /api/tasks/{id}/session` — start an executor session on demand
- `POST /api/tasks/{id}/shell` — materialize the workdir Shell pane
- `GET  /api/tasks/{id}/terminal[?pane=shell]` — interactive terminal WebSocket

## Security

Same trust model as `ty serve`: an unauthenticated localhost API. Annotation
bundles are written by the daemon, only inside the task's worktree under
`.taskyou/annotations/` (self-gitignored).

Host permissions are `<all_urls>` because Chrome's `captureVisibleTab` (used
to bake your markers into the screenshot) accepts only `<all_urls>` or
`activeTab` — and `activeTab` grants die on every page reload, which would
silently drop screenshots in the edit loop this tool exists for. Tab→task
auto-matching still only ever targets loopback hosts (`localhost`, `127.0.0.1`,
and RFC-reserved `.localhost` / `.test` subdomains) by port; other pages are
annotatable only if you manually pick a task. This is a
load-unpacked dev tool talking to your own machine.
