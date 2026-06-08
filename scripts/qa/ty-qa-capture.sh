#!/usr/bin/env bash
# Print what the TUI currently shows. Optional arg: scrollback lines (default 0 = visible).
#
# Usage: ty-qa-capture.sh [scrollback-lines]
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

if [[ -n "${1:-}" ]]; then
  qtmux capture-pane -t "$TY_UI_PANE" -p -S "-$1" | sed 's/[[:space:]]*$//'
else
  qtmux capture-pane -t "$TY_UI_PANE" -p | sed 's/[[:space:]]*$//'
fi
