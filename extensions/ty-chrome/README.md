# ty-chrome — TaskYou Live Annotate

Point, click, and annotate any page served by a taskyou task's dev server, and
deliver that feedback — selectors, DOM excerpts, your comments, and a
screenshot with numbered markers — straight into the task's executor pane.
The side panel "teleports" the executor into Chrome: watch its output live and
send follow-ups without leaving the page.

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

1. Run the API server: `ty serve` (default `:8080`)
2. Chrome → `chrome://extensions` → enable **Developer mode** → **Load
   unpacked** → select this directory (`extensions/ty-chrome`)
3. Pin the extension. Click it to open the side panel.

Requires the taskyou build from this branch or later (the
`/api/tasks/{id}/annotations` endpoint and `port`/`has_executor` task fields).

## Use

1. Start a task whose executor runs your app on the task port
   (e.g. `rails s -p $WORKTREE_PORT` in the task shell).
2. Browse `http://localhost:<port>`. The extension badge shows the task id.
3. Open the side panel → **Annotate this page** (or use the on-page toolbar):
   - **Select** — click any element, describe what should change
   - **Box** — drag a rectangle over a region
   - **Note** — page-level comment
4. Optionally add an overall instruction, then **Send**.
5. Watch the executor pick it up in the side panel's live console; the page
   reloads as it edits. Annotate again. Loop.

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
