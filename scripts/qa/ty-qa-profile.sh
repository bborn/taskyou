#!/usr/bin/env bash
# Profile the real TUI's rendering: launch the isolated instance with CPU + heap
# profiling enabled, drive a render-heavy stress sequence (navigation, open/close
# detail, filter), quit gracefully so the profiles flush, then print the hottest
# render call stacks.
#
# This is the "run the QA harness with profiling enabled to capture frame times,
# memory allocations, and render call stacks" workflow.
#
# Usage:
#   scripts/qa/ty-qa-up.sh            # build + isolated instance + seed tasks
#   scripts/qa/ty-qa-profile.sh       # profile a stress run
#
# Output:
#   $TY_QA_ROOT/cpu.prof  $TY_QA_ROOT/mem.prof  (+ a top-functions summary)
#   Inspect interactively: go tool pprof "$TY_BIN" /tmp/ty-qa/cpu.prof
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
ty_qa_require_built

COLS="${TY_QA_COLS:-230}"
ROWS="${TY_QA_ROWS:-55}"
CPU_PROF="$TY_QA_ROOT/cpu.prof"
MEM_PROF="$TY_QA_ROOT/mem.prof"
ITERS="${TY_QA_PROFILE_ITERS:-40}"

tmux kill-session -t "$TY_UI_SESSION" 2>/dev/null || true
rm -f "$CPU_PROF" "$MEM_PROF"

echo "==> launching TUI with profiling ($COLS x $ROWS)"
tmux new-session -d -s "$TY_UI_SESSION" -x "$COLS" -y "$ROWS" -n tui \
  "WORKTREE_DB_PATH='$WORKTREE_DB_PATH' WORKTREE_SESSION_ID='$WORKTREE_SESSION_ID' '$TY_BIN' \
    --debug-state-file '$TY_QA_STATE' --cpuprofile '$CPU_PROF' --memprofile '$MEM_PROF'"
sleep 4

echo "==> driving render stress: $ITERS navigation cycles + detail open/close"
for ((i = 0; i < ITERS; i++)); do
  tmux send-keys -t "$TY_UI_PANE" Down
  sleep 0.05
  tmux send-keys -t "$TY_UI_PANE" Up
  sleep 0.05
  tmux send-keys -t "$TY_UI_PANE" Right
  sleep 0.05
  tmux send-keys -t "$TY_UI_PANE" Left
  sleep 0.05
done
# Open a detail view, let it tick, then close — exercises the detail render path.
tmux send-keys -t "$TY_UI_PANE" Enter; sleep 1
tmux send-keys -t "$TY_UI_PANE" Escape; sleep 0.5
# Open and close the filter to exercise that path too.
tmux send-keys -t "$TY_UI_PANE" "/"; sleep 0.3
tmux send-keys -t "$TY_UI_PANE" Escape; sleep 0.3

echo "==> quitting gracefully (Ctrl+C) so profiles flush"
tmux send-keys -t "$TY_UI_PANE" C-c
sleep 2
tmux kill-session -t "$TY_UI_SESSION" 2>/dev/null || true

if [[ ! -s "$CPU_PROF" ]]; then
  echo "ty-qa-profile: no CPU profile captured ($CPU_PROF). Did the TUI exit cleanly?" >&2
  exit 1
fi

echo
echo "==> CPU profile written: $CPU_PROF"
echo "==> Heap profile written: $MEM_PROF"
echo
echo "=== Top render hot spots (cumulative CPU) ==="
go tool pprof -nodecount=20 -cum -top "$TY_BIN" "$CPU_PROF" 2>/dev/null | sed -n '1,28p' || true
echo
echo "Inspect interactively:"
echo "  go tool pprof '$TY_BIN' '$CPU_PROF'      # then: top / list KanbanBoard.View / web"
echo "  go tool pprof '$TY_BIN' '$MEM_PROF'      # heap allocations"
