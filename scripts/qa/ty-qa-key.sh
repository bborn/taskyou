#!/usr/bin/env bash
# Send keystrokes to the TUI. tmux key names (Enter, Escape, Up, Down) are sent
# as keys; bare strings are typed literally.
#
# Examples:
#   ty-qa-key.sh P Enter                 # focus In-Progress column, open detail
#   ty-qa-key.sh Down Down Enter         # move selection, open
#   ty-qa-key.sh n                       # new-task form
#   ty-qa-key.sh '!'                     # toggle dangerous/safe mode
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

if [[ $# -eq 0 ]]; then
  echo "usage: ty-qa-key.sh <key> [key...]" >&2
  exit 1
fi

qtmux send-keys -t "$TY_UI_PANE" "$@"
sleep "${TY_QA_KEY_DELAY:-0.4}"
