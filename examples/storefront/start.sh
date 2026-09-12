#!/usr/bin/env bash
# Prepare the practice repository, then open TaskYou. No task executes automatically.
set -euo pipefail
cd "$(dirname "$0")"
for program in git tmux ty node; do
  command -v "$program" >/dev/null || { echo "Missing $program. See README.md for setup." >&2; exit 1; }
done
if [[ ! -d .git ]]; then
  git init -q
  git add README.md index.html style.css start.sh
  git -c user.name='TaskYou demo' -c user.email='demo@taskyou.local' commit -qm 'Start the storefront'
fi
exec ty
