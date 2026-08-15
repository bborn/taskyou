#!/bin/bash
# `ty plugins run claude-profile-router status` — show what the router sees.
# Same numbers route.sh decides on, so a surprising routing choice can be
# checked against reality without reading the daemon log.
set -uo pipefail

_pre_profiles="${TY_CLAUDE_PROFILES:-}"
_pre_max="${TY_CLAUDE_MAX_PERCENT:-}"
_pre_bin="${TY_BIN:-}"
if [[ -n "${TASK_PLUGIN_DIR:-}" && -f "$TASK_PLUGIN_DIR/config.env" ]]; then
  # shellcheck disable=SC1091
  source "$TASK_PLUGIN_DIR/config.env"
fi
[[ -n "$_pre_profiles" ]] && TY_CLAUDE_PROFILES="$_pre_profiles"
[[ -n "$_pre_max" ]] && TY_CLAUDE_MAX_PERCENT="$_pre_max"
[[ -n "$_pre_bin" ]] && TY_BIN="$_pre_bin"

TY="${TY_BIN:-ty}"
MAX_PERCENT="${TY_CLAUDE_MAX_PERCENT:-90}"

if [[ -z "${TY_CLAUDE_PROFILES:-}" ]]; then
  echo "No profiles configured. Copy config.example.env to config.env and set TY_CLAUDE_PROFILES."
  exit 0
fi

echo "Routing threshold: skip a profile at or above ${MAX_PERCENT}% used"
echo
for raw in $TY_CLAUDE_PROFILES; do
  dir="${raw/#\~/$HOME}"
  "$TY" usage --config-dir "$dir" || echo "  (unavailable)"
done
