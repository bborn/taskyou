# Attachments

Uploads are local files. SQLite stores metadata and a SHA-256 content hash;
bytes live in `<database-path>.attachments/<hash>` with private permissions.
Back up the database **and that directory together**. Identical files share one
stored copy. No cloud storage or paid service is involved.

Opening an existing database with this build migrates legacy blobs one at a
time. Each file is synced before its blob is cleared; interrupted migrations
resume on the next open. The schema/protocol change requires restarting older
daemons with the new build. Downgrading to a binary that expects blobs is not
supported without restoring a pre-migration backup.

Freed SQLite pages are reused. Migration does not automatically vacuum the
live database or delete task logs. Database file size therefore stays roughly
the same until an explicit maintenance vacuum. Unreferenced stored files are
retained; automatic garbage collection is deliberately not part of this change.

For execution, attachments are copied under `.claude/attachments/task-<id>/`
in the task's worktree. IDs and hashes isolate duplicate filenames. The
`.claude` directory preserves existing Claude read permissions, but every
executor receives ordinary file paths. Execution copies survive individual
turns for resume and disappear with worktree cleanup; durable originals remain.

For remote tasks, files travel over the configured SSH connection to the remote
worktree. Transfers are bounded, checksum-verified, and renamed into place only
after successful verification. A transfer failure prevents prompt delivery.

In the mobile composer, Attach files opens the native file chooser directly.
Multiple files and pasted images are supported, with a 32 MiB per-file limit.
Files appear as removable chips; pending selections survive reloads. Send can
submit attachments alone and includes the selected IDs through the HTTP input
API (`attachment_ids`). Busy-agent refusal keeps both text and files pending.
Removing a chip removes it from that reply; All files manages task attachments.
All stored task attachments are included when starting or retrying a task.
