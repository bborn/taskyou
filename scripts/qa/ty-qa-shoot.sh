#!/usr/bin/env bash
# Render a single ty TUI screen to a PNG using VHS.
#
# Why VHS (not `tmux capture-pane`): a detached tmux session mis-reports its
# width to bubbletea, so modals/centred views overflow and render corrupted in
# captures. VHS runs the TUI in its own correctly-sized headless terminal, so
# the screenshot matches what real users see. ty renders in-pane (runLocal)
# whenever $TMUX is set, so we set a dummy TMUX and don't need a real tmux here.
#
# Usage:
#   ty-qa-shoot.sh <cwd> <out.png> [<vhs-line> ...]
#     cwd       directory to launch ty in (drives first-run detection)
#     out.png   destination PNG (the final TUI frame is captured)
#     vhs-line  optional extra VHS tape commands run after the TUI appears, e.g.
#               "Sleep 5s"  "Enter"  'Type "ty"'  "Sleep 2s"
#               (when omitted, the script waits 6s and screenshots)
#
# Examples:
#   ty-qa-shoot.sh "$TY_QA_PROJECTS/demo" /tmp/card.png "Sleep 9s"   # git-repo card (waits for claude -p inference)
#   ty-qa-shoot.sh /tmp/plain /tmp/welcome.png "Sleep 5s"            # welcome fork
#   ty-qa-shoot.sh /tmp/plain /tmp/picker.png "Sleep 5s" "Enter" "Sleep 1s" 'Type "ty"' "Sleep 2s"
#
# Requires: vhs (brew install vhs), magick (brew install imagemagick).
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
ty_qa_require_built

CWD="$1"; OUT="$2"; shift 2
command -v vhs   >/dev/null || { echo "ty-qa: vhs not installed (brew install vhs)" >&2; exit 1; }
command -v magick >/dev/null || { echo "ty-qa: imagemagick not installed (brew install imagemagick)" >&2; exit 1; }

W="${TY_QA_SHOT_W:-1180}"
H="${TY_QA_SHOT_H:-900}"
FS="${TY_QA_SHOT_FONTSIZE:-20}"

TAPE="$(mktemp -t tyqa-XXXX).tape"
GIF="${OUT%.png}.gif"
{
  echo "Output \"$GIF\""
  echo "Set FontSize $FS"
  echo "Set Width $W"
  echo "Set Height $H"
  echo "Set Padding 24"
  echo 'Set Shell "bash"'
  echo 'Env TMUX "vhs"'                                   # make ty render in-pane
  echo "Env WORKTREE_DB_PATH \"$WORKTREE_DB_PATH\""
  echo "Env WORKTREE_SESSION_ID \"$WORKTREE_SESSION_ID\""
  echo 'Hide'
  if [ -n "${TY_QA_SHOT_KEEP_DB:-}" ]; then
    # Keep the (seeded) DB — for board/detail shots with data.
    echo "Type \"cd $CWD && clear\""
  else
    echo "Type \"rm -f $WORKTREE_DB_PATH && cd $CWD && clear\""   # fresh DB => true first run
  fi
  echo 'Enter'
  echo 'Show'
  echo "Type \"$TY_BIN\""
  echo 'Enter'
  if [ "$#" -eq 0 ]; then echo 'Sleep 6s'; fi
  for line in "$@"; do echo "$line"; done
} > "$TAPE"

vhs "$TAPE" >/dev/null
# VHS's `Screenshot` command is unreliable across versions; the robust path is
# to coalesce the recorded gif and keep its final frame.
FRAMES="$(mktemp -d)"
magick "$GIF" -coalesce "$FRAMES/f_%04d.png"
cp "$(ls "$FRAMES"/f_*.png | tail -1)" "$OUT"
rm -rf "$FRAMES" "$TAPE"
echo "shot -> $OUT"
