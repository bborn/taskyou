#!/usr/bin/env bash
# Screenshot the "start a project from a GitHub repo URL" onboarding path
# against an isolated ty instance: Welcome fork -> folder picker -> clone view
# -> the ordinary project card.
#
# Every shot runs with HOME pointed at a throwaway home under $TY_QA_ROOT, so a
# clone lands in $TY_QA_ROOT/home/Projects and never touches the real ~/Projects.
# That fake home is also what the folder picker lists, so it is populated with
# believable repos (see the content standard in README.md).
#
# One shot clones for real (small public repo, a few seconds); the rest need no
# network. Nothing is written outside $TY_QA_ROOT.
#
# Usage: scripts/qa/ty-qa-clone-shots.sh [out-dir]      (default $TY_QA_ROOT/shots)
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

OUT="${1:-$TY_QA_ROOT/shots}"
HOMEDIR="$TY_QA_ROOT/home"
PROJECTS="$HOMEDIR/Projects"
PLAIN="$TY_QA_ROOT/firstrun/just-a-folder"   # no signals => Welcome fork
REPO_URL="https://github.com/charmbracelet/bubbletea"
REPO_NAME="bubbletea"

echo "==> Building ty -> $TY_BIN"
( cd "$TY_REPO_ROOT" && go build -o "$TY_BIN" ./cmd/task )

# Only now: everything below runs against the throwaway home (the go build above
# would otherwise rebuild its cache inside it).
export HOME="$HOMEDIR"
export TY_QA_SHOT_ENV="HOME"

echo "==> Throwaway home at $HOMEDIR"
rm -rf "$HOMEDIR"
mkdir -p "$PROJECTS" "$PLAIN" "$OUT"

# The picker seeds from $HOME/Projects — give it a believable shelf of repos.
for p in storefront payments-api mobile-ios data-pipeline design-system; do
  git -C "$PROJECTS" init -q "$p"
  git -C "$PROJECTS/$p" config user.email qa@ty.local
  git -C "$PROJECTS/$p" config user.name "ty qa"
  printf '# %s\n' "$p" > "$PROJECTS/$p/README.md"
  git -C "$PROJECTS/$p" add -A
  git -C "$PROJECTS/$p" commit -qm init
done

shoot() { # $1 = name, rest = tape lines
  local name="$1"; shift
  TY_QA_SHOT_W="${TY_QA_SHOT_W:-1180}" TY_QA_SHOT_H="${TY_QA_SHOT_H:-760}" \
    "$TY_QA_DIR/ty-qa-shoot.sh" "$PLAIN" "$OUT/$name.png" "$@"
}

# Typing the URL a character at a time is VHS's only option; a real user pastes.
TYPE_URL="Type \"$REPO_URL\""

echo; echo "==> 1/8 welcome fork"
shoot welcome "Sleep 5s"

echo; echo "==> 2/8 pasted URL becomes a clone offer"
shoot picker-paste "Sleep 5s" "Enter" "Sleep 1s" "$TYPE_URL" "Sleep 2s"

echo; echo "==> 3/8 a URL that doesn't parse, inline"
shoot picker-error "Sleep 5s" "Enter" "Sleep 1s" 'Type "https://github.com/charmbracelet"' "Sleep 2s"

echo; echo "==> 4/8 destination shown before cloning"
shoot clone-confirm "Sleep 5s" "Enter" "Sleep 1s" "$TYPE_URL" "Sleep 1s" "Enter" "Sleep 2s"

echo; echo "==> 5/8 cloning (spinner)"
shoot clone-progress "Sleep 5s" "Enter" "Sleep 1s" "$TYPE_URL" "Sleep 1s" "Enter" "Sleep 500ms" "Enter" "Sleep 900ms"

# The clone from shot 5 (if it finished) is the real thing; shot 6 waits it out
# and lands on the ordinary project card — the point of the whole feature.
echo; echo "==> 6/8 clone lands -> ordinary project card"
rm -rf "${PROJECTS:?}/$REPO_NAME"
shoot clone-done "Sleep 5s" "Enter" "Sleep 1s" "$TYPE_URL" "Sleep 1s" "Enter" "Sleep 500ms" "Enter" "Sleep 25s"

echo; echo "==> 7/8 destination already holds this repo -> use it as-is"
rm -rf "${PROJECTS:?}/$REPO_NAME"
git -C "$PROJECTS" init -q "$REPO_NAME"
git -C "$PROJECTS/$REPO_NAME" remote add origin "$REPO_URL.git"
shoot clone-reuse "Sleep 5s" "Enter" "Sleep 1s" "$TYPE_URL" "Sleep 1s" "Enter" "Sleep 2s"

echo; echo "==> 8/8 the name is taken by something else -> non-colliding destination"
rm -rf "${PROJECTS:?}/$REPO_NAME"
mkdir -p "$PROJECTS/$REPO_NAME/notes"
printf 'sketches\n' > "$PROJECTS/$REPO_NAME/notes/README.md"
shoot clone-collision "Sleep 5s" "Enter" "Sleep 1s" "$TYPE_URL" "Sleep 1s" "Enter" "Sleep 2s"

echo; echo "==> bonus: clone fails -> git's own words, still in the UI"
shoot clone-failed "Sleep 5s" "Enter" "Sleep 1s" \
  'Type "charmbracelet/no-such-repository"' "Sleep 1s" "Enter" "Sleep 500ms" "Enter" "Sleep 6s"

echo; echo "==> shots in $OUT"
ls -1 "$OUT"
