#!/usr/bin/env bash
# Drive the FIRST-RUN onboarding experience across folder types against an
# isolated ty instance. Exercises the launch decision tree:
#   - project candidate (git repo)      -> enriched "New Project Detected" card
#   - project candidate (non-git marker)-> card with Worktrees: false (git optional)
#   - junk folder (no signals)          -> Welcome fork (set up a project / start a task)
#
# Each scenario uses a FRESH isolated DB so it's a true first run. The suggestion
# card runs a real `claude -p` inference (needs claude on PATH), so allow ~15s.
#
# Usage: scripts/qa/ty-qa-firstrun.sh
#        tmux -L "$TY_QA_TMUX_SOCKET" attach -t task-ui-<sid>   # watch / drive manually
#        scripts/qa/ty-qa-down.sh       # tear down
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

ROOT="$TY_QA_ROOT/firstrun"
GITPROJ="$ROOT/acme-rocket"
MARKER="$ROOT/marker-only"
PLAIN="$ROOT/just-a-folder"
SID="$TY_UI_SESSION"

echo "==> Building ty -> $TY_BIN"
( cd "$TY_REPO_ROOT" && go build -o "$TY_BIN" ./cmd/task )

echo "==> Preparing scenario folders under $ROOT"
rm -rf "$ROOT"; mkdir -p "$GITPROJ" "$MARKER" "$PLAIN"
# A) git repo with a README -> candidate, worktrees on
git -C "$GITPROJ" init -q
git -C "$GITPROJ" config user.email qa@ty.local
git -C "$GITPROJ" config user.name "ty qa"
printf '# Acme Rocket\n\nTool for launching rockets.\n' > "$GITPROJ/README.md"
git -C "$GITPROJ" add -A && git -C "$GITPROJ" commit -qm init
# B) non-git folder with a marker file -> candidate, worktrees OFF (git optional)
printf '{"name":"marker-pkg"}\n' > "$MARKER/package.json"
# C) PLAIN stays empty -> not a candidate -> Welcome fork

launch() { # $1 = cwd
  qtmux kill-session -t "$SID" 2>/dev/null || true
  rm -f "$WORKTREE_DB_PATH" "$TY_QA_STATE"   # fresh DB => true first run
  qtmux new-session -d -s "$SID" -x "${TY_QA_COLS:-220}" -y "${TY_QA_ROWS:-50}" -n tui -c "$1" \
    "WORKTREE_DB_PATH='$WORKTREE_DB_PATH' WORKTREE_SESSION_ID='$WORKTREE_SESSION_ID' '$TY_BIN' --debug-state-file '$TY_QA_STATE'"
}

cap() { qtmux capture-pane -t "${SID}:tui" -p | sed 's/[[:space:]]*$//' | grep -v '^[[:space:]]*$'; }

echo; echo "### Scenario A: git repo -> enriched suggestion card (inference, ~15s)"
launch "$GITPROJ"; sleep 16; cap | head -22

echo; echo "### Scenario B: non-git marker folder -> card with Worktrees: false (~15s)"
launch "$MARKER"; sleep 16; cap | head -22

echo; echo "### Scenario C: plain folder -> Welcome fork"
launch "$PLAIN"; sleep 6; cap | head -18

cat <<EOF

==> Drive manually from here:
    tmux -L $TY_QA_TMUX_SOCKET attach -t $SID
    Welcome fork:  enter = Set up a project (-> fuzzy folder picker), →/enter = Just start a task
    Folder picker: type to fuzzy-filter, ↑↓ select, → descend, enter pick, esc back
    Suggestion card: y = Create Project, n = Not Now
    Tear down:     scripts/qa/ty-qa-down.sh --purge
EOF
