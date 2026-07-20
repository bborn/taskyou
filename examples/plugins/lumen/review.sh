#!/bin/bash
# Summarise every commit on this branch against its base — the "what did this
# task actually do" view, for reading before you open a PR.
#
# Resolves the base explicitly instead of using the explain ladder: being
# always branch-wide, never just the uncommitted edits, is the whole reason
# this exists separately from `explain`.
#
# Caveat, named in the banner rather than hidden: on a shared pipeline branch
# this range spans sibling steps' commits too, not only this task's.
set -euo pipefail

# shellcheck disable=SC1091
source "${TASK_PLUGIN_DIR:-$(dirname "$0")}/lib.sh"

lumen_preflight

if ! base=$(git merge-base origin/HEAD HEAD 2>/dev/null); then
  echo "cannot resolve base branch (no origin/HEAD)"
  exit 0
fi

short=$(git rev-parse --short "$base")

if [[ "$(git rev-list --count "$base..HEAD" 2>/dev/null || echo 0)" == "0" ]]; then
  echo "no commits vs $short"
  exit 0
fi

commits=$(git rev-list --count "$base..HEAD")
files=$(git diff --name-only "$base..HEAD" | wc -l | tr -d ' ')

lumen_banner "reviewed $commits commit(s), $files file(s) vs $short"
echo
lumen_run explain "$base..HEAD"
