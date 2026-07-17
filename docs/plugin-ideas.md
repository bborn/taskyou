# Plugin ideas

A menu of things TaskYou plugins can do, to seed community (and your own)
plugins. Everything here is buildable on today's contract — [hooks](plugins.md)
(fire on task events) and [actions](plugins.md#actions-user-invoked) (run on
demand with the task's env). A plugin is just a directory with executables; it
can be any language and can bundle its own config/binaries.

✅ = shipped as an example in [`examples/plugins/`](../examples/plugins/).

## Notifications & awareness (hooks)

- ✅ **desktop-notify** — native OS notification on done/blocked/failed.
- ✅ **slack** — post task updates to a Slack channel (incoming webhook).
- **sound** — play a chime on `task.done`, a different one on `task.failed`
  (`afplay`/`paplay`/terminal bell). ~10 lines.
- **say / TTS** — `say "task 42 is done"` (macOS) or `espeak`.
- **phone push** — ntfy.sh / Pushover / Telegram bot for away-from-desk pings.
- **auth-alert** — loud, unmissable alert on `task.auth_required` (the executor
  needs re-login and everything is stalled until you notice).

## Logging & analytics (hooks)

- **webhook** — POST the task-event JSON to a configured URL. The universal
  primitive; every integration above is a specialization of this.
- **timetrack** — append one JSONL row per event; derive cycle time, throughput,
  blocked-rate over time. A personal productivity ledger.
- **status-file** — maintain a tiny JSON of live counts for a tmux statusline or
  menubar widget.

## Worktree & quality (actions)

- ✅ **worktree** — show the task's diff; run its tests.
- **lint** — run the project linter in the worktree, surface pass/fail.
- **format** — run the formatter and report what changed.
- **branch-copy** — copy the task's branch name / PR URL to the clipboard.

## Integrations (hooks + actions)

- **linear / jira / github-issue** — on `task.done`, comment or transition the
  linked issue; an action to open it. (Needs an id convention + API token in the
  plugin's `config.env`.)
- **calendar / timeblock** — log completed tasks to a calendar.
- **journal** — append a daily standup line ("finished #42: …") to a notes file.

## Ambient / fun

- **confetti** — a celebratory splash (or `cowsay`) on `task.done`.
- **now-playing** — pause music while an executor is actively running.

## Where should plugins live? (in-repo vs. own repo)

- **In-repo `examples/plugins/`** — small, canonical, copy-paste starting points
  that ship with TaskYou and are covered by the loader's tests. The three above
  live here. Best for anything short enough to read in one sitting.
- **Its own repo** — when a plugin grows an independent release cadence, ships a
  compiled binary or heavier dependencies, or has a real surface of its own
  (config, docs, versioning). Install by dropping (or symlinking) its directory
  into `~/.config/task/plugins/`. This is where a bigger integration — say a
  token-compressing proxy or a full issue-tracker sync — belongs.

Rule of thumb: **start in `examples/`; graduate to a repo when it earns one.**
