List tasks from the workflow queue GitHub issues.

Input: $ARGUMENTS

Parse optional filters:
- `-p` or `--project`: Filter by project (offerlab, influencekit, personal)
- `-t` or `--type`: Filter by type (code, writing, thinking)
- `-P` or `--priority`: Filter by priority (high, low)
- `-s` or `--status`: Filter by status (queued, in-progress, done) - default: queued

Use `gh issue list --repo $TASK_REPO` with appropriate `--label` flags to filter. If TASK_REPO is not set, use `bborn/workflow`.

Display results in a clear format showing:
- Issue number
- Title
- Labels (project, type, priority)
- Created date

If no tasks found, say the queue is empty.
