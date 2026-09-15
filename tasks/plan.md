# Workspace panel implementation

Approved scope: shared extensible panel host, Shell default, PR summary, files/Markdown; TUI and GUI; isolated QA screenshots and a PR.

1. Shared providers and persistent per-task tabs, with CLI and HTTP access. Prove canonical identity, isolation, bounded file previews, and restore behavior with Go tests.
2. GUI split workspace and panel launcher. Use pane-specific transport for simultaneous terminals without window zoom; verify browser behavior and build.
3. TUI workspace viewer beside the live agent, keeping durable tmux sessions separate from panel views. Verify keys and process survival in the real QA harness.
4. Run focused/full checks and existing pane QA suites, capture and inspect TUI/GUI evidence, publish screenshots, commit and submit the PR.

The design is in docs/superpowers/specs/2026-09-14-workspace-panel-design.md. Existing tmux pane hosting will be retained where possible, with an independently addressable workspace viewer added to it.
