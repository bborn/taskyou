# Cooperative TUI restart

Normal `ty restart` no longer lists or kills TUI sessions. It waits for the old
daemon lock to be released, starts the daemon, and writes a unique reload request
into that database's settings. Local clients poll asynchronously, acknowledge the
request, wait for forms and task mutations, hand back borrowed panes, then exit
Bubble Tea and re-exec the current executable in the same terminal/process.
Navigation state crosses exec through a one-use environment value. A failed pane
handoff cancels reload. A failed exec resumes the current build. SSH clients do
not enable local re-exec; old clients ignore the request and remain running.

Unit coverage verifies request uniqueness/database isolation, token acknowledgement,
form deferral, pending-save deferral, pane handoff ordering, and failed handoff.
Focused race tests cover the reload model and shared request protocol.

Actual QA used `/private/tmp/ty-qa-reload`, two private tmux TUI sessions, one
backlog task, and a harmless sleep process as an agent pane. The executable was
atomically replaced with a different inode, then the actual `ty restart` command
was run against the QA database. `lsof` verified:

- TUI A mapped the replacement inode with the same PID and pane, restored task
  #10000's detail view, and reattached the same surviving sleep pane.
- TUI B retained its old executable inode and unchanged unsaved draft.
- Cancelling B's draft through its normal confirmation triggered the deferred
  reload into the new inode, still with the same PID and terminal.

Raw identities/results: `/private/tmp/ty-qa-reload/restart-result.json`.
No live daemon, task, agent, or terminal was restarted by these tests.
