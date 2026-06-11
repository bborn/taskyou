# ty-chrome: Browser Annotations → Task Executor

**Date:** 2026-06-10
**Status:** Approved (autonomous run — decisions documented inline)

## Problem

When a taskyou task runs a dev server (e.g. a Rails app on the task's dedicated
`Port`), visual product feedback travels a lossy path: the user looks at the
browser, alt-tabs to the TUI, and *describes* what they see in prose. The
executor (a Claude Code session in the task's tmux pane) never sees the page,
the element, or the user's pointing finger.

ty-chrome closes that loop. A Chrome extension lets the user point, click,
draw, and annotate directly on the running page. The annotations — selectors,
DOM excerpts, a screenshot with markers baked in, and the user's comments —
are delivered to the task's executor as rich context. The executor edits the
code; the dev server reloads; the user sees the change and annotates again.
The extension also "teleports" the executor into the browser: a side panel
shows the live executor output and accepts follow-up input, so the user never
has to leave the page.

## Canonical use case

Bruno works on a Rails app. The task's worktree runs `rails s -p $WORKTREE_PORT`
in the task shell pane. He browses `http://localhost:3142`, clicks the
extension, draws a box around a misaligned card, clicks the "Save" button and
comments "this should be a primary button, and move it above the fold." He
hits Send. The executor reads the annotation bundle, edits the view/CSS,
Rails reloads, Bruno refreshes and sees the fix — all without leaving Chrome.

## Approaches considered

**A. Text-only via existing `POST /api/tasks/{id}/input`.** No daemon changes;
extension flattens annotations into one line of text sent via tmux send-keys.
Rejected: send-keys is single-line (newlines submit the Claude prompt
prematurely), no screenshot support, selectors + DOM excerpts crammed into one
line are lossy and fragile.

**B. File-drop endpoint + pane nudge (chosen).** New
`POST /api/tasks/{id}/annotations` writes `annotation.md` + `screenshot.png`
into `<worktree>/.taskyou/annotations/<timestamp>/`, then sends a one-line
*literal* (`send-keys -l`) nudge into the Claude pane: "read this file and make
the requested changes." Claude Code reads markdown and views PNGs natively, so
the executor gets full fidelity. The pane injection stays single-line and
robust. Reuses the existing `ty serve` HTTP API (already CORS-permissive),
the existing task `Port` for tab→task matching, and the existing pane
plumbing (`ClaudePaneID`).

**C. Native messaging host / dedicated WebSocket bridge.** A separate process
registered as a Chrome native-messaging host. Rejected: a new process,
install-time manifest registration, and duplicate plumbing for what `ty serve`
already exposes. YAGNI.

## Architecture

```
┌─ Chrome ────────────────────────────────┐      ┌─ ty serve (:8080) ─────────┐
│ tab: localhost:3142 (Rails on task port)│      │ GET  /api/tasks (+port)    │
│  └ content.js overlay                   │      │ POST /api/tasks/{id}/      │
│     element pick / box / note / markers │ HTTP │       annotations  ◄── new │
│ side panel (sidepanel.html)             ├─────►│ GET  /api/tasks/{id}/output│
│  task match · executor stream · input   │      │ POST /api/tasks/{id}/input │
│ service worker (sw.js)                  │      └─────┬──────────────────────┘
│  tab→task match · screenshot · API calls│            │ writes bundle, then
└─────────────────────────────────────────┘            ▼ tmux send-keys -l
                                            <worktree>/.taskyou/annotations/<ts>/
                                              annotation.md + screenshot.png
                                                       │ read by
                                                       ▼
                                            Claude executor pane (ClaudePaneID)
                                              edits code → dev server reloads
```

Components and their single purposes:

1. **Daemon: task JSON enrichment** (`internal/web/handlers.go`) — expose
   `port`, `worktree_path`, `has_executor` on `taskJSON`. The extension matches
   the active tab's URL port against task ports to find "the task behind this
   page." `has_executor` (= `ClaudePaneID != ""`) tells the UI whether
   annotations can be delivered live.

2. **Daemon: annotation intake** (`internal/web/annotations.go`, new) —
   `POST /api/tasks/{id}/annotations`. Validates the task, resolves the drop
   root (`task.WorktreePath`, falling back to the project path for
   non-worktree projects), writes the bundle, ensures
   `.taskyou/annotations/.gitignore` (`*`) so bundles never dirty git status,
   and nudges the pane if one exists. Returns
   `{ok, path, nudged}` — `nudged:false` (with the bundle still written) when
   the task has no live executor pane.

3. **Extension: service worker** (`sw.js`) — owns all daemon HTTP calls
   (content scripts can't reliably hit localhost cross-origin; the SW can,
   with host permissions). Maintains tab→task matches by polling
   `/api/tasks?status=processing` (+ `blocked`) on tab activation/navigation.
   Captures the visible tab (markers included, so the screenshot carries the
   annotations visually). Routes messages between side panel and content
   script.

4. **Extension: content overlay** (`content.js`) — injected on demand via
   `chrome.scripting`. Shadow-DOM toolbar with three modes:
   - **Select**: hover-highlight any element; click captures a robust CSS
     selector, tag/id/classes, trimmed text, an outerHTML excerpt (≤1500
     chars), bounding rect, and a small set of computed styles.
   - **Box**: drag a rectangle over a region.
   - **Note**: page-level comment with no anchor.
   Every annotation gets a numbered marker pinned on the page and a comment
   popover. Send (from overlay or panel) triggers screenshot → POST → toast
   with the result, then clears markers.

5. **Extension: side panel** (`sidepanel.html/js`) — the "teleported
   executor." Shows: connection status + editable server URL
   (`chrome.storage`, default `http://127.0.0.1:8080`); the matched task card
   (or a dropdown of processing/blocked tasks when no port match); the pending
   annotation count; an instruction textarea + Send; and a live executor view
   that polls `GET /api/tasks/{id}/output` (ANSI-stripped) every 2.5s while
   visible, with a one-line follow-up input that posts to `/input`.

## Annotation payload (extension → daemon)

```json
{
  "url": "http://localhost:3142/products",
  "title": "Products — MyApp",
  "viewport": {"width": 1440, "height": 900, "dpr": 2},
  "instruction": "overall instruction (optional)",
  "annotations": [
    {"kind": "element", "label": 1, "selector": "#product-7 > .card-footer > button",
     "tag": "button", "text": "Save", "html": "<button class=…>…</button>",
     "rect": {"x": 312, "y": 540, "w": 88, "h": 32},
     "styles": {"color": "…", "backgroundColor": "…", "fontSize": "…"},
     "comment": "make this a primary button"},
    {"kind": "region", "label": 2, "rect": {"x": 0, "y": 200, "w": 600, "h": 180},
     "comment": "too much whitespace here"},
    {"kind": "note", "label": 3, "comment": "general note"}
  ],
  "screenshot": "data:image/png;base64,…"
}
```

## annotation.md format (daemon → executor)

Markdown with a header (URL, page title, captured-at, viewport), the overall
instruction, then one numbered section per annotation: comment first (the
user's intent), then selector / rect / text / styles, and the HTML excerpt in
a code fence. Ends with a pointer to `screenshot.png` ("markers ①②③ on the
screenshot correspond to the numbered annotations"). The pane nudge is:

```
[ty-chrome] Browser annotations received for this task. Read
.taskyou/annotations/<ts>/annotation.md and view screenshot.png, then make the
requested changes.
```

(sent as a single line via `tmux send-keys -l`, then `Enter` separately).

## Error handling

- Daemon unreachable → side panel shows disconnected state; Send disabled.
- No port match → manual task picker (processing/blocked tasks).
- Task has no executor pane → bundle still written, response `nudged:false`,
  panel offers "Queue task for execution" (existing `/execute` endpoint).
- Screenshot capture failure (e.g. minimized window) → send without
  screenshot; annotation.md notes its absence.
- Oversized payloads → daemon caps request body at 20 MB; HTML excerpts
  trimmed client-side.

## Security posture

Same trust model as `ty serve` today: unauthenticated localhost API with
permissive CORS. The extension talks to a server the user explicitly runs.
The annotation endpoint only writes inside the task's worktree under
`.taskyou/annotations/` (path is server-derived from a timestamp, never from
client input).

**Amendment (discovered in implementation):** host permissions are
`<all_urls>` rather than localhost-only, because `captureVisibleTab` accepts
only `<all_urls>` or `activeTab`, and `activeTab` grants expire on page
reload — which would silently drop screenshots in the reload-heavy edit loop
this tool targets. Functional scoping is preserved in code: tab→task
auto-matching only considers `localhost`/`127.0.0.1` ports.

## Testing

- Go: handler tests (httptest + fake CommandRunner + temp worktree) for the
  happy path, no-pane path, fallback-to-project-path, gitignore creation,
  payload caps, and literal send-keys arguments. Task JSON field tests.
- Extension: no build step, no test framework; correctness validated by the
  end-to-end demo (isolated ty instance + Playwright persistent context with
  the unpacked extension, real tmux pane receiving the nudge).

## Out of scope (YAGNI)

Freehand drawing (box + element + note cover the feedback loop), annotation
history browsing in the panel, multi-page annotation sessions, auth for
`ty serve`, Firefox/Safari ports, screenshot-region cropping, editing
annotations after placement (delete + redo instead).
