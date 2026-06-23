# ty-chrome — TaskYou Live Annotate

Point, click, and annotate any page served by a taskyou task's dev server, and
deliver that feedback — selectors, DOM excerpts, your comments, and a
screenshot with numbered markers — straight into the task's executor pane.
The side panel "teleports" the executor into Chrome: watch its output live and
send follow-ups without leaving the page.

| Annotate the live page | Side panel: task + live executor |
|---|---|
| ![Annotating a page: numbered marker, draggable region box, comment popover](screenshots/annotate.png) | ![Side panel with matched task, annotation count, and live executor console](screenshots/sidepanel.png) |

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
`localhost:<port>`, the extension finds the processing/blocked task with that
port. No match? Pick the task manually in the side panel.

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

## Browser bridge (executor → your browser)

While the side panel is open on a matched task, the executor can see and drive
your live tab instead of launching its own browser. On first connect the
daemon drops `.taskyou/browser/HOWTO.md` into the worktree with ready-to-use
curl commands, and annotation nudges mention the bridge — so the executor
discovers it on its own:

- `screenshot` / `snapshot` — written into the worktree as PNG/HTML files the
  executor can Read directly (the PNG is its "eyes")
- `console` — real page console logs + uncaught JS errors (MAIN-world tap)
- `click` / `type` — interact via CSS selectors
- `navigate` (localhost-only) / `reload`

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
- `GET  /api/tasks/{id}/output` — live executor console (polled)
- `POST /api/tasks/{id}/input` — follow-up messages to the executor

## Security

Same trust model as `ty serve`: an unauthenticated localhost API. Annotation
bundles are written by the daemon, only inside the task's worktree under
`.taskyou/annotations/` (self-gitignored).

Host permissions are `<all_urls>` because Chrome's `captureVisibleTab` (used
to bake your markers into the screenshot) accepts only `<all_urls>` or
`activeTab` — and `activeTab` grants die on every page reload, which would
silently drop screenshots in the edit loop this tool exists for. Tab→task
auto-matching still only ever targets `localhost`/`127.0.0.1` ports; other
pages are annotatable only if you manually pick a task. This is a
load-unpacked dev tool talking to your own machine.
