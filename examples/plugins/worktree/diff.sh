#!/bin/bash
# Show a compact summary of the task's worktree changes.
# WORKTREE_PATH is provided when the action is run with a task in context.
set -euo pipefail

wt="${WORKTREE_PATH:-}"
if [[ -z "$wt" || ! -d "$wt" ]]; then
  echo "no worktree for this task"
  exit 0
fi
cd "$wt"

changed=$(git status --porcelain | wc -l | tr -d ' ')
stat=$(git diff --stat 2>/dev/null | tail -1 | sed 's/^ *//')

if [[ "$changed" == "0" && -z "$stat" ]]; then
  echo "worktree clean"
  exit 0
fi

# First line is what the TUI banner shows; the rest is visible in the CLI.
echo "${stat:-$changed uncommitted file(s)}"
echo
git status --short
