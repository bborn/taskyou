#!/bin/bash
# Summarise the task's changes in prose, using lumen's AI explain.
#
# Picks the narrowest interesting target: staged changes, else the working
# tree, else this branch vs its base. The banner always names what was
# summarised.
set -euo pipefail

# TASK_PLUGIN_DIR is set by ty; fall back to the script's own directory so this
# is still runnable by hand while debugging.
# shellcheck disable=SC1091
source "${TASK_PLUGIN_DIR:-$(dirname "$0")}/lib.sh"

lumen_preflight

if ! lumen_diff_target; then
  # git diff never sees untracked files, and a task whose whole output is new
  # files is common — say so rather than the misleading bare "no changes".
  untracked=$(git ls-files --others --exclude-standard | lumen_count)
  if [[ "$untracked" != "0" ]]; then
    echo "no changes ($untracked untracked file(s) — git add to include them)"
  else
    echo "no changes"
  fi
  exit 0
fi

files=$(lumen_changed_files)

# First line is the TUI banner; the rest is visible in the CLI and web UI.
lumen_banner "explained $files file(s) vs $LUMEN_TARGET_LABEL"
echo
lumen_explain_target
