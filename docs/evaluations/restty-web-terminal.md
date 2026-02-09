# Evaluation: Restty for Web Terminal Integration

**Date:** 2026-02-08
**Repository:** https://github.com/wiedymi/restty
**Version evaluated:** 0.1.16
**License:** MIT

## Summary

Restty is a browser-based terminal emulator powered by Ghostty's VT parser (compiled to WASM), WebGPU/WebGL2 rendering, and TypeScript text shaping. It provides a high-fidelity terminal experience in the browser with GPU-accelerated rendering, pane splitting, theme support, and a WebSocket-based PTY protocol.

**Recommendation: Worth prototyping, with caveats.** Restty is a promising young library that could enable a web-based terminal view for TaskYou tasks, but its early-stage maturity and Bun-only PTY server require careful integration planning.

## What Restty Is

- **Frontend terminal renderer** — Renders terminal output in a browser using WASM (libghostty-vt) + WebGPU/WebGL2
- **PTY client** — Connects to a backend PTY server over WebSocket using a simple JSON protocol
- **Pane manager** — Supports split panes, theme switching, font customization, touch gestures
- **NOT a server** — The included PTY server (`playground/pty-server.ts`) is a Bun-only development tool, not a production server

## Architecture Overview

```
┌─────────────────────────────────────────┐
│  Browser (Restty Frontend)              │
│  ┌───────────┐  ┌────────────────────┐  │
│  │ WASM VT   │  │ WebGPU/WebGL2      │  │
│  │ Parser    │──│ Renderer           │  │
│  │ (ghostty) │  │ + Text Shaper      │  │
│  └───────────┘  └────────────────────┘  │
│         ▲                               │
│         │ WebSocket (JSON + binary)     │
└─────────┼───────────────────────────────┘
          │
┌─────────▼───────────────────────────────┐
│  PTY Server (needs to be built in Go)   │
│  ┌───────────┐  ┌────────────────────┐  │
│  │ WebSocket │──│ PTY (os/exec +     │  │
│  │ Handler   │  │ creack/pty)        │  │
│  └───────────┘  └────────────────────┘  │
│         │                               │
│         ▼                               │
│  tmux session / claude process          │
└─────────────────────────────────────────┘
```

## PTY WebSocket Protocol

The protocol is minimal and well-defined:

**Client → Server:**
```json
{ "type": "input", "data": "<keystrokes>" }
{ "type": "resize", "cols": 80, "rows": 24 }
```

**Server → Client:**
- Binary frames: raw terminal output (UTF-8 encoded)
- JSON frames:
```json
{ "type": "status", "shell": "bash" }
{ "type": "error", "message": "...", "errors": [...] }
{ "type": "exit", "code": 0 }
```

## Fit with TaskYou

### Current Architecture
TaskYou currently uses:
- **Tmux** for task process isolation (each task runs in a tmux window)
- **SSH server** (Wish) for remote TUI access
- **Bubble Tea** for terminal UI
- **MCP server** for agent communication

### Integration Opportunities

1. **Web dashboard for task monitoring** — A web UI where users can see live terminal output of running tasks, without needing `tmux attach` or SSH. This is the primary use case.

2. **Remote task interaction** — Allow sending input to blocked tasks (responding to `taskyou_needs_input`) through a browser.

3. **Agent terminal view** — Provide a web-accessible terminal showing what Claude/agents are doing in real-time.

### What We'd Need to Build

| Component | Effort | Description |
|-----------|--------|-------------|
| Go PTY WebSocket server | Medium | Proxy tmux pane output over WebSocket. Use `creack/pty` or read directly from tmux capture-pane |
| Web frontend | Medium | HTML page with Restty connecting to the Go server |
| Authentication | Low-Medium | Token-based auth for WebSocket connections (reuse existing SSH key infra or simple tokens) |
| Task routing | Low | Map task IDs to their tmux panes and WebSocket connections |

## Strengths

- **GPU-accelerated rendering** — WebGPU with WebGL2 fallback means smooth rendering of fast terminal output (important for watching Claude stream)
- **Ghostty WASM core** — Correct VT parsing, same quality as Ghostty desktop terminal
- **Clean PTY protocol** — Simple JSON messages, easy to implement a Go server-side
- **Pane splitting** — Could show Claude pane + Shell pane side-by-side in browser
- **MIT licensed** — No licensing concerns
- **Touch/mobile support** — Works on tablets/phones
- **Modular exports** — Can import just the parts needed (`restty/pty`, `restty/theme`, etc.)
- **Active development** — 80 stars, commits from today (2026-02-08), responsive maintainer

## Concerns

### Maturity
- **v0.1.16** — Pre-1.0, API may change
- **Created 2026-02-07** — Literally 1 day old as a public repo
- **80 stars** — Small community, limited battle-testing
- **No production users documented** — Unknown reliability under load

### PTY Server Gap
- The included PTY server is **Bun-only** (`Bun.spawn` + `Bun.Terminal`)
- We'd need to write our own Go WebSocket PTY server
- This is ~200-400 lines of Go but requires careful handling of:
  - Process lifecycle management
  - Terminal resize propagation
  - Connection drops / reconnection
  - Security (auth, sandboxing)

### Dependency Chain
- Pulls in `text-shaper` (0.1.17) — also young
- Requires WASM blob (`libghostty-vt`) — ~2MB download
- WebGPU is still experimental in some browsers (WebGL2 fallback helps)

### Alternatives Considered

| Library | Maturity | Rendering | Size | Notes |
|---------|----------|-----------|------|-------|
| **xterm.js** | Very mature (8+ years, 18k+ stars) | Canvas/WebGL | ~400KB | Industry standard, powers VS Code web terminal |
| **Restty** | Very new (1 day, 80 stars) | WebGPU/WebGL2 via WASM | ~2MB+ | Better rendering quality, less proven |
| **ttyd** | Mature | xterm.js-based | Server binary | Full solution but C-based, harder to integrate |
| **gotty** | Older | xterm.js-based | Go binary | Simpler but less maintained |

**xterm.js is the safer choice** for production use given its maturity. Restty offers better rendering quality (Ghostty's VT parser is excellent) but at the cost of proven reliability.

## Prototype Plan

If we proceed with a prototype, the suggested approach:

### Phase 1: Go PTY WebSocket Server (~1 day)
Build a minimal Go WebSocket server that:
- Accepts WebSocket connections at `/pty?task_id=<id>`
- Attaches to the task's tmux pane using `tmux capture-pane` or pipes
- Forwards output as binary WebSocket frames
- Accepts input/resize JSON messages
- Handles auth via simple bearer token

### Phase 2: Static Web Terminal Page (~0.5 day)
- Single HTML page served by the daemon
- Imports Restty from CDN or bundled
- Connects to the PTY WebSocket for a given task
- Minimal UI: task selector dropdown + terminal view

### Phase 3: Integration (~0.5 day)
- Add HTTP routes to the taskd daemon
- Wire up task ID → tmux pane mapping
- Add link in TUI detail view: "Open in browser: http://localhost:PORT/task/123"

## Decision Matrix

| Criterion | Restty | xterm.js |
|-----------|--------|----------|
| Rendering quality | ★★★★★ | ★★★★☆ |
| Maturity/reliability | ★★☆☆☆ | ★★★★★ |
| Bundle size | ★★★☆☆ | ★★★★☆ |
| API simplicity | ★★★★☆ | ★★★★☆ |
| Community/support | ★☆☆☆☆ | ★★★★★ |
| Mobile support | ★★★★☆ | ★★★☆☆ |
| Pane splitting built-in | ★★★★★ | ☆☆☆☆☆ |
| License | MIT ★★★★★ | MIT ★★★★★ |

## Recommendation

**For a prototype/experiment:** Use Restty. The rendering quality is noticeably better, the built-in pane splitting maps well to our Claude+Shell split view, and the clean PTY protocol makes Go server implementation straightforward. The risk is acceptable for an internal tool.

**For production deployment:** Consider starting with xterm.js for reliability, with a plan to evaluate switching to Restty once it reaches v1.0+ maturity. Alternatively, build the Go WebSocket PTY server with a clean interface so the frontend terminal library can be swapped later.

**Regardless of choice:** The Go PTY WebSocket server is the same work either way — the protocol is nearly identical. Build the server first, then pick whichever frontend feels right.

## Next Steps

1. [ ] Build Go WebSocket PTY server as `internal/server/pty.go`
2. [ ] Create minimal web terminal page
3. [ ] Test with both Restty and xterm.js to compare real-world experience
4. [ ] Decide on frontend library based on prototype results
5. [ ] Add authentication layer
6. [ ] Integrate with taskd daemon
