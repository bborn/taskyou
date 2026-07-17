#!/bin/bash
# Run the task worktree's tests. Uses $TEST_CMD if set (export it, or put it in
# a config.env next to this script); otherwise auto-detects by project type.
set -euo pipefail

wt="${WORKTREE_PATH:-}"
if [[ -z "$wt" || ! -d "$wt" ]]; then
  echo "no worktree for this task"
  exit 0
fi

if [[ -n "${TASK_PLUGIN_DIR:-}" && -f "$TASK_PLUGIN_DIR/config.env" ]]; then
  # shellcheck disable=SC1091
  source "$TASK_PLUGIN_DIR/config.env"
fi

cd "$wt"

cmd="${TEST_CMD:-}"
if [[ -z "$cmd" ]]; then
  if [[ -f go.mod ]]; then
    cmd="go test ./..."
  elif [[ -f package.json ]]; then
    cmd="npm test"
  elif [[ -f Rakefile || -d spec ]]; then
    cmd="bundle exec rspec"
  else
    echo "no TEST_CMD set and could not detect project type"
    exit 0
  fi
fi

echo "running: $cmd"
if eval "$cmd"; then
  echo "tests passed"
else
  echo "tests FAILED"
  exit 1
fi
