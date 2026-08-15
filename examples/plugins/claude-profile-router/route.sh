#!/bin/bash
# task.route hook: pick the Claude profile with the most rate-limit headroom.
#
# ty runs this synchronously just before it spawns a task and reads stdout back
# as the decision, so stdout carries KEY=VALUE lines and nothing else — every
# diagnostic goes to stderr (which lands in the daemon log).
#
#   CLAUDE_CONFIG_DIR=<dir>   run this task under that profile
#   HOLD=1 / REASON=<text>    every profile is spent; keep the task queued
#   (no output)               no opinion; ty spawns as already configured
#
# Printing nothing is always safe, so every failure path here does exactly that.
set -uo pipefail

say() { echo "claude-profile-router: $*" >&2; }

# config.env holds the settings, but anything already in the environment wins —
# that is what makes a one-off `TY_CLAUDE_MAX_PERCENT=50 ./route.sh` a usable way
# to try a threshold without editing the file.
_pre_profiles="${TY_CLAUDE_PROFILES:-}"
_pre_max="${TY_CLAUDE_MAX_PERCENT:-}"
_pre_projects="${TY_CLAUDE_PROJECTS:-}"
_pre_bin="${TY_BIN:-}"
if [[ -n "${TASK_PLUGIN_DIR:-}" && -f "$TASK_PLUGIN_DIR/config.env" ]]; then
  # shellcheck disable=SC1091
  source "$TASK_PLUGIN_DIR/config.env"
fi
[[ -n "$_pre_profiles" ]] && TY_CLAUDE_PROFILES="$_pre_profiles"
[[ -n "$_pre_max" ]] && TY_CLAUDE_MAX_PERCENT="$_pre_max"
[[ -n "$_pre_projects" ]] && TY_CLAUDE_PROJECTS="$_pre_projects"
[[ -n "$_pre_bin" ]] && TY_BIN="$_pre_bin"

TY="${TY_BIN:-ty}"
MAX_PERCENT="${TY_CLAUDE_MAX_PERCENT:-90}"

if [[ -z "${TY_CLAUDE_PROFILES:-}" ]]; then
  say "TY_CLAUDE_PROFILES not set (see config.example.env)"
  exit 0
fi

if ! command -v "$TY" >/dev/null 2>&1; then
  say "ty not found on PATH (set TY_BIN in config.env)"
  exit 0
fi

# Optional project allowlist, so routing can be tried on one project first.
if [[ -n "${TY_CLAUDE_PROJECTS:-}" ]]; then
  match=""
  for p in $TY_CLAUDE_PROJECTS; do
    [[ "$p" == "${TASK_PROJECT:-}" ]] && match=1 && break
  done
  [[ -z "$match" ]] && exit 0
fi

best_dir=""
best_pct=""
exhausted_low=""   # lowest usage among profiles that were over the threshold

for raw in $TY_CLAUDE_PROFILES; do
  dir="${raw/#\~/$HOME}"

  # --percent prints one bare number: the binding limit's used percent.
  if ! pct=$("$TY" usage --config-dir "$dir" --percent 2>/dev/null); then
    say "skipping $dir (usage unavailable — expired login?)"
    continue
  fi
  if [[ ! "$pct" =~ ^[0-9]+$ ]]; then
    say "skipping $dir (unparseable usage: '$pct')"
    continue
  fi

  if (( pct >= MAX_PERCENT )); then
    say "$dir at ${pct}% (>= ${MAX_PERCENT}%), skipping"
    if [[ -z "$exhausted_low" ]] || (( pct < exhausted_low )); then
      exhausted_low="$pct"
    fi
    continue
  fi

  if [[ -z "$best_pct" ]] || (( pct < best_pct )); then
    best_pct="$pct"
    best_dir="$dir"
  fi
done

if [[ -n "$best_dir" ]]; then
  say "routing to $best_dir (${best_pct}% used)"
  echo "CLAUDE_CONFIG_DIR=$best_dir"
  echo "REASON=${best_pct}% of its binding limit used"
  exit 0
fi

# Nothing usable. Only hold the task if we actually saw an exhausted profile —
# if every probe merely failed, stay out of the way and let ty spawn normally
# rather than parking the whole board behind a broken credential lookup.
if [[ -n "$exhausted_low" ]]; then
  echo "HOLD=1"
  echo "REASON=every Claude profile is at or above ${MAX_PERCENT}% (best is ${exhausted_low}%)"
  exit 0
fi

say "no profile could be evaluated; leaving this task alone"
