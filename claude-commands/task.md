Create a task in the workflow queue GitHub issues.

Input: $ARGUMENTS

Parse the input for:
- Task description (the main text)
- `-p` or `--project`: Project name (offerlab/ol, influencekit/ik, personal)
- `-t` or `--type`: Task type (code/c, writing/w, thinking/t)
- `-P` or `--priority`: Priority level (high/h, low/l)
- `-b` or `--body`: Additional details for the issue body

Create a GitHub issue with:
- Title: the task description
- Labels: Always include `status:queued`, plus any specified project/type/priority labels
  - Projects: `project:offerlab`, `project:influencekit`, `project:personal`
  - Types: `type:code`, `type:writing`, `type:thinking`
  - Priority: `priority:high`, `priority:low`

Use `gh issue create --repo $TASK_REPO` to create the issue. If TASK_REPO is not set, use `bborn/workflow`.

After creation, report the issue number and URL.
