# Task workspace panel

Status: proposal for discussion; no application changes implemented.

## Objective

Keep the task conversation/executor on the left and turn the shell area on
the right into a tabbed workspace. Shell is the initial tab. A `+` launcher
opens other task resources without replacing the conversation. Both TUI and
GUI expose the same resources and actions, with renderers suited to each.

Assumptions: preserve existing agent sessions, preserve shell processes when
switching tabs, support local and remotely placed tasks, and treat a terminal
browser fallback as an explicit capability difference. A replacement chat
engine is outside this change: the existing agent interaction remains the
left-hand surface.

## What BB does

Examined GitHub source on 2026-09-14. The SDK was verified against commit
`18d20f304ff6bc6328676913126db09b8f325c23`.

- [Panel action contract](https://github.com/get-bb/bb/blob/18d20f304ff6bc6328676913126db09b8f325c23/packages/plugin-sdk/src/app-contract.ts#L589):
  an action registers its identity, title, component, optional launch function,
  and framing. Opening supplies a title and JSON parameters. Identical
  parameters focus an existing tab; different targets can open separate tabs.
- [Launcher](https://github.com/get-bb/bb/blob/18d20f304ff6bc6328676913126db09b8f325c23/apps/app/src/components/secondary-panel/NewTabActions.tsx):
  combines terminal/browser actions with plugin-contributed actions.
- [File opener integration](https://github.com/get-bb/bb/blob/18d20f304ff6bc6328676913126db09b8f325c23/apps/app/src/components/plugin/file-opener-tabs.ts):
  routes file targets to plugin panels and retains their source context.

Adopt the separation between panel host, resource identity, and provider.
BB's React component contract cannot itself supply a terminal UI renderer.

## Existing TaskYou seams

- `desktop/src/components/TerminalPane.tsx`: fixed Agent/Shell tabs; mounts
  only the active terminal attachment.
- `desktop/src/components/DetailView.tsx`: vertically stacked description and
  terminal, with a reply composer on phones. The requested desktop layout is
  a real layout change, not just a rename of Shell.
- `internal/ui/detail_view.go`: agent and shell live on the daemon tmux
  server. The TUI normally views the task window through a nested client;
  hiding a local shell parks it in another daemon window. Opening task detail
  does not borrow the agent's pane into the TUI server.
- `internal/ui/detail_remote_shell.go`: remote shell view can be detached
  without killing its remote process.
- `internal/web/handlers_gui.go`, `internal/web/terminal.go`: session/shell
  bootstrap and pane-specific terminal transport. Remote terminal discovery
  already has shared implementation in `internal/executor/remote_terminal.go`.
- `internal/github/pr.go`: cached PR title, state, aggregate CI result,
  mergeability, and diff totals. Individual checks, reviews, and comments
  would require further backend work.
- `internal/parity/parity_test.go`: verifies keybinding coverage and declared
  API routes. It does not yet verify dynamic panel provider coverage.

## User experience

```text
┌──────────────────────────┬────────────────────────────────┐
│ Task conversation / agent│ Shell  PR  README.md  +        │
│                          ├────────────────────────────────┤
│                          │ Active workspace view          │
│                          │                                │
└──────────────────────────┴────────────────────────────────┘
```

The launcher searches available actions and files. Opening the same resource
focuses its existing tab. Tabs support close, next/previous, focus, and panel
collapse. Reopening a task restores its tabs. Focus, selected tab, and split
width are per client so another open client cannot steal focus or resize the
layout. A narrow screen switches between conversation and workspace using the
same tabs and actions; it does not squeeze two unreadable columns together.

| View | GUI | TUI | Delivery |
| --- | --- | --- | --- |
| Shell | Existing terminal, initially selected | Persistent tmux shell view | First release |
| Pull request | Cached PR summary and refresh/open action | Same fields and actions in text | First release |
| Files / Markdown | File picker and read-only preview | File picker and text/Glamour preview | First release |
| Changes | File list and code diff | File list and colored unified diff | Follow-up provider |
| Browser | URL/resource panel; embedded preview where supported | URL/resource panel with open/copy action | Follow-up provider |
| Run logs | Follow task/routine output | Same stream in viewport | Follow-up provider |

Browser embedding is capability-dependent: arbitrary websites cannot be
assumed to permit iframe embedding. On SSH, opening a URL must target the
viewer's browser or present a copyable link, not launch a browser on the host.
Remote preview URLs must resolve for the viewing client, rather than blindly
using the remote host's localhost address. Full browser automation, side
chats, review agents, and a plugin marketplace are separate additions.

## Shared contract

Introduce `internal/panel` for provider metadata, canonical resource identity,
parameter validation, resource loading, and actions. Keep task/worktree/host
resolution in shared services. HTTP handlers and the TUI call those services;
providers do not depend on `internal/ui` or React.

Illustrative descriptor, not a final API schema:

```go
type Instance struct {
    ID         string          `json:"id"`
    TaskID     int64           `json:"task_id"`
    ProviderID string          `json:"provider_id"`
    Resource   string          `json:"resource"`
    Params     json.RawMessage `json:"params"`
}
```

A provider declares a stable ID, label, parameter schema, supported actions,
availability for this task, and rendering capabilities. Identity derives from
validated, canonical parameters, not arbitrary JSON serialization order.
Instances identify resources; tmux pane IDs and network connections are
runtime handles resolved separately and revalidated after reconnects.

Proposed HTTP surface:

- `GET /api/tasks/{id}/panel-providers`: available providers and capabilities.
- `GET /api/tasks/{id}/panels`: persisted open resources and order.
- `POST /api/tasks/{id}/panels`: validate and idempotently open a resource.
- `DELETE /api/tasks/{id}/panels/{panelID}`: close its tab descriptor.
- `GET /api/tasks/{id}/panels/{panelID}/content`: typed resource data.
- `POST /api/tasks/{id}/panels/{panelID}/actions/{action}`: shared actions.

Persist small versioned descriptors in SQLite, scoped by task. Broadcast tab
changes using the existing event infrastructure where practical; keep local
selection stable. Actions have explicit input schemas. Opening and inspecting
panels should also be reachable by CLI/agents through the same core service.
Existing terminal routes can remain compatible while being consumed by Shell.

Start with a compiled provider registry, with each builtin having TUI and GUI
renderers. That is an extensible architecture without requiring a runtime
plugin loader. A later external provider protocol should offer shared data
primitives (Markdown, tables, logs, terminal sessions, links) that both hosts
can render. Custom rich GUI renderers require a declared TUI renderer or a
useful fallback. An arbitrary React plugin is not cross-surface support.

## Terminal ownership and TUI hosting

Separate a resource's lifetime from the lifetime of its visible view.
Switching, collapsing, closing a tab, or losing the UI connection detaches its
viewer and cancels view subscriptions; it does not kill an agent or shell.
Stopping a session is a distinct action. Restoring a missing session reports
its state and offers restart; it must not silently duplicate a running shell.

The native GUI attachment in `desktop/src-tauri/src/terminal.rs` zooms the
requested pane in a grouped tmux window. Zoom is window-wide. Two terminal
components cannot simply attach to Agent and Shell simultaneously. Resolve
this before shipping the split: use an independently addressable pane
transport, or change daemon session layout with a compatibility migration.
The existing WebSocket capture/input path is a candidate to reuse, but its
polling, key handling, resize effects, and fidelity need runtime verification.

For the TUI, a right-side TaskYou panel viewer can own the tab strip and render
Bubble Tea content, attaching to the shell when Shell is selected. It remains
a view on the UI server, separate from durable task processes. This requires
explicit focus/key routing between chat, launcher, text content, and shell.
Do not insert nonterminal views into executor pane discovery or reuse the
agent process's pane for a panel helper. The existing nested full-window
viewer must be adapted so selecting a non-shell provider retains the agent
on the left. Validate this hosting seam in a small prototype before building
the complete launcher.

## Success criteria and verification

1. New tasks default to Shell; opening PR or a file leaves the conversation
   visible on a wide screen. Equivalent keyboard actions exist in the TUI.
2. Opening the same file twice focuses one tab. Separate files open separate
   tabs. Tab restoration tolerates unavailable providers and missing files.
3. A long-running shell command survives tab changes, collapse, task changes,
   UI exit, and reconnect. Returning reconnects to the same task session.
4. Simultaneous TUI, native GUI, and browser views neither steal input focus
   nor zoom another client's window. Resize ownership is explicit and tested.
5. Shell, PR, and file providers work for local and remote task placement;
   unavailable resources explain why without breaking the rest of the panel.
6. File reads are confined to the resolved task/project root, including
   symlinks. Bound previews by size; render binary/unsupported files clearly.
   A panel ID cannot access another task's session or files.
7. Core/service tests cover identity, persistence, validation, actions, and
   session ownership. HTTP tests verify the same behavior. Renderer contract
   tests require both surfaces for every builtin provider. Retain the current
   parity harness and add coverage for providers beyond static keybindings.
8. Manual runtime verification covers native GUI, browser, local TUI, SSH TUI,
   concurrent viewers, reconnect, and narrow layouts. Mocked rendering alone
   cannot prove terminal lifecycle correctness.

Implementation validation commands (not run for this design document):

```sh
go test ./internal/panel/... ./internal/web/... ./internal/ui/... ./internal/parity/...
go test ./...
npm --prefix desktop run build
cargo test --manifest-path desktop/src-tauri/Cargo.toml
```

Follow the existing Go formatting/error conventions, colocated `*_test.go`
tests with `t.TempDir()` fixtures, and React/TypeScript component conventions.
New backend tests live alongside `internal/panel` and HTTP handlers; native
attachment tests belong with the Rust terminal implementation.

## Boundaries and remaining decisions

- Always preserve durable sessions and surface parity; keep backend logic
  below UI packages and expose it over HTTP.
- Review the pane-hosting prototype and cross-client resize policy before
  committing to a terminal transport or daemon-layout migration.
- Never kill sessions as a consequence of closing a view, weaken parity
  checks, or execute arbitrary plugin-supplied shell strings to load content.
- Default scope is a provider host plus Shell, cached PR status, and file
  previews. Rich browser embedding and runtime third-party plugin loading
  need separate specifications once this contract has been exercised.

Recommended implementation sequence: prove simultaneous agent/panel hosting;
build the shared registry and persistent descriptors with Shell; add PR and
file providers in both interfaces; then extend with Changes and Browser.
