#!/usr/bin/env bash
# Freeze (or unfreeze) the isolated instance's daemon so seeded data stays put.
#
# The ty TUI always ensures a daemon on launch, and that daemon executes `queued`
# tasks the moment it sees them — which mutates a seeded board out from under your
# screenshots. Freezing parks a harmless decoy in the daemon pidfile so no real
# executor starts; unfreezing removes it. ty-qa-shoot.sh freezes automatically
# for data shots (TY_QA_SHOT_KEEP_DB=1); use this when driving the TUI by hand.
#
# Usage:
#   scripts/qa/ty-qa-freeze.sh          # freeze (default)
#   scripts/qa/ty-qa-freeze.sh --off    # unfreeze
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

if [[ "${1:-}" == "--off" ]]; then
  ty_qa_unfreeze_daemon
else
  ty_qa_freeze_daemon
fi
