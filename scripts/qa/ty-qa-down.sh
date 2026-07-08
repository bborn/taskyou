#!/usr/bin/env bash
# Tear down the isolated instance: kill its tmux sessions and prune QA worktrees.
# Pass --purge to also delete the DB and project tree.
#
# Usage: ty-qa-down.sh [--purge]
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

# Stop the isolated full daemon (Tier 3), if running. Its PID file is keyed to the
# instance DB dir (see getPidFilePath), so this never touches the live daemon.
if [[ -f "$TY_QA_ROOT/daemon.pid" ]]; then
  kill "$(cat "$TY_QA_ROOT/daemon.pid")" 2>/dev/null || true
fi
tmux kill-session -t "$TY_UI_SESSION" 2>/dev/null || true
tmux kill-session -t "$TY_DAEMON_SESSION" 2>/dev/null || true

if [[ -d "$TY_QA_PROJECTS" ]]; then
  for p in "$TY_QA_PROJECTS"/*/; do
    [[ -d "${p}.git" ]] && git -C "$p" worktree prune 2>/dev/null || true
  done
fi

if [[ "${1:-}" == "--purge" ]]; then
  rm -rf "$TY_QA_ROOT"
  echo "purged $TY_QA_ROOT"
else
  echo "stopped sessions; DB kept at $WORKTREE_DB_PATH (use --purge to delete everything)"
fi
