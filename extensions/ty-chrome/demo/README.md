# ty-chrome demo harness

`demo.js` drives the full annotate→send→nudge loop with Playwright against an
isolated taskyou instance (throwaway DB, dedicated tmux socket `tychrome-demo`,
demo storefront on :3142, `ty serve` on :8765). It loads the unpacked extension
in a persistent Chromium context and screenshots every stage to
`/tmp/tychrome-demo/shots/`.

Setup (one-time per demo run):
1. Build: `go build -buildvcs=false -o /tmp/tychrome-demo/ty ./cmd/task`
2. Environment: see `docs/superpowers/plans/2026-06-10-ty-chrome-annotations.md`
   Task 6 — creates the demo app repo, seeds the task (port 3142), starts the
   dev server + executor pane on the isolated socket.
3. Serve with the tmux shim on PATH so send-keys reaches the isolated socket:
   `PATH=/tmp/tychrome-demo/bin:$PATH WORKTREE_DB_PATH=/tmp/tychrome-demo/tasks.db ty serve --port 8765`
4. `node demo.js` (needs `playwright` resolvable from the working directory)

For the live-executor finale, run `claude` inside the demo task pane first; the
nudge arrives at its prompt and it applies the annotated changes.
