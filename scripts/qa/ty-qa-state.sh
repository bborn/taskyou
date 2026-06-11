#!/usr/bin/env bash
# Read the TUI's debug state (written by --debug-state-file). With no args, prints
# a summary. With a jq filter arg, runs it against the raw JSON.
#
# Examples:
#   ty-qa-state.sh                       # summary
#   ty-qa-state.sh '.detail.has_panes'   # jq filter (requires jq)
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

if [[ ! -f "$TY_QA_STATE" ]]; then
  echo "no debug state at $TY_QA_STATE — is the TUI running (ty-qa-tui.sh)?" >&2
  exit 1
fi

if [[ $# -ge 1 ]]; then
  command -v jq >/dev/null || { echo "jq required for filters" >&2; exit 1; }
  jq "$1" "$TY_QA_STATE"
else
  python3 - "$TY_QA_STATE" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
dash = d.get("dashboard") or {}
det = d.get("detail") or {}
print("view            :", d.get("view"))
print("focused_column  :", dash.get("focused_column"))
print("selected_task   :", dash.get("selected_task_id"))
if det:
    print("detail.task_id  :", det.get("task_id"))
    print("detail.status   :", det.get("status"))
    print("detail.has_panes:", det.get("has_panes"))
PY
fi
