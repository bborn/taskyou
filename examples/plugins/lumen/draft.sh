#!/bin/bash
# Draft a commit message for the task's staged changes.
#
# This prints the message and stops — it does NOT commit. An action is one
# keypress with a model in the loop and no undo, so committing is left to you.
#
# Staged-only by design: `lumen draft` is defined over the staged diff, so this
# action does not use the explain ladder.
set -euo pipefail

# shellcheck disable=SC1091
source "${TASK_PLUGIN_DIR:-$(dirname "$0")}/lib.sh"

lumen_preflight

if git diff --cached --quiet 2>/dev/null; then
  echo "nothing staged — git add first"
  exit 0
fi

files=$(git diff --cached --name-only | lumen_count)

lumen_banner "drafted commit message for $files staged file(s)"
echo

# TASK_TITLE is absent when the action runs with no task in context, and `set
# -u` would abort on a bare reference. An empty -c is worse than no -c, so
# branch rather than passing "".
title="${TASK_TITLE:-}"
if [[ -n "$title" ]]; then
  lumen_run draft -c "$title"
else
  lumen_run draft
fi
