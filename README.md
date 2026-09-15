# QA evidence — task 5289 (log-first task status)

Screenshots produced by `scripts/qa/` against the isolated instance at
`/tmp/ty-qa`, seeded with `ty-qa-seed.sh`. They live on this branch rather
than in the PR so no binaries are merged into `main`.

The usual host (`ty-qa-publish.sh` → R2) needs an `rclone` remote that is not
configured on this machine.

- `board.png` — the board after the change (the projection read path)
- `refused.png` — a done-write refused in the TUI for a task with an open PR
- `auditlog.png` — `ty debug status-log` / `status-consistency`
