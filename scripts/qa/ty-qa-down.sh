#!/usr/bin/env bash
# Tear down the isolated instance: kill its tmux sessions and prune QA worktrees.
# Pass --purge to also delete the DB and project tree.
#
# Usage: ty-qa-down.sh [--purge]
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

qtmux kill-session -t "$TY_UI_SESSION" 2>/dev/null || true
qtmux kill-session -t "$TY_DAEMON_SESSION" 2>/dev/null || true
# Kill the whole isolated tmux server (it only ever hosts this qa instance).
qtmux kill-server 2>/dev/null || true

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
